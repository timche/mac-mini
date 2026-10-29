package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Kibibytes in a gibibyte, which is the unit every size below counts in, so the
// thresholds read as the numbers they are named for.
const GiB = 1 << 20

// Config is every number and path a sweep decides by. Each one is an environment
// variable so that a test can trip the same arithmetic with megabytes and minutes
// rather than filling a disk or waiting an hour to prove it.
type Config struct {
	LowKB      int64
	CriticalKB int64

	// Per cent of one core, and the unbroken span a process has to hold it for.
	CPUShare  float64
	CPUWindow time.Duration

	// How long a process has to have looked left behind by a session before it is reported.
	// An hour, the CPU window's own span and for its reason: a session that has just exited
	// leaves its children reparented to launchd for a moment while they finish, and the span
	// is what tells that apart from a leak.
	StrayWindow time.Duration

	OncallDeadline time.Duration

	// The wait after an on-call session has asked its question and nobody has answered:
	// a reminder, a warning that the deadline is close, and the deadline at which the
	// session is told to decide for itself.
	RemindAfter   time.Duration
	WarnAfter     time.Duration
	HandoverAfter time.Duration

	// How big one of this machine's own logs may get before the watch caps it, keeping one
	// previous generation. Ten megabytes: weeks of a quiet agent's lines, and two
	// generations of every log on the list together are a rounding error against the volume
	// the same check is counting free space on.
	LogCapKB int64

	Home         string
	MachineDir   string
	ScratchRoot  string
	LogsRoot     string
	HerdrRoot    string
	ProjectsRoot string

	CPUAllowPath string
	EnvFile      string

	// The installed root helper, read for the one list it holds: the daemons it will
	// restart without a password. A message about a busy daemon on that list can name the
	// command Tim runs instead of the sudo kill he has to think about.
	RootHelper string

	// Where shibuya is and the token it takes. The URL is in the checkout, being no secret;
	// the token is not, being one — and it is generated on this Mac rather than resolved from
	// 1Password, so a sweep that happens twice an hour costs no `op run` at all.
	SwitchConfig string
	SwitchToken  string

	StateDir string

	// The plist of the agent that runs the five-minute check itself. Nothing on this Mac reads
	// its liveness — a watch cannot notice itself not running, which is what shibuya is for —
	// so this is here for `hachiko status`, which says whether the agent is loaded and when
	// its last check finished.
	WatchPlist string

	// The sweep that follows a removed worktree, which keeps all of its own: a state
	// directory, a lock beside it rather than the watch's, and the plist of the agent that
	// runs it. Each of the two holds its lock across minutes of work, so a gc waiting on the
	// watch or the watch on a gc would be an interval of neither.
	GCStateDir string
	GCLock     string
	GCPlist    string

	// Where the sweep prunes worktree entries. Its own name rather than HACHIKO_PROJECTS,
	// which it falls back to: the watch only reads that root to say which checkout a busy
	// process is in, and the sweep runs git in every repository under it.
	GCProjectsRoot string

	// How long the watch gives the sweep's last-run stamp before it says nothing is
	// sweeping. Six intervals of the sweep's own ten minutes.
	GCStaleAfter time.Duration

	// How often the sweep prunes docker's build cache and the images nothing references, and
	// how old docker's own `until` filter has to find one of them — passed to docker as
	// written, since the format is its to decide. The one thing the sweep takes that no
	// removed worktree left behind: a week of builds is layers nothing refers to and buildkit
	// cache nothing will reuse, and neither goes when a worktree does. A day apart because
	// the first prune of a day reclaims what the ones after it would have, and a week of age
	// because a layer from this morning is one today's build is about to want.
	GCPruneEvery time.Duration
	GCPruneAge   string

	// `hachiko update`, which is the one thing on this Mac that moves a version. Its own
	// state directory and its own lock for the reason gc's are its own: an upgrade holds
	// the lock across however long brew takes, and the five-minute check may not wait on
	// it. The hour is local, since what it is for is the hours nobody is working.
	UpdateStateDir string
	UpdateLock     string
	UpdateHour     int

	// The repositories `hachiko sync` keeps upstream, and the plist of the agent that does
	// it. Its own state directory for the reason gc's is its own: what sync remembers about
	// a paused repository and a failure it has posted is nothing the five-minute check reads
	// or writes.
	SyncConfig   string
	SyncStateDir string
	SyncPlist    string

	// The three things the watch says about sync. The first is how long the heartbeat may go
	// unwritten before nothing is syncing at all — ten minutes, which is long enough for the
	// whole of a retry ladder against an unreachable remote and short enough that an hour's
	// writing is not lost. The second is how long a tree may stay dirty with sync alive, and
	// the third how far past its own push_delay a repository's oldest unpushed commit may
	// get before it is late rather than waiting.
	SyncStaleAfter time.Duration
	SyncDirtyAfter time.Duration
	SyncLateAfter  time.Duration

	// The plist of the agent that polls Discord for a reply, and how old its heartbeat may
	// get before the watch says nothing is listening. Its state lives beside the rest of
	// what the listener keeps for itself, in the watch's state directory but in no file the
	// watch owns.
	ListenPlist      string
	ListenStaleAfter time.Duration

	// mochi, the Discord tool beside hachiko: its plist, the heartbeat it writes while its
	// gateway connection is up, and how old that may get. The one thing hachiko knows about
	// mochi, which is a module of its own.
	MochiPlist      string
	MochiStamp      string
	MochiStaleAfter time.Duration

	// What is left of the account's Claude usage limits, which `claude-hooks statusline`
	// writes from the one event Claude Code says it in, and how much of a window may be gone
	// before the watch says so. Eighty per cent, because what the message is for is holding
	// work back while there is still some left to hold back.
	UsageStateDir    string
	UsageWarnPercent float64

	// The channel and the one account replies are taken from, both empty unless Tim has
	// filled them in, which is what turns the bot and `hachiko listen` on.
	Discord DiscordConfig

	// How long a resolved incident's post stays in the forum after it was closed. Three
	// days: long enough that a morning's reading of what happened overnight is still there,
	// and short enough that the forum is what is happening rather than an archive. Zero
	// leaves every post where it is.
	PostKeep time.Duration

	Host            string
	hostFromTailnet bool

	// What to call this Mac in a message somebody else writes. shibuya posts about a Mac it
	// cannot reach and has only the check-in to go by, so the name goes with the check-in;
	// without one it falls back to the host slug, which is the router's spelling rather than
	// anybody's.
	Display string
}

