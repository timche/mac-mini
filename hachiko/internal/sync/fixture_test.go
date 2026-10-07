package sync

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/discord"
)

var base = time.Unix(1700000000, 0)

// A repository as the fixture's git answers for one, which is the whole of what a pass can
// learn: what is uncommitted, what is not on the remote and when the oldest of those was
// made, what is on the remote and not here, and the two shas a pause is re-armed against.
//
// Every command that changes any of it goes through the fixture, so a commit really does
// empty the status and a push really does empty the unpushed list — a stub that answered the
// same thing before and after would be a test of the reading and not of the pass.
type fakeRepo struct {
	status      string
	staged      []string
	unpushed    []string
	oldest      time.Time
	behind      []string
	head        string
	upstream    string
	noUpstream  bool
	midRebase   bool
	detached    bool
	pseudoRef   string
	pushRefused int
	pushText    string

	// Scripted failures, each as the text the command would have printed. A count, because
	// the whole of the retry ladder is about a command that fails a few times and then does
	// not.
	fetchFails  int
	fetchText   string
	commitFails int
	rebaseText  string
	abortFails  bool
}

type fixture struct {
	t    *testing.T
	home string
	now  time.Time
	log  bytes.Buffer

	repo   config.SyncRepo
	retry  config.SyncRetry
	tree   *fakeRepo
	dry    bool
	noSelf bool
	selfID fileID

	calls   []string
	slept   []time.Duration
	sent    []discord.Outgoing
	sendErr string
	threads int
}

func newFixture(t *testing.T) *fixture {
	home := t.TempDir()

	f := &fixture{
		t:     t,
		home:  home,
		now:   base,
		repo:  config.SyncRepo{Path: filepath.Join(home, "repo"), Debounce: 5 * time.Second, Remote: "origin", Pull: true, Recheck: 10 * time.Minute, FetchInterval: time.Minute},
		retry: config.SyncRetry{Attempts: 3, Base: time.Second, Max: 4 * time.Second},
		tree:  &fakeRepo{head: "aaa", upstream: "bbb"},
	}
	return f
}

func (f *fixture) at(seconds int64) *fixture {
	f.now = base.Add(time.Duration(seconds) * time.Second)
	return f
}

// Sleeping moves the clock, so a retry ladder's own backoff is the time that passes in a
// test and a push delay runs out without anything waiting for it.
func (f *fixture) deps() Deps {
	return Deps{
		Now:   func() time.Time { return f.now },
		Log:   &f.log,
		Sleep: func(d time.Duration) { f.slept = append(f.slept, d); f.now = f.now.Add(d) },
		Git:   f.git,
		Send:  f.send,
		Self:  func() (fileID, bool) { return f.selfID, !f.noSelf },
	}
}

