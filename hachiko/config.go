package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Kibibytes in a gibibyte, which is the unit every size below counts in, so the
// thresholds read as the numbers they are named for.
const gib = 1 << 20

// Config is every number and path a sweep decides by. Each one is an environment
// variable so that a test can trip the same arithmetic with megabytes and minutes
// rather than filling a disk or waiting an hour to prove it.
type Config struct {
	LowKB      int64
	CriticalKB int64
	BigKB      int64
	GrowthKB   int64

	// Per cent of one core, and the unbroken span a process has to hold it for.
	CPUShare  float64
	CPUWindow time.Duration

	OncallDeadline time.Duration

	// The wait after an on-call session has asked its question and nobody has answered:
	// a reminder, a warning that the deadline is close, and the deadline at which the
	// session is told to decide for itself.
	RemindAfter   time.Duration
	WarnAfter     time.Duration
	HandoverAfter time.Duration

	// What one directory read may take before it is abandoned, and what the whole walk
	// may take before the sweep goes on without it. Defence in depth behind
	// PrunedPaths: no single open may cost the machine its only monitor.
	DirTimeout  time.Duration
	WalkTimeout time.Duration

	// How long a directory that would not answer stays skipped before it is tried again.
	StallRetry time.Duration

	Home         string
	MachineDir   string
	TmpRoot      string
	ScratchRoot  string
	LogsRoot     string
	HerdrRoot    string
	ProjectsRoot string

	CPUAllowPath string
	EnvFile      string
	StateDir     string

	Host string
}

// LowGB and CriticalGB are what the log and the messages name the thresholds by,
// so a check with the thresholds turned down reads as the numbers it was given.
func (c Config) LowGB() int64      { return c.LowKB / gib }
func (c Config) CriticalGB() int64 { return c.CriticalKB / gib }

// WorkspaceLabel is the herdr workspace the on-call session goes in: the one for
// this machine's own repository, so a session that starts there has the machine's
// instructions loaded.
func (c Config) WorkspaceLabel() string { return filepath.Base(c.MachineDir) }

func configFromEnv() Config {
	home, _ := os.UserHomeDir()
	if h := os.Getenv("HOME"); h != "" {
		home = h
	}

	machineDir := envString("MAC_MINI_DIR", filepath.Join(home, ".mac-mini"))

	cache := filepath.Join(home, ".cache")
	if _, err := os.Stat(filepath.Join(home, "Library", "Caches")); err == nil {
		cache = filepath.Join(home, "Library", "Caches")
	}

	host, _ := os.Hostname()
	host, _, _ = strings.Cut(host, ".")

	// Beside the wrapper rather than beside the binary: the wrapper is what mise
	// links into ~/.local/bin, and the env file is linked next to it, while the
	// binary lives in a cache that holds nothing else.
	envFile := envString("HACHIKO_ENV_FILE", filepath.Join(home, ".local", "bin", "hachiko.env.op"))

	return Config{
		LowKB:      envInt64("HACHIKO_LOW_GB", 100) * gib,
		CriticalKB: envInt64("HACHIKO_CRITICAL_GB", 20) * gib,
		BigKB:      envInt64("HACHIKO_BIG_KB", gib),
		GrowthKB:   envInt64("HACHIKO_GROWTH_KB", 2*gib),

		CPUShare:  float64(envInt64("HACHIKO_CPU_SHARE", 50)),
		CPUWindow: time.Duration(envInt64("HACHIKO_CPU_WINDOW", 3600)) * time.Second,

		OncallDeadline: time.Duration(envInt64("HACHIKO_ONCALL_DEADLINE", 600)) * time.Second,

		RemindAfter:   time.Duration(envInt64("HACHIKO_REMIND_AFTER", 3600)) * time.Second,
		WarnAfter:     time.Duration(envInt64("HACHIKO_WARN_AFTER", 9900)) * time.Second,
		HandoverAfter: time.Duration(envInt64("HACHIKO_HANDOVER_AFTER", 10800)) * time.Second,

		DirTimeout:  time.Duration(envInt64("HACHIKO_DIR_TIMEOUT", 3)) * time.Second,
		WalkTimeout: time.Duration(envInt64("HACHIKO_WALK_TIMEOUT", 60)) * time.Second,
		StallRetry:  time.Duration(envInt64("HACHIKO_STALL_RETRY", 3600)) * time.Second,

		Home:         home,
		MachineDir:   machineDir,
		TmpRoot:      envString("HACHIKO_TMP", "/private/tmp"),
		ScratchRoot:  envString("CLAUDE_CODE_TMPDIR", filepath.Join(home, ".cache", "claude-tmp")),
		LogsRoot:     filepath.Join(home, "Library", "Logs"),
		HerdrRoot:    filepath.Join(home, ".herdr", "worktrees"),
		ProjectsRoot: envString("HACHIKO_PROJECTS", filepath.Join(home, "projects")),

		CPUAllowPath: envString("HACHIKO_CPU_ALLOW", filepath.Join(home, ".config", "hachiko", "cpu-allow")),
		EnvFile:      envFile,
		StateDir:     envString("HACHIKO_STATE_DIR", filepath.Join(cache, "hachiko")),

		Host: host,
	}
}