// LowGB and CriticalGB are what the log and the messages name the thresholds by,
// so a check with the thresholds turned down reads as the numbers it was given.
func (c Config) LowGB() int64      { return c.LowKB / GiB }
func (c Config) CriticalGB() int64 { return c.CriticalKB / GiB }

// The label of the agent that runs the check itself, spelled from its own plist's name for
// the reason the three below are: a status screen may not name an agent other than the one
// whose plist it looked for.
func (c Config) WatchLabel() string {
	return strings.TrimSuffix(filepath.Base(c.WatchPlist), ".plist")
}

// Where the sweep writes the time it last finished, and the label of the agent that runs
// it. Both derived rather than configured, so the stamp the sweep writes and the stamp the
// watch reads cannot be two paths, and the command a message tells Tim to run cannot name
// an agent other than the one whose plist decided whether to look at all.
func (c Config) GCStamp() string { return filepath.Join(c.GCStateDir, "last-run") }
func (c Config) GCLabel() string {
	return strings.TrimSuffix(filepath.Base(c.GCPlist), ".plist")
}

// When the last update that finished did so, derived rather than configured for the reason
// the two below are: the stamp the update writes and the stamp the schedule reads cannot be
// two paths.
func (c Config) UpdateStamp() string { return filepath.Join(c.UpdateStateDir, "last-run") }

// The same two for sync, derived for the same two reasons: the heartbeat sync writes and
// the heartbeat the watch reads cannot be two paths, and the command a message tells Tim to
// run cannot name an agent other than the one whose plist decided whether to look at all.
func (c Config) SyncStamp() string { return filepath.Join(c.SyncStateDir, "heartbeat") }
func (c Config) SyncLabel() string {
	return strings.TrimSuffix(filepath.Base(c.SyncPlist), ".plist")
}

// And the same two for the listener, whose heartbeat goes in the directory it already keeps
// what it has seen and said in — beside state.json rather than inside it, since a
// five-second loop may not take the lock the sweep holds across a herdr call and an
// `op run`.
func (c Config) ListenStamp() string { return filepath.Join(c.StateDir, "listen", "heartbeat") }
func (c Config) ListenLabel() string {
	return strings.TrimSuffix(filepath.Base(c.ListenPlist), ".plist")
}

func (c Config) MochiLabel() string {
	return strings.TrimSuffix(filepath.Base(c.MochiPlist), ".plist")
}

