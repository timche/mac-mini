package sync

import (
	"strings"
	"testing"
	"time"
)

func TestADirtyTreeBecomesOneCommitAndAPush(t *testing.T) {
	f := newFixture(t)
	f.tree.status = "?? a.md\x00 M b.md\x00"
	f.tree.staged = []string{"a.md", "b.md"}

	result, log := f.pass(pushWhenDue)

	if result.Outcome != Pushed {
		t.Fatalf("outcome %v", result.Outcome)
	}
	if !f.ran("commit -m Update a.md, b.md") {
		t.Errorf("the commit was not the one subject expected: %v", f.calls)
	}
	if !f.ran("push origin main") {
		t.Errorf("nothing was pushed: %v", f.calls)
	}
	if !strings.Contains(log, "pushed `Update a.md, b.md`") {
		t.Errorf("log: %s", log)
	}
}

// Committing before the push loop is what makes the rebase safe: it runs over a checkpoint
// rather than over half-written work.
func TestTheCommitComesBeforeTheFetch(t *testing.T) {
	f := newFixture(t)
	f.tree.status = " M a.md\x00"
	f.tree.staged = []string{"a.md"}

	f.pass(pushWhenDue)

	commit, fetch := -1, -1
	for i, call := range f.calls {
		if strings.HasPrefix(call, "commit ") && commit < 0 {
			commit = i
		}
		if call == "fetch origin" && fetch < 0 {
			fetch = i
		}
	}
	if commit < 0 || fetch < 0 || commit > fetch {
		t.Errorf("commit at %d, fetch at %d: %v", commit, fetch, f.calls)
	}
}

func TestAnUnpushedCommitIsPushedWithoutANewOne(t *testing.T) {
	f := newFixture(t)
	f.tree.unpushed = []string{"deadbee Update a.md"}
	f.tree.oldest = base

	result, log := f.pass(pushWhenDue)

	if result.Outcome != Pushed {
		t.Fatalf("outcome %v", result.Outcome)
	}
	if f.ranAny("commit ") {
		t.Errorf("a commit was made with nothing staged: %v", f.calls)
	}
	if !strings.Contains(log, "pushed work that was already committed") {
		t.Errorf("log: %s", log)
	}
}

// The common case with the fetch interval on, so it says nothing at all: a line a minute
// about a repository that is fine is a log nobody reads.
func TestAnIdleRepositorySaysNothing(t *testing.T) {
	f := newFixture(t)

	result, log := f.pass(pushWhenDue)

	if result.Outcome != Nothing {
		t.Fatalf("outcome %v", result.Outcome)
	}
	if log != "" {
		t.Errorf("log: %s", log)
	}
}

func TestACleanTreeTakesTheRemoteCommitDownWithoutPushing(t *testing.T) {
	f := newFixture(t)
	f.tree.behind = []string{"cafe123 somebody else"}

	result, log := f.pass(pushWhenDue)

	if result.Outcome != Pulled {
		t.Fatalf("outcome %v", result.Outcome)
	}
	if f.ranAny("push ") {
		t.Errorf("something was pushed: %v", f.calls)
	}
	if !strings.Contains(log, "1 commit came down from origin") {
		t.Errorf("log: %s", log)
	}
}

// With pulling off there is nothing to do at all on an idle repository, so the pass does not
// even reach git's index.
func TestWithoutPullingAnIdleRepositoryIsLeftAlone(t *testing.T) {
	f := newFixture(t)
	f.repo.Pull = false
	f.tree.behind = []string{"cafe123 somebody else"}

	result, _ := f.pass(pushWhenDue)

	if result.Outcome != Nothing {
		t.Fatalf("outcome %v", result.Outcome)
	}
	if f.ran("add -A") || f.ran("fetch origin") {
		t.Errorf("an idle repository was touched: %v", f.calls)
	}
}

// Something can land between the fetch and the push, which is the whole reason the rejection
// is still handled after a fetch that found nothing.
func TestARejectedPushRebasesAndPushesAgain(t *testing.T) {
	f := newFixture(t)
	f.tree.unpushed = []string{"deadbee mine"}
	f.tree.oldest = base
	f.tree.pushRefused = 1
	f.tree.pushText = " ! [rejected]        main -> main (fetch first)"

	result, _ := f.pass(pushWhenDue)

	if result.Outcome != Pushed {
		t.Fatalf("outcome %v", result.Outcome)
	}
	if !f.ran("pull --rebase --autostash origin main") {
		t.Errorf("no rebase followed the rejection: %v", f.calls)
	}
	if pushes := strings.Count(strings.Join(f.calls, "\n"), "push origin main"); pushes != 2 {
		t.Errorf("%d pushes, want 2: %v", pushes, f.calls)
	}
}

