package gc

import (
	"context"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/process"
)

// Deps is everything a sweep learns about the machine or does to it. Each one is a function
// so that every decision below is driven by a test without a docker daemon, a real process
// to kill, a git repository or a folder anyone would miss.
type Deps struct {
	Now    func() time.Time
	Log    io.Writer
	Getpid func() int

	// The account the sweep is running as. Nothing it does not own is ever signalled: a
	// process of another uid in a removed worktree is a kill this account cannot make and a
	// decision that is not a sweep's.
	Getuid func() int

	// Whether there is a docker on PATH whose daemon answers. Neither being true is a
	// sweep that skips the two docker passes in silence — a Mac with OrbStack stopped is
	// not a Mac with a fault.
	Docker func() bool

	// Every compose container on the daemon, running or not, with the three labels that
	// decide: the project, the directory it was started from, and the compose files it was
	// started with.
	Containers func() ([]Container, error)
	Down       func(project string) ([]byte, error)

	IsDir   func(path string) bool
	Exists  func(path string) bool
	ReadDir func(path string) ([]string, error)

	Remove   func(path string) error
	Rmdir    func(path string) error
	ReadFile func(path string) ([]byte, error)

	// Whether anything in the tree under a path was written since a moment. What gives a
	// finished session's scratch folder its week of grace.
	TouchedSince func(path string, since time.Time) bool

	Processes func() ([]process.Process, error)
	CWDs      func() ([]CWD, error)
	Signal    func(pid int, sig syscall.Signal) error
	Sleep     func(d time.Duration)

	Git   func() bool
	Prune func(repo string, dry bool) ([]byte, error)
}

// Container is one compose container as its own labels describe it. The labels rather than
// `docker compose ls`, which reports the config files alone: a project started from a file
// passed with -f, or named something compose does not look for, has a working directory and
// no config file that would recognise it.
type Container struct {
	Project     string
	WorkingDir  string
	ConfigFiles []string
}

// CWD is one process and the directory it is sitting in.
type CWD struct {
	PID int
	Dir string
}

// Every subprocess here is one a stale mount or a dead daemon could stop for good, and a
// sweep that never returns is one holding the lock that keeps the next six from running.
// `down` gets the longest because it stops containers and waits on them.
const (
	infoTimeout  = 10 * time.Second
	listTimeout  = 20 * time.Second
	downTimeout  = 180 * time.Second
	pruneTimeout = 30 * time.Second
)

func realDeps(cfg config.Config) Deps {
	return Deps{
		Now:    config.ClockFromEnv(),
		Log:    os.Stdout,
		Getpid: os.Getpid,
		Getuid: os.Getuid,

		Docker: func() bool {
			if _, err := exec.LookPath("docker"); err != nil {
				return false
			}
			_, err := run(infoTimeout, "docker", "info")
			return err == nil
		},
		Containers: containers,
		Down: func(project string) ([]byte, error) {
			return run(downTimeout, "docker", "compose", "-p", project, "down", "-v", "--remove-orphans")
		},
		IsDir: func(path string) bool {
			info, err := os.Stat(path)
			return err == nil && info.IsDir()
		},
		Exists: func(path string) bool {
			_, err := os.Lstat(path)
			return err == nil
		},
		ReadDir: readDir,

		Remove:   os.RemoveAll,
		Rmdir:    os.Remove,
		ReadFile: os.ReadFile,

		TouchedSince: touchedSince,

		Processes: process.Sample,
		CWDs:      cwds,
		Signal:    func(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) },
		Sleep:     time.Sleep,

		Git: func() bool {
			_, err := exec.LookPath("git")
			return err == nil
		},
		Prune: func(repo string, dry bool) ([]byte, error) {
			args := []string{"-C", repo, "worktree", "prune", "-v"}
			if dry {
				args = append(args, "--dry-run")
			}
			return run(pruneTimeout, "git", args...)
		},
	}
}

// Combined output rather than stdout alone, because what these commands have to say about
// a failure is the half a message quotes: docker writes it on stderr, and so does git's own
// `worktree prune -v`.
func run(limit time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()

	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func containers() ([]Container, error) {
	const format = `{{.Label "com.docker.compose.project"}}	` +
		`{{.Label "com.docker.compose.project.working_dir"}}	` +
		`{{.Label "com.docker.compose.project.config_files"}}`

	out, err := run(listTimeout, "docker", "ps", "--all",
		"--filter", "label=com.docker.compose.project", "--format", format)
	if err != nil {
		return nil, err
	}

	var found []Container
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(fields) < 2 || fields[0] == "" {
			continue
		}

		c := Container{Project: fields[0], WorkingDir: fields[1]}
		if len(fields) > 2 {
			for _, file := range strings.Split(fields[2], ",") {
				if file = strings.TrimSpace(file); file != "" {
					c.ConfigFiles = append(c.ConfigFiles, file)
				}
			}
		}
		found = append(found, c)
	}
	return found, nil
}

// Names only, sorted, so a sweep walks a folder in the order a log reads in. Whether one of
// them is a directory is IsDir's to say, since a name is all this has to pass around.
func readDir(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

// `find <dir> -newermt <since> -print -quit`, which is what decided this in bash: anything
// at all in the tree, the directory itself included. It stops at the first one, because
// one file written this week is the whole of the answer.
//
// A tree it cannot read is one it says was touched: a scratch folder whose contents cannot
// be looked at is not a folder to remove on the strength of not having seen anything in it.
func touchedSince(path string, since time.Time) bool {
	touched := false

	err := filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(since) {
			touched = true
			return filepath.SkipAll
		}
		return nil
	})

	return touched || err != nil
}

// One lsof for every process on the machine rather than one per pid: the sweep has to know
// about every cwd there is, and asking about them one at a time costs more than the sweep
// does. No /proc branch, because there is no /proc on a Mac.
//
// lsof exits non-zero when it could not look at some of what it was asked about, which on a
// Mac is every process of another account — so what it printed is read whatever it returned.
func cwds() ([]CWD, error) {
	out, err := process.Run(process.LsofTimeout, "lsof", "-a", "-d", "cwd", "-Fpn")
	if err != nil && len(out) == 0 {
		return nil, err
	}
	return parseCWDs(string(out)), nil
}

func parseCWDs(out string) []CWD {
	var found []CWD
	pid := 0

	for _, line := range strings.Split(out, "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			pid = atoi(line[1:])
		case 'n':
			if pid > 0 {
				found = append(found, CWD{PID: pid, Dir: line[1:]})
			}
		}
	}
	return found
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