// The reading `claude-hooks statusline` leaves behind, derived rather than configured for
// the reason every stamp above is: the file the tap writes and the file the watch reads
// cannot be two paths. The folder is what moves, by the one variable both tools read.
func (c Config) UsageFile() string { return filepath.Join(c.UsageStateDir, "latest.json") }

// WorkspaceLabel is the herdr workspace the on-call session goes in: the one for
// this machine's own repository, so a session that starts there has the machine's
// instructions loaded.
func (c Config) WorkspaceLabel() string { return filepath.Base(c.MachineDir) }

// Dir is hachiko's folder in HOME: the four config files mise links into it, the ping token
// tools/shibuya/secrets.sh generates beside them, the references its secrets are resolved from,
// and the state below. The binary stays in ~/Library/Caches, which macOS and cleaning tools
// treat as clearable — the right place for something a build makes again in seconds and the
// wrong one for the record of what has been reported.
func Dir(home string) string { return filepath.Join(home, ".hachiko") }

// The state directories under one folder rather than beside the config, because `sync`
// is a config file and the sweep's, the update's and sync's own folders cannot be told apart
// from it by name.
func stateRoot(home string) string { return filepath.Join(Dir(home), "state") }

// The variables that name each of those four, for a test, a fixture or a second hachiko that
// must not read the live one. The fifth folder is nothing of hachiko's: `claude-hooks
// statusline` writes the usage reading into it, so that variable moves the file for the
// writer as well, which is the whole point of it being one variable.
const (
	StateDirVar       = "HACHIKO_STATE_DIR"
	GCStateDirVar     = "HACHIKO_GC_STATE_DIR"
	UpdateStateDirVar = "HACHIKO_UPDATE_STATE_DIR"
	SyncStateDirVar   = "HACHIKO_SYNC_STATE_DIR"
	UsageStateDirVar  = "HACHIKO_USAGE_STATE_DIR"
)

