package watch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/config"
)

// The one thing the fixture cannot answer for: whether what the watch reads about a
// repository really is the tree sync writes. Real git in a temp folder, no remote reached —
// what is checked is which changes the two readings count, not what sync does about them.
func liveRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on PATH")
	}

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(root, "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	run := func(dir string, args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(root, "repo", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	remote := filepath.Join(root, "remote.git")
	repo := filepath.Join(root, "repo")
	run(root, "init", "--bare", "--initial-branch=main", remote)
	run(root, "clone", remote, repo)
	run(repo, "config", "user.name", "Test")
	run(repo, "config", "user.email", "test@example.invalid")
	run(repo, "config", "commit.gpgsign", "false")

	write("keep/a.md", "one\n")
	write("other/b.md", "one\n")
	run(repo, "add", "-A")
	run(repo, "commit", "-m", "Both")
	run(repo, "push", "--set-upstream", "origin", "main")

	// A session's own work in the same checkout: uncommitted, and committed but not pushed —
	// the commit touching the paths sync syncs, which is the case a pathspec alone cannot
	// tell from one of sync's.
	write("other/b.md", "a session's\n")
	write("keep/c.md", "a session's\n")
	run(repo, "add", "keep/c.md")
	run(repo, "commit", "-m", "A session's own commit")

	return repo
}

// A commit sync made, as sync makes one: the trailer is the whole of what marks it.
func syncCommit(t *testing.T, repo, name string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(repo, name), []byte("sync's\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "-A", "--", "keep"},
		{"commit", "-m", "Say something about the change", "--trailer", config.SyncedTrailer, "--", "keep"},
	} {
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}

// What is not sync's to commit is not sync's to be behind on: a session's uncommitted work
// and its own unpushed commit are in the same tree, and reading the whole of it would have
// the watch report a sync that is doing exactly its job.
func TestALimitedRepositoryIsReadInsideItsPathsAlone(t *testing.T) {
	repo := liveRepo(t)

	limited := syncRepoState(SyncRepo{Path: repo, Paths: []string{"keep"}})
	if !limited.Read {
		t.Fatal("git would not answer about the repository")
	}
	if limited.Dirty {
		t.Error("a change outside the paths read as a dirty tree")
	}
	if !limited.Oldest.IsZero() {
		t.Errorf("a commit outside the paths read as one waiting to be pushed: %s", limited.Oldest)
	}

	// And the same repository read whole, which is what every repository sync keeps upstream
	// was before this: both of those are what it is behind on.
	whole := syncRepoState(SyncRepo{Path: repo})
	if !whole.Dirty || whole.Oldest.IsZero() {
		t.Errorf("read whole: %+v", whole)
	}
}

// Sync's own commit is the one it would push by itself, and the one the watch holds it to:
// a session's commit inside the very paths sync syncs carries no trailer, so a pathspec alone
// could not have told the two apart.
func TestOnlySyncsOwnUnpushedCommitCountsAsLate(t *testing.T) {
	repo := liveRepo(t)

	if state := syncRepoState(SyncRepo{Path: repo, Paths: []string{"keep"}}); !state.Oldest.IsZero() {
		t.Fatalf("a session's commit inside the paths read as sync's: %s", state.Oldest)
	}

	syncCommit(t, repo, "keep/d.md")

	state := syncRepoState(SyncRepo{Path: repo, Paths: []string{"keep"}})
	if state.Oldest.IsZero() {
		t.Error("sync's own unpushed commit was not counted")
	}
}

func TestALimitedRepositoryIsDirtyWhenItsOwnPathsAre(t *testing.T) {
	repo := liveRepo(t)

	if err := os.WriteFile(filepath.Join(repo, "keep", "a.md"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if state := syncRepoState(SyncRepo{Path: repo, Paths: []string{"keep"}}); !state.Dirty {
		t.Errorf("%+v", state)
	}
}
