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

// A commit of a session's, timed: real git writes the committer time a push delay is
// measured against, so a commit meant to be two hours old has to be made as one.
func (l *live) sessionCommit(name, body, subject string, at time.Time) {
	l.t.Helper()

	l.write(name, body)
	l.run(l.repo, "add", "-A")

	stamp := at.Format(time.RFC3339)
	l.t.Setenv("GIT_AUTHOR_DATE", stamp)
	l.t.Setenv("GIT_COMMITTER_DATE", stamp)
	l.run(l.repo, "commit", "-m", subject)

	os.Unsetenv("GIT_AUTHOR_DATE")
	os.Unsetenv("GIT_COMMITTER_DATE")
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

// A session's own commit is not sync's to publish: it may be one the session is still
// shaping, and one it amends or squashes after sync has pushed it is one it can no longer
// push without forcing. So nothing of a session's starts a push, however long it sits.
func TestLiveASessionsOwnCommitIsNeverWhatStartsAPush(t *testing.T) {
	l := newLive(t)
	l.twoFolders()
	repo := l.limited()
	repo.PushDelay = time.Minute
	_, loop := l.daemon(repo)

	l.write("keep/a.md", "a session's, inside the paths\n")
	l.run(l.repo, "add", "-A")
	l.run(l.repo, "commit", "-m", "A session's own commit")

	// Well past the delay, and with the recheck and every other reason for a pass gone by.
	for i := 0; i < 3; i++ {
		l.tick(loop, 30*time.Minute)
	}

	if got := l.onTheRemote(); len(got) != 2 {
		t.Fatalf("a session's commit was pushed: %q", got)
	}
	if len(l.sent) != 0 {
		t.Fatalf("sent: %v", l.sent)
	}

	// Sync's own commit is what publishes the branch, and the session's goes up underneath
	// it — a push publishes the branch rather than a commit, and that is the point: the
	// session's work is on the remote as soon as sync has anything of its own to send.
	l.write("keep/b.md", "sync's\n")
	l.settle(loop)
	l.tick(loop, time.Minute+time.Second)

	got := l.onTheRemote()
	if len(got) != 4 || got[1] != "A session's own commit" {
		t.Fatalf("on the remote: %q", got)
	}

	// And the one it made says so, which is how the two were told apart.
	if trailer := l.run(l.repo, "log", "--format=%(trailers:key=Synced-by,valueonly)", "-1"); trailer != "hachiko sync" {
		t.Errorf("the trailer on sync's own commit is %q", trailer)
	}
}

// The delay is counted from the oldest commit of sync's own, not from a session's: a session
// that committed an hour ago does not make sync's fresh commit due at once.
func TestLiveThePushDelayIsCountedFromSyncsOwnOldestCommit(t *testing.T) {
	l := newLive(t)
	l.twoFolders()
	repo := l.limited()
	repo.PushDelay = time.Hour
	_, loop := l.daemon(repo)

	// Two hours old as git itself records it, since a push delay is measured against the
	// committer time real git wrote rather than against anything a test decided.
	l.sessionCommit("keep/a.md", "a session's\n", "A session's own commit", l.now.Add(-2*time.Hour))

	l.write("keep/b.md", "sync's\n")
	l.settle(loop)

	if got := l.onTheRemote(); len(got) != 2 {
		t.Fatalf("it pushed on the session's commit being old: %q", got)
	}
	if !strings.Contains(l.log.String(), "pushing in") {
		t.Errorf("log:\n%s", l.log.String())
	}

	l.tick(loop, time.Hour+time.Second)

	if got := l.onTheRemote(); len(got) != 4 {
		t.Fatalf("on the remote: %q", got)
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
