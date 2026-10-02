package main

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
	"time"
)

type sweeper struct {
	cfg   Config
	deps  Deps
	store Store
	dry   bool
}

func (s sweeper) say(format string, args ...any) {
	logger{out: s.deps.Log, now: s.deps.Now}.say(format, args...)
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
		case errors.Is(err, errLockHeld):
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
	switch {
	case errors.Is(err, errStateCorrupt):
		s.say("%v", err)
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

	headline := disk.headline
	if headline == "" {
		headline = cpu.headline
	}
	if headline == "" && lowNow {
		headline = fmt.Sprintf("Disk: only %s GB free", gbStr(free))
	}
	if headline == "" && truncated {
		headline = fmt.Sprintf("Disk: truncated %s, %s GB free", clip(disk.truncatedPath, pathLimit), gbStr(free))
	}

	newIncident := disk.fired || cpu.fired || lowNow || truncated

	// The full detail, which the first message carries when it is urgent and the
	// session's own report carries otherwise.
	details := fmt.Sprintf("hachiko on %s: %s GB free", s.cfg.Host, gbStr(free))
	if level != 0 {
		details += fmt.Sprintf(", under the %d GB threshold", level)
	}
	details += "." + disk.report + cpu.report + disk.truncated

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
			s.say("would open an on-call session as %s and send: %s", kind, headline)
		}
		s.say("%s GB free, %d file(s) growing fast, %d over %s GB, %d process(es) hot of %d sampled, %d director(ies) skipped for not answering",
			gbStr(free), len(disk.growing), len(disk.sizes), gbStr(s.cfg.BigKB), len(cpu.hot), cpu.sampled, len(disk.stalled))
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
		raised = s.raise(state, now, kind, headline, details, free, truncated)
	}

	s.chaseLateReports(state, now)

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

	state.Disk = DiskSample{At: now.Unix(), Files: disk.sizes}
	state.CPU = cpu.sample
	state.StalledDirs = disk.stalled

	s.store.ForgetReportedExcept(state.Pending)

	return s.store.Save(state)
}

type diskFindings struct {
	sizes         map[string]int64
	growing       []Growing
	stalled       []string
	report        string
	truncated     string
	truncatedPath string
	headline      string
	fired         bool
	stillGoing    []string
	fresh         []string
}

