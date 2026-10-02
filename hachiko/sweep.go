package main

import (
	"fmt"
	"strings"
	"time"
)

type sweeper struct {
	cfg   Config
	deps  Deps
	store Store
	dry   bool
}

// One line per thing that happened and nothing at all on a quiet check: this runs
// every five minutes forever, into a log somebody reads only when something is
// wrong.
func (s sweeper) say(format string, args ...any) {
	fmt.Fprintf(s.deps.Log, "%s hachiko: %s\n",
		s.deps.Now().Format("2006-01-02T15:04:05-0700"), fmt.Sprintf(format, args...))
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
		lock, ok, takenFrom := s.store.Acquire(5*time.Minute, now)
		if !ok {
			return nil
		}
		defer lock.Release()

		if takenFrom != "" {
			s.say("taking over a lock left behind by %s", takenFrom)
		}
	}

	state, err := s.store.Load()
	if err != nil {
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

	headline := disk.headline
	if headline == "" {
		headline = cpu.headline
	}
	if headline == "" && lowNow {
		headline = fmt.Sprintf("Disk: only %s GB free", gbStr(free))
	}

	newIncident := disk.fired || cpu.fired || lowNow

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
	if !disk.fired && !lowNow && cpu.fired {
		kind = "cpu"
	}

	if s.dry {
		if level != 0 {
			s.say("would report %s GB free, under the %d GB threshold", gbStr(free), level)
		}
		if newIncident {
			s.say("would open an on-call session as %s and send: %s", kind, headline)
		}
		s.say("%s GB free, %d file(s) growing fast, %d over %s GB, %d process(es) hot of %d sampled",
			gbStr(free), len(disk.growing), len(disk.sizes), gbStr(s.cfg.BigKB), len(cpu.hot), cpu.sampled)
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
		raised = s.raise(state, now, kind, headline, details, free, disk.truncated != "")
	}

	s.chaseLateReports(state, now)

	// What stays flagged: whatever was flagged before and is still going, plus what
	// this check raised, and only if the message actually left the machine.
	keptFiles, keptProcs := disk.stillGoing, cpu.stillGoing
	if raised {
		keptFiles = append(keptFiles, disk.fresh...)
		keptProcs = append(keptProcs, cpu.fresh...)
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

	return s.store.Save(state)
}

type diskFindings struct {
	sizes      map[string]int64
	growing    []Growing
	report     string
	truncated  string
	headline   string
	fired      bool
	stillGoing []string
	fresh      []string
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

	sizes, growing := growth(state.Disk.Files, s.deps.BigFiles(), s.cfg.GrowthKB, had)
	out := diskFindings{sizes: sizes, growing: growing}

	for _, g := range growing {
		line := fmt.Sprintf("%s — %s GB, grew %s GB since the last sample (%s GB/hour), written by %s",
			g.Path, gbStr(g.KB), gbStr(g.GrewKB), rateStr(g.GrewKB, span), s.deps.Writers(g.Path))
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
				g.Path, rateStr(g.GrewKB, span), gbStr(free))
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
				s.cfg.CriticalGB(), worst)
		case s.dry:
			s.say("would truncate %s", worst)
		default:
			// Never an rm and never a kill: the writer keeps its descriptor and its
			// offset, so a log it appends to goes on working and the space comes back
			// at once, where an unlinked file frees nothing until the writer exits and
			// a killed worker takes a session's work with it.
			if err := s.deps.Truncate(worst); err != nil {
				s.say("could not truncate %s: %v", worst, err)
			} else {
				s.say("truncated %s to keep the disk alive; its writer was not touched", worst)
				out.truncated += "\n  truncated " + worst
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
			line += ", cwd " + cwd
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

		line += "\n    " + clip(p.Command, 200)
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

	tab, err := s.deps.Oncall(kind, s.brief(incident, details))
	if err != nil {
		s.say("no on-call session was opened: %v", err)
		tab = ""
	}

	// Urgent is free space already critical, or a file this truncated: either way Tim
	// needs the whole of it now rather than when an agent has finished reading.
	message := headline
	if free < s.cfg.CriticalKB || truncated || tab == "" {
		message = details
	}

	if tab != "" {
		message += fmt.Sprintf("\n\nAn agent is looking into it in herdr (workspace %s, tab %s); details to follow.",
			s.cfg.WorkspaceLabel(), tab)
	} else {
		message += "\n\nOn-call session could not start."
	}

	// A failed send must leave the incident unraised, so the next check tries again
	// rather than going quiet about it.
	if err := s.deps.Send(message); err != nil {
		s.say("the alert did not send and is left to the next check: %v", err)
		return false
	}

	if tab != "" {
		if state.Pending == nil {
			state.Pending = map[string]Pending{}
		}
		state.Pending[incident] = Pending{OpenedAt: now.Unix(), Tab: tab, Details: details}
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

// The guarantee behind the session: it needs herdr, a Claude login and usage left,
// and a watch that only ever spoke through it would be silent exactly when that
// chain broke.
func (s sweeper) chaseLateReports(state *State, now time.Time) {
	for _, id := range sortedKeys(state.Pending) {
		p := state.Pending[id]

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
