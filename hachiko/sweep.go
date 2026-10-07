package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/logs"
	"github.com/timche/mac-mini/hachiko/internal/process"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

type sweeper struct {
	cfg   config.Config
	deps  Deps
	store statedir.Store
	dry   bool
}

func (s sweeper) say(format string, args ...any) {
	logs.Logger{Out: s.deps.Log, Now: s.deps.Now}.Say(format, args...)
}

// Every message about an incident in one place. With the bot configured, the first message
// of an incident opens a thread and everything after it goes inside, so a reminder three
// hours later is under the alert it is about rather than further down a channel — and the
// thread is what `hachiko listen` reads a reply out of. With only the webhook, the thread
// is nothing and the message goes to the channel exactly as it always did.
func (s sweeper) send(state *statedir.State, incident, message string) error {
	out := Outgoing{Text: message}

	switch thread, known := state.Threads[incident]; {
	case incident == "":
	case known:
		out.Thread = thread
	default:
		out.OpenThread = incident
	}

	thread, err := s.deps.Send(out)
	if err != nil {
		return err
	}
	if thread != "" && incident != "" {
		if state.Threads == nil {
			state.Threads = map[string]string{}
		}
		state.Threads[incident] = thread
	}
	return nil
}

func (s sweeper) run() error {
	now := s.deps.Now()

	free, err := s.deps.FreeKB()
	if err != nil {
		s.say("there is no reading of free space for %s, so this check did nothing: %v", s.cfg.Home, err)
		return err
	}

	// A dry run changes nothing at all, the state directory and the lock included.
	if !s.dry {
		lock, takenFrom, err := s.store.Acquire(5*time.Minute, now)

		switch {
		case errors.Is(err, statedir.ErrLockHeld):
			return nil
		case errors.Is(err, syscall.ENOSPC):
			// The fault this whole thing exists to catch. A sweep that stopped here would
			// be silent exactly when it has something to say.
			s.say("there is no room left to take a lock in %s, so this check runs without one", s.cfg.StateDir)
		case err != nil:
			s.say("the lock in %s could not be taken, so this check runs without one: %v", s.cfg.StateDir, err)
		}

		if lock != nil {
			defer lock.Release()
		}
		if takenFrom != "" {
			s.say("taking over a lock left behind by %s", takenFrom)
		}
	}

	state, err := s.store.Load()
	corrupt := false
	switch {
	case errors.Is(err, statedir.ErrCorrupt):
		s.say("%v", err)
		corrupt = true
	case err != nil:
		return err
	}

	disk := s.disk(state, now, free)
	cpu := s.cpu(state, now)

	// 20 below 100, so a disk that keeps filling after the first alert says so once
	// more; nothing is sent again until it is back over 100.
	level := int64(0)
	if free < s.cfg.LowKB {
		level = s.cfg.LowGB()
	}
	if free < s.cfg.CriticalKB {
		level = s.cfg.CriticalGB()
	}

	before := state.LowSpaceLevel
	lowNow := level != 0 && (before == 0 || level < before)

	// A truncate is the one thing here that changes somebody's disk, so it is always an
	// incident of its own — including on a run where the file was already flagged and
	// the threshold was already crossed, which is every run but the first.
	truncated := disk.truncated != ""

	newIncident := disk.fired || cpu.fired || lowNow || truncated

	alert := s.alert(disk, cpu, free, level, lowNow, truncated)

	// disk when anything about the disk fired, since that is the half with a deadline
	// on it; the kind only decides which session the incident goes to, and one
	// session takes the whole of a run either way.
	kind := "disk"
	if !disk.fired && !lowNow && !truncated && cpu.fired {
		kind = "cpu"
	}

	if s.dry {
		if level != 0 {
			s.say("would report %s GB free, under the %d GB threshold", gbStr(free), level)
		}
		if newIncident {
			s.say("would open an on-call session as %s and send: %s", kind, alert.lead())
		}
		s.say("%s GB free, %d file(s) growing fast, %d over %s GB, %d process(es) hot of %d sampled, %d director(ies) skipped for not answering",
			gbStr(free), len(disk.growing), len(disk.sizes), gbStr(s.cfg.BigKB), len(cpu.hot), cpu.sampled, len(disk.stalled))

		// Said rather than sent: a dry run that checked in would tell shibuya this Mac is
		// being watched on a run that watched nothing.
		if reasons := sweepFaults(corrupt, disk, cpu); len(reasons) > 0 {
			s.say("would tell shibuya this sweep did not finish its job: %s", strings.Join(reasons, "; "))
		} else {
			s.say("would check in with shibuya")
		}

		s.say("dry run over")
		return nil
	}

	if lowNow {
		s.say("only %s GB free, under the %d GB threshold", gbStr(free), level)
	}
	if level == 0 && before != 0 {
		s.say("free space is back over %d GB", s.cfg.LowGB())
	}

	raised := false
	if newIncident {
		raised = s.raise(state, now, kind, alert, free, truncated)
	}

	// Whether what fired an open incident is still firing. A reading where it is not is the
	// other half of what the session needs: without it a daemon that stops spinning by
	// itself leaves a session waiting on a question about a process that is not there any
	// more, and nobody is told until somebody asks.
	//
	// A reading that is missing is not a reading that is clear, so neither of these is taken
	// from one: a walk that ran out of its seconds saw only part of the disk, and a check
	// with no process sample saw no processes at all. A directory the walk was told to skip
	// is the third of them, and the wait is where that is caught, since what it has to be
	// measured against is the files the question was asked about.
	cleared := map[string]string{}
	if level == 0 && !disk.cutShort && disk.report == "" && disk.truncated == "" {
		cleared["disk"] = fmt.Sprintf("Nothing is growing fast any more, and free space is back over every mark at %s.",
			wording.GBUnit(free))
	}
	if cpu.read && cpu.report == "" {
		cleared["cpu"] = fmt.Sprintf("Nothing is using much CPU any more, out of the %s this check looked at.",
			wording.CountOf(int64(cpu.sampled), "process", "processes"))
	}

	// This minute's numbers, which every message about a question nobody has answered
	// carries: the answer to a three-hour-old question is about a machine that has moved.
	reading := nowReading{
		free:        free,
		prevFree:    state.Disk.FreeKB,
		level:       level,
		span:        disk.span,
		spanClamped: disk.spanClamped,
		grewKB:      disk.grewKB,
		sizes:       disk.sizes,
		writers:     disk.writers,
		sections:    joinBlocks(disk.truncated, disk.report, cpu.report, cpu.sudo),
		cleared:     cleared,
		stalled:     disk.stalled,
	}

	s.chaseLateReports(state, now)
	s.chaseAnswers(state, now, reading)

	// What stays flagged: whatever was flagged before and is still going, plus what
	// this check raised, and only if the message actually left the machine.
	keptFiles, keptProcs := disk.stillGoing, cpu.stillGoing
	if raised {
		keptFiles = append(keptFiles, disk.fresh...)
		keptProcs = append(keptProcs, cpu.fresh...)
	} else {
		// A file nobody has heard about yet keeps the size it was measured against, so
		// the growth that failed to send goes on accumulating against the same baseline.
		// Advancing it would let a writer that slows to a gigabyte an interval slip
		// under the threshold for good, having already taken the disk.
		for _, path := range disk.fresh {
			if path == disk.truncatedPath {
				continue
			}
			if before, ok := state.Disk.Files[path]; ok {
				disk.sizes[path] = before
			} else {
				delete(disk.sizes, path)
			}
		}
	}

	state.AlertedFiles, state.AlertedProcs = keptFiles, keptProcs

	switch {
	case level == 0:
		state.LowSpaceLevel = 0
	case !lowNow || raised:
		state.LowSpaceLevel = level
	}

	state.Disk = statedir.DiskSample{At: now.Unix(), Files: disk.sizes, FreeKB: free}
	state.CPU = cpu.sample
	state.Stalled = disk.stalled

	expected := stillExpected(state)
	s.sayDroppedOutcomes(expected)
	s.store.ForgetReportedExcept(expected)

	// A thread for an incident nobody is waiting on any more. Dropped here rather than when
	// the wait ends, because the session's outcome message goes into it after that and the
	// listener has no reason to poll it afterwards.
	for incident := range state.Threads {
		if !expected[incident] {
			delete(state.Threads, incident)
		}
	}

	// Last, so that what shibuya is told about this Mac is what the check decided rather
	// than what it had got to — and so that a Worker that will not answer costs the sweep
	// its ten seconds after everything else is done.
	s.checkIn(state, corrupt, disk, cpu, free, len(expected))

	return s.store.Save(state)
}

