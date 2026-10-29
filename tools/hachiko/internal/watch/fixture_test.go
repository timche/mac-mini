package watch

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/oncall"
	"github.com/timche/mac-mini/hachiko/internal/process"
	"github.com/timche/mac-mini/hachiko/internal/shibuya"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
	"github.com/timche/mac-mini/hachiko/internal/update"
	"github.com/timche/mac-mini/hachiko/internal/usage"
)

// The thresholds come down to megabytes and minutes, so an hour of CPU is a few scripted
// samples. Everything a sweep learns or does is a function on the fixture, so nothing here
// reads the real disk, samples the real processes, asks 1Password for anything or reaches
// the network.
const (
	mb = int64(1024)

	// A megabyte, so a log a test caps is one it can write in a moment and the message about
	// it still reads in the unit a real one is measured in.
	logCapKB = 1024
	oneCore  = 300.0

	// The account hachiko runs as, and one that is nobody's but macOS's.
	accountUID = 501
	systemUID  = 0
)

var base = time.Unix(1700000000, 0)

type fixture struct {
	t     *testing.T
	cfg   config.Config
	store statedir.Store
	log   bytes.Buffer

	now      time.Time
	freeGB   int64
	procs    []process.Process
	procsErr error
	pid      int
	uid      int

	// What the forum sweep was asked for, what it answers with, and what it refuses with.
	pruned      []time.Duration
	prunedLines []string
	prunedErr   error

	cwd       map[int]string
	allowlist string

	// The pids launchd itself is running, and the failure to read them: without that list
	// nothing may be called left behind, since every agent on this Mac has a parent of
	// launchd. Empty unless a test puts a job in it, which is the Mac where a process with
	// ppid 1 is nothing launchd knows about.
	jobs    map[int]bool
	jobsErr error

	// Whether this Mac has the agent that runs the worktree sweep, and when that sweep last
	// finished — which is on this very check unless a test pins it, since these tests walk
	// the clock for hours and a sweep is not what they are about.
	gcStamp     time.Time
	gcInstalled bool

	// The same two for sync, and what git says about each repository it is meant to be
	// keeping upstream — all of it on this very check unless a test pins it, for the same
	// reason.
	syncBeat      time.Time
	syncInstalled bool
	syncRepos     []SyncRepo
	syncState     map[string]SyncRepoState

	// And for the listener, whose heartbeat is its own clock with no plist behind it: an
	// unconfigured listener never writes one, so a fixture with none is a Mac where
	// answering from Discord is off.
	listenBeat    time.Time
	listenBeating bool

	// And mochi's, which counts from its plist like sync's: off unless a test installs it.
	mochiBeat      time.Time
	mochiInstalled bool

	// What the status line last said is left of the account's usage limits, and whether
	// anything has said it at all: a Mac where no session has drawn a status line since the
	// file was made has no reading, which is what every check written before this assumes.
	usageReading usage.Reading
	usageRead    bool

	// When the Mac last started, which is long before any stamp unless a test pins it.
	booted time.Time

	// What the nightly update has left behind: the steps it is failing on, and which incident
	// each has been raised as — the second being written back by the check itself, so a test
	// reads it to see that a step is raised once and no more.
	updateFailing   []update.Handed
	updateRaised    map[string]update.Raised
	updateRaisedErr error

	oncallName    string
	oncallBrief   string
	oncallCalls   int
	oncallErr     error
	oncallBlocked bool

	// What herdr would say the on-call agent of each kind is doing, and what hachiko did
	// to it: one entry per cancelled question and the prompt that replaced it.
	status       map[string]string
	statusErr    error
	interrupts   []string
	interruptErr error

	// Whether the esc landed before the interrupt failed. That is the failure that leaves the
	// agent with no question, nothing to do, and hachiko owing it a prompt.
	interruptEsc bool

	// The prompts that went on their own, with no esc before them.
	prompts   []string
	promptErr error

	sent         []string
	outcomes     []bool
	sendAttempts int
	sendErr      error

	// Whether a bot is configured, which is the only thing that makes a thread: which
	// thread each message went into, and which incidents opened one.
	threads bool
	sentTo  []string
	opened  []string

	// What each sweep told shibuya, and what the switch answered: the state to remember and
	// the line to log when that state is new.
	checkins     []shibuya.Checkin
	checkinState string
	checkinSay   string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	home := t.TempDir()
	for _, dir := range []string{"tmp", "elsewhere", "Library/Logs", "Library/Caches", "projects"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.Config{
		LowKB:      100 * config.GiB,
		CriticalKB: 20 * config.GiB,

		CPUShare:       50,
		CPUWindow:      time.Hour,
		StrayWindow:    time.Hour,
		OncallDeadline: 10 * time.Minute,

		LogCapKB: logCapKB,

		Home:         home,
		MachineDir:   filepath.Join(home, ".mac-mini"),
		ScratchRoot:  filepath.Join(home, "scratch"),
		LogsRoot:     filepath.Join(home, "Library", "Logs"),
		HerdrRoot:    filepath.Join(home, ".herdr", "worktrees"),
		ProjectsRoot: filepath.Join(home, "projects"),

		UsageWarnPercent: 80,

		CPUAllowPath: filepath.Join(home, "cpu-allow"),
		EnvFile:      filepath.Join(home, "hachiko.env.op"),
		StateDir:     filepath.Join(home, "Library", "Caches", "hachiko"),

		Host: "mac-mini",
	}

	return &fixture{
		t:      t,
		cfg:    cfg,
		store:  statedir.Store{Dir: cfg.StateDir},
		now:    base,
		freeGB: 500,
		// A pid no sample of the fixture's holds, so nothing is excluded for being
		// hachiko's own unless a check says so.
		pid: 999001,
		// A switch that takes every check-in, which is the Mac with shibuya configured and
		// answering; a test that wants it otherwise says so.
		checkinState: shibuya.Sent,
		// The account hachiko runs as, which every process the fixture makes belongs to
		// unless a test says otherwise: a process of somebody else's is a system process, and
		// that is a test of its own.
		uid:  accountUID,
		cwd:  map[int]string{},
		jobs: map[int]bool{},
		// Nothing is waiting on a question until a test says so, which is what every
		// check written before the wait existed assumes.
		status: map[string]string{},

		gcInstalled: true,

		syncInstalled: true,
		syncState:     map[string]SyncRepoState{},
	}
}

func (f *fixture) lastSweep() time.Time {
	if f.gcStamp.IsZero() {
		return f.now
	}
	return f.gcStamp
}

func (f *fixture) lastBeat() time.Time {
	if f.syncBeat.IsZero() {
		return f.now
	}
	return f.syncBeat
}

func (f *fixture) lastMochiBeat() time.Time {
	if f.mochiBeat.IsZero() {
		return f.now
	}
	return f.mochiBeat
}

func (f *fixture) lastListenBeat() time.Time {
	if f.listenBeat.IsZero() {
		return f.now
	}
	return f.listenBeat
}

func (f *fixture) at(seconds int64) *fixture {
	f.now = base.Add(time.Duration(seconds) * time.Second)
	return f
}

func (f *fixture) deps() Deps {
	return Deps{
		Now:    func() time.Time { return f.now },
		Log:    &f.log,
		Getpid: func() int { return f.pid },
		Getuid: func() int { return f.uid },

		FreeKB: func() (int64, error) { return f.freeGB * config.GiB, nil },

		Processes: func() ([]process.Process, error) {
			if f.procsErr != nil {
				return nil, f.procsErr
			}
			return f.procs, nil
		},
		CWD: func(pid int) string { return f.cwd[pid] },
		LaunchdJobs: func() (map[int]bool, error) {
			if f.jobsErr != nil {
				return nil, f.jobsErr
			}
			return f.jobs, nil
		},

		GCLastRun: func() (time.Time, bool) { return f.lastSweep(), f.gcInstalled },

		SyncLastBeat:  func() (time.Time, bool) { return f.lastBeat(), f.syncInstalled },
		SyncRepos:     func() []SyncRepo { return f.syncRepos },
		SyncRepoState: func(repo SyncRepo) SyncRepoState { return f.syncState[repo.Path] },

		ListenLastBeat: func() (time.Time, bool) { return f.lastListenBeat(), f.listenBeating },
		MochiLastBeat:  func() (time.Time, bool) { return f.lastMochiBeat(), f.mochiInstalled },
		UsageReading:   func() (usage.Reading, bool) { return f.usageReading, f.usageRead },
		Booted:         func() time.Time { return f.booted },

		UpdateFailing: func() []update.Handed { return f.updateFailing },
		UpdateRaised: func() map[string]update.Raised {
			raised := map[string]update.Raised{}
			for step, was := range f.updateRaised {
				raised[step] = was
			}
			return raised
		},
		SetUpdateRaised: func(raised map[string]update.Raised) error {
			if f.updateRaisedErr != nil {
				return f.updateRaisedErr
			}
			f.updateRaised = raised
			return nil
		},

		Oncall: func(name, brief string) (oncall.Session, error) {
			f.oncallCalls++
			f.oncallName, f.oncallBrief = name, brief

			switch {
			case f.oncallErr != nil:
				return oncall.Session{}, f.oncallErr
			case f.oncallBlocked:
				// The agent is on a question whose cancelling did not work, so nothing of
				// the update reached it.
				return oncall.Session{
					Tab: name + "-0000",
					Say: fmt.Sprintf("The agent is already waiting for you in herdr — attach: workspace `.mac-mini`, tab `%s-0000`. This update did not reach it.", name),
				}, nil
			}

			session := oncall.Session{
				Tab:       name + "-0000",
				Delivered: true,
				Say:       fmt.Sprintf("An agent is looking into it — attach in herdr: workspace `.mac-mini`, tab `%s-0000`. Details to follow.", name),
			}

			// What the real oncaller does with an agent herdr would refuse a prompt to: the
			// question goes and the brief takes its place, which leaves it working.
			if f.status[name] == oncall.StatusBlocked {
				f.interrupts = append(f.interrupts, "The situation changed\n"+brief)
				f.status[name] = oncall.StatusWorking
				session.Cancelled = true
			}
			return session, nil
		},

		AgentStatus: func(kind string) (string, error) {
			if f.statusErr != nil {
				return "", f.statusErr
			}
			if status, ok := f.status[kind]; ok {
				return status, nil
			}
			return "idle", nil
		},
		Interrupt: func(kind, lead, data string) (bool, error) {
			if f.interruptErr != nil {
				// An esc that landed without its prompt leaves the agent off its question and
				// with nothing to do, which from herdr is indistinguishable from Tim having
				// answered it.
				if f.interruptEsc {
					f.status[kind] = "idle"
				}
				return f.interruptEsc, f.interruptErr
			}
			f.interrupts = append(f.interrupts, lead+"\n"+data)
			// The agent herdr refuses a prompt to is the one still on its question, so a
			// cancelled question leaves it working on what it was handed instead.
			f.status[kind] = oncall.StatusWorking
			return true, nil
		},
		Prompt: func(kind, lead, data string) error {
			if f.promptErr != nil {
				return f.promptErr
			}
			f.prompts = append(f.prompts, lead+"\n"+data)
			// A prompt queues behind whatever the agent is doing, and an agent that has been
			// handed one is working on it.
			f.status[kind] = oncall.StatusWorking
			return nil
		},
		Send: func(out discord.Outgoing) (string, error) {
			f.sendAttempts++
			if f.sendErr != nil {
				return "", f.sendErr
			}
			f.sent = append(f.sent, out.Text)
			f.outcomes = append(f.outcomes, out.Outcome)
			f.sentTo = append(f.sentTo, out.Thread)

			// What the bot answers when a message opened a thread: an id of its own, which
			// the sweep records against the incident. Without a bot there is none.
			if out.OpenThread != "" && f.threads {
				f.opened = append(f.opened, out.OpenThread)
				return "thread-" + out.OpenThread, nil
			}
			return "", nil
		},
		PrunePosts: func(keep time.Duration) ([]string, error) {
			f.pruned = append(f.pruned, keep)
			if f.prunedErr != nil {
				return nil, f.prunedErr
			}
			return f.prunedLines, nil
		},
		CheckIn: func(in shibuya.Checkin) (string, string) {
			f.checkins = append(f.checkins, in)
			return f.checkinState, f.checkinSay
		},
	}
}

// A stand-in for the installed /usr/local/libexec/claude-root, in the shape the real one
// writes its allowlist in, so the parsing is read off the file rather than described.
func (f *fixture) rootHelper(daemons ...string) {
	f.t.Helper()

	body := "#!/bin/bash\nallowed_daemons=(\n"
	for _, name := range daemons {
		body += fmt.Sprintf("  %q\n", name+" com.apple."+name)
	}
	body += ")\n"

	path := filepath.Join(f.cfg.Home, "claude-root")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		f.t.Fatal(err)
	}
	f.cfg.RootHelper = path
}

