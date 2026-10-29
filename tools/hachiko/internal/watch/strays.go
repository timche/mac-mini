package watch

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/process"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// Stray is a process a session started and nothing is waiting on any more: this account's,
// reparented to launchd, no launchd job and no app of its own, and sitting in a checkout, a
// worktree or the scratch folder Claude Code gives a session.
//
// What this exists for is the leak Claude Code has on macOS: an MCP server, a subagent or a
// Bash-tool child outlives the session that started it and is reparented to launchd, where
// nothing ever reaps it ([anthropics/claude-code#33947](https://github.com/anthropics/claude-code/issues/33947),
// closed as not planned). Neither of the other two watches sees one. The CPU watch wants
// half a core for an hour, and an abandoned language server sits idle holding a gigabyte;
// `hachiko gc` kills what a *removed* worktree left behind, and a session that ended in a
// worktree somebody is still working in removed nothing.
type Stray struct {
	Process process.Process
	Dir     string

	// What the directory says started it, in the words a message uses: a worktree, a
	// checkout, or the scratch folder of a session. This is the half that makes it a session's
	// leftover rather than a daemon of this account's, so it is never empty.
	Where string

	Since time.Time
}

func (s Stray) LeftFor(now time.Time) time.Duration { return now.Sub(s.Since) }

// Which processes look left behind, by the tests that together mean "a session started this
// and the session is gone". Every one of them is a reading rather than a judgement, which is
// why this reports and kills nothing: a worker whose session ended is indistinguishable from
// a worker somebody meant to leave running, and the difference is in the transcripts an
// on-call agent reads and not in a ps.
func straysAmong(cfg config.Config, procs []process.Process, jobs, mine map[int]bool, uid int, allow []string, cwd func(pid int) string) []Stray {
	var found []Stray

	for _, p := range procs {
		switch {
		// Never pid 1, never anything between this process and it, and only this account's:
		// a daemon of root's with a parent of launchd is macOS starting a daemon.
		case p.PID <= 1, p.PPID != 1, mine[p.PID], p.UID != uid,
			// launchd's own, which is the test that makes a parent of launchd mean anything at
			// all — the agents this Mac installs and the apps LaunchServices started are both in
			// that list, and both have ppid 1.
			jobs[p.PID],
			shipped(p.Path()),
			allowed(allow, p.Path(), p.Name()):
			continue
		}

		dir := cwd(p.PID)
		if dir == "" {
			continue
		}

		where, ok := startedBy(cfg, dir, uid)
		if !ok {
			continue
		}

		found = append(found, Stray{Process: p, Dir: dir, Where: where})
	}
	return found
}

// An app, a helper inside one, or anything macOS ships. Most of what a ps of this account
// answers with has a parent of launchd and no job of its own — XPC services, widget
// extensions, a Spotlight importer — and an app's own helper processes are the same: the
// 5 GB helper that runs OrbStack's Linux guest is in the bundle beside the app launchd is
// running. None of them is a session's to leave behind.
//
// /usr/local is deliberately not here: it is where this Mac's own tooling installs, which
// is exactly where a leftover worker's interpreter lives.
func shipped(path string) bool {
	for _, inside := range []string{".app/", ".appex/", ".xpc/"} {
		if strings.Contains(path, inside) {
			return true
		}
	}
	for _, root := range []string{"/System/", "/Library/", "/bin/", "/sbin/",
		"/usr/bin/", "/usr/sbin/", "/usr/libexec/", "/usr/share/"} {
		if strings.HasPrefix(path, root) {
			return true
		}
	}
	return false
}

// What the working directory says started it, and false for one that says nothing. This is
// the test that makes a process a session's leftover rather than any daemon of this
// account's, and it is the same set of roots `hachiko gc` sweeps: the two worktree roots, a
// checkout under the projects root, and the folder Claude Code gives a session to keep its
// own files in. A process that is not in one of those was not started by a session working
// on something, whatever else is true of it.
func startedBy(cfg config.Config, dir string, uid int) (string, bool) {
	if where := repoOf(cfg, dir); where != "" {
		return where, true
	}
	if _, ok := under(filepath.Join(cfg.ScratchRoot, fmt.Sprintf("claude-%d", uid)), dir); ok {
		return "a session's own scratch folder", true
	}
	return "", false
}

