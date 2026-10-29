package sync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/config"
)

// Two repositories, one of which cannot be synced at all. They are separate repositories, so
// one with an `origin/HEAD` nobody set is no reason for the other to stop being published —
// and a daemon that exited over it would take both down and meet the same refusal on every
// restart for as long as the Mac is up.
func TestARepositoryThatCannotStartDoesNotStopTheOthers(t *testing.T) {
	l := newLive(t)
	l.twoFolders()

	broken := filepath.Join(filepath.Dir(l.repo), "not-a-repo")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}

	d, err := l.daemonFor(l.limited(), config.SyncRepo{Path: broken, Remote: "origin"})
	if err != nil {
		t.Fatalf("one repository it could not start stopped the other: %v", err)
	}

	if len(d.sync.Repos) != 1 || d.sync.Repos[0].Path != l.repo {
		t.Fatalf("the repositories it started on are %+v", d.sync.Repos)
	}
	if !strings.Contains(l.log.String(), "not-a-repo is not being synced") {
		t.Errorf("log:\n%s", l.log.String())
	}
	if len(l.sent) != 1 || !strings.Contains(l.sent[0].Text, "not being synced at all") {
		t.Fatalf("sent: %v", l.sent)
	}

	// And the one that did start syncs, which is the whole point of letting it.
	loop := d.loopFor(d.sync.Repos[0], 0)
	loop.start()

	l.write("keep/a.md", "two\n")
	l.settle(loop)

	if got := l.onTheRemote(); got[0] != "Update keep/a.md" {
		t.Errorf("on the remote: %q", got)
	}
}

// A limited repository whose remote names no default branch: the one case that is a
// repository rather than a missing clone, and the message says the command that records it.
func TestALimitedRepositoryWithNoDefaultBranchIsNotSynced(t *testing.T) {
	l := newLive(t)
	l.twoFolders()
	l.run(l.repo, "remote", "set-head", "origin", "--delete")

	_, err := l.daemonFor(l.limited())
	if err == nil {
		t.Fatal("it started on the one repository it could not read a branch for")
	}
	if !strings.Contains(err.Error(), "could be synced") {
		t.Errorf("got %v", err)
	}
	if len(l.sent) != 1 || !strings.Contains(l.sent[0].Text, "git remote set-head origin --auto") {
		t.Fatalf("sent: %v", l.sent)
	}
}