func (f *fixture) allow(lines string) {
	f.t.Helper()
	if err := os.WriteFile(f.cfg.CPUAllowPath, []byte(lines), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// One process in the shape ps prints it. The cumulative CPU time is the number every
// CPU rule is about.
func (f *fixture) proc(pid int, cpuSeconds float64, start string, command string) {
	startedAt, err := time.ParseInLocation("Mon Jan 2 15:04:05 2006", start, time.Local)
	if err != nil {
		f.t.Fatal(err)
	}

	f.procs = []process.Process{{
		PID:       pid,
		PPID:      1,
		UID:       accountUID,
		User:      "timche",
		RSSKB:     524288,
		CPU:       time.Duration(cpuSeconds * float64(time.Second)),
		Start:     start,
		StartedAt: startedAt,
		Command:   command,
	}}
}

// The same as something launchd started and root owns, which is every daemon on the Mac and
// the shape hachiko used to call orphaned.
func (f *fixture) systemProc(pid int, cpuSeconds float64, start, command string) {
	f.t.Helper()
	f.proc(pid, cpuSeconds, start, command)
	f.procs[0].UID, f.procs[0].User = systemUID, "root"
}

func (f *fixture) setProcs(procs ...process.Process) { f.procs = procs }

// What the status line last said about one of the account's usage windows, as `claude-hooks
// statusline` wrote it down: the share of it that has gone and when it starts again.
func (f *fixture) usageAt(key string, used float64, resets time.Time) {
	if f.usageReading.Windows == nil {
		f.usageReading.Windows = map[string]usage.Window{}
	}
	f.usageReading.ReadAt = f.now
	f.usageReading.Windows[key] = usage.Window{Used: used, Resets: resets}
	f.usageRead = true
}

const firstStart = "Mon Sep 28 20:06:06 2026"

// Samples five minutes apart, each adding `per` seconds of CPU: 150 of the 300
// between two samples is half a core, which is the threshold, and 240 is well over
// it. Twelve intervals make the hour the rule is about, and measuring twelve
// intervals takes thirteen samples.
func (f *fixture) cpuRuns(samples int, per float64) string {
	f.t.Helper()

	var all strings.Builder
	for i := range samples {
		f.proc(7018, float64(i)*per, firstStart, "/usr/local/bin/node worker.js")
		all.WriteString(f.at(int64(i) * 300).sweep())
	}
	return all.String()
}

func (f *fixture) sweep() string {
	f.t.Helper()
	f.log.Reset()

	s := sweeper{cfg: f.cfg, deps: f.deps(), store: f.store, dry: false}
	if err := s.run(); err != nil {
		f.t.Fatalf("sweep: %v", err)
	}
	return f.log.String()
}

func (f *fixture) dryRun() string {
	f.t.Helper()
	f.log.Reset()

	s := sweeper{cfg: f.cfg, deps: f.deps(), store: f.store, dry: true}
	if err := s.run(); err != nil {
		f.t.Fatalf("dry run: %v", err)
	}
	return f.log.String()
}

func (f *fixture) state() *statedir.State {
	f.t.Helper()
	state, err := f.store.Load()
	if err != nil {
		f.t.Fatal(err)
	}
	return state
}

func (f *fixture) lastSent() string {
	f.t.Helper()
	if len(f.sent) == 0 {
		f.t.Fatal("nothing was sent")
	}
	return f.sent[len(f.sent)-1]
}

func (f *fixture) sentCount() int { return len(f.sent) }

// What `hachiko notify` does to the state: a marker and nothing else, so it needs no
// lock and cannot wait on a sweep holding one.
func (f *fixture) notify(incident string) {
	f.t.Helper()
	if err := f.store.MarkReported(incident, "", false, false); err != nil {
		f.t.Fatal(err)
	}
}

// A report with the one line the wait reads out of it: what the session says it would do
// if nobody answers its question.
func (f *fixture) notifyWithFallback(incident, fallback string) {
	f.t.Helper()
	if err := f.store.MarkReported(incident, fallback, false, false); err != nil {
		f.t.Fatal(err)
	}
}

// The message the session marks as the one that resolves the incident, which is the only
// kind that ends the wait.
func (f *fixture) notifyOutcome(incident string) {
	f.t.Helper()
	if err := f.store.MarkReported(incident, "", true, false); err != nil {
		f.t.Fatal(err)
	}
}

// The message it sends before its one fix, which is what records that the attempt happened.
func (f *fixture) notifyAttempt(incident string) {
	f.t.Helper()
	if err := f.store.MarkReported(incident, "", false, true); err != nil {
		f.t.Fatal(err)
	}
}

// The agent puts its question up, which is where the wait starts.
func (f *fixture) blocks(kind string) { f.status[kind] = oncall.StatusBlocked }

func (f *fixture) lastInterrupt() string {
	f.t.Helper()
	if len(f.interrupts) == 0 {
		f.t.Fatal("no question was cancelled")
	}
	return f.interrupts[len(f.interrupts)-1]
}

// The last prompt that went on its own, with no esc before it: what hachiko owes an agent
// whose question its own esc already took away.
func (f *fixture) lastPrompt() string {
	f.t.Helper()
	if len(f.prompts) == 0 {
		f.t.Fatal("no prompt went on its own")
	}
	return f.prompts[len(f.prompts)-1]
}

func (f *fixture) onlyPendingID() string {
	f.t.Helper()
	ids := statedir.SortedKeys(f.state().Pending)
	if len(ids) != 1 {
		f.t.Fatalf("expected one pending incident, got %v", ids)
	}
	return ids[0]
}

var errSendFailed = errors.New("Discord answered 500")
