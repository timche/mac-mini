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
	"github.com/timche/mac-mini/hachiko/internal/discord"
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

	// Every volume, with whether a container is using it. Both halves matter: the one no
	// container uses is the candidate, and the one some container does is the record that a
	// project is not finished with.
	Volumes      func() ([]Volume, error)
	RemoveVolume func(name string) ([]byte, error)

	// Docker's build cache and the images nothing refers to, by age. Two commands of their
	// own rather than one `docker system prune`, which takes volumes and networks with it —
	// and a volume here is somebody's database, swept only by the rule above.
	PruneBuilder func(until string) ([]byte, error)
	PruneImages  func(until string) ([]byte, error)

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

	// Reaches the channel Tim watches, and answers with the thread it opened. The only
	// thing that ever sees the webhook or the bot token, and it is the watch's own sender:
	// a failure here goes out the same way an alert does.
	Send func(out discord.Outgoing) (string, error)
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

// Volume is one named volume. Project is `com.docker.compose.project`, which is the only
// thing a volume carries about where it came from — never the folder, which is why a sweep
// has to have written down which projects ran in a worktree.
type Volume struct {
	Name      string
	Project   string
	Anonymous bool
	InUse     bool
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
	infoTimeout   = 10 * time.Second
	listTimeout   = 20 * time.Second
	downTimeout   = 180 * time.Second
	volumeTimeout = 60 * time.Second
	pruneTimeout  = 30 * time.Second

	// A prune walks every layer and every cache record the daemon has, so it is the one here
	// whose honest worst case is minutes rather than seconds — and it runs once a day, where
	// the others run six times an hour.
	cachePruneTimeout = 180 * time.Second
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
		Volumes: volumes,
		RemoveVolume: func(name string) ([]byte, error) {
			return run(volumeTimeout, "docker", "volume", "rm", name)
		},

		PruneBuilder: func(until string) ([]byte, error) {
			return run(cachePruneTimeout, "docker", "builder", "prune", "--force", "--filter", "until="+until)
		},
		// No `-a`, so this is the images nothing refers to by tag and nothing else: a base
		// image a project still names is one the next build would only pull again, however
		// long it has been since anybody built with it.
		PruneImages: func(until string) ([]byte, error) {
			return run(cachePruneTimeout, "docker", "image", "prune", "--force", "--filter", "until="+until)
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

		Send: func(out discord.Outgoing) (string, error) { return discord.SendThroughOP(cfg, out) },
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

// Two listings rather than one: `--filter dangling=true` is docker's own answer to which
// volumes no container is using, and reading it off the full list would mean deciding for
// ourselves what counts as a user.
func volumes() ([]Volume, error) {
	out, err := run(listTimeout, "docker", "volume", "ls", "--format", "{{.Name}}\t{{.Labels}}")
	if err != nil {
		return nil, err
	}

	idle, err := run(listTimeout, "docker", "volume", "ls",
		"--filter", "dangling=true", "--format", "{{.Name}}")
	if err != nil {
		return nil, err
	}

	unused := map[string]bool{}
	for _, name := range strings.Split(string(idle), "\n") {
		if name = strings.TrimSpace(name); name != "" {
			unused[name] = true
		}
	}

	var found []Volume
	for _, line := range strings.Split(string(out), "\n") {
		name, labels, _ := strings.Cut(strings.TrimRight(line, "\r"), "\t")
		if name = strings.TrimSpace(name); name == "" {
			continue
		}
		found = append(found, parseVolume(name, labels, !unused[name]))
	}
	return found, nil
}

// `{{.Labels}}` is `k=v` pairs joined with commas. A value holding one would be read wrong,
// which neither of the two keys this looks for can hold: compose rejects a project name
// outside [a-z0-9_-], and the anonymous marker has no value at all.
func parseVolume(name, labels string, inUse bool) Volume {
	v := Volume{Name: name, InUse: inUse, Anonymous: anonymousName(name)}

	for _, label := range strings.Split(labels, ",") {
		key, value, _ := strings.Cut(strings.TrimSpace(label), "=")
		switch key {
		case "com.docker.compose.project":
			v.Project = value
		case "com.docker.volume.anonymous":
			v.Anonymous = true
		}
	}
	return v
}

// What docker names a volume nobody named: 64 hex characters. Checked as well as the label,
// because the label is compose's and a volume from a Dockerfile's VOLUME has neither.
func anonymousName(name string) bool {
	if len(name) != 64 {
		return false
	}
	for _, r := range name {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
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