// What shibuya is told, and the one line about it. A sweep that ran is liveness whatever it
// found: the alert about a disk is hachiko's own, and all this says is that the watch is
// still running — or that it ran and did not finish its job, which is a Mac whose monitor is
// half blind and nothing a dead man's switch would ever notice by itself.
func (s sweeper) checkIn(state *statedir.State, corrupt bool, disk diskFindings, cpu cpuFindings, free int64, open int) {
	in := Checkin{
		FreeGB:        gbNum(free),
		OpenIncidents: open,
		HotProcesses:  len(cpu.hot),
		Display:       s.cfg.Display,
	}

	if reasons := sweepFaults(corrupt, disk, cpu); len(reasons) > 0 {
		in.Failed, in.Reason = true, strings.Join(reasons, "; ")
	}

	was := state.Switch
	now, line := s.deps.CheckIn(in)
	state.Switch = now

	// One line per state change and nothing on a check that went the way the last one did.
	// This runs every five minutes for ever, and twelve lines an hour about a token that is
	// still missing would bury the lines that matter.
	if now == was {
		return
	}
	if line != "" {
		s.say("%s", line)
	}
	if now == checkinSent && was != "" {
		s.say("shibuya is hearing from this Mac again")
	}
}

// The honest set: the three ways a sweep runs and comes back knowing less than it should.
// Not an incident, because none of them is a fault of the Mac's — they are faults of the
// watch, and the only thing that can report them is something off the machine.
func sweepFaults(corrupt bool, disk diskFindings, cpu cpuFindings) []string {
	var reasons []string

	if disk.cutShort {
		reasons = append(reasons, "the walk ran out of its seconds and saw only part of the disk")
	}
	if !cpu.read {
		reasons = append(reasons, "there was no process sample, so nothing was watching the CPU")
	}
	if corrupt {
		reasons = append(reasons, "the state file could not be read, so this check measured nothing against the last")
	}
	return reasons
}

