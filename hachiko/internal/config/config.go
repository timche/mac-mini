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

	// The installed root helper, read for the one list it holds: the daemons it will
	// restart without a password. A message about a busy daemon on that list can name the
	// command Tim runs instead of the sudo kill he has to think about.
	RootHelper string

	// The bot token and the approval secret, in a file of their own: `op run` fails outright
	// on a reference it cannot resolve, so naming a field that does not exist yet beside the
	// webhook would stop every alert rather than leaving one feature off. Passed only when
	// the channel and the user are configured, which is the same moment those fields exist.
	DiscordEnvFile string

	// Where shibuya is and the token it takes. The URL is in the checkout, being no secret;
	// the token is not, being one — and it is generated on this Mac rather than resolved from
	// 1Password, so a sweep that happens twice an hour costs no `op run` at all.
	SwitchConfig string
	SwitchToken  string

	StateDir string

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

	// The channel and the one account replies are taken from, both empty unless Tim has
	// filled them in, which is what turns the bot and `hachiko listen` on.
	Discord DiscordConfig

	Host string

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

// Where the sweep writes the time it last finished, and the label of the agent that runs
// it. Both derived rather than configured, so the stamp the sweep writes and the stamp the
// watch reads cannot be two paths, and the command a message tells Tim to run cannot name
// an agent other than the one whose plist decided whether to look at all.
func (c Config) GCStamp() string { return filepath.Join(c.GCStateDir, "last-run") }
func (c Config) GCLabel() string {
	return strings.TrimSuffix(filepath.Base(c.GCPlist), ".plist")
}

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

// WorkspaceLabel is the herdr workspace the on-call session goes in: the one for
// this machine's own repository, so a session that starts there has the machine's
// instructions loaded.
func (c Config) WorkspaceLabel() string { return filepath.Base(c.MachineDir) }

func FromEnv() Config {
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

	projects := envString("HACHIKO_PROJECTS", filepath.Join(home, "projects"))

	return Config{
		LowKB:      envInt64("HACHIKO_LOW_GB", 100) * GiB,
		CriticalKB: envInt64("HACHIKO_CRITICAL_GB", 20) * GiB,
		BigKB:      envInt64("HACHIKO_BIG_KB", GiB),
		GrowthKB:   envInt64("HACHIKO_GROWTH_KB", 2*GiB),

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
		ProjectsRoot: projects,

		CPUAllowPath: envString("HACHIKO_CPU_ALLOW", filepath.Join(home, ".config", "hachiko", "cpu-allow")),
		EnvFile:      envFile,
		RootHelper:   envString("HACHIKO_ROOT_HELPER", "/usr/local/libexec/claude-root"),
		DiscordEnvFile: envString("HACHIKO_DISCORD_ENV_FILE",
			filepath.Join(home, ".local", "bin", "hachiko.discord.env.op")),
		SwitchConfig: envString("HACHIKO_SWITCH_CONFIG",
			filepath.Join(home, ".config", "hachiko", "shibuya")),
		SwitchToken: envString("HACHIKO_SWITCH_TOKEN",
			filepath.Join(home, ".config", "hachiko", "shibuya-token")),

		StateDir: envString("HACHIKO_STATE_DIR", filepath.Join(cache, "hachiko")),

		GCStateDir: envString("HACHIKO_GC_STATE_DIR", filepath.Join(cache, "hachiko-gc")),
		GCLock:     envString("HACHIKO_GC_LOCK", filepath.Join(cache, "hachiko-gc.lock")),
		GCPlist: envString("HACHIKO_GC_PLIST",
			filepath.Join(home, "Library", "LaunchAgents", "io.github.timche.hachiko-gc.plist")),
		GCProjectsRoot: envString("HACHIKO_GC_PROJECTS", projects),
		GCStaleAfter:   time.Duration(envInt64("HACHIKO_GC_STALE", 3600)) * time.Second,

		SyncConfig: envString("HACHIKO_SYNC_CONFIG",
			filepath.Join(home, ".config", "hachiko", "sync")),
		SyncStateDir: envString("HACHIKO_SYNC_STATE_DIR", filepath.Join(cache, "hachiko-sync")),
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

		Discord: readDiscordConfig(envString("HACHIKO_DISCORD_CONFIG",
			filepath.Join(home, ".config", "hachiko", "discord"))),

		Host:    host,
		Display: envString("HACHIKO_DISPLAY", "Mac mini"),
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
func ClockFromEnv() func() time.Time {
	if at := envInt64("HACHIKO_NOW", 0); at > 0 {
		return func() time.Time { return time.Unix(at, 0) }
	}
	return time.Now
}
