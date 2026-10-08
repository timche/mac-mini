package sync

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/self"
)

// The one thing the fixture above cannot answer for: whether real git does what the pass
// assumes it does. A bare repository in a temp folder is the remote, so none of this reaches
// the network, and a second clone of it is the other machine whose commits arrive.
//
// Everything else is injected as it is everywhere else here: the clock, so a push delay runs
// out without anything waiting an hour, and the sender, so nothing is posted.
type live struct {
	t      *testing.T
	repo   string
	remote string
	other  string

	now  time.Time
	log  bytes.Buffer
	sent []discord.Outgoing
}

func newLive(t *testing.T) *live {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on PATH")
	}

	// A global config of its own, so the account's own — commit signing above all — is not
	// what decides whether these commits can be made.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(root, "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	l := &live{
		t:      t,
		repo:   filepath.Join(root, "repo"),
		remote: filepath.Join(root, "remote.git"),
		other:  filepath.Join(root, "other"),
		// Real time rather than the fixture's, because a push delay is measured against a
		// committer time real git wrote: a clock three years behind git's would be a commit
		// that is three years from being due.
		now: time.Now().Truncate(time.Second),
	}

	l.run(root, "init", "--bare", "--initial-branch=main", l.remote)
	l.clone(l.repo)

	l.write("README.md", "first\n")
	l.run(l.repo, "add", "-A")
	l.run(l.repo, "commit", "-m", "First")
	l.run(l.repo, "push", "--set-upstream", "origin", "main")

	// What a clone of a repository that has commits in it records by itself, and what a
	// limited repository reads the branch it syncs on out of. This one was cloned while the
	// remote was still empty, so there was no HEAD to copy.
	l.run(l.repo, "remote", "set-head", "origin", "main")

	l.clone(l.other)
	return l
}

func (l *live) clone(into string) {
	l.run(filepath.Dir(into), "clone", l.remote, into)
	l.run(into, "config", "user.name", "Test")
	l.run(into, "config", "user.email", "test@example.invalid")
	l.run(into, "config", "commit.gpgsign", "false")
	l.run(into, "config", "pull.rebase", "true")
}