// What the next sweep inherits: the ones still being skipped, the ones that stalled
// again with their clock moved on, and the newly stalled. A directory that was retried
// and did not stall is gone from the list, which is what puts it back in the walk.
func (s sweeper) rememberStalls(was []statedir.Stall, stalled, retried []string, now time.Time) []statedir.Stall {
	keep := make([]statedir.Stall, 0, len(was)+len(stalled))
	seen := map[string]bool{}

	for _, old := range was {
		switch {
		case statedir.Contains(stalled, old.Dir):
			old.LastAt = now.Unix()
		case statedir.Contains(retried, old.Dir):
			continue
		}
		keep = append(keep, old)
		seen[old.Dir] = true
	}

	for _, dir := range stalled {
		if seen[dir] {
			continue
		}
		s.say("%s did not answer a read within %s, so it is skipped until it is tried again in %s",
			dir, s.cfg.DirTimeout, s.cfg.StallRetry)
		keep = append(keep, statedir.Stall{Dir: dir, FirstAt: now.Unix(), LastAt: now.Unix()})
		seen[dir] = true
	}

	sort.Slice(keep, func(i, j int) bool { return keep[i].Dir < keep[j].Dir })
	return keep
}

type diskFindings struct {
	cutShort    bool
	sizes       map[string]int64
	growing     []Growing
	stalled     []statedir.Stall
	span        time.Duration
	spanClamped bool
	grewKB      int64
	writers     []string

	// The `Growing fast` section, one bullet per file, and the `Emptied` line: built once
	// here and read by the first alert, by every reminder and by the prompt that hands the
	// decision over.
	report        string
	truncated     string
	truncatedPath string

	// The file that fired first, which the lead names and which the short form of the
	// first message carries on its own.
	firstPath   string
	firstBullet string

	fired      bool
	stillGoing []string
	fresh      []string
}

