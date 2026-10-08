package gc

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/process"
)

// The account the sweep runs as, and one that is nobody's but macOS's.
const (
	accountUID = 501
	systemUID  = 0
)

var base = time.Unix(1700000000, 0)

// Everything the sweep learns or does is a function on the fixture, except the filesystem:
// a scratch sweep that never made a folder and never removed one would be a test of the
// rule and not of the sweep, so those deps are the real ones under a temp home.
type fixture struct {
	t   *testing.T
	cfg config.Config
	log bytes.Buffer

	home string
	now  time.Time
	uid  int
	pid  int

	dockerUp      bool
	containers    []Container
	containersErr string
	downs         []string
	downErr       map[string]string

	volumes    []Volume
	volumesErr error
	removed    []string
	volumeErr  map[string]string

	procs    []process.Process
	procsErr error
	cwds     []CWD
	cwdsErr  error

	// Which pids are still there, which of them shrug off SIGTERM, and which shrug off
	// SIGKILL too. The same map answers for the session records, whose pid in the filename
	// is what says a session is live.
	alive    map[int]bool
	stubborn map[int]bool
	immortal map[int]bool
	signals  []string

	// What docker was asked to prune, with the age it was asked for, and what it says back.
	// Keyed by the half of docker it is: `builder` and `image`.
	dockerPruned   []string
	dockerPruneOut map[string]string
	dockerPruneErr map[string]string

	// Run from inside the prune, which is the only moment a test can see what the sweep had
	// written down before the long-running half of it started.
	whilePruning func()

	gitUp    bool
	pruned   []string
	pruneOut map[string]string
	pruneErr map[string]string

	sent    []discord.Outgoing
	sendErr string
	threads int
}

func newFixture(t *testing.T) *fixture {
	home := t.TempDir()

	f := &fixture{
		t:    t,
		home: home,
		now:  base,
		uid:  accountUID,
		pid:  9000,

		dockerUp: true,
		gitUp:    true,

		downErr:   map[string]string{},
		volumeErr: map[string]string{},
		alive:     map[int]bool{},
		stubborn:  map[int]bool{},
		immortal:  map[int]bool{},
		pruneOut:  map[string]string{},
		pruneErr:  map[string]string{},

		// Nothing old enough to prune, which is what the daily prune finds on all but the
		// first run of a day — and what keeps a sweep with nothing else to do a quiet one.
		// The two spellings are docker's own: `docker image prune` prints "Total reclaimed
		// space:" and buildkit's prune prints "Total:" and a tab.
		dockerPruneOut: map[string]string{
			"builder": "ID\tRECLAIMABLE\tSIZE\nTotal:\t0B\n",
			"image":   "Total reclaimed space: 0B\n",
		},
		dockerPruneErr: map[string]string{},

		cfg: config.Config{
			GCPruneEvery:   24 * time.Hour,
			GCPruneAge:     "168h",
			Home:           home,
			HerdrRoot:      filepath.Join(home, ".herdr", "worktrees"),
			ScratchRoot:    filepath.Join(home, ".cache", "claude-tmp"),
			GCProjectsRoot: filepath.Join(home, "projects"),
			GCStateDir:     filepath.Join(home, "state"),
			GCLock:         filepath.Join(home, "gc.lock"),
			Host:           "mac-mini",
		},
	}
	return f
}

func (f *fixture) at(seconds int64) *fixture {
	f.now = base.Add(time.Duration(seconds) * time.Second)
	return f
}

func (f *fixture) deps() Deps {
	real := realDeps(f.cfg)

	return Deps{
		Now:    func() time.Time { return f.now },
		Log:    &f.log,
		Getpid: func() int { return f.pid },
		Getuid: func() int { return f.uid },

		Docker:     func() bool { return f.dockerUp },
		Containers: func() ([]Container, error) { return f.containers, errOf(f.containersErr) },
		Down: func(project string) ([]byte, error) {
			f.downs = append(f.downs, project)
			if said, bad := f.downErr[project]; bad {
				return []byte(said), fmt.Errorf("exit status 1")
			}
			return []byte("Container " + project + "-db  Removed\n"), nil
		},
		Volumes:      func() ([]Volume, error) { return f.volumes, f.volumesErr },
		PruneBuilder: func(until string) ([]byte, error) { return f.dockerPrune("builder", until) },
		PruneImages:  func(until string) ([]byte, error) { return f.dockerPrune("image", until) },
		RemoveVolume: func(name string) ([]byte, error) {
			if said, bad := f.volumeErr[name]; bad {
				return []byte(said), fmt.Errorf("exit status 1")
			}
			f.removed = append(f.removed, name)
			return []byte(name + "\n"), nil
		},

		IsDir:        real.IsDir,
		Exists:       real.Exists,
		ReadDir:      real.ReadDir,
		Remove:       real.Remove,
		Rmdir:        real.Rmdir,
		ReadFile:     real.ReadFile,
		TouchedSince: real.TouchedSince,

		Processes: func() ([]process.Process, error) { return f.procs, f.procsErr },
		CWDs:      func() ([]CWD, error) { return f.cwds, f.cwdsErr },
		Signal:    f.signal,
		Sleep:     func(time.Duration) {},

		Git: func() bool { return f.gitUp },
		Prune: func(repo string, dry bool) ([]byte, error) {
			f.pruned = append(f.pruned, repo)
			if said, bad := f.pruneErr[repo]; bad {
				return []byte(said), fmt.Errorf("exit status 128")
			}
			return []byte(f.pruneOut[repo]), nil
		},

		Send: f.send,
	}
}