func FromEnv() Config {
	home := homeDir()

	machineDir := envString("MAC_MINI_DIR", filepath.Join(home, ".mac-mini"))

	state := stateRoot(home)

	stateDir := envString(StateDirVar, filepath.Join(state, "watch"))
	host := os.Getenv("HACHIKO_HOST")
	hostFromTailnet := false
	if host == "" {
		host, hostFromTailnet = machineName(stateDir)
	}

	// In hachiko's own folder, where mise links it beside the config, so the alert has
	// somewhere to go without the binary in the cache knowing where the checkout is.
	envFile := envString("HACHIKO_ENV_FILE", filepath.Join(Dir(home), "hachiko.env.op"))

	projects := envString("HACHIKO_PROJECTS", filepath.Join(home, "projects"))

	return Config{
		LowKB:      envInt64("HACHIKO_LOW_GB", 100) * GiB,
		CriticalKB: envInt64("HACHIKO_CRITICAL_GB", 20) * GiB,

		CPUShare:  float64(envInt64("HACHIKO_CPU_SHARE", 50)),
		CPUWindow: time.Duration(envInt64("HACHIKO_CPU_WINDOW", 3600)) * time.Second,

		StrayWindow: time.Duration(envInt64("HACHIKO_STRAY_WINDOW", 3600)) * time.Second,

		OncallDeadline: time.Duration(envInt64("HACHIKO_ONCALL_DEADLINE", 600)) * time.Second,

		RemindAfter:   time.Duration(envInt64("HACHIKO_REMIND_AFTER", 3600)) * time.Second,
		WarnAfter:     time.Duration(envInt64("HACHIKO_WARN_AFTER", 9900)) * time.Second,
		HandoverAfter: time.Duration(envInt64("HACHIKO_HANDOVER_AFTER", 10800)) * time.Second,

		LogCapKB: envInt64("HACHIKO_LOG_CAP_KB", 10*1024),

		Home:         home,
		MachineDir:   machineDir,
		ScratchRoot:  envString("CLAUDE_CODE_TMPDIR", filepath.Join(home, ".cache", "claude-tmp")),
		LogsRoot:     filepath.Join(home, "Library", "Logs"),
		HerdrRoot:    filepath.Join(home, ".herdr", "worktrees"),
		ProjectsRoot: projects,

		CPUAllowPath: envString("HACHIKO_CPU_ALLOW", filepath.Join(Dir(home), "cpu-allow")),
		EnvFile:      envFile,
		RootHelper:   envString("HACHIKO_ROOT_HELPER", "/usr/local/libexec/claude-root"),
		SwitchConfig: envString("HACHIKO_SWITCH_CONFIG", filepath.Join(Dir(home), "shibuya")),
		SwitchToken:  envString("HACHIKO_SWITCH_TOKEN", filepath.Join(Dir(home), "shibuya-token")),

		StateDir: stateDir,
		WatchPlist: envString("HACHIKO_PLIST",
			filepath.Join(home, "Library", "LaunchAgents", "io.github.timche.hachiko.plist")),

		GCStateDir: envString(GCStateDirVar, filepath.Join(state, "gc")),
		GCLock:     envString("HACHIKO_GC_LOCK", filepath.Join(state, "gc.lock")),
		GCPlist: envString("HACHIKO_GC_PLIST",
			filepath.Join(home, "Library", "LaunchAgents", "io.github.timche.hachiko-gc.plist")),
		GCProjectsRoot: envString("HACHIKO_GC_PROJECTS", projects),
		GCStaleAfter:   time.Duration(envInt64("HACHIKO_GC_STALE", 3600)) * time.Second,
		GCPruneEvery:   time.Duration(envInt64("HACHIKO_GC_PRUNE_EVERY", 86400)) * time.Second,
		GCPruneAge:     envString("HACHIKO_GC_PRUNE_AGE", "168h"),

		UpdateStateDir: envString(UpdateStateDirVar, filepath.Join(state, "update")),
		UpdateLock:     envString("HACHIKO_UPDATE_LOCK", filepath.Join(state, "update.lock")),
		UpdateHour:     envHour("HACHIKO_UPDATE_HOUR", 4),

		SyncConfig:   envString("HACHIKO_SYNC_CONFIG", filepath.Join(Dir(home), "sync")),
		SyncStateDir: envString(SyncStateDirVar, filepath.Join(state, "sync")),
		SyncPlist: envString("HACHIKO_SYNC_PLIST",
			filepath.Join(home, "Library", "LaunchAgents", "io.github.timche.hachiko-sync.plist")),
		SyncStaleAfter: time.Duration(envInt64("HACHIKO_SYNC_STALE", 600)) * time.Second,
		SyncDirtyAfter: time.Duration(envInt64("HACHIKO_SYNC_DIRTY", 600)) * time.Second,
		SyncLateAfter:  time.Duration(envInt64("HACHIKO_SYNC_LATE", 1800)) * time.Second,

		ListenPlist: envString("HACHIKO_LISTEN_PLIST",
			filepath.Join(home, "Library", "LaunchAgents", "io.github.timche.hachiko-listen.plist")),
		// Sync's ten minutes, and for one reason of its own on top of sync's: the listener's
		// agent throttles one start to every five, so a listener that exited for a new binary
		// inside its first five minutes is away for the remainder of them, and anything
		// shorter would report that as a listener that had stopped.
		ListenStaleAfter: time.Duration(envInt64("HACHIKO_LISTEN_STALE", 600)) * time.Second,

		MochiPlist: envString("HACHIKO_MOCHI_PLIST",
			filepath.Join(home, "Library", "LaunchAgents", "io.github.timche.mochi.plist")),
		MochiStamp: envString("HACHIKO_MOCHI_STAMP", filepath.Join(home, ".mochi", "heartbeat")),
		// The listener's ten minutes, for the listener's reason: mochi's agent throttles a
		// start to one every five.
		MochiStaleAfter: time.Duration(envInt64("HACHIKO_MOCHI_STALE", 600)) * time.Second,

		UsageStateDir:    envString(UsageStateDirVar, filepath.Join(state, "usage")),
		UsageWarnPercent: float64(envInt64("HACHIKO_USAGE_WARN", 80)),

		Discord: readDiscordConfig(envString("HACHIKO_DISCORD_CONFIG",
			filepath.Join(Dir(home), "discord"))),

		PostKeep: time.Duration(envInt64("HACHIKO_POST_KEEP", 3*86400)) * time.Second,

		Host:            host,
		hostFromTailnet: hostFromTailnet,
		Display:         envString("HACHIKO_DISPLAY", "Mac mini"),
	}
}

func homeDir() string {
	home, _ := os.UserHomeDir()
	if h := os.Getenv("HOME"); h != "" {
		home = h
	}
	return home
}

func envString(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// Midnight is an hour like any other, so zero cannot mean unset here the way it does for
// every threshold below.
func envHour(name string, fallback int) int {
	v, err := strconv.Atoi(os.Getenv(name))
	if err != nil || v < 0 || v > 23 {
		return fallback
	}
	return v
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
func ClockFromEnv() func() time.Time {
	if at := envInt64("HACHIKO_NOW", 0); at > 0 {
		return func() time.Time { return time.Unix(at, 0) }
	}
	return time.Now
}
