package watch

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/logs"
	"github.com/timche/mac-mini/hachiko/internal/oncall"
	"github.com/timche/mac-mini/hachiko/internal/process"
	"github.com/timche/mac-mini/hachiko/internal/shibuya"
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

// Every message about an incident in one place: the first message of an incident opens a
// thread and everything after it goes inside, so a reminder three hours later is under the
// alert it is about rather than further down a channel — and the thread is what `hachiko
// listen` reads a reply out of.
func (s sweeper) send(state *statedir.State, incident, message string) error {
	out := discord.Outgoing{Text: message}

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

	// Inside the lock, because two sweeps capping the same log would race each other's
	// generation.
	s.capLogs(state)

	span, spanClamped := sampleSpan(state, now)
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

	newIncident := cpu.fired || lowNow

	alert := s.alert(cpu, free, level, lowNow)

	// Ahead of everything that follows, because this is the one thing here that is about
	// hachiko's own agents rather than about the machine, and because it is the half of the
	// sweep a dry run reaches too.
	if s.liveness(state, now)[statedir.AgentSync] {
		s.syncRepos(state, now)
	}

	// disk when free space fired, since that is the half with a deadline on it; the kind
	// only decides which session the incident goes to, and one session takes the whole of
	// a run either way.
	kind := "disk"
	if !lowNow && cpu.fired {
		kind = "cpu"
	}

	if s.dry {
		if level != 0 {
			s.say("would report %s GB free, under the %d GB threshold", gbStr(free), level)
		}
		if newIncident {
			s.say("would open an on-call session as %s and send: %s", kind, alert.lead())
		}
		s.say("%s GB free, %d process(es) hot of %d sampled", gbStr(free), len(cpu.hot), cpu.sampled)

		if s.cfg.PostKeep > 0 && s.cfg.Discord.ChannelID != "" {
			s.say("would look through the forum for posts resolved more than %s ago",
				wording.DurationPhrase(s.cfg.PostKeep))
		}

		s.updates(state, now)

		// Said rather than sent: a dry run that checked in would tell shibuya this Mac is
		// being watched on a run that watched nothing.
		if reasons := sweepFaults(corrupt, cpu); len(reasons) > 0 {
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
		// Free space already critical: Tim needs the whole of it now rather than when an
		// agent has finished reading.
		raised = s.raise(state, now, fmt.Sprintf("%s-%d", kind, now.Unix()), alert,
			free < s.cfg.CriticalKB)
	}

	// After the disk and the CPU, which are the halves with a deadline on them, and before
	// everything that chases an answer, so a step raised this minute is one the wait below
	// starts counting from.
	s.updates(state, now)

	// Whether what fired an open incident is still firing. A reading where it is not is the
	// other half of what the session needs: without it a daemon that stops spinning by
	// itself leaves a session waiting on a question about a process that is not there any
	// more, and nobody is told until somebody asks.
	//
	// A reading that is missing is not a reading that is clear, which is why the CPU half
	// is taken from a sample that was actually read: a check with no process sample saw no
	// processes at all. Free space is read before anything else here, and a sweep with no
	// reading of it has already returned.
	cleared := map[string]string{}
	if level == 0 {
		cleared["disk"] = fmt.Sprintf("Free space is back over every mark, at %s.", wording.GBUnit(free))
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
		span:        span,
		spanClamped: spanClamped,
		sections:    joinBlocks(cpu.report, cpu.sudo),
		cleared:     cleared,
	}

	s.chaseLateReports(state, now)
	s.chaseAnswers(state, now, reading)

	// What stays flagged: whatever was flagged before and is still going, plus what
	// this check raised, and only if the message actually left the machine.
	keptProcs := cpu.stillGoing
	if raised {
		keptProcs = append(keptProcs, cpu.fresh...)
	}

	state.AlertedProcs = keptProcs

	switch {
	case level == 0:
		state.LowSpaceLevel = 0
	case !lowNow || raised:
		state.LowSpaceLevel = level
	}

	state.Disk = statedir.DiskSample{At: now.Unix(), FreeKB: free}
	state.CPU = cpu.sample

	expected := stillExpected(state)
	s.sayDroppedOutcomes(expected)
	s.store.ForgetReportedExcept(expected)

	// A wait can end before the session reports, when its question went away with the
	// session, and the outcome it sends hours later belongs in the same thread.
	state.CloseThreads(expected, now, closedThreadKeep)

	// Once an hour, and after everything this check has to say has been said: a message
	// about an incident may not wait behind the forum's housekeeping.
	s.prunePosts(state, now)

	// Last, so that what shibuya is told about this Mac is what the check decided rather
	// than what it had got to — and so that a Worker that will not answer costs the sweep
	// its ten seconds after everything else is done.
	s.checkIn(state, corrupt, cpu, free, len(expected))

	return s.store.Save(state)
}

// What shibuya is told, and the one line about it. A sweep that ran is liveness whatever it
// found: the alert about a disk is hachiko's own, and all this says is that the watch is
// still running — or that it ran and did not finish its job, which is a Mac whose monitor is
// half blind and nothing a dead man's switch would ever notice by itself.
func (s sweeper) checkIn(state *statedir.State, corrupt bool, cpu cpuFindings, free int64, open int) {
	in := shibuya.Checkin{
		FreeGB:        gbNum(free),
		OpenIncidents: open,
		HotProcesses:  len(cpu.hot),
		Display:       s.cfg.Display,
	}

	if reasons := sweepFaults(corrupt, cpu); len(reasons) > 0 {
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
	if now == shibuya.Sent && was != "" {
		s.say("shibuya is hearing from this Mac again")
	}
}

// The honest set: the two ways a sweep runs and comes back knowing less than it should.
// Not an incident, because neither is a fault of the Mac's — they are faults of the watch,
// and the only thing that can report them is something off the machine.
func sweepFaults(corrupt bool, cpu cpuFindings) []string {
	var reasons []string

	if !cpu.read {
		reasons = append(reasons, "there was no process sample, so nothing was watching the CPU")
	}
	if corrupt {
		reasons = append(reasons, "the state file could not be read, so this check measured nothing against the last")
	}
	return reasons
}

// The interval this check measures against the last, and whether it is one the check had
// to invent. A span of nothing or less is not an interval, it is a clock that moved: the
// machine slept, somebody set the time, a sample carries a timestamp in the future. A
// second is enough to divide a loss by for a rate; it is not enough to extrapolate from,
// so the fact that it was invented travels with it.
func sampleSpan(state *statedir.State, now time.Time) (time.Duration, bool) {
	if !state.HasDiskSample() {
		return time.Second, true
	}
	if span := now.Sub(time.Unix(state.Disk.At, 0)); span > 0 {
		return span, false
	}
	return time.Second, true
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

		// What stopping it takes, said once and up front: the root helper's command, which the
		// session may run after a handover, or a sudo kill, which only Tim may. The whole of
		// the first dasd night turned on a fix that needed Tim and a message that never said so.
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
// a deadline on it; critical free space goes above merely low, because what the lead has to
// say first is how bad it already is.
func (s sweeper) alert(cpu cpuFindings, free, level int64, lowNow bool) alertText {
	out := alertText{marker: wording.MarkerDisk}

	switch {
	case free < s.cfg.CriticalKB && lowNow:
		out.marker = wording.MarkerDown
		out.sentence = fmt.Sprintf("Disk critical: %s free", wording.GBUnit(free))
	case lowNow:
		out.sentence = fmt.Sprintf("Low disk space: %s free", wording.GBUnit(free))
	case cpu.fired:
		out.marker, out.sentence = wording.MarkerBusy, cpu.sentence
	}

	// Free space belongs in a message the disk is part of and nowhere else: a busy daemon
	// says nothing about how much room is left.
	freeField := ""
	if out.marker != wording.MarkerBusy && out.sentence != "" {
		freeField = "**" + wording.LabelFreeSpace + ":** " + wording.GBUnit(free)
		if level != 0 {
			freeField += fmt.Sprintf(", under the %d GB mark", level)
		}
	}

	out.short = joinBlocks(freeField, wording.Section(wording.LabelBusy, nonEmpty(cpu.firstBullet)), cpu.sudo)
	out.full = joinBlocks(freeField, cpu.report, cpu.sudo)

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

// Sections with a blank line between them, and nothing for the ones there was nothing to
// say about: a message is never a form with blanks in it, and two findings that run
// together are two nobody reads to the end of.
func joinBlocks(blocks ...string) string { return strings.Join(nonEmpty(blocks...), "\n\n") }

// The order the alert goes out in: the session first, so the message can name the
// tab it is waiting in, then one line from hachiko. The brief carries the incident
// id and the one command that reaches the channel, because the session is never
// handed the bot token itself.
//
// The id arrives already minted, because the half of the check that raised it is the half
// that has to write down which incident it was: the kind and the time are the whole of an id
// and the kind is the half of it everything below reads back.
//
// Urgent is what the message to Tim carries: the whole of the detail now rather than when an
// agent has finished reading it, since a short form is only worth sending where somebody is
// going to read the rest for him.
func (s sweeper) raise(state *statedir.State, now time.Time, incident string, alert alertText, urgent bool) bool {
	kind := statedir.KindOf(incident)

	session, err := s.deps.Oncall(kind, s.brief(incident, alert.brief()))
	if err != nil {
		s.say("no on-call session was opened: %v", err)
		session = oncall.Session{}
	}

	// A session the brief never reached is urgent whatever fired, since nothing is going to
	// read it for him.
	blocks := alert.short
	if urgent || !session.Delivered {
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
func (s sweeper) expectAQuestion(state *statedir.State, now time.Time, kind, incident string, session oncall.Session) {
	if state.Waiting == nil {
		state.Waiting = map[string]statedir.Waiting{}
	}

	w := state.Waiting[kind]
	if w.Incident != incident {
		w.Incident, w.Opened = incident, now.Unix()
		w.Steps, w.Default, w.Asked = nil, "", statedir.Asked{}

		// One attempt per incident, and this is another incident: a disk that filled again
		// after the agent emptied a log is a fresh fault with a fresh reading behind it, and
		// the fix that did not hold is one of the things the session now knows.
		w.Attempted = false

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
		if w, ok := state.Waiting[statedir.KindOf(marker)]; ok && w.Incident == marker {
			continue
		}

		newest := newestPending(state.Pending, statedir.KindOf(marker))
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
		if at := pending[id].OpenedAt; statedir.KindOf(id) == kind && at > openedAt {
			newest, openedAt = id, at
		}
	}
	return newest
}

// Every incident a report could still arrive under: one that owes its first, and one
// whose session is on its question and owes the outcome.
const closedThreadKeep = 7 * 24 * time.Hour

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
	kind := statedir.KindOf(incident)
	if w, ok := state.Waiting[kind]; ok && w.Incident == incident && fallback != "" {
		w.Default = fallback
		state.Waiting[kind] = w
	}
}

// And the same for the attempt, taken before the marker goes. The session's first report is
// also where it says what it is about to try, so that one message carries both flags as often
// as not — and this is the one marker a sweep consumes and deletes, which would have left the
// attempt recorded nowhere and the next prompt handing it another.
func (s sweeper) rememberAttempt(state *statedir.State, incident string, attempt bool) {
	if !attempt {
		return
	}

	kind := statedir.KindOf(incident)
	if w, ok := state.Waiting[kind]; ok && w.Incident == incident && !w.Attempted {
		w.Attempted = true
		state.Waiting[kind] = w
		s.say("the %s on-call agent is making its one attempt at fixing %s, so nothing hands it another",
			kind, incident)
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

			// The attempt only where the marker is this incident's own, which the fallback
			// deliberately is not: an option the session named under the incident this one
			// superseded is still the option it would take, where an attempt it made against
			// that incident is spent against that incident. A fresh one has a fresh reading
			// behind it and gets its own attempt, which is what expectAQuestion resets.
			if from != id && s.store.ReportedAttempt(from) {
				s.say("the attempt the %s on-call agent reported was against %s, which %s superseded, so this incident keeps its own",
					statedir.KindOf(id), from, id)
			}
			s.rememberAttempt(state, id, from == id && s.store.ReportedAttempt(from))
			delete(state.Pending, id)

			// Unless the same message was the outcome, which chaseAnswers reads straight after
			// this and only while the marker is there. A session that fixes what it found,
			// verifies it and closes inside one five-minute interval is the fix-first path
			// working exactly as intended, and clearing the marker here left the wait running
			// on an incident that was over, the post unresolved and nothing in the log saying
			// an outcome had arrived.
			if !s.store.ReportedOutcome(from) {
				s.store.ClearReported(from)
				s.store.ClearReported(id)
			}
			continue
		}

		waited := now.Sub(time.Unix(p.OpenedAt, 0))
		if waited < s.cfg.OncallDeadline {
			continue
		}

		late := wording.Lead(wording.MarkerDegraded, fmt.Sprintf("No report from the agent on %s after %s",
			wording.IncidentWords(statedir.KindOf(id)), wording.DurationPhrase(waited))).
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
