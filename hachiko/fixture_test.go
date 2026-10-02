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
)

var base = time.Unix(1700000000, 0)

type fixture struct {
	t     *testing.T
	cfg   Config
	store Store
	log   bytes.Buffer

	now     time.Time
	freeGB  int64
	watched []string
	procs   []Process
	pid     int

	// What the walk reports it could not look at, and what it was told to skip.
	stalls   []string
	cutShort bool
	skipped  []string

	cwd       map[int]string
	writer    string
	allowlist string

	oncallName    string
	oncallBrief   string
	oncallCalls   int
	oncallErr     error
	oncallBlocked bool

	sent         []string
	sendAttempts int
	sendErr      error
	truncated    []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	home := t.TempDir()
	for _, dir := range []string{"tmp", "elsewhere", "Library/Logs", "Library/Caches", "projects"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cfg := Config{
		LowKB:      100 * gib,
		CriticalKB: 20 * gib,
		BigKB:      bigKB,
		GrowthKB:   growKB,

		CPUShare:       50,
		CPUWindow:      time.Hour,
		OncallDeadline: 10 * time.Minute,

		DirTimeout:  3 * time.Second,
		WalkTimeout: time.Minute,

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
		pid:    999001,
		cwd:    map[int]string{},
		writer: "4242 (fake-worker)",
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

		FreeKB:   func() (int64, error) { return f.freeGB * gib, nil },
		BigFiles: f.bigFiles,

		Writers: func(string) string { return f.writer },
		Truncate: func(path string) error {
			if err := os.Truncate(path, 0); err != nil {
				return err
			}
			f.truncated = append(f.truncated, path)
			return nil
		},

		Processes: func() ([]Process, error) { return f.procs, nil },
		CWD:       func(pid int) string { return f.cwd[pid] },

		Oncall: func(name, brief string) (OncallSession, error) {
			f.oncallCalls++
			f.oncallName, f.oncallBrief = name, brief

			switch {
			case f.oncallErr != nil:
				return OncallSession{}, f.oncallErr
			case f.oncallBlocked:
				return OncallSession{
					Tab: name + "-0000",
					Say: fmt.Sprintf("The agent is already waiting for you in herdr (workspace .mac-mini, tab %s-0000); this update was not delivered to it.", name),
				}, nil
			}

			return OncallSession{
				Tab:       name + "-0000",
				Delivered: true,
				Say:       fmt.Sprintf("An agent is looking into it in herdr (workspace .mac-mini, tab %s-0000); details to follow.", name),
			}, nil
		},

		Send: func(message string) error {
			f.sendAttempts++
			if f.sendErr != nil {
				return f.sendErr
			}
			f.sent = append(f.sent, message)
			return nil
		},
	}
}

// Real files with real allocated sizes, so the truncate rules and the sparse-image
// reasoning are exercised rather than described.
func (f *fixture) bigFiles(skip []string) WalkResult {
	f.skipped = skip

	out := WalkResult{Stalled: f.stalls, CutShort: f.cutShort}
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
		RSSKB:     524288,
		CPU:       time.Duration(cpuSeconds * float64(time.Second)),
		Start:     start,
		StartedAt: startedAt,
		Command:   command,
	}}
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
	if err := f.store.MarkReported(incident); err != nil {
		f.t.Fatal(err)
	}
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