func (l *live) run(dir string, args ...string) string {
	l.t.Helper()

	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		l.t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (l *live) write(name, body string) {
	l.t.Helper()

	path := filepath.Join(l.repo, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		l.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		l.t.Fatal(err)
	}
}

// A commit on the other machine, pushed, which is what arrives on this one's next fetch.
func (l *live) elsewhere(name, body, subject string) {
	l.t.Helper()

	if err := os.WriteFile(filepath.Join(l.other, name), []byte(body), 0o644); err != nil {
		l.t.Fatal(err)
	}
	l.run(l.other, "add", "-A")
	l.run(l.other, "commit", "-m", subject)
	l.run(l.other, "push", "origin", "main")
}

// The subjects on the remote's own branch, newest first.
func (l *live) onTheRemote() []string {
	return fields(l.run(l.remote, "log", "--format=%s", "main"), "\n")
}

func (l *live) daemon(repo config.SyncRepo) (*daemon, *loop) {
	state, err := filepath.EvalSymlinks(l.t.TempDir())
	if err != nil {
		l.t.Fatal(err)
	}

	d := &daemon{
		cfg:   config.Config{Home: l.repo, Host: "mac-mini", SyncStateDir: state},
		sync:  config.Sync{Mode: config.ModeLive, Retry: config.SyncRetry{Attempts: 2, Base: time.Millisecond, Max: time.Millisecond}},
		store: &Store{Dir: state},
		deps: Deps{
			Now:   func() time.Time { return l.now },
			Log:   &l.log,
			Sleep: func(d time.Duration) { l.now = l.now.Add(d) },
			Git:   runGit,
			Send: func(out discord.Outgoing) (string, error) {
				l.sent = append(l.sent, out)
				return "thread-1", nil
			},
			Self: func() (self.ID, bool) { return self.ID{}, false },
		},
	}
	d.sync.Repos = []config.SyncRepo{repo}

	// Through the startup every daemon goes through, so a limited repository's branch is the
	// one real git resolves out of this clone rather than one a test decided.
	if err := d.prepare(); err != nil {
		l.t.Fatal(err)
	}
	repo = d.sync.Repos[0]

	loop := d.loopFor(repo, 0)
	loop.start()
	return d, loop
}

func (l *live) repoConfig() config.SyncRepo {
	return config.SyncRepo{
		Path: l.repo, Debounce: 5 * time.Second, Remote: "origin", Pull: true,
		Recheck: 10 * time.Minute, FetchInterval: time.Minute,
	}
}

func (l *live) tick(loop *loop, after time.Duration) {
	l.now = l.now.Add(after)
	loop.tick(l.now)
}

// A write, then the tick that starts the debounce and the one that ends it.
func (l *live) settle(loop *loop) {
	l.tick(loop, time.Second)
	l.tick(loop, 6*time.Second)
}

func TestLiveAWriteBecomesACommitOnTheRemote(t *testing.T) {
	l := newLive(t)
	_, loop := l.daemon(l.repoConfig())

	l.write("a.md", "one\n")
	l.write("b.md", "two\n")
	l.settle(loop)

	if got := l.onTheRemote(); len(got) != 2 || got[0] != "Update a.md, b.md" {
		t.Fatalf("on the remote: %q", got)
	}
}

func TestLiveAPushWaitsOutItsDelayAndThenGoesUp(t *testing.T) {
	l := newLive(t)
	repo := l.repoConfig()
	repo.PushDelay = time.Hour
	_, loop := l.daemon(repo)

	l.write("a.md", "one\n")
	l.settle(loop)

	if got := l.onTheRemote(); len(got) != 1 {
		t.Fatalf("a held commit reached the remote: %q", got)
	}
	if got := l.run(l.repo, "log", "--format=%s", "-1"); got != "Update a.md" {
		t.Fatalf("it was not committed locally either: %q", got)
	}

	// A second write while the first is held: both go up in one push, and the newer one does
	// not postpone the older one's deadline.
	l.write("b.md", "two\n")
	l.settle(loop)
	l.tick(loop, time.Hour)

	got := l.onTheRemote()
	if len(got) != 3 || got[0] != "Update b.md" || got[1] != "Update a.md" {
		t.Fatalf("on the remote: %q", got)
	}
}

// The remote moves between this machine's fetch and its push, which no fetch can prevent.
func TestLiveARejectedPushRebasesAndLandsBoth(t *testing.T) {
	l := newLive(t)
	_, loop := l.daemon(l.repoConfig())

	// Committed here first, and only then does the other machine push: the fetch inside the
	// pass finds nothing, and the push is what discovers it.
	l.write("a.md", "mine\n")
	l.run(l.repo, "add", "-A")
	l.run(l.repo, "commit", "-m", "Mine")
	l.elsewhere("b.md", "theirs\n", "Theirs")

	loop.p.git.run("fetch", "origin")
	l.run(l.repo, "update-ref", "refs/remotes/origin/main", l.run(l.repo, "rev-parse", "HEAD~1"))

	l.tick(loop, time.Minute)
	l.tick(loop, time.Minute)

	got := l.onTheRemote()
	if len(got) != 3 {
		t.Fatalf("on the remote: %q", got)
	}
	if got[0] != "Mine" || got[1] != "Theirs" {
		t.Errorf("the rebase did not put this machine's work on top: %q", got)
	}
}

// A conflict is nobody's to retry: the rebase is aborted so whoever fixes it finds an
// ordinary working tree, the repository is paused, and it resumes by itself once either side
// moves.
func TestLiveAConflictAbortsAndPausesAndThenResumes(t *testing.T) {
	l := newLive(t)
	d, loop := l.daemon(l.repoConfig())

	l.elsewhere("README.md", "theirs\n", "Theirs")
	l.write("README.md", "mine\n")
	l.settle(loop)

	if !loop.paused {
		t.Fatal("the conflict did not pause the repository")
	}
	if _, inside := os.Stat(filepath.Join(l.repo, ".git", "rebase-merge")); inside == nil {
		t.Error("the tree was left mid-rebase")
	}
	if status := l.run(l.repo, "status", "--porcelain"); status != "" {
		t.Errorf("the tree is not an ordinary working tree: %q", status)
	}
	if got := l.run(l.repo, "log", "--format=%s", "-1"); got != "Update README.md" {
		t.Errorf("the work was not committed before the rebase: %q", got)
	}

	at, paused := d.store.PausedAt(l.repo)
	if !paused || at.Head == "" {
		t.Fatalf("the pause was not written down: %+v", at)
	}
	if len(l.sent) != 1 || !strings.Contains(l.sent[0].Text, "rebase conflicted") {
		t.Fatalf("sent: %v", l.sent)
	}

	// Nothing has moved, so the fetch interval looks and leaves it alone.
	l.tick(loop, 2*time.Minute)
	if !loop.paused {
		t.Fatal("it resumed with neither side moved")
	}

	// Resolved by hand, the way Tim would: take the remote's side.
	l.run(l.repo, "reset", "--hard", "origin/main")
	l.tick(loop, 2*time.Minute)

	if loop.paused {
		t.Fatalf("it stayed paused after HEAD moved:\n%s", l.log.String())
	}
	if _, still := d.store.PausedAt(l.repo); still {
		t.Error("the pause is still in the state")
	}
	if len(l.sent) != 2 || !strings.Contains(l.sent[1].Text, "syncing again") {
		t.Fatalf("sent: %v", l.sent)
	}

	// And it syncs again, rather than only being unpaused.
	l.write("c.md", "after\n")
	l.settle(loop)

	if got := l.onTheRemote(); got[0] != "Update c.md" {
		t.Errorf("on the remote: %q", got)
	}
}

// The fetch interval is what brings another machine's writes down at all: without it a
// repository only ever pulls when it has something of its own to push.
func TestLiveTheFetchIntervalTakesAnotherMachinesCommitDown(t *testing.T) {
	l := newLive(t)
	_, loop := l.daemon(l.repoConfig())

	l.elsewhere("b.md", "theirs\n", "Theirs")
	l.tick(loop, 2*time.Minute)

	if got := l.run(l.repo, "log", "--format=%s", "-1"); got != "Theirs" {
		t.Errorf("the other machine's commit did not arrive: %q", got)
	}
}

// A remote that is not there is retried and then reported, and the repository is left
// committed rather than losing the work.
func TestLiveAnUnreachableRemoteIsReportedAndTheWorkIsCommitted(t *testing.T) {
	l := newLive(t)
	_, loop := l.daemon(l.repoConfig())

	l.write("a.md", "one\n")
	if err := os.RemoveAll(l.remote); err != nil {
		t.Fatal(err)
	}
	l.settle(loop)

	if got := l.run(l.repo, "log", "--format=%s", "-1"); got != "Update a.md" {
		t.Fatalf("the work was not committed: %q", got)
	}
	if len(l.sent) != 1 || !strings.Contains(l.sent[0].Text, "could not be pushed") {
		t.Fatalf("sent: %v", l.sent)
	}
	if !loop.outstanding {
		t.Error("the failed push left nothing to recheck")
	}
}
