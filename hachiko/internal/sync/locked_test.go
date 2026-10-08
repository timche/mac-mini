package sync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const lockText = "fatal: Unable to create '/Users/x/repo/.git/index.lock': File exists."

// A session in the same tree runs git a few times a second and each lock is held for a
// fraction of one. A pass that met one and posted would be a message about a race that was
// over before Tim's phone lit up.
func TestALockedIndexIsNotAFailureAtFirst(t *testing.T) {
	f := newFixture(t)
	f.tree.status = " M a.md\x00"
	f.tree.staged = []string{"a.md"}
	f.tree.commitFails, f.tree.commitText = 1, lockText

	result, log := f.pass(pushNow)

	if result.Outcome != Nothing || !result.Locked {
		t.Fatalf("outcome %v, locked %v, failure %+v", result.Outcome, result.Locked, result.Failure)
	}
	if len(f.sent) != 0 {
		t.Errorf("it posted about a lock: %v", f.sent)
	}
	if !strings.Contains(log, "index is locked") {
		t.Errorf("log: %s", log)
	}
}

// And the next pass commits, because that is what a race looks like from here.
func TestALockThatClearsIsCommittedOnTheNextPass(t *testing.T) {
	f := newFixture(t)
	f.tree.commitFails, f.tree.commitText = 1, lockText

	_, l := f.loop()
	write(f, l, " M a.md\x00", "a.md")

	if len(f.sent) != 0 {
		t.Fatalf("sent: %v", f.sent)
	}
	f.log.Reset()
	tick(f, l, time.Second)

	if !strings.Contains(f.log.String(), "pushed `Update a.md`") {
		t.Errorf("log: %s", f.log.String())
	}
	if !l.lockedSince.IsZero() {
		t.Error("the lock is still being timed after a pass that committed")
	}
}

// A lock that outlasts the grace is one a killed git left behind, which nothing here can
// remove: that is Tim's, and it is the same message as any other commit that will not happen.
func TestALockThatLastsIsReported(t *testing.T) {
	f := newFixture(t)
	f.tree.commitFails, f.tree.commitText = 100, lockText

	_, l := f.loop()
	write(f, l, " M a.md\x00", "a.md")
	tick(f, l, lockedGrace+time.Second)

	if len(f.sent) != 1 {
		t.Fatalf("sent %d messages: %v", len(f.sent), f.sent)
	}
	if !strings.Contains(f.sent[0].Text, "could not be committed") {
		t.Errorf("sent: %s", f.sent[0].Text)
	}
	if !l.outstanding {
		t.Error("the failure left nothing to recheck")
	}
}

// Anything else a commit says is a commit that will not be made however often it is tried —
// the signing key the agent has lost is the usual one — and that is a failure at once.
func TestACommitThatFailsForAnyOtherReasonIsStillAFailureAtOnce(t *testing.T) {
	f := newFixture(t)
	f.tree.status = " M a.md\x00"
	f.tree.staged = []string{"a.md"}
	f.tree.commitFails = 1

	result, _ := f.pass(pushNow)

	if result.Outcome != NeedsHuman || result.Failure.Kind != failedCommit {
		t.Fatalf("outcome %v, failure %+v", result.Outcome, result.Failure)
	}
}

// Real git refusing to work in a tree another git is already writing to, which is what the
// fixture above only describes: the lock file is what every git in this repository takes, and
// `add` is the first pass command to meet it.
func TestLiveAnIndexSomebodyElseLockedIsWaitedOutAndThenReported(t *testing.T) {
	l := newLive(t)
	l.twoFolders()
	_, loop := l.daemon(l.limited())

	lock := filepath.Join(l.repo, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	l.write("keep/a.md", "two\n")
	l.settle(loop)

	if got := l.run(l.repo, "log", "--format=%s", "-1"); got != "Both" {
		t.Fatalf("it committed through the lock: %q", got)
	}
	if len(l.sent) != 0 {
		t.Fatalf("a lock was posted at once: %v", l.sent)
	}
	if !strings.Contains(l.log.String(), "index is locked") {
		t.Errorf("log:\n%s", l.log.String())
	}

	// The same lock a minute later is one nothing is going to release.
	l.tick(loop, lockedGrace+time.Second)

	if len(l.sent) != 1 || !strings.Contains(l.sent[0].Text, "could not be committed") {
		t.Fatalf("sent: %v", l.sent)
	}

	// And once it is gone the work lands, with nothing to re-arm.
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	l.tick(loop, time.Second)

	if got := l.onTheRemote(); got[0] != "Update keep/a.md" {
		t.Errorf("on the remote: %q", got)
	}
	if len(l.sent) != 2 || !strings.Contains(l.sent[1].Text, "being committed again") {
		t.Fatalf("sent: %v", l.sent)
	}
}