// Rejected again on a freshly rebased branch: something else is writing to it, and retrying
// would only lose whichever race.
func TestARejectionOnAFreshlyRebasedBranchNeedsAPerson(t *testing.T) {
	f := newFixture(t)
	f.tree.unpushed = []string{"deadbee mine"}
	f.tree.oldest = base
	f.tree.pushRefused = 2
	f.tree.pushText = " ! [rejected]        main -> main (non-fast-forward)"

	result, _ := f.pass(pushWhenDue)

	if result.Outcome != NeedsHuman || result.Failure.Kind != failedPush {
		t.Fatalf("outcome %v, failure %+v", result.Outcome, result.Failure)
	}
	if len(f.slept) != 0 {
		t.Errorf("it backed off a rejection: %v", f.slept)
	}
}

// With pulling off a rejection is nobody's to fix but a person's, so there is no rebase at
// all.
func TestWithoutPullingARejectionNeedsAPersonAtOnce(t *testing.T) {
	f := newFixture(t)
	f.repo.Pull = false
	f.tree.unpushed = []string{"deadbee mine"}
	f.tree.pushRefused = 1
	f.tree.pushText = " ! [rejected]        main -> main (fetch first)"

	result, _ := f.pass(pushWhenDue)

	if result.Outcome != NeedsHuman {
		t.Fatalf("outcome %v", result.Outcome)
	}
	if f.ranAny("pull ") {
		t.Errorf("it rebased with pulling off: %v", f.calls)
	}
}

// `! [remote rejected]` is a hook or a protected branch, which no amount of rebasing fixes —
// so it is retried as an ordinary failure rather than rebased over.
func TestARemoteRejectionIsNotReadAsANonFastForward(t *testing.T) {
	if isRejection(" ! [remote rejected] main -> main (pre-receive hook declined)") {
		t.Error("a hook declining was read as a non-fast-forward")
	}
	for _, text := range []string{
		" ! [rejected] main -> main",
		"hint: Updates were rejected because the tip is behind. fetch first",
		"error: failed to push some refs (non-fast-forward)",
	} {
		if !isRejection(text) {
			t.Errorf("%q was not read as a rejection", text)
		}
	}
}

func TestAnUnreachableRemoteBacksOffAlongTheLadderAndThenReports(t *testing.T) {
	f := newFixture(t)
	f.retry.Attempts, f.retry.Base, f.retry.Max = 4, time.Second, 2*time.Second
	f.tree.unpushed = []string{"deadbee mine"}
	f.tree.oldest = base
	f.tree.pushRefused = 4
	f.tree.pushText = "fatal: unable to access 'https://github.com/o/r/': Could not resolve host"

	result, _ := f.pass(pushWhenDue)

	if result.Outcome != Unreachable || result.Failure.Kind != failedPush {
		t.Fatalf("outcome %v, failure %+v", result.Outcome, result.Failure)
	}
	// Doubling from base, capped at max, and nothing after the last attempt.
	want := []time.Duration{time.Second, 2 * time.Second, 2 * time.Second}
	if len(f.slept) != len(want) {
		t.Fatalf("slept %v, want %v", f.slept, want)
	}
	for i, d := range want {
		if f.slept[i] != d {
			t.Errorf("slept %v, want %v", f.slept, want)
		}
	}
}

// An offline machine with nothing of its own to send is not a failure at all: retrying the
// ladder and posting about it every minute would make ordinary idleness look broken. The
// same rule as a docker that is down.
func TestAnUnreachableRemoteWithNothingToPushIsNotAFailure(t *testing.T) {
	f := newFixture(t)
	f.tree.fetchFails, f.tree.fetchText = 9, "fatal: unable to access: Could not resolve host"

	result, log := f.pass(pushWhenDue)

	if result.Outcome != Nothing || result.Failure != nil {
		t.Fatalf("outcome %v, failure %+v", result.Outcome, result.Failure)
	}
	if len(f.sent) != 0 {
		t.Errorf("it posted about being offline: %v", f.sent)
	}
	if !strings.Contains(log, "there is nothing to push") {
		t.Errorf("log: %s", log)
	}
}