func (f *fixture) dockerPrune(what, until string) ([]byte, error) {
	f.dockerPruned = append(f.dockerPruned, what+" until="+until)

	if f.whilePruning != nil {
		f.whilePruning()
	}

	if said, bad := f.dockerPruneErr[what]; bad {
		return []byte(said), fmt.Errorf("exit status 1")
	}
	return []byte(f.dockerPruneOut[what]), nil
}

// What docker says the two halves of a prune got back, each in the spelling that half
// really prints.
func (f *fixture) reclaims(cache, images string) {
	f.dockerPruneOut["builder"] = "ID\tRECLAIMABLE\tSIZE\nabc\ttrue\t" + cache + "\nTotal:\t" + cache + "\n"
	f.dockerPruneOut["image"] = "Deleted Images:\ndeleted: sha256:abc\n\nTotal reclaimed space: " + images + "\n"
}

func errOf(text string) error {
	if text == "" {
		return nil
	}
	return fmt.Errorf("%s", text)
}

func (f *fixture) signal(pid int, sig syscall.Signal) error {
	if sig == 0 {
		if f.alive[pid] {
			return nil
		}
		return syscall.ESRCH
	}

	f.signals = append(f.signals, fmt.Sprintf("%d:%d", pid, sig))

	switch {
	case sig == syscall.SIGTERM && !f.stubborn[pid]:
		delete(f.alive, pid)
	case sig == syscall.SIGKILL && !f.immortal[pid]:
		delete(f.alive, pid)
	}
	return nil
}

func (f *fixture) send(out discord.Outgoing) (string, error) {
	if f.sendErr != "" {
		return "", fmt.Errorf("%s", f.sendErr)
	}

	f.sent = append(f.sent, out)
	if out.OpenThread == "" {
		return "", nil
	}
	f.threads++
	return fmt.Sprintf("thread-%d", f.threads), nil
}

func (f *fixture) sweep() string  { return f.run(false) }
func (f *fixture) dryRun() string { return f.run(true) }
func (f *fixture) lastSent() string {
	f.t.Helper()
	if len(f.sent) == 0 {
		f.t.Fatal("nothing was sent")
	}
	return f.sent[len(f.sent)-1].Text
}

func (f *fixture) run(dry bool) string {
	f.t.Helper()
	f.log.Reset()

	s := &sweeper{cfg: f.cfg, deps: f.deps(), store: Store{Dir: f.cfg.GCStateDir}, dry: dry}
	if err := s.run(); err != nil {
		f.t.Fatalf("the sweep failed: %v", err)
	}
	return f.log.String()
}

func (f *fixture) state() *State {
	f.t.Helper()

	state, err := (Store{Dir: f.cfg.GCStateDir}).Load()
	if err != nil {
		f.t.Fatal(err)
	}
	return state
}

// A worktree of herdr's, made and then removed, which is the shape every sweep here is
// about. The path comes back either way.
func (f *fixture) herdrWorktree(repo, branch string, keep bool) string {
	f.t.Helper()

	path := filepath.Join(f.cfg.HerdrRoot, repo, branch)
	if keep {
		f.mkdir(path)
	}
	return path
}

func (f *fixture) mkdir(path string) string {
	f.t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		f.t.Fatal(err)
	}
	return path
}

func (f *fixture) write(path, content string) string {
	f.t.Helper()
	f.mkdir(filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
	return path
}

// A process as ps prints one, which is the only thing that says who owns a pid.
func (f *fixture) process(pid int, uid int, command string) process.Process {
	return process.Process{
		PID: pid, PPID: 1, UID: uid, User: owner(uid), Command: command,
		Start: "Mon Jan 1 00:00:00 2024", StartedAt: base,
	}
}

func owner(uid int) string {
	if uid == accountUID {
		return "timche"
	}
	return "root"
}

// A process sitting in a directory, as the two reads that find one would report it.
func (f *fixture) sitting(pid, uid int, dir, command string) {
	f.procs = append(f.procs, f.process(pid, uid, command))
	f.cwds = append(f.cwds, CWD{PID: pid, Dir: dir})
	f.alive[pid] = true
}

func lines(log string) []string {
	var kept []string
	for _, line := range strings.Split(strings.TrimRight(log, "\n"), "\n") {
		if line != "" {
			kept = append(kept, line)
		}
	}
	return kept
}