func (s sweeper) disk(state *statedir.State, now time.Time, free int64) diskFindings {
	had := state.HasDiskSample()
	prevAt := now
	if had {
		prevAt = time.Unix(state.Disk.At, 0)
	}
	// A span of nothing or less is not an interval, it is a clock that moved: the machine
	// slept, somebody set the time, a sample carries a timestamp in the future. A second is
	// enough to divide a growth by for a rate in a message; it is not enough to extrapolate
	// from, so the fact that it was invented travels with it.
	span, clamped := now.Sub(prevAt), false
	if span <= 0 {
		span, clamped = time.Second, true
	}

	out := diskFindings{span: span, spanClamped: clamped || !had}

	// What is still being skipped and what is due to be tried again. Everything not
	// skipped is walked, so a directory being retried either stalls again or is quietly
	// back.
	skip, retried := statedir.DueForRetry(state.Stalled, now, s.cfg.StallRetry)
	walk := s.deps.BigFiles(skip)

	stalledNow := map[string]bool{}
	for _, dir := range walk.Stalled {
		stalledNow[dir] = true
	}

	// One line when a directory stops answering and one when it starts again, and
	// nothing in between: a line every five minutes forever would bury the lines that
	// matter.
	for _, was := range state.Stalled {
		if statedir.Contains(retried, was.Dir) && !stalledNow[was.Dir] {
			s.say("%s answered again after %s, so it is back in the walk",
				was.Dir, hmStr(now.Sub(time.Unix(was.FirstAt, 0))))
		}
	}

	out.stalled = s.rememberStalls(state.Stalled, walk.Stalled, retried, now)

	out.cutShort = walk.CutShort
	if walk.CutShort {
		s.say("the walk ran out of its %s, so this check saw only part of the disk", s.cfg.WalkTimeout)
	}

	sizes, growing := growth(state.Disk.Files, walk.Files, s.cfg.GrowthKB, had)
	out.sizes, out.growing = sizes, growing

	var bullets []string
	for _, g := range growing {
		// A path and an lsof command name are both chosen by whatever filled the disk,
		// and both end up in a message Discord caps and in a prompt an agent reads.
		writer := wording.Safe(s.deps.Writers(g.Path), wording.WriterLimit)
		path := wording.Safe(g.Path, wording.PathLimit)

		line := fmt.Sprintf("%s — %s GB, grew %s GB since the last sample (%s GB/hour), written by %s",
			path, gbStr(g.KB), gbStr(g.GrewKB), rateStr(g.GrewKB, span), writer)

		bullet := fmt.Sprintf("%s — %s, up %s in %s (%s), written by %s",
			wording.CodeSpan(path), wording.GBUnit(g.KB), wording.GBUnit(g.GrewKB), wording.DurationPhrase(span),
			wording.RatePhrase(g.GrewKB, span), writer)
		bullets = append(bullets, bullet)

		// Everything a projection and a stale question are read from: what the files
		// hachiko can see are gaining between two samples, and who is holding them.
		out.grewKB += g.GrewKB
		if !statedir.Contains(out.writers, writer) {
			out.writers = append(out.writers, writer)
		}

		if !s.dry && state.AlertedFile(g.Path) {
			out.stillGoing = append(out.stillGoing, g.Path)
			continue
		}

		s.say("growing fast: %s", line)
		if s.dry {
			continue
		}

		out.fired = true
		out.fresh = append(out.fresh, g.Path)
		if out.firstPath == "" {
			out.firstPath, out.firstBullet = path, bullet
		}
	}
	out.report = wording.Section(wording.LabelGrowing, bullets)

	// The fastest grower or nothing: the biggest offender is the one worth a
	// truncate, and a check that worked its way down the list would eventually reach
	// a file that is somebody's.
	if free < s.cfg.CriticalKB && len(growing) > 0 {
		worst := growing[0].Path

		switch {
		case !truncatable(s.cfg, worst):
			s.say("free space is under %d GB and %s is the fastest growing, but it is not a log this may truncate",
				s.cfg.CriticalGB(), wording.Safe(worst, wording.PathLimit))
		case s.dry:
			s.say("would truncate %s", wording.Safe(worst, wording.PathLimit))
		default:
			// Never an rm and never a kill: the writer keeps its descriptor and its
			// offset, so a log it appends to goes on working and the space comes back
			// at once, where an unlinked file frees nothing until the writer exits and
			// a killed worker takes a session's work with it.
			if err := s.deps.Truncate(worst); err != nil {
				s.say("could not truncate %s: %v", wording.Safe(worst, wording.PathLimit), err)
			} else {
				s.say("truncated %s to keep the disk alive; its writer was not touched", wording.Safe(worst, wording.PathLimit))
				out.truncated = "**" + wording.LabelEmptied + ":** " + wording.CodeSpan(wording.Safe(worst, wording.PathLimit)) +
					" — its writer was left running, so the space is back now"
				out.truncatedPath = worst
				// The size it is now, so the next check measures growth from the
				// truncate rather than reporting a file that shrank.
				out.sizes[worst] = 0
			}
		}
	}

	return out
}