func (s sweeper) disk(state *State, now time.Time, free int64) diskFindings {
	had := state.hasDiskSample()
	prevAt := now
	if had {
		prevAt = time.Unix(state.Disk.At, 0)
	}
	span := now.Sub(prevAt)
	if span <= 0 {
		span = time.Second
	}

	out := diskFindings{}
	walk := s.deps.BigFiles(state.StalledDirs)

	// Once each, and then remembered: a directory that will not answer is one this has
	// nothing more to say about, and a line every five minutes forever would bury the
	// lines that matter.
	for _, dir := range walk.Stalled {
		if !contains(state.StalledDirs, dir) {
			s.say("%s did not answer a read within %s, so it is skipped from here on", dir, s.cfg.DirTimeout)
		}
	}
	out.stalled = mergeSorted(state.StalledDirs, walk.Stalled)

	if walk.CutShort {
		s.say("the walk ran out of its %s, so this check saw only part of the disk", s.cfg.WalkTimeout)
	}

	sizes, growing := growth(state.Disk.Files, walk.Files, s.cfg.GrowthKB, had)
	out.sizes, out.growing = sizes, growing

	for _, g := range growing {
		// A path and an lsof command name are both chosen by whatever filled the disk,
		// and both end up in a message Discord caps and in a prompt an agent reads.
		line := fmt.Sprintf("%s — %s GB, grew %s GB since the last sample (%s GB/hour), written by %s",
			clip(g.Path, pathLimit), gbStr(g.KB), gbStr(g.GrewKB), rateStr(g.GrewKB, span),
			clip(s.deps.Writers(g.Path), writerLimit))
		out.report += "\n  " + line

		if !s.dry && state.alertedFile(g.Path) {
			out.stillGoing = append(out.stillGoing, g.Path)
			continue
		}

		s.say("growing fast: %s", line)
		if s.dry {
			continue
		}

		out.fired = true
		out.fresh = append(out.fresh, g.Path)
		if out.headline == "" {
			out.headline = fmt.Sprintf("Disk: %s growing %s GB/h, %s GB free",
				clip(g.Path, pathLimit), rateStr(g.GrewKB, span), gbStr(free))
		}
	}

	// The fastest grower or nothing: the biggest offender is the one worth a
	// truncate, and a check that worked its way down the list would eventually reach
	// a file that is somebody's.
	if free < s.cfg.CriticalKB && len(growing) > 0 {
		worst := growing[0].Path

		switch {
		case !truncatable(s.cfg, worst):
			s.say("free space is under %d GB and %s is the fastest growing, but it is not a log this may truncate",
				s.cfg.CriticalGB(), clip(worst, pathLimit))
		case s.dry:
			s.say("would truncate %s", clip(worst, pathLimit))
		default:
			// Never an rm and never a kill: the writer keeps its descriptor and its
			// offset, so a log it appends to goes on working and the space comes back
			// at once, where an unlinked file frees nothing until the writer exits and
			// a killed worker takes a session's work with it.
			if err := s.deps.Truncate(worst); err != nil {
				s.say("could not truncate %s: %v", clip(worst, pathLimit), err)
			} else {
				s.say("truncated %s to keep the disk alive; its writer was not touched", clip(worst, pathLimit))
				out.truncated += "\n  truncated " + clip(worst, pathLimit)
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
	sample     CPUSample
	hot        []Hot
	sampled    int
	report     string
	headline   string
	fired      bool
	stillGoing []string
	fresh      []string
}

func (s sweeper) cpu(state *State, now time.Time) cpuFindings {
	procs, err := s.deps.Processes()
	if err != nil {
		s.say("there is no process sample for this check: %v", err)
		return cpuFindings{sample: state.CPU}
	}

	sample, hot := cpuHot(state.CPU, procs, now, s.cfg.CPUShare, s.cfg.CPUWindow)
	out := cpuFindings{sample: sample, hot: hot, sampled: len(procs)}

	mine := ownTree(procs, s.deps.Getpid())
	allowlist := readAllowlist(s.cfg.CPUAllowPath)

	for _, h := range hot {
		p := h.Process
		if mine[p.PID] || allowed(allowlist, p.Path(), p.Name()) {
			continue
		}

		line := fmt.Sprintf("pid %d %s — %.0f%% of a core for %s, up %s, %s MB resident, ppid %d",
			p.PID, p.Name(), h.Share, hmStr(h.HotFor(now)), hmStr(now.Sub(p.StartedAt)), mbStr(p.RSSKB), p.PPID)
		if p.PPID == 1 {
			line += " (orphaned)"
		}

		if cwd := s.deps.CWD(p.PID); cwd != "" {
			line += ", cwd " + clip(cwd, pathLimit)
			if where := repoOf(s.cfg, cwd); where != "" {
				line += ", in " + where
				// The shape that caused the incident this exists for: a worker whose
				// session ended, reparented to launchd and still spending a core on
				// work nobody wants.
				if p.PPID == 1 {
					line += " — orphaned inside a checkout, so the session that started it is gone"
				}
			}
		}

		line += "\n    " + clip(p.Command, argsLimit)
		out.report += "\n  " + line

		key := p.Key()
		if !s.dry && state.alertedProc(key) {
			out.stillGoing = append(out.stillGoing, key)
			continue
		}

		s.say("hot for an hour: %s", line)
		if s.dry {
			continue
		}

		out.fired = true
		out.fresh = append(out.fresh, key)
		if out.headline == "" {
			out.headline = fmt.Sprintf("CPU: %s pid %d at %.0f%% of a core for %s",
				p.Name(), p.PID, h.Share, hmStr(h.HotFor(now)))
		}
	}

	return out
}

// Every string in a message or a prompt that something other than hachiko chose: a
// path a worker made up, an lsof command name, a command line. Discord caps a message
// at 2,000 characters, and one of these at a megabyte would be the whole of it.
const (
	pathLimit   = 200
	writerLimit = 200
	argsLimit   = 200
)

func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// The order the alert goes out in: the session first, so the message can name the
// tab it is waiting in, then one line from hachiko. The brief carries the incident
// id and the one command that reaches the channel, because the session is never
// handed the webhook itself.
func (s sweeper) raise(state *State, now time.Time, kind, headline, details string, free int64, truncated bool) bool {
	incident := fmt.Sprintf("%s-%d", kind, now.Unix())

	session, err := s.deps.Oncall(kind, s.brief(incident, details))
	if err != nil {
		s.say("no on-call session was opened: %v", err)
		session = OncallSession{}
	}

	// Urgent is free space already critical, or a file this truncated: either way Tim
	// needs the whole of it now rather than when an agent has finished reading. So is a
	// session the brief never reached, since nothing is going to read it for him.
	message := headline
	if free < s.cfg.CriticalKB || truncated || !session.Delivered {
		message = details
	}

	if session.Say != "" {
		message += "\n\n" + session.Say
	} else {
		message += "\n\nOn-call session could not start."
	}

	// A failed send must leave the incident unraised, so the next check tries again
	// rather than going quiet about it.
	if err := s.deps.Send(message); err != nil {
		s.say("the alert did not send and is left to the next check: %v", err)
		return false
	}

	// Only a session the brief reached has a report to wait for. One that is blocked on
	// its own question, or that never got the brief, has already had the whole of it
	// sent on its behalf.
	if session.Delivered {
		if state.Pending == nil {
			state.Pending = map[string]Pending{}
		}
		state.Pending[incident] = Pending{OpenedAt: now.Unix(), Tab: session.Tab, Details: details}
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
func (s sweeper) resolveReports(state *State) map[string]string {
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

func newestPending(pending map[string]Pending, kind string) string {
	newest, openedAt := "", int64(-1)

	for _, id := range sortedKeys(pending) {
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

// The guarantee behind the session: it needs herdr, a Claude login and usage left,
// and a watch that only ever spoke through it would be silent exactly when that
// chain broke.
func (s sweeper) chaseLateReports(state *State, now time.Time) {
	reported := s.resolveReports(state)

	for _, id := range sortedKeys(state.Pending) {
		p := state.Pending[id]

		if from, ok := reported[id]; ok {
			if from == id {
				s.say("the on-call session reported on %s", id)
			} else {
				s.say("the on-call session reported on %s, which %s superseded", from, id)
			}
			delete(state.Pending, id)
			s.store.ClearReported(from)
			s.store.ClearReported(id)
			continue
		}

		waited := now.Sub(time.Unix(p.OpenedAt, 0))
		if waited < s.cfg.OncallDeadline {
			continue
		}

		late := fmt.Sprintf(`%s

The on-call agent has not reported after %d minutes, so these are hachiko's own raw details.
Attach: herdr workspace %s, tab %s`, p.Details, int(waited.Minutes()), s.cfg.WorkspaceLabel(), p.Tab)

		if err := s.deps.Send(late); err != nil {
			s.say("the on-call session has not reported on %s and the raw details did not send either: %v", id, err)
			continue
		}

		s.say("the on-call session has not reported on %s; sent the raw details instead", id)
		delete(state.Pending, id)
	}
}
