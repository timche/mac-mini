package status

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
	"github.com/timche/mac-mini/hachiko/internal/watch"
)

var base = time.Date(2026, 10, 8, 12, 40, 0, 0, time.UTC)

// The whole screen against a Mac that is only as this fixture describes it: nothing here
// reaches launchd, a volume, a repository or a log, so every state a section can be in is one
// line of setup rather than a machine in that state.
type fixture struct {
	t   *testing.T
	cfg config.Config
	now time.Time
	out bytes.Buffer

	colour bool

	jobs map[string]Job

	// By the agent's key rather than its label, since the key is what the state and the table
	// both call one. An agent missing from here is one with no stamp at all.
	stamps map[string]time.Time

	freeKB  int64
	freeErr error

	state    *statedir.State
	stateErr error

	repos    []Repo
	reposErr error
	trees    map[string]RepoState

	logs []Log
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	home := "/Users/someone"
	agents := filepath.Join(home, "Library", "LaunchAgents")

	return &fixture{
		t:   t,
		now: base,

		cfg: config.Config{
			Home:       home,
			Host:       "mac-mini",
			LowKB:      100 * config.GiB,
			CriticalKB: 20 * config.GiB,
			LogCapKB:   10 * 1024,

			StateDir: filepath.Join(home, "Library", "Caches", "hachiko"),

			WatchPlist:  filepath.Join(agents, "io.github.timche.hachiko.plist"),
			GCPlist:     filepath.Join(agents, "io.github.timche.hachiko-gc.plist"),
			SyncPlist:   filepath.Join(agents, "io.github.timche.hachiko-sync.plist"),
			ListenPlist: filepath.Join(agents, "io.github.timche.hachiko-listen.plist"),

			GCStaleAfter:     time.Hour,
			SyncStaleAfter:   10 * time.Minute,
			ListenStaleAfter: 10 * time.Minute,
			SyncLateAfter:    30 * time.Minute,
		},

		// Every agent loaded and every stamp this minute, which is a Mac with nothing wrong
		// with it: a test says what it is about by the one thing it changes.
		jobs: map[string]Job{
			"io.github.timche.hachiko":        {Loaded: true},
			"io.github.timche.hachiko-gc":     {Loaded: true},
			"io.github.timche.hachiko-sync":   {Loaded: true, PID: 2345},
			"io.github.timche.hachiko-listen": {Loaded: true, PID: 3456},
		},
		stamps: map[string]time.Time{
			"watch":              base.Add(-2 * time.Minute),
			statedir.AgentGC:     base.Add(-4 * time.Minute),
			statedir.AgentSync:   base.Add(-20 * time.Second),
			statedir.AgentListen: base.Add(-20 * time.Second),
		},

		freeKB: 400 * config.GiB,
		state:  &statedir.State{},
		trees:  map[string]RepoState{},
	}
}

func (f *fixture) deps() Deps {
	return Deps{
		Now:    func() time.Time { return f.now },
		Out:    &f.out,
		Colour: f.colour,

		Job: func(label string) Job { return f.jobs[label] },
		Stamp: func(agent watch.Agent) (time.Time, bool) {
			at, stamped := f.stamps[agent.Key]
			return at, stamped
		},

		FreeKB: func() (int64, error) { return f.freeKB, f.freeErr },
		State:  func() (*statedir.State, error) { return f.state, f.stateErr },

		Repos:     func() ([]Repo, error) { return f.repos, f.reposErr },
		RepoState: func(repo Repo) RepoState { return f.trees[repo.Path] },

		Logs: func() []Log { return f.logs },
	}
}

func (f *fixture) report() string {
	f.t.Helper()
	return screen{cfg: f.cfg, deps: f.deps()}.report()
}

// One section of it, by the name at the top of it, so a test about the disk is not one a line
// about an agent can pass or fail.
func (f *fixture) section(name string) string {
	f.t.Helper()

	_, after, found := strings.Cut(f.report(), "\n"+name+"\n")
	if !found {
		f.t.Fatalf("there is no %s section in:\n%s", name, f.report())
	}
	before, _, _ := strings.Cut(after, "\n\n")
	return before
}

// The row a subject is on, which is what every assertion here is about: a screen is read by
// finding the line for the thing and then reading across it.
func (f *fixture) row(section, subject string) string {
	f.t.Helper()

	for _, line := range strings.Split(f.section(section), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), subject) {
			return line
		}
	}
	f.t.Fatalf("there is no row for %q in the %s section:\n%s", subject, section, f.section(section))
	return ""
}

func (f *fixture) repo(path string, state RepoState) {
	f.repos = append(f.repos, Repo{Path: filepath.Join(f.cfg.Home, path)})
	f.trees[filepath.Join(f.cfg.Home, path)] = state
}

func (f *fixture) limited(path string, state RepoState, paths ...string) {
	f.repo(path, state)
	f.repos[len(f.repos)-1].Paths = paths
}

func (f *fixture) log(name string, kb int64) {
	f.logs = append(f.logs, Log{
		Path: filepath.Join(f.cfg.Home, "Library", "Logs", name),
		KB:   kb,
		Read: true,
	})
}

func (f *fixture) ago(d time.Duration) int64 { return f.now.Add(-d).Unix() }

func quoted(text string) string { return fmt.Sprintf("%q", text) }