type cpuFindings struct {
	sample statedir.CPUSample

	// Whether there was a process sample at all. An empty one is a reading; a failed one is
	// the absence of a reading, and nothing may be concluded from it about what is running.
	read    bool
	hot     []Hot
	sampled int

	// The `Busy processes` section, one bullet per process, and the bullet the short form
	// of a first alert carries on its own.
	report      string
	firstBullet string

	// The line naming what stopping a busy system process would take, which is a line about
	// Tim rather than about the session: either the one command the root helper allows
	// without a password, or the sudo kill that is his to decide on.
	sudo       string
	sentence   string
	fired      bool
	stillGoing []string
	fresh      []string
}

func (s sweeper) cpu(state *statedir.State, now time.Time) cpuFindings {
	procs, err := s.deps.Processes()
	if err != nil {
		s.say("there is no process sample for this check: %v", err)
		return cpuFindings{sample: state.CPU}
	}

	sample, hot := cpuHot(state.CPU, procs, now, s.cfg.CPUShare, s.cfg.CPUWindow)
	out := cpuFindings{sample: sample, read: true, hot: hot, sampled: len(procs)}

	mine := process.OwnTree(procs, s.deps.Getpid())
	allowlist := readAllowlist(s.cfg.CPUAllowPath)

	var bullets []string
	for _, h := range hot {
		p := h.Process
		if mine[p.PID] || allowed(allowlist, p.Path(), p.Name()) {
			continue
		}

		// A name taken out of a command line, which is the attacker's half of this.
		name := wording.Safe(p.Name(), wording.NameLimit)

		line := fmt.Sprintf("pid %d %s — %.0f%% of a core for %s, up %s, %s MB resident, ppid %d",
			p.PID, p.Name(), h.Share, hmStr(h.HotFor(now)), hmStr(now.Sub(p.StartedAt)), mbStr(p.RSSKB), p.PPID)

		bullet := fmt.Sprintf("%s — %.0f%% of a core for %s, %s memory, started %s",
			wording.ProcessLabel(name, p.PID), h.Share, wording.DurationPhrase(h.HotFor(now)),
			wording.GBUnit(p.RSSKB), wording.TimePhrase(p.StartedAt, now))

		// Every daemon launchd starts has ppid 1, so a parent of launchd on its own says
		// nothing: "orphaned" about root's dasd described how macOS starts daemons rather
		// than anything being wrong with it. What is worth saying about a process that is not
		// this account's is the thing the session cannot do about it.
		system := p.UID != s.deps.Getuid()
		if system {
			line += ", system process owned by " + wording.Safe(p.Owner(), wording.UserLimit)
			bullet += ", a system process owned by " + wording.PlainWords(wording.Safe(p.Owner(), wording.UserLimit))
		}

		if cwd := s.deps.CWD(p.PID); cwd != "" {
			line += ", cwd " + wording.Safe(cwd, wording.PathLimit)
			bullet += ", in " + wording.CodeSpan(wording.Safe(cwd, wording.PathLimit))
			// Built out of the path's own components, so it is as much the attacker's
			// choosing as the path is.
			if where := wording.Safe(repoOf(s.cfg, cwd), wording.PathLimit); where != "" {
				line += ", in " + where
				bullet += " (" + wording.PlainWords(where) + ")"
				// The shape that caused the incident this exists for: a worker whose
				// session ended, reparented to launchd and still spending a core on
				// work nobody wants. This account's and inside a checkout, both: those two
				// together are what make a parent of launchd mean a session that is gone.
				if p.PPID == 1 && !system {
					line += " — left behind by the session that started it, which is gone"
					bullet += " — left behind by the session that started it, which is gone"
				}
			}
		}

		line += "\n    " + wording.Safe(p.Command, wording.ArgsLimit)

		// The command line on a continuation line of its own: it is the longest thing in any
		// of these and the one Tim scans rather than reads.
		bullet += "\n  " + wording.CodeSpan(wording.Safe(p.Command, wording.ArgsLimit))
		bullets = append(bullets, bullet)

		// What the session cannot do about it, said once and up front. sudo is on its never
		// list, so a system process is one it may only recommend stopping — and the whole of
		// the dasd night turned on a fix that needed Tim and a message that never said so.
		if system && out.sudo == "" {
			out.sudo = s.stoppingIt(p)
		}

		key := p.Key()
		if !s.dry && state.AlertedProc(key) {
			out.stillGoing = append(out.stillGoing, key)
			continue
		}

		s.say("hot for an hour: %s", line)
		if s.dry {
			continue
		}

		out.fired = true
		out.fresh = append(out.fresh, key)
		if out.sentence == "" {
			out.sentence = fmt.Sprintf("%s is busy: %.0f%% of a core for %s",
				wording.PlainWords(name), h.Share, wording.DurationPhrase(h.HotFor(now)))
			out.firstBullet = bullet
		}
	}
	out.report = wording.Section(wording.LabelBusy, bullets)

	return out
}

