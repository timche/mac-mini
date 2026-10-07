// Package gc sweeps what a finished session leaves behind: a removed worktree's compose
// project and the processes still sitting in its folder, the scratch folder of a session
// nobody is running any more, and the worktree entries git keeps for folders that are gone.
// It runs from the io.github.timche.hachiko-gc LaunchAgent.
//
// A garbage collector rather than a hook on the removal, because no removal reliably hands
// us one. herdr removes a worktree, so does Claude Code, so does `git worktree remove` by
// hand, and git has no post-remove hook at all; Claude Code's WorktreeRemove does not fire
// for a git worktree (anthropics/claude-code#94212), and both it and WorktreeCreate replace
// Claude Code's own git behaviour rather than adding to it, so registering one to clean up
// would mean owning worktree creation. The trigger is therefore time, which is what the
// LaunchAgent is for.
//
// Conservative by construction: a compose project whose folder still exists is somebody's
// session, and so is a process whose cwd still exists, a scratch folder a live session
// claims, and a worktree git still finds on disk. Only the gone ones are swept, and only
// inside the roots this machine makes them in.
package gc

import (
	"errors"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/logs"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
)

// A lock left by a killed run would otherwise stop every sweep after it, so one older than
// the interval the LaunchAgent runs on is taken over.
const lockStale = 10 * time.Minute

// A dry run changes nothing at all: no container, no process, no file, and not the lock
// either.
func Run(cfg config.Config, dry bool) error {
	return (&sweeper{cfg: cfg, deps: realDeps(cfg), dry: dry}).run()
}

type sweeper struct {
	cfg  config.Config
	deps Deps
	dry  bool

	// Whether the daemon answered, asked once: two sweeps need it and a daemon that is down
	// costs the whole of `docker info`'s timeout to establish.
	dockerUp    bool
	dockerKnown bool
}

// Only ever one line of output per thing done, and nothing at all on a quiet sweep: this
// runs every ten minutes forever, into a log somebody reads only when something is wrong.
func (s *sweeper) say(format string, args ...any) {
	logs.Logger{Out: s.deps.Log, Now: s.deps.Now, Name: "hachiko gc"}.Say(format, args...)
}

func (s *sweeper) run() error {
	now := s.deps.Now()

	if !s.dry {
		lock, takenFrom, err := statedir.LockDir(s.cfg.GCLock, lockStale, now, "an earlier run")

		switch {
		case errors.Is(err, statedir.ErrLockHeld):
			return nil
		case err != nil:
			s.say("the lock %s could not be taken, so this sweep runs without one: %v", s.cfg.GCLock, err)
		}

		if lock != nil {
			defer lock.Release()
		}
		if takenFrom != "" {
			s.say("taking over a lock left behind by %s", takenFrom)
		}
	}

	s.compose()
	s.processes()
	s.scratch(now)
	s.worktreeEntries()

	if s.dry {
		s.say("dry run over")
	}
	return nil
}
