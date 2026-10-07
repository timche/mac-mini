package sync

import (
	"strings"
	"testing"
)

// `--once` is the explicit "sync now" and reports the way a pass of the daemon's does: the
// same message, and a conflict it meets pauses the repository rather than leaving the daemon
// to rebase over it again.
func TestOnceReportsWhatTheDaemonWouldAndRecordsAPause(t *testing.T) {
	f := newFixture(t)
	f.tree.behind = []string{"cafe123 theirs"}
	f.tree.rebaseText = "CONFLICT (content): Merge conflict in a.md"
	f.tree.unpushed = []string{"deadbee mine"}
	f.tree.oldest = base

	err := f.daemon().once()
	if err == nil {
		t.Fatal("a conflict was not a non-zero exit")
	}
	if !strings.Contains(err.Error(), "1 of 1 repositories did not sync") {
		t.Errorf("err: %v", err)
	}

	if _, paused := f.state().Paused[f.repo.Path]; !paused {
		t.Error("the conflict did not pause the repository")
	}
	if len(f.sent) != 1 {
		t.Fatalf("sent: %v", f.sent)
	}
	if !strings.Contains(f.lastSent(), "**Not on the remote:** `deadbee mine`") {
		t.Errorf("the message does not say what is stuck: %s", f.lastSent())
	}
}

func TestOnceOverAHealthyRepositoryExitsZero(t *testing.T) {
	f := newFixture(t)
	f.tree.status, f.tree.staged = " M a.md\x00", []string{"a.md"}

	if err := f.daemon().once(); err != nil {
		t.Fatalf("err: %v", err)
	}
	if !f.ran("push origin main") {
		t.Errorf("calls: %v", f.calls)
	}
}

// A dry `--once` is still a dry run: it changes nothing and exits zero, since there is
// nothing it could have failed at.
func TestADryOnceChangesNothing(t *testing.T) {
	f := newFixture(t)
	f.dry = true
	f.tree.status, f.tree.staged = " M a.md\x00", []string{"a.md"}

	if err := f.daemon().once(); err != nil {
		t.Fatalf("err: %v", err)
	}
	for _, forbidden := range []string{"add ", "commit ", "push ", "pull "} {
		if f.ranAny(forbidden) {
			t.Errorf("a dry --once ran `git %s`: %v", forbidden, f.calls)
		}
	}
	if log := f.log.String(); !strings.Contains(log, "would commit `Update a.md`") {
		t.Errorf("log: %s", log)
	}
}
