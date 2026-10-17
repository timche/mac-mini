package watch

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"syscall"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
)

// The whole of what may be capped, by exact path and never by pattern: each of these is this
// machine's own log, named by a plist or a wrapper in this repository. Anything not on the
// list is somebody's — a log under a worktree sits beside a build somebody is waiting on —
// and the free-space thresholds are what catch one of those.
//
// Every one of them is held open by the shell in its plist, which opened it with `>>` and
// keeps it for the length of a run: the herdr server and the two hachiko agents that never
// finish one, and the two sweeps for as long as each takes. So each is copied and then
// emptied in place rather than renamed out from under its writer, which is what newsyslog
// does and which against a held descriptor is worse than doing nothing — the writer goes on
// appending to the archive and the empty file left at the path never grows enough to be
// rotated again.
func cappedLogs(cfg config.Config) []string {
	under := func(name string) string { return filepath.Join(cfg.LogsRoot, name) }

	return []string{
		under("herdr.log"),
		under("hachiko-sync.log"),
		under("hachiko-listen.log"),
		under("hachiko.log"),
		under("hachiko-gc.log"),
	}
}

// The same list for anything that only wants to know which paths are bounded: `hachiko
// status` prints each one's size against the cap, and a second list there would be a screen
// that leaves out the log the drift is in.
func CappedLogPaths(cfg config.Config) []string {
	return cappedLogs(cfg)
}

// One stat per file, which is the whole cost on a Mac with nothing to cap — every sweep but
// a handful. No Discord either way: a log this bounds is hygiene rather than news, and a
// message every time herdr's log filled would be a message nobody can act on.
func (s sweeper) capLogs(state *statedir.State) {
	for _, path := range cappedLogs(s.cfg) {
		s.capLog(state, path)
	}
}

func (s sweeper) capLog(state *statedir.State, path string) {
	// Lstat and not Stat. These are account-owned paths and hachiko runs as the account, so
	// there is no privilege to be had through one of them — but a link at either path is
	// still a write going somewhere nobody asked for, and the cheap refusal is not to look
	// through it in the first place.
	info, err := os.Lstat(path)
	if err != nil {
		s.capFine(state, path)
		return
	}

	kb := info.Size() / 1024
	kept := path + ".0"

	switch {
	case info.Mode()&os.ModeSymlink != 0:
		s.capTrouble(state, path, "%s is a symlink, so nothing of it was capped", path)
		return
	case !info.Mode().IsRegular():
		s.capFine(state, path)
		return
	case kb <= s.cfg.LogCapKB:
		s.capFine(state, path)
		return
	}

	// Every path a cap writes, each of which a link could be waiting at: the generation, and
	// the file the copy fills before it becomes one.
	for _, beside := range []string{kept, kept + ".tmp"} {
		if was, err := os.Lstat(beside); err == nil && was.Mode()&os.ModeSymlink != 0 {
			s.capTrouble(state, path, "%s is a symlink, so %s was not capped", beside, path)
			return
		}
	}

	if s.dry {
		s.say("would cap %s at %s MB, keeping what is there now as %s", path, mbStr(kb), kept)
		return
	}

	if err := copyAndTruncate(path, kept); err != nil {
		s.capTrouble(state, path, "%s is %s MB and could not be capped: %v", path, mbStr(kb), err)
		return
	}
	s.capFine(state, path)
	s.say("capped %s at %s MB; what was there is in %s", path, mbStr(kb), kept)
}

// Said once per log and not again until the log is capped or is nothing to complain about.
// A link somebody left at one of these paths, and a copy that failed because the disk is
// full, are both still there five minutes later: this runs twelve times an hour for ever,
// and twelve identical lines would bury the lines that matter.
//
// A dry run says it and remembers nothing, being a session asking rather than the agent.
func (s sweeper) capTrouble(state *statedir.State, path, format string, args ...any) {
	if s.dry {
		s.say(format, args...)
		return
	}
	if statedir.Contains(state.CapTrouble, path) {
		return
	}

	s.say(format, args...)
	state.CapTrouble = append(state.CapTrouble, path)
	sort.Strings(state.CapTrouble)
}

// The complaint forgotten, so the next thing to go wrong with this log is said again.
func (s sweeper) capFine(state *statedir.State, path string) {
	if s.dry || !statedir.Contains(state.CapTrouble, path) {
		return
	}

	keep := make([]string, 0, len(state.CapTrouble))
	for _, was := range state.CapTrouble {
		if was != path {
			keep = append(keep, was)
		}
	}
	state.CapTrouble = keep
}

// One generation kept, replacing whatever was there: two of them bound a log at twice the
// threshold, and the one before this is the half of a runaway an incident is usually in.
//
// The single descriptor is the whole of the safety at the source: the file that is read is
// the file that is emptied, so a link swapped in between the two cannot be what gets emptied,
// and O_NOFOLLOW is what refuses one swapped in between the Lstat above and this.
//
// The copy fills a file of its own and only becomes the generation on the way out, because
// what makes a log worth capping — a disk with nothing left — is also what makes the copy
// fail halfway. Writing into the generation directly meant a failed copy had already
// destroyed the one good copy of the log and left a truncated half of it in its place, which
// is the worst possible moment to lose it.
//
// Between the last byte copied and the truncate the writer may append a line, and the
// truncate throws it away. Accepted: the window is microseconds, these are logs read only
// when something is wrong, and the alternative is stopping the writer — which for herdr is
// every session on this Mac and for sync is the thing that publishes the work. The writer
// opened with `>>`, so its next write lands at nothing rather than leaving a sparse hole the
// size of what was emptied.
func copyAndTruncate(path, kept string) error {
	from, err := os.OpenFile(path, os.O_RDWR|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer from.Close()

	info, err := from.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is no longer a regular file", path)
	}

	tmp := kept + ".tmp"

	// One left behind by a run that was killed between the copy and the rename. It is
	// hachiko's own name and nothing else writes it, so a plain file there is this sweep's to
	// take back; anything that is not — a link, a directory somebody made — is left where it
	// is and the exclusive create below is what refuses to go round it.
	if was, err := os.Lstat(tmp); err == nil && was.Mode().IsRegular() {
		os.Remove(tmp)
	}

	// The log's own permissions, so a generation is readable by exactly whoever could read
	// the log it came out of and no wider.
	to, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, info.Mode().Perm())
	if err != nil {
		return err
	}

	if _, err := io.Copy(to, from); err != nil {
		to.Close()
		os.Remove(tmp)
		return err
	}
	if err := to.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, kept); err != nil {
		os.Remove(tmp)
		return err
	}

	return from.Truncate(0)
}