// The one thing Tim can do about a process this account does not own. A daemon the root
// helper allows is one command with no password behind it, which is the difference between
// a message he acts on from his phone and one he has to sit down for; anything else is the
// honest `sudo kill`, which launchd may well undo.
func (s sweeper) stoppingIt(p process.Process) string {
	if command := wording.RestartDaemonCommand(s.cfg, p.Name()); command != "" {
		return "**You can run:** " + wording.CodeSpan(command) + " — it restarts the daemon with no password needed."
	}
	return fmt.Sprintf("**Needs you:** %s — stopping a system process needs sudo, and launchd starts most daemons again.",
		wording.CodeSpan(fmt.Sprintf("sudo kill %d", p.PID)))
}

// What a first alert says, in both lengths it may go out in. The lead and the action line
// are the same either way; what differs is how much detail is under them, since a message
// that is not urgent leaves the reading to the session and Tim gets one screen.
type alertText struct {
	marker   string
	sentence string

	// The one or two fields that matter, and every finding there was.
	short string
	full  string
}

func (a alertText) lead() string { return a.marker + " " + a.sentence }

// What goes to the session, which needs the whole of it however the message to Tim is
// shortened.
func (a alertText) brief() string { return joinBlocks(a.lead(), a.full) }

// The lead, which is the one line Tim is certain to read and also the title of the forum
// post this opens. The disk goes first when both halves fired, because it is the half with
// a deadline on it; critical free space and a file hachiko emptied go above a file merely
// growing, because what the lead has to say first is how bad it already is.
func (s sweeper) alert(disk diskFindings, cpu cpuFindings, free, level int64, lowNow, truncated bool) alertText {
	out := alertText{marker: wording.MarkerDisk}

	switch {
	case truncated:
		out.marker = wording.MarkerDown
		out.sentence = fmt.Sprintf("Disk critical: %s free, and %s was emptied",
			wording.GBUnit(free), wording.BaseLabel(wording.Safe(disk.truncatedPath, wording.PathLimit)))
	case free < s.cfg.CriticalKB && (disk.fired || lowNow):
		out.marker = wording.MarkerDown
		out.sentence = fmt.Sprintf("Disk critical: %s free", wording.GBUnit(free))
	case disk.fired:
		out.sentence = fmt.Sprintf("Disk filling: %s is growing fast", wording.BaseLabel(disk.firstPath))
	case lowNow:
		out.sentence = fmt.Sprintf("Low disk space: %s free", wording.GBUnit(free))
	case cpu.fired:
		out.marker, out.sentence = wording.MarkerBusy, cpu.sentence
	}

	// Free space belongs in a message the disk is part of and nowhere else: a busy daemon
	// says nothing about how much room is left.
	freeField := ""
	if out.marker != wording.MarkerBusy && (out.sentence != "" || disk.report != "") {
		freeField = "**" + wording.LabelFreeSpace + ":** " + wording.GBUnit(free)
		if level != 0 {
			freeField += fmt.Sprintf(", under the %d GB mark", level)
		}
	}

	out.short = joinBlocks(freeField, disk.truncated,
		wording.Section(wording.LabelGrowing, nonEmpty(disk.firstBullet)),
		wording.Section(wording.LabelBusy, nonEmpty(cpu.firstBullet)), cpu.sudo)
	out.full = joinBlocks(freeField, disk.truncated, disk.report, cpu.report, cpu.sudo)

	return out
}

func nonEmpty(items ...string) []string {
	var keep []string
	for _, item := range items {
		if item != "" {
			keep = append(keep, item)
		}
	}
	return keep
}

// Sections joined by single newlines, with nothing for the ones there was nothing to say
// about: a message is never a form with blanks in it.
func joinBlocks(blocks ...string) string { return strings.Join(nonEmpty(blocks...), "\n") }

