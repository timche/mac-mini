package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// The thresholds come down to megabytes and minutes, so a file growing past one is a
// few writes and an hour of CPU is a few scripted samples. Everything a sweep learns
// or does is a function on the fixture, so nothing here reads the real disk, samples
// the real processes, asks 1Password for anything or reaches the network.
const (
	mb      = int64(1024)
	bigKB   = 1024
	growKB  = 2048
	oneCore = 300.0

	// The account hachiko runs as, and one that is nobody's but macOS's.
	accountUID = 501
	systemUID  = 0
)

var base = time.Unix(1700000000, 0)

type fixture struct {
	t     *testing.T
	cfg   config.Config
	store Store
	log   bytes.Buffer

	now      time.Time
	freeGB   int64
	watched  []string
	procs    []Process
	procsErr error
	pid      int
	uid      int

	// A file the incident is about that gains keepKB on every check, which is what keeps an
	// incident open across a test that walks the clock: a file that has stopped growing is a
	// trigger that has cleared, and that is a test of its own.
	keepGrowing string
	keepKB      int64

	// What the walk reports it could not look at, and what it was told to skip.
	stalls   []string
	cutShort bool
	skipped  []string

	// Sizes the walk reports with no bytes behind them, for a test about what a message
	// says rather than about what the walk finds: a message that names 4.3 GB is one no
	// test could write 4.3 GB to prove.
	claimed []FileSize

	// A claimed file that gains claimStep on every check, which is what keepGrowing does for
	// a real one: an incident that has stopped is a trigger that has cleared, and that is
	// news of its own.
	keepClaiming string
	claimStep    int64
	claimedKB    int64

	// A truncate that is recorded and not carried out, for a test whose paths are the real
	// machine's rather than the temporary home's: emptying /private/tmp/devbackend.log to
	// read a message back would empty the Mac's own file.
	noTruncate bool

	cwd       map[int]string
	writer    string
	allowlist string

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
	sendAttempts int
	sendErr      error
	truncated    []string

	// Whether a bot is configured, which is the only thing that makes a thread: which
	// thread each message went into, and which incidents opened one.
	threads bool
	sentTo  []string
	opened  []string

	// What each sweep told shibuya, and what the switch answered: the state to remember and
	// the line to log when that state is new.
	checkins     []Checkin
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
		BigKB:      bigKB,
		GrowthKB:   growKB,

		CPUShare:       50,
		CPUWindow:      time.Hour,
		OncallDeadline: 10 * time.Minute,

		DirTimeout:  3 * time.Second,
		WalkTimeout: time.Minute,
		StallRetry:  time.Hour,

		Home:         home,
		MachineDir:   filepath.Join(home, ".mac-mini"),
		TmpRoot:      filepath.Join(home, "tmp"),
		ScratchRoot:  filepath.Join(home, "scratch"),
		LogsRoot:     filepath.Join(home, "Library", "Logs"),
		HerdrRoot:    filepath.Join(home, ".herdr", "worktrees"),
		ProjectsRoot: filepath.Join(home, "projects"),

		CPUAllowPath: filepath.Join(home, "cpu-allow"),
		EnvFile:      filepath.Join(home, "hachiko.env.op"),
		StateDir:     filepath.Join(home, "Library", "Caches", "hachiko"),

		Host: "mac-mini",
	}

	return &fixture{
		t:      t,
		cfg:    cfg,
		store:  Store{dir: cfg.StateDir},
		now:    base,
		freeGB: 500,
		// A pid no sample of the fixture's holds, so nothing is excluded for being
		// hachiko's own unless a check says so.
		pid: 999001,
		// A switch that takes every check-in, which is the Mac with shibuya configured and
		// answering; a test that wants it otherwise says so.
		checkinState: checkinSent,
		// The account hachiko runs as, which every process the fixture makes belongs to
		// unless a test says otherwise: a process of somebody else's is a system process, and
		// that is a test of its own.
		uid: accountUID,
		cwd: map[int]string{},
		// Built the way the real lsof reader builds one, so a test reads the writer back in
		// the shape a message carries rather than in one nothing produces.
		writer: wording.PIDLabel("fake-worker", "4242"),
		// Nothing is waiting on a question until a test says so, which is what every
		// check written before the wait existed assumes.
		status: map[string]string{},
	}
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

		FreeKB:   func() (int64, error) { return f.freeGB * config.GiB, nil },
		BigFiles: f.bigFiles,

		Writers: func(string) string { return f.writer },
		Truncate: func(path string) error {
			if !f.noTruncate {
				if err := os.Truncate(path, 0); err != nil {
					return err
				}
			}
			f.truncated = append(f.truncated, path)
			return nil
		},

		Processes: func() ([]Process, error) {
			if f.procsErr != nil {
				return nil, f.procsErr
			}
			return f.procs, nil
		},
		CWD: func(pid int) string { return f.cwd[pid] },

		Oncall: func(name, brief string) (OncallSession, error) {
			f.oncallCalls++
			f.oncallName, f.oncallBrief = name, brief

			switch {
			case f.oncallErr != nil:
				return OncallSession{}, f.oncallErr
			case f.oncallBlocked:
				// The agent is on a question whose cancelling did not work, so nothing of
				// the update reached it.
				return OncallSession{
					Tab: name + "-0000",
					Say: fmt.Sprintf("The agent is already waiting for you in herdr — attach: workspace `.mac-mini`, tab `%s-0000`. This update did not reach it.", name),
				}, nil
			}

			session := OncallSession{
				Tab:       name + "-0000",
				Delivered: true,
				Say:       fmt.Sprintf("An agent is looking into it — attach in herdr: workspace `.mac-mini`, tab `%s-0000`. Details to follow.", name),
			}

			// What the real oncaller does with an agent herdr would refuse a prompt to: the
			// question goes and the brief takes its place, which leaves it working.
			if f.status[name] == statusBlocked {
				f.interrupts = append(f.interrupts, "The situation changed\n"+brief)
				f.status[name] = statusWorking
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
			f.status[kind] = statusWorking
			return true, nil
		},
		Prompt: func(kind, lead, data string) error {
			if f.promptErr != nil {
				return f.promptErr
			}
			f.prompts = append(f.prompts, lead+"\n"+data)
			// A prompt queues behind whatever the agent is doing, and an agent that has been
			// handed one is working on it.
			f.status[kind] = statusWorking
			return nil
		},
		Send: func(out Outgoing) (string, error) {
			f.sendAttempts++
			if f.sendErr != nil {
				return "", f.sendErr
			}
			f.sent = append(f.sent, out.Text)
			f.sentTo = append(f.sentTo, out.Thread)

			// What the bot answers when a message opened a thread: an id of its own, which
			// the sweep records against the incident. Without a bot there is none.
			if out.OpenThread != "" && f.threads {
				f.opened = append(f.opened, out.OpenThread)
				return "thread-" + out.OpenThread, nil
			}
			return "", nil
		},
		CheckIn: func(in Checkin) (string, string) {
			f.checkins = append(f.checkins, in)
			return f.checkinState, f.checkinSay
		},
	}
}