func (f *fixture) git(dir string, args ...string) Output {
	f.calls = append(f.calls, strings.Join(args, " "))
	t := f.tree

	ok := func(stdout string) Output { return Output{OK: true, Stdout: stdout} }
	bad := func(text string) Output { return Output{Text: text} }

	switch strings.Join(args, " ") {
	case "rev-parse --is-inside-work-tree":
		return ok("true\n")
	case "rev-parse --abbrev-ref HEAD":
		if t.detached {
			return ok("HEAD\n")
		}
		return ok("main\n")
	case "rev-parse -q --verify MERGE_HEAD", "rev-parse -q --verify CHERRY_PICK_HEAD", "rev-parse -q --verify REVERT_HEAD":
		if args[3] == t.pseudoRef {
			return ok("ccc\n")
		}
		return bad("")
	case "rev-parse --abbrev-ref --symbolic-full-name @{upstream}":
		if t.noUpstream {
			return bad("fatal: no upstream configured for branch 'main'")
		}
		return ok("origin/main\n")
	case "rev-parse HEAD":
		return ok(t.head + "\n")
	case "rev-parse @{upstream}":
		return ok(t.upstream + "\n")
	case "status --porcelain=v1 -z":
		return ok(t.status)
	case "log --oneline @{upstream}..HEAD":
		if t.noUpstream {
			return bad("fatal: no upstream")
		}
		return ok(strings.Join(t.unpushed, "\n"))
	case "log --oneline HEAD..@{upstream}":
		return ok(strings.Join(t.behind, "\n"))
	case "log --format=%ct @{upstream}..HEAD":
		if len(t.unpushed) == 0 || t.oldest.IsZero() {
			return ok("")
		}
		var stamps []string
		for range t.unpushed {
			stamps = append(stamps, fmt.Sprint(t.oldest.Unix()))
		}
		return ok(strings.Join(stamps, "\n"))
	case "diff --cached --name-only -z":
		return ok(strings.Join(t.staged, "\x00"))
	case "add -A":
		return ok("")
	case "rebase --abort":
		if t.abortFails {
			return bad("fatal: no rebase in progress")
		}
		t.midRebase = false
		return ok("")
	case "fetch origin":
		if t.fetchFails > 0 {
			t.fetchFails--
			return bad(t.fetchText)
		}
		return ok("")
	}

	switch {
	case args[0] == "commit":
		if t.commitFails > 0 {
			t.commitFails--
			return bad("error: cannot lock ref 'HEAD': Unable to create '.git/index.lock': File exists.")
		}
		t.unpushed = append([]string{"deadbee " + args[2]}, t.unpushed...)
		if t.oldest.IsZero() {
			t.oldest = f.now
		}
		t.status, t.staged = "", nil
		t.head = fmt.Sprintf("head-%d", len(t.unpushed))
		return ok("")

	case args[0] == "push":
		if t.pushRefused > 0 {
			t.pushRefused--
			return bad(t.pushText)
		}
		t.unpushed, t.oldest, t.noUpstream = nil, time.Time{}, false
		return ok("")

	case args[0] == "pull":
		if t.rebaseText != "" {
			t.midRebase = true
			return bad(t.rebaseText)
		}
		t.behind, t.upstream = nil, fmt.Sprintf("up-%d", len(f.calls))
		return ok("")
	}

	f.t.Fatalf("the fixture was asked for `git %s`, which it does not answer", strings.Join(args, " "))
	return Output{}
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

func (f *fixture) daemon() *daemon {
	return &daemon{
		cfg:   config.Config{Home: f.home, Host: "mac-mini", SyncStateDir: filepath.Join(f.home, "state")},
		sync:  config.Sync{Mode: config.ModeLive, Retry: f.retry, Repos: []config.SyncRepo{f.repo}},
		deps:  f.deps(),
		store: &Store{Dir: filepath.Join(f.home, "state")},
		dry:   f.dry,
		beat:  !f.dry,
	}
}

// One pass, with whatever the fixture's repository and script say, and the log it wrote.
func (f *fixture) pass(mode pushMode) (Result, string) {
	f.t.Helper()
	f.log.Reset()

	d := f.daemon()
	result := d.passer(f.repo).run(mode)
	d.record(f.repo, d.git(f.repo.Path), result)

	return result, f.log.String()
}

// The loop, which is the half that decides when a pass happens. It is driven a tick at a
// time rather than being started, since a daemon loop does not return.
func (f *fixture) loop() (*daemon, *loop) {
	d := f.daemon()
	l := d.loopFor(f.repo, 0)
	l.start()

	return d, l
}

func (f *fixture) lastSent() string {
	f.t.Helper()
	if len(f.sent) == 0 {
		f.t.Fatal("nothing was sent")
	}
	return f.sent[len(f.sent)-1].Text
}

func (f *fixture) state() *State {
	f.t.Helper()

	state, err := (&Store{Dir: filepath.Join(f.home, "state")}).load()
	if err != nil {
		f.t.Fatal(err)
	}
	return state
}

func (f *fixture) ran(command string) bool {
	for _, call := range f.calls {
		if call == command {
			return true
		}
	}
	return false
}

func (f *fixture) ranAny(prefix string) bool {
	for _, call := range f.calls {
		if strings.HasPrefix(call, prefix) {
			return true
		}
	}
	return false
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