// A fetch that fails while there is something to push is a failed attempt like a failed push.
func TestAFailedFetchWithSomethingToPushBacksOffAndThenPushes(t *testing.T) {
	f := newFixture(t)
	f.tree.unpushed = []string{"deadbee mine"}
	f.tree.oldest = base
	f.tree.fetchFails, f.tree.fetchText = 1, "fatal: Could not read from remote repository"

	result, _ := f.pass(pushWhenDue)

	if result.Outcome != Pushed {
		t.Fatalf("outcome %v", result.Outcome)
	}
	if len(f.slept) != 1 {
		t.Errorf("slept %v", f.slept)
	}
}

func TestARebaseConflictIsAbortedAndReported(t *testing.T) {
	f := newFixture(t)
	f.tree.behind = []string{"cafe123 theirs"}
	f.tree.rebaseText = "CONFLICT (content): Merge conflict in a.md"

	result, log := f.pass(pushWhenDue)

	if result.Outcome != NeedsHuman || result.Failure.Kind != failedConflict {
		t.Fatalf("outcome %v, failure %+v", result.Outcome, result.Failure)
	}
	if !f.ran("rebase --abort") {
		t.Errorf("the rebase was left half-finished: %v", f.calls)
	}
	if f.tree.midRebase {
		t.Error("the tree is still mid-rebase")
	}
	if !strings.Contains(log, "conflicted") {
		t.Errorf("log: %s", log)
	}
}

// An abort that fails leaves a tree nobody can work in, so it is said out loud rather than
// swallowed.
func TestAnAbortThatFailsIsSaidOutLoud(t *testing.T) {
	f := newFixture(t)
	f.tree.behind = []string{"cafe123 theirs"}
	f.tree.rebaseText = "CONFLICT (content): Merge conflict in a.md"
	f.tree.abortFails = true

	_, log := f.pass(pushWhenDue)

	if !strings.Contains(log, "left mid-rebase") {
		t.Errorf("log: %s", log)
	}
}

func TestACommitThatFailsIsAFailureOfItsOwn(t *testing.T) {
	f := newFixture(t)
	f.tree.status = " M a.md\x00"
	f.tree.staged = []string{"a.md"}
	f.tree.commitFails = 1

	result, log := f.pass(pushWhenDue)

	if result.Outcome != NeedsHuman || result.Failure.Kind != failedCommit {
		t.Fatalf("outcome %v, failure %+v", result.Outcome, result.Failure)
	}
	if f.ranAny("push ") {
		t.Errorf("it pushed after a commit that failed: %v", f.calls)
	}
	if !strings.Contains(log, "could be committed") {
		t.Errorf("log: %s", log)
	}
}

// A branch that has never been published has no upstream to fetch against, nothing upstream
// to rebase onto, and always something to push.
func TestABranchWithNoUpstreamIsPushedWithSetUpstream(t *testing.T) {
	f := newFixture(t)
	f.tree.noUpstream = true

	result, _ := f.pass(pushWhenDue)

	if result.Outcome != Pushed {
		t.Fatalf("outcome %v", result.Outcome)
	}
	if !f.ran("push --set-upstream origin main") {
		t.Errorf("calls: %v", f.calls)
	}
	if f.ran("fetch origin") {
		t.Errorf("it fetched against an upstream that does not exist: %v", f.calls)
	}
}

func TestAFreshCommitIsHeldBackUntilTheDelayIsUp(t *testing.T) {
	f := newFixture(t)
	f.repo.PushDelay = time.Hour
	f.tree.status = " M a.md\x00"
	f.tree.staged = []string{"a.md"}

	result, log := f.pass(pushWhenDue)

	if result.Outcome != Held {
		t.Fatalf("outcome %v", result.Outcome)
	}
	if f.ranAny("push ") {
		t.Errorf("a held commit was pushed: %v", f.calls)
	}
	if !strings.Contains(log, "pushing in 1h0m0s") {
		t.Errorf("log: %s", log)
	}
}

// The wait is measured from the commit rather than remembered, so a restart does not reset
// it and a commit that was already due when the daemon came up goes out at once.
func TestACommitOlderThanTheDelayGoesUpAtOnce(t *testing.T) {
	f := newFixture(t)
	f.repo.PushDelay = time.Hour
	f.tree.unpushed = []string{"deadbee old"}
	f.tree.oldest = base.Add(-2 * time.Hour)

	result, _ := f.pass(pushWhenDue)

	if result.Outcome != Pushed {
		t.Fatalf("outcome %v", result.Outcome)
	}
}