// Roots are the trees a runaway log lands in: the tmp everything reaches for, the
// account's own home, and the two folders inside ~/Library that hold logs and caches
// rather than another app's data. The rest of ~/Library is not walked at all — see
// PrunedPaths — so those two come back as roots of their own.
func (c Config) Roots() []string {
	return []string{
		c.TmpRoot,
		c.Home,
		filepath.Join(c.Home, "Library", "Logs"),
		filepath.Join(c.Home, "Library", "Caches"),
	}
}

// PrunedPaths is the whole of what the walk does not open. Everything else under the
// roots is walked, because a runaway log lands wherever somebody found convenient.
//
// All of them are about TCC. Under launchd an unsigned binary is its own
// TCC-responsible process and has none of the grants a herdr pane has, and what it meets
// is not a refusal: a folder behind Full Disk Access answers "operation not permitted" in
// microseconds and is no trouble, but a folder behind a consent prompt makes open() wait
// for a dialog on a screen nobody is looking at. That is a sweep that never returns, on a
// Mac whose only monitor it is. It cannot be discovered at runtime either, because the
// same code run from a session walks all of it fine.
//
// ~/Library goes wholesale: it is every other app's data, macOS guards it under Device
// Control and Data Access, and reading it raises "Data Access Blocked" as well as
// hundreds of TCC requests. Logs and Caches are walked as roots of their own instead,
// being this machine's own output rather than anybody's data — the first is where a
// runaway log lands and where a truncate is allowed, the second is where hachiko's own
// binary lives. Pruning ~/Library also takes the Containers folders with it, which held
// the VM disk images that are sparse and so apparently hundreds of gigabytes they are not
// occupying, and iCloud Drive and the CloudStorage mounts, which are consent-guarded too.
//
// The home folders below it are the documents-and-media consent category. Public is the
// odd one out: it is not guarded at all, it is the folder the Mac shares out, and nothing
// that writes a log writes it there.
func (c Config) PrunedPaths() []string {
	under := func(parts ...string) string {
		return filepath.Join(append([]string{c.Home}, parts...)...)
	}

	return []string{
		under("Library"),

		under("Desktop"),
		under("Documents"),
		under("Downloads"),
		under("Movies"),
		under("Music"),
		under("Pictures"),
		under("Public"),
	}
}

func envString(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func envInt64(name string, fallback int64) int64 {
	v, err := strconv.ParseInt(os.Getenv(name), 10, 64)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

// HACHIKO_NOW is how a check reads as the timeline it is about instead of waiting
// for real minutes to pass.
func clockFromEnv() func() time.Time {
	if at := envInt64("HACHIKO_NOW", 0); at > 0 {
		return func() time.Time { return time.Unix(at, 0) }
	}
	return time.Now
}
