// Package status is `hachiko status`: one screen that says whether this Mac's own machinery
// is healthy, for a person or a session sitting in a terminal. Everything on it can be found
// out by hand — four logs, a `launchctl print` each, a `df`, a state file and a `git status`
// per repository — and the point of it is that nobody does that at the moment they need to
// know, least of all a session that has just been told something is wrong with the machine.
//
// It is the one command here that only reads. No state is written, the watch's lock is never
// taken, nothing is sent anywhere and `op` is never called: a screen somebody looks at twice
// a day may not spend the service account's daily limit, and may not be the reason a sweep
// waited. It exits non-zero only when it could not run at all — a check that could not read
// something says so in its own section and the exit code stays nought, because this reports
// problems rather than being one more thing that has them.
package status

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/process"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
	"github.com/timche/mac-mini/hachiko/internal/sync"
	"github.com/timche/mac-mini/hachiko/internal/watch"
)

// Job is what launchd has to say about one agent: whether it has it at all, and the pid when
// it is running something this minute. An agent on an interval has none between runs, which
// is not the same thing as not being there.
type Job struct {
	Loaded bool
	PID    int
}

// Repo is one repository `hachiko sync` is meant to be keeping upstream, out of sync's own
// config so that the list this prints cannot be a different list from the one sync syncs.
type Repo struct {
	Path      string
	PushDelay time.Duration
}

// What git and sync's own state say about one of them. Read says whether git answered at
// all: a repository nothing could be read from is one this says it could not read rather
// than one it calls clean.
type RepoState struct {
	Read     bool
	Dirty    bool
	Unpushed int
	Oldest   time.Time

	// When a rebase conflicted, which is sync's record of a repository it will not touch
	// again until one of the two sides moves. Zero unless it is paused.
	Paused time.Time

	Trouble string
}

// One of the logs the watch caps, and how big it is now. Read is false for one nothing has
// written yet, which is an agent that has had nothing to say rather than a log that is
// missing.
type Log struct {
	Path string
	KB   int64
	Read bool
}

// Deps is everything the screen is read out of. Each one is a function so that every section
// and every state it can be in is driven by a test without a launchd, a disk to fill, a
// repository or a log of the right size.
type Deps struct {
	Now func() time.Time
	Out io.Writer

	// Whether colour is wanted at all, which is whether anybody is looking: a pipe gets none,
	// since the first thing done with this output is to grep it or paste it somewhere.
	Colour bool

	Job func(label string) Job

	// The stamp an agent writes, read the way the watch reads it — the same table, so a
	// threshold or a path cannot be two things.
	Stamp func(agent watch.Agent) (time.Time, bool)

	FreeKB func() (int64, error)

	// The watch's own state, read without its lock. A sweep holds that lock across a herdr
	// call and an `op run`, so a screen that waited for it would be a screen that hangs for
	// minutes; what it costs is reading an incident out of a file a sweep may be rewriting,
	// which is one line of a report being a few seconds old.
	State func() (*statedir.State, error)

	Repos     func() ([]Repo, error)
	RepoState func(repo Repo) RepoState

	Logs func() []Log
}

func Run(cfg config.Config) error {
	return screen{cfg: cfg, deps: realDeps(cfg)}.write()
}

func realDeps(cfg config.Config) Deps {
	return Deps{
		Now:    config.ClockFromEnv(),
		Out:    os.Stdout,
		Colour: isTerminal(os.Stdout),

		Job:   func(label string) Job { return job(os.Getuid(), label) },
		Stamp: func(agent watch.Agent) (time.Time, bool) { return agent.Last() },

		FreeKB: func() (int64, error) { return watch.FreeKB(cfg.Home) },
		State:  func() (*statedir.State, error) { return statedir.Store{Dir: cfg.StateDir}.Load() },

		Repos:     func() ([]Repo, error) { return repos(cfg) },
		RepoState: func(repo Repo) RepoState { return repoState(cfg, repo) },

		Logs: func() []Log { return logs(cfg) },
	}
}

// A character device is a terminal and a file or a pipe is not, which is the whole of the
// test: there is no isatty in the standard library and nothing here may need cgo to decide
// whether to print an escape code.
func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

const launchctlTimeout = 10 * time.Second

// `launchctl print gui/<uid>/<label>`, which answers for a job launchd has and fails for one
// it does not. Parsed defensively, because the block it prints is nested, undocumented and
// launchd's to change: the one line wanted is matched as a whole key, the first of them wins,
// and anything that does not parse leaves a job that is loaded and running nothing — which is
// exactly what an agent on an interval looks like between its runs.
func job(uid int, label string) Job {
	out, err := process.Run(launchctlTimeout, "launchctl", "print", fmt.Sprintf("gui/%d/%s", uid, label))
	if err != nil {
		return Job{}
	}

	found := Job{Loaded: true}
	for _, line := range strings.Split(string(out), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "pid" {
			continue
		}
		if pid, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && pid > 0 {
			found.PID = pid
			break
		}
	}
	return found
}

func repos(cfg config.Config) ([]Repo, error) {
	loaded, err := config.LoadSync(cfg.SyncConfig, cfg.Home)
	if err != nil {
		return nil, err
	}

	out := make([]Repo, 0, len(loaded.Repos))
	for _, repo := range loaded.Repos {
		out = append(out, Repo{Path: repo.Path, PushDelay: repo.PushDelay})
	}
	return out, nil
}

// Two git commands and a read of sync's own state, all of them read-only and bounded: a git
// that hangs on a mount which has gone away may not be what a person is left looking at.
// Nothing is read from a remote, so neither command goes near the network.
func repoState(cfg config.Config, repo Repo) RepoState {
	status, err := process.Run(gitTimeout, "git", "-C", repo.Path, "status", "--porcelain=v1")
	if err != nil {
		return RepoState{Trouble: "git would not say what is in it"}
	}

	state := RepoState{Read: true, Dirty: len(strings.TrimSpace(string(status))) > 0}

	if at, paused := (&sync.Store{Dir: cfg.SyncStateDir}).PausedAt(repo.Path); paused {
		state.Paused = time.Unix(at.Since, 0)
	}

	// A branch with no upstream has nothing to be behind, and git says so by failing.
	stamps, err := process.Run(gitTimeout, "git", "-C", repo.Path, "log", "--format=%ct", "@{upstream}..HEAD")
	if err != nil {
		return state
	}

	lines := strings.Fields(string(stamps))
	state.Unpushed = len(lines)
	if len(lines) == 0 {
		return state
	}
	if seconds, err := strconv.ParseInt(lines[len(lines)-1], 10, 64); err == nil {
		state.Oldest = time.Unix(seconds, 0)
	}
	return state
}

const gitTimeout = 10 * time.Second

// One lstat per log, which is the whole cost of this section.
func logs(cfg config.Config) []Log {
	var found []Log

	for _, path := range watch.CappedLogPaths(cfg) {
		one := Log{Path: path}
		if info, err := os.Lstat(path); err == nil {
			one.Read, one.KB = true, info.Size()/1024
		}
		found = append(found, one)
	}
	return found
}