// Real files with real allocated sizes, so the truncate rules and the sparse-image
// reasoning are exercised rather than described.
func (f *fixture) bigFiles(skip []string) WalkResult {
	f.skipped = skip

	out := WalkResult{CutShort: f.cutShort}

	// A directory the walk was told to skip is never opened, so it cannot be reported as
	// one that would not answer.
	for _, dir := range f.stalls {
		if !contains(skip, dir) {
			out.Stalled = append(out.Stalled, dir)
		}
	}

	if f.claimed != nil {
		out.Files = append(out.Files, f.claimed...)
		sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Path < out.Files[j].Path })
		return out
	}

	for _, path := range f.watched {
		info, err := os.Lstat(path)
		if err != nil {
			continue
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			continue
		}
		if kb := st.Blocks / 2; kb >= f.cfg.BigKB {
			out.Files = append(out.Files, FileSize{Path: path, KB: kb})
		}
	}
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Path < out.Files[j].Path })
	return out
}

// Appends kb kibibytes of real bytes, which is what makes the file's allocated size
// the number the sweep reads.
func (f *fixture) grow(rel string, kb int64) string {
	f.t.Helper()

	path := filepath.Join(f.cfg.Home, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		f.t.Fatal(err)
	}
	defer file.Close()

	if _, err := file.Write(bytes.Repeat([]byte("x"), int(kb*1024))); err != nil {
		f.t.Fatal(err)
	}

	if !contains(f.watched, path) {
		f.watched = append(f.watched, path)
	}
	return path
}

// Whatever size the test wants the walk to report for a path. A relative one gets a real
// empty file under the temporary home, so a truncate has something to open; an absolute one
// is taken as written and nothing is created, which is for a test about what a message says
// about /private/tmp rather than about what is on this disk.
func (f *fixture) claim(rel string, kb int64) string {
	f.t.Helper()

	path := rel
	if !filepath.IsAbs(rel) {
		path = filepath.Join(f.cfg.Home, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			f.t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			f.t.Fatal(err)
		}
	}

	for i, file := range f.claimed {
		if file.Path == path {
			f.claimed[i].KB = kb
			return path
		}
	}
	f.claimed = append(f.claimed, FileSize{Path: path, KB: kb})
	return path
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

	f.procs = []Process{{
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

func (f *fixture) setProcs(procs ...Process) { f.procs = procs }

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
	if f.keepGrowing != "" {
		f.grow(f.keepGrowing, f.keepKB)
	}
	if f.keepClaiming != "" {
		f.claimedKB += f.claimStep
		f.claim(f.keepClaiming, f.claimedKB)
	}
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

func (f *fixture) state() *State {
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
	if err := f.store.MarkReported(incident, "", false); err != nil {
		f.t.Fatal(err)
	}
}

// A report with the one line the wait reads out of it: what the session says it would do
// if nobody answers its question.
func (f *fixture) notifyWithFallback(incident, fallback string) {
	f.t.Helper()
	if err := f.store.MarkReported(incident, fallback, false); err != nil {
		f.t.Fatal(err)
	}
}

// The message the session marks as the one that resolves the incident, which is the only
// kind that ends the wait.
func (f *fixture) notifyOutcome(incident string) {
	f.t.Helper()
	if err := f.store.MarkReported(incident, "", true); err != nil {
		f.t.Fatal(err)
	}
}

// The agent puts its question up, which is where the wait starts.
func (f *fixture) blocks(kind string) { f.status[kind] = statusBlocked }

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
	ids := sortedKeys(f.state().Pending)
	if len(ids) != 1 {
		f.t.Fatalf("expected one pending incident, got %v", ids)
	}
	return ids[0]
}

func wants(t *testing.T, text, want string) {
	t.Helper()
	if !strings.Contains(text, want) {
		t.Errorf("expected %q in:\n%s", want, text)
	}
}

func lacks(t *testing.T, text, unwanted string) {
	t.Helper()
	if strings.Contains(text, unwanted) {
		t.Errorf("did not expect %q in:\n%s", unwanted, text)
	}
}

func equal[T comparable](t *testing.T, got, want T, what string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v, want %v", what, got, want)
	}
}

var errSendFailed = errors.New("the webhook answered 500")
