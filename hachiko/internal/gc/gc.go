// Package gc sweeps what a finished session leaves behind: a removed worktree's compose
// project and the volumes that project made, the processes still sitting in its folder, the
// scratch folder of a session nobody is running any more, and the worktree entries git
// keeps for folders that are gone. It runs from the io.github.timche.hachiko-gc LaunchAgent.
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
//
// This is the one part of hachiko that kills a process and takes a database down, so what
// it tells Tim is only what it could not do: a sweep that worked is lines in a log nobody
// reads, and a compose project that will not go down is a message.
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

// A dry run changes nothing at all: no container, no process, no file, and neither the
// state directory nor the lock.
func Run(cfg config.Config, dry bool) error {
	return (&sweeper{
		cfg:   cfg,
		deps:  realDeps(cfg),
		store: Store{Dir: cfg.GCStateDir},
		dry:   dry,
	}).run()
}

type sweeper struct {
	cfg   config.Config
	deps  Deps
	store Store
	dry   bool

	// What this sweep could not do, gathered as it goes: a failure is only worth a message
	// while it persists, and whether it persists is this list measured against the last
	// sweep's.
	failures []Failure

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

	state, err := s.store.Load()
	if err != nil {
		// A state file that cannot be read costs this sweep the record of which compose
		// projects ran in a worktree, which is the whole of what authorises a volume
		// removal — so it removes no volume this run rather than stopping.
		s.say("%v", err)
	}

	// The order the bash script swept in, with the volumes behind the compose projects: a
	// project this sweep has just taken down has no volumes left to find, so the record of
	// it can be forgotten in the same run.
	s.compose(state)
	s.volumes(state)
	s.processes()
	s.scratch(now)
	s.worktreeEntries()

	if s.dry {
		s.say("dry run over")
		return nil
	}

	s.report(state, now)

	if err := s.store.Save(state); err != nil {
		s.say("the state could not be written, so the next sweep starts from nothing: %v", err)
	}

	// Last, and only on a sweep that ran to the end: it is what the watch reads to tell a
	// Mac nothing is sweeping from one that is.
	if err := s.store.Stamp(now); err != nil {
		s.say("the last-run stamp could not be written, so the watch will say this sweep has stopped: %v", err)
	}
	return nil
}
