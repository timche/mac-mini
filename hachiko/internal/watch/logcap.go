package watch

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/timche/mac-mini/hachiko/internal/config"
)

// How one log is capped, which is decided by who holds it open and not by anything about the
// file. A rename-and-recreate is what newsyslog does, and against a writer that holds the
// descriptor it is worse than doing nothing: the writer goes on appending to the archive and
// the empty file left at the path never grows enough to be rotated again. So a log with a
// long-running writer behind it is copied and then emptied in place instead, and one whose
// writer reopens the path every run is renamed, which has no window at all.
type capHow int

const (
	capCopy capHow = iota
	capRename
)

type cappedLog struct {
	path string
	how  capHow
}

// The whole of what may be capped, by exact path and never by pattern: each of these is this
// machine's own log, named by a plist or a wrapper in this repository, and the classification
// is which of those writes it and how. Anything not on the list is somebody's — a log under a
// worktree sits beside a build somebody is waiting on — and the free-space thresholds are
// what catch one of those.
func cappedLogs(cfg config.Config) []cappedLog {
	under := func(name string) string { return filepath.Join(cfg.LogsRoot, name) }

	return []cappedLog{
		// Held open for the life of the Mac: the herdr server and the two hachiko agents that
		// never finish a run, each opened with `>>` by the shell in its plist, and the
		// ssh-agent wrapper, which opens its own with `exec >>` when nobody is watching.
		{path: under("herdr.log"), how: capCopy},
		{path: under("hachiko-sync.log"), how: capCopy},
		{path: under("hachiko-listen.log"), how: capCopy},
		{path: under("ssh-agent.log"), how: capCopy},

		// This sweep's own stdout, which its plist opens with `>>` for each five-minute run.
		// Between runs there is nothing to orphan and a rename would be safe, but the process
		// doing the capping is the one holding it: a rename would send the rest of this
		// sweep's lines to the archive, the line saying it had capped anything among them.
		{path: under("hachiko.log"), how: capCopy},

		// The one that is genuinely reopened: launchd opens it itself from the gc agent's
		// StandardOutPath, once per ten-minute run, and the sweep exits between them. A
		// rename during a run that is still going costs that run's remaining lines to the
		// archive, which is where rotation always leaves the most recent ones.
		{path: under("hachiko-gc.log"), how: capRename},
	}
}

// One stat per file, which is the whole cost on a Mac with nothing to cap — every sweep but
// a handful. No Discord either way: a log this bounds is hygiene rather than news, and a
// message every time herdr's log filled would be a message nobody can act on.
func (s sweeper) capLogs() {
	for _, log := range cappedLogs(s.cfg) {
		s.capLog(log)
	}
}

func (s sweeper) capLog(log cappedLog) {
	// Lstat and not Stat. These are account-owned paths and hachiko runs as the account, so
	// there is no privilege to be had through one of them — but a link at either path is
	// still a write going somewhere nobody asked for, and the cheap refusal is not to look
	// through it in the first place.
	info, err := os.Lstat(log.path)
	if err != nil {
		return
	}

	kb := info.Size() / 1024
	kept := log.path + ".0"

	switch {
	case info.Mode()&os.ModeSymlink != 0:
		s.say("%s is a symlink, so nothing of it was capped", log.path)
		return
	case !info.Mode().IsRegular():
		return
	case kb <= s.cfg.LogCapKB:
		return
	}

	if was, err := os.Lstat(kept); err == nil && was.Mode()&os.ModeSymlink != 0 {
		s.say("%s is a symlink, so %s was not capped", kept, log.path)
		return
	}

	if s.dry {
		s.say("would cap %s at %s MB, keeping what is there now as %s", log.path, mbStr(kb), kept)
		return
	}

	if err := log.cap(kept); err != nil {
		s.say("%s is %s MB and could not be capped: %v", log.path, mbStr(kb), err)
		return
	}
	s.say("capped %s at %s MB; what was there is in %s", log.path, mbStr(kb), kept)
}

// One generation kept, replacing whatever was there: two of them bound a log at twice the
// threshold, and the one before this is the half of a runaway an incident is usually in.
func (l cappedLog) cap(kept string) error {
	if l.how == capRename {
		return os.Rename(l.path, kept)
	}
	return copyAndTruncate(l.path, kept)
}

// The single descriptor is the whole of the safety: the file that is read is the file that is
// emptied, so a link swapped in between the two cannot be what gets emptied, and O_NOFOLLOW
// is what refuses one swapped in between the Lstat above and this.
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

	to, err := os.OpenFile(kept, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(to, from); err != nil {
		to.Close()
		return err
	}
	if err := to.Close(); err != nil {
		return err
	}

	return from.Truncate(0)
}