// How long each has looked left behind, carried forward from the last check the way the CPU
// window is. The span is the whole of what tells a leak from a session that has just exited,
// whose children are reparented to launchd for the moment it takes them to finish — and
// because it is a span of time rather than a count of checks, an interval the agent missed
// costs nothing.
func straysHeld(prev map[string]int64, found []Stray, now time.Time, window time.Duration) (map[string]int64, []Stray) {
	since := make(map[string]int64, len(found))
	var held []Stray

	for _, s := range found {
		key := s.Process.Key()

		at := prev[key]
		if at == 0 {
			at = now.Unix()
		}
		since[key] = at

		s.Since = time.Unix(at, 0)
		if now.Sub(s.Since) >= window {
			held = append(held, s)
		}
	}
	return since, held
}

type strayFindings struct {
	since map[string]int64

	// Whether both readings this needs came back. A process sample without launchctl's own
	// list of jobs is not a reading of what was left behind but a reading in which every
	// agent and every app of this account's looks abandoned, so it is the absence of one.
	read   bool
	strays []Stray

	// The `Left behind` section, one bullet per process, and the bullet a short first alert
	// carries on its own.
	report      string
	firstBullet string

	sentence   string
	fired      bool
	stillGoing []string
	fresh      []string
}

func (s sweeper) strays(state *statedir.State, now time.Time, procs []process.Process, sampled bool) strayFindings {
	if !sampled {
		return strayFindings{since: state.Strays}
	}

	jobs, err := s.deps.LaunchdJobs()
	if err != nil {
		s.say("launchctl would not say which processes are its own jobs, so nothing watched for what a session left behind: %v", err)
		return strayFindings{since: state.Strays}
	}

	mine := process.OwnTree(procs, s.deps.Getpid())

	// The CPU watch's own file: a command Tim has written down as expected to be running here
	// is not one either watch has anything to say about, and a second list to keep would go
	// stale against the first.
	found := straysAmong(s.cfg, procs, jobs, mine, s.deps.Getuid(),
		readAllowlist(s.cfg.CPUAllowPath), s.deps.CWD)

	since, held := straysHeld(state.Strays, found, now, s.cfg.StrayWindow)
	out := strayFindings{since: since, read: true, strays: held}

	var bullets []string
	for _, st := range held {
		p := st.Process

		// A name and a path taken out of a command line, which is the attacker's half of this.
		name := wording.Safe(p.Name(), wording.NameLimit)
		dir := wording.Safe(st.Dir, wording.PathLimit)
		where := wording.Safe(st.Where, wording.PathLimit)

		line := fmt.Sprintf("pid %d %s — left behind for %s, up %s, %s MB resident, in %s (%s), ppid %d",
			p.PID, p.Name(), hmStr(st.LeftFor(now)), hmStr(now.Sub(p.StartedAt)),
			mbStr(p.RSSKB), dir, where, p.PPID)
		line += "\n    " + wording.Safe(p.Command, wording.ArgsLimit)

		bullet := fmt.Sprintf("%s — left behind for %s, %s memory, started %s, in %s (%s)",
			wording.ProcessLabel(name, p.PID), wording.DurationPhrase(st.LeftFor(now)),
			wording.GBUnit(p.RSSKB), wording.TimePhrase(p.StartedAt, now),
			wording.CodeSpan(dir), wording.PlainWords(where))

		// The command line on a continuation line of its own: it is the longest thing in any of
		// these and the one Tim scans rather than reads.
		bullet += "\n  " + wording.CodeSpan(wording.Safe(p.Command, wording.ArgsLimit))
		bullets = append(bullets, bullet)

		key := p.Key()
		if !s.dry && state.AlertedStray(key) {
			out.stillGoing = append(out.stillGoing, key)
			continue
		}

		s.say("left behind by a session that has ended: %s", line)
		if s.dry {
			continue
		}

		out.fired = true
		out.fresh = append(out.fresh, key)
		if out.firstBullet == "" {
			out.firstBullet = bullet
		}
	}

	out.report = wording.Section(wording.LabelStrays, bullets)
	if out.fired {
		out.sentence = strayLead(held)
	}
	return out
}

// The lead, which is the one line a notification shows: the process where there is one, and
// the count and what it is holding where there are several, since a hundred of them is the
// news rather than the first one's name. Over everything left behind rather than over what
// this check is first to notice — a second one appearing is a reason to say how many there
// are now.
func strayLead(held []Stray) string {
	if len(held) == 1 {
		return fmt.Sprintf("%s was left behind by a session that has ended",
			wording.PlainWords(wording.Safe(held[0].Process.Name(), wording.NameLimit)))
	}

	var kb int64
	for _, s := range held {
		kb += s.Process.RSSKB
	}
	return fmt.Sprintf("%s left behind by sessions that have ended, holding %s",
		wording.CountOf(int64(len(held)), "process", "processes"), wording.GBUnit(kb))
}