// The order the alert goes out in: the session first, so the message can name the
// tab it is waiting in, then one line from hachiko. The brief carries the incident
// id and the one command that reaches the channel, because the session is never
// handed the webhook itself.
func (s sweeper) raise(state *statedir.State, now time.Time, kind string, alert alertText, free int64, truncated bool) bool {
	incident := fmt.Sprintf("%s-%d", kind, now.Unix())

	session, err := s.deps.Oncall(kind, s.brief(incident, alert.brief()))
	if err != nil {
		s.say("no on-call session was opened: %v", err)
		session = OncallSession{}
	}

	// Urgent is free space already critical, or a file this truncated: either way Tim
	// needs the whole of it now rather than when an agent has finished reading. So is a
	// session the brief never reached, since nothing is going to read it for him.
	blocks := alert.short
	if free < s.cfg.CriticalKB || truncated || !session.Delivered {
		blocks = alert.full
	}

	say := session.Say
	if say == "" {
		say = "No on-call session could be started, so nothing is being worked on it."
	}

	message := wording.Lead(alert.marker, alert.sentence).Block(blocks).Can(say).About(incident, s.cfg.Host).String()

	// A failed send must leave the incident unraised, so the next check tries again
	// rather than going quiet about it.
	if err := s.send(state, incident, message); err != nil {
		s.say("the alert did not send and is left to the next check: %v", err)
		return false
	}

	// Only a session the brief reached has a report to wait for. One that is blocked on
	// its own question, or that never got the brief, has already had the whole of it
	// sent on its behalf.
	if session.Delivered {
		if state.Pending == nil {
			state.Pending = map[string]statedir.Pending{}
		}
		// The detail blocks and not the lead: the message that goes out if nothing reports
		// has a lead of its own saying that nobody did.
		state.Pending[incident] = statedir.Pending{OpenedAt: now.Unix(), Tab: session.Tab, Details: alert.full}
		s.expectAQuestion(state, now, kind, incident, session)
	}

	// An escalation supersedes the incident it escalated from, and the same session
	// has the new brief: waiting on the older one as well would say the agent went
	// quiet in the same breath as a message about what it is working on.
	for id := range state.Pending {
		if id != incident && strings.HasPrefix(id, kind+"-") {
			delete(state.Pending, id)
		}
	}

	return true
}

// The standing orders end every on-call session on a question, so a session that was
// briefed is one to start watching for an answer. The clock itself does not start here:
// it starts when a check first sees the agent actually blocked, which is minutes later
// and is the moment Tim's wait began.
//
// A new incident keeps the kind's clock and resets its steps: he has been unanswered
// since the first question either way, but the handover is a decision about an incident
// and each one gets its own.
func (s sweeper) expectAQuestion(state *statedir.State, now time.Time, kind, incident string, session OncallSession) {
	if state.Waiting == nil {
		state.Waiting = map[string]statedir.Waiting{}
	}

	w := state.Waiting[kind]
	if w.Incident != incident {
		w.Incident, w.Opened = incident, now.Unix()
		w.Steps, w.Default, w.Asked = nil, "", statedir.Asked{}

		// Both of these are one check's half of a two-check judgement about the incident that
		// has just been superseded: that it is about to fill the disk, and that it has stopped
		// by itself. Neither carries over, least of all the second — a writer that stopped and
		// started again is the thing that raised this one.
		w.Worsening, w.Clear = 0, 0

		// A prompt owed on the incident that has just been superseded is a prompt nothing
		// owes any more: the brief above has reached the agent, and the lead that was owed
		// says Tim has not answered a question that no longer exists. Sent against the new
		// incident it handed over a decision on this minute's alert the moment it arrived,
		// with the steps it came with, so the whole of the clock — reminder, warning, the
		// three hours — was recorded as spent before Tim had seen anything.
		w.Owed, w.OwedSteps = "", nil

		// And the three graces, all of them "when the agent was last seen in this state": it
		// has just been handed a new brief, so each of them starts from now rather than from
		// something it was doing about the incident before this one.
		w.Busy, w.Settled, w.Escs = 0, 0, 0
	}
	w.Tab = session.Tab
	if session.Cancelled {
		w.Nudged = now.Unix()
	}
	state.Waiting[kind] = w
}

func (s sweeper) brief(incident, details string) string {
	return fmt.Sprintf(`%s

Incident id: %s

To report to the channel Tim watches, write your message to a file and run:

  hachiko notify %s <file>

Nothing else you can run reaches that channel. He has already had one line saying what fired; if nothing reports within %d minutes hachiko sends its own raw details instead, and says that you did not report.`,
		details, incident, incident, int(s.cfg.OncallDeadline.Minutes()))
}

