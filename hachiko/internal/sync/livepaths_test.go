package sync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
)

// The same real git as the tests beside these, against the one thing a fixture cannot answer
// for: whether `git commit -- <paths>` really does leave the rest of somebody's working tree
// where it was. `--only` is git's own word for it and this is what proves it.
func (l *live) limited() config.SyncRepo {
	repo := l.repoConfig()
	repo.Paths, repo.Pull = []string{"keep"}, false
	return repo
}

// Two folders on the remote: `keep`, which is sync's, and `other`, which is a session's.
func (l *live) twoFolders() {
	l.t.Helper()

	l.write("keep/a.md", "one\n")
	l.write("other/b.md", "one\n")
	l.run(l.repo, "add", "-A")
	l.run(l.repo, "commit", "-m", "Both")
	l.run(l.repo, "push", "origin", "main")

	// The other machine was cloned before any of this, so it is brought up to date here: a
	// commit of its own on top of what it has would otherwise be one the remote rejects.
	l.run(l.other, "pull", "origin", "main")
}

func (l *live) read(name string) string {
	l.t.Helper()

	body, err := os.ReadFile(filepath.Join(l.repo, name))
	if err != nil {
		l.t.Fatal(err)
	}
	return string(body)
}

func TestLiveALimitedPassCommitsItsPathsAndLeavesTheRestOfTheTree(t *testing.T) {
	l := newLive(t)
	l.twoFolders()
	_, loop := l.daemon(l.limited())

	// A session's work in the same checkout: one change it has staged, one it has not, and a
	// file it has not added at all.
	l.write("other/b.md", "a session's, unstaged\n")
	l.write("other/c.md", "a session's, untracked\n")
	l.write("other/d.md", "a session's, staged\n")
	l.run(l.repo, "add", "other/d.md")

	// And sync's own, inside the paths: one tracked file changed and one new.
	l.write("keep/a.md", "two\n")
	l.write("keep/new.md", "new\n")
	l.settle(loop)

	if got := l.onTheRemote(); got[0] != "Update keep/a.md, keep/new.md" {
		t.Fatalf("on the remote: %q", got)
	}

	// The commit carries the paths and nothing else, so the subject is the whole of it.
	carried := fields(l.run(l.repo, "show", "--name-only", "--format=", "HEAD"), "\n")
	if want := []string{"keep/a.md", "keep/new.md"}; strings.Join(carried, " ") != strings.Join(want, " ") {
		t.Errorf("the commit carried %q", carried)
	}

	// Everything of a session's is exactly where it was: the staged change still staged and
	// still uncommitted, the unstaged one still unstaged, the untracked one still untracked.
	if got := l.run(l.repo, "diff", "--cached", "--name-only"); got != "other/d.md" {
		t.Errorf("staged: %q", got)
	}
	if got := l.run(l.repo, "diff", "--name-only"); got != "other/b.md" {
		t.Errorf("unstaged: %q", got)
	}
	if got := l.run(l.repo, "ls-files", "--others", "--exclude-standard"); got != "other/c.md" {
		t.Errorf("untracked: %q", got)
	}
	if got := l.read("other/b.md"); got != "a session's, unstaged\n" {
		t.Errorf("the unstaged change was touched: %q", got)
	}
}

// With pulling off nothing rewrites the tree: no fetch on the interval, so no rebase and no
// autostash, which is what would take a session's uncommitted work through a rebase.
func TestLiveALimitedRepoLeavesAnotherMachinesCommitWhereItIs(t *testing.T) {
	l := newLive(t)
	l.twoFolders()
	_, loop := l.daemon(l.limited())

	l.elsewhere("e.md", "theirs\n", "Theirs")

	for i := 0; i < 3; i++ {
		l.tick(loop, time.Minute+time.Second)
	}

	if got := l.run(l.repo, "log", "--format=%s", "-1"); got != "Both" {
		t.Errorf("something came down: %q", got)
	}
	if _, err := os.Stat(filepath.Join(l.repo, "e.md")); err == nil {
		t.Error("another machine's file arrived in the tree")
	}
}

// A rejected push is where a repository that does not pull stops: rebasing is the one thing
// it may not do, so this is a person's to look at rather than something to retry.
func TestLiveARejectedPushOnALimitedRepoIsReportedAndNotRebased(t *testing.T) {
	l := newLive(t)
	l.twoFolders()
	_, loop := l.daemon(l.limited())

	l.elsewhere("e.md", "theirs\n", "Theirs")

	// A session's uncommitted work, which an autostash would have taken through the rebase
	// this must not do.
	l.write("other/b.md", "a session's\n")

	l.write("keep/a.md", "two\n")
	l.settle(loop)

	if got := l.run(l.repo, "log", "--format=%s", "-1"); got != "Update keep/a.md" {
		t.Fatalf("the work was not committed: %q", got)
	}
	if got := l.run(l.repo, "log", "--format=%s"); strings.Contains(got, "Theirs") {
		t.Errorf("it rebased onto the remote:\n%s", got)
	}
	if _, inside := os.Stat(filepath.Join(l.repo, ".git", "rebase-merge")); inside == nil {
		t.Error("the tree was left mid-rebase")
	}
	if got := l.read("other/b.md"); got != "a session's\n" {
		t.Errorf("a session's uncommitted work was moved: %q", got)
	}
	if len(l.sent) != 1 || !strings.Contains(l.sent[0].Text, "could not be pushed") {
		t.Fatalf("sent: %v", l.sent)
	}
	if !loop.outstanding {
		t.Error("the failed push left nothing to recheck")
	}
}

// The branch comes out of this clone's own `origin/HEAD`, and a pass on any other branch is
// one that would commit Tim's files onto somebody's work in progress.
func TestLiveALimitedRepoSyncsOnTheRemotesDefaultBranchAlone(t *testing.T) {
	l := newLive(t)
	l.twoFolders()
	d, loop := l.daemon(l.limited())

	if got := d.onBranch[l.repo]; got != "main" {
		t.Fatalf("it syncs on %q", got)
	}

	l.run(l.repo, "checkout", "-b", "side")
	l.write("keep/a.md", "two\n")
	l.settle(loop)

	if got := l.run(l.repo, "log", "--format=%s", "-1"); got != "Both" {
		t.Errorf("it committed on another branch: %q", got)
	}
	if got := l.run(l.repo, "status", "--porcelain=v1", "--", "keep"); got != "M keep/a.md" {
		t.Errorf("the tree is %q", got)
	}
	if got := l.onTheRemote(); len(got) != 2 {
		t.Errorf("on the remote: %q", got)
	}
	if !strings.Contains(l.log.String(), "side is checked out, and this repository is synced on main alone") {
		t.Errorf("log:\n%s", l.log.String())
	}
}