// A steady stream of edits cannot postpone a push past its delay: the wait is the oldest
// commit's, not the newest's.
func TestANewerCommitDoesNotPostponeAnOverdueOne(t *testing.T) {
	f := newFixture(t)
	f.repo.PushDelay = time.Hour
	f.tree.unpushed = []string{"deadbee old"}
	f.tree.oldest = base.Add(-2 * time.Hour)
	f.tree.status = " M a.md\x00"
	f.tree.staged = []string{"a.md"}

	result, _ := f.pass(pushWhenDue)

	if result.Outcome != Pushed {
		t.Fatalf("outcome %v", result.Outcome)
	}
	if !f.ranAny("commit ") {
		t.Errorf("the new work was not committed: %v", f.calls)
	}
}

// Waiting to publish never means falling behind, so a held pass still takes the remote's
// commits down.
func TestAHeldPushStillTakesTheRemoteCommitDown(t *testing.T) {
	f := newFixture(t)
	f.repo.PushDelay = time.Hour
	f.tree.unpushed = []string{"deadbee mine"}
	f.tree.oldest = base
	f.tree.behind = []string{"cafe123 theirs"}

	result, log := f.pass(pushWhenDue)

	if result.Outcome != Held {
		t.Fatalf("outcome %v", result.Outcome)
	}
	if !f.ran("pull --rebase --autostash origin main") {
		t.Errorf("a held pass did not rebase: %v", f.calls)
	}
	if !strings.Contains(log, "1 commit came down from origin") {
		t.Errorf("log: %s", log)
	}
}

// A conflict during a held pass is still a conflict: it aborts and reports rather than
// waiting out the delay first.
func TestAConflictDuringAHeldPassIsReported(t *testing.T) {
	f := newFixture(t)
	f.repo.PushDelay = time.Hour
	f.tree.unpushed = []string{"deadbee mine"}
	f.tree.oldest = base
	f.tree.behind = []string{"cafe123 theirs"}
	f.tree.rebaseText = "CONFLICT (content): Merge conflict in a.md"

	result, _ := f.pass(pushWhenDue)

	if result.Outcome != NeedsHuman || result.Failure.Kind != failedConflict {
		t.Fatalf("outcome %v, failure %+v", result.Outcome, result.Failure)
	}
}

// `--once` is the explicit "sync now", so it pushes whatever the delay says.
func TestOncePushesWhateverTheDelaySays(t *testing.T) {
	f := newFixture(t)
	f.repo.PushDelay = time.Hour
	f.tree.unpushed = []string{"deadbee mine"}
	f.tree.oldest = base

	result, _ := f.pass(pushNow)

	if result.Outcome != Pushed {
		t.Fatalf("outcome %v", result.Outcome)
	}
}

// A delay over a repository somebody has already pushed by hand waits for nothing, since the
// wait is read off `@{upstream}..HEAD` and there is nothing in it.
func TestADelayOverARepositoryWithNothingUnpushedWaitsForNothing(t *testing.T) {
	f := newFixture(t)
	f.repo.PushDelay = time.Hour

	result, _ := f.pass(pushWhenDue)

	if result.Outcome != Nothing {
		t.Fatalf("outcome %v", result.Outcome)
	}
}

// Somebody resolving a conflict by hand has a dirty tree that is theirs to finish: a commit
// would land inside their rebase or merge.
func TestATreeMidRebaseOrMergeIsLeftAlone(t *testing.T) {
	for _, tc := range []struct {
		name      string
		detached  bool
		pseudoRef string
	}{
		{"rebase", true, ""},
		{"merge", false, "MERGE_HEAD"},
		{"cherry-pick", false, "CHERRY_PICK_HEAD"},
		{"revert", false, "REVERT_HEAD"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.tree.status = "UU a.md\x00"
			f.tree.staged = []string{"a.md"}
			f.tree.unpushed = []string{"aaa earlier"}
			f.tree.detached, f.tree.pseudoRef = tc.detached, tc.pseudoRef

			result, log := f.pass(pushNow)

			if result.Outcome != Nothing {
				t.Fatalf("outcome %v", result.Outcome)
			}
			for _, prefix := range []string{"add", "commit", "push", "pull", "fetch"} {
				if f.ranAny(prefix) {
					t.Errorf("ran %s: %v", prefix, f.calls)
				}
			}
			if !strings.Contains(log, "left alone:") {
				t.Errorf("log: %s", log)
			}
		})
	}
}