// Which pending incident each marker is a report on. `hachiko notify` leaves a marker
// rather than editing the state, because a sweep holds the lock across a herdr call and
// an `op run` and the session has no minutes to spend waiting for it.
//
// A session re-briefed by an escalation keeps the incident id it was first handed in its
// scrollback, and reports under it — so a marker with no pending incident of its own is
// a report on the newest pending incident of the same kind, which is the one that
// superseded it. Reading it any other way loses the report and then says the agent went
// quiet about the very thing it just answered.
func (s sweeper) resolveReports(state *statedir.State) map[string]string {
	reported := map[string]string{}

	for _, marker := range s.store.ReportedIDs() {
		if _, ok := state.Pending[marker]; ok {
			reported[marker] = marker
		}
	}

	for _, marker := range s.store.ReportedIDs() {
		if _, ok := state.Pending[marker]; ok {
			continue
		}
		// A marker for an incident that is already waiting on its question is that
		// session's second report, the outcome — not a late first report on whatever is
		// pending now. Reading it as one would close the new incident on the strength of a
		// message about the old one.
		if w, ok := state.Waiting[kindOf(marker)]; ok && w.Incident == marker {
			continue
		}

		newest := newestPending(state.Pending, kindOf(marker))
		if newest == "" {
			continue
		}
		if _, taken := reported[newest]; !taken {
			reported[newest] = marker
		}
	}

	return reported
}

func newestPending(pending map[string]statedir.Pending, kind string) string {
	newest, openedAt := "", int64(-1)

	for _, id := range statedir.SortedKeys(pending) {
		if at := pending[id].OpenedAt; kindOf(id) == kind && at > openedAt {
			newest, openedAt = id, at
		}
	}
	return newest
}

func kindOf(incident string) string {
	kind, _, _ := strings.Cut(incident, "-")
	return kind
}

// Every incident a report could still arrive under: one that owes its first, and one
// whose session is on its question and owes the outcome.
func stillExpected(state *statedir.State) map[string]bool {
	keep := map[string]bool{}
	for id := range state.Pending {
		keep[id] = true
	}
	for _, w := range state.Waiting {
		keep[w.Incident] = true
	}
	return keep
}

// A marker for an incident nothing is waiting on any more is dropped, which is right: the
// report itself has already gone to the channel. An outcome dropped in silence is not. The
// line `hachiko notify` prints goes to the session's own pane, so an outcome that arrives
// after the wait on it has gone leaves nothing anywhere a person would look.
func (s sweeper) sayDroppedOutcomes(keep map[string]bool) {
	for _, id := range s.store.ReportedIDs() {
		if keep[id] || !s.store.ReportedOutcome(id) {
			continue
		}
		s.say("the on-call session reported the outcome of %s, which nothing was waiting on any more", id)
	}
}

func (s sweeper) rememberFallback(state *statedir.State, incident, fallback string) {
	kind := kindOf(incident)
	if w, ok := state.Waiting[kind]; ok && w.Incident == incident && fallback != "" {
		w.Default = fallback
		state.Waiting[kind] = w
	}
}

// The guarantee behind the session: it needs herdr, a Claude login and usage left,
// and a watch that only ever spoke through it would be silent exactly when that
// chain broke.
func (s sweeper) chaseLateReports(state *statedir.State, now time.Time) {
	reported := s.resolveReports(state)

	for _, id := range statedir.SortedKeys(state.Pending) {
		p := state.Pending[id]

		if from, ok := reported[id]; ok {
			if from == id {
				s.say("the on-call session reported on %s", id)
			} else {
				s.say("the on-call session reported on %s, which %s superseded", from, id)
			}
			// The one line of the report the wait has a use for, taken before the marker
			// goes: the session is about to ask its question, and this is what it said it
			// would do if nobody answered it.
			s.rememberFallback(state, id, s.store.ReportedFallback(from))
			delete(state.Pending, id)
			s.store.ClearReported(from)
			s.store.ClearReported(id)
			continue
		}

		waited := now.Sub(time.Unix(p.OpenedAt, 0))
		if waited < s.cfg.OncallDeadline {
			continue
		}

		late := wording.Lead(wording.MarkerDegraded, fmt.Sprintf("No report from the agent on %s after %s",
			wording.IncidentWords(kindOf(id)), wording.DurationPhrase(waited))).
			Block(p.Details).
			Can(wording.AttachAction(s.cfg, p.Tab)).
			About(id, s.cfg.Host).
			String()

		if err := s.send(state, id, late); err != nil {
			s.say("the on-call session has not reported on %s and the raw details did not send either: %v", id, err)
			continue
		}

		s.say("the on-call session has not reported on %s; sent the raw details instead", id)
		delete(state.Pending, id)
	}
}
