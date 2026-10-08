package watch

import (
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// The other half: sync is alive and says nothing is wrong, and a repository is getting
// nowhere all the same.
func behind(f *fixture, state SyncRepoState) {
	// The heartbeat left on this very check, because what these are about is a sync that is
	// alive: a test that walks the clock for an hour would otherwise be reporting a sync that
	// has stopped.
	f.syncBeat = time.Time{}
	f.syncRepos = []SyncRepo{{Path: "/Users/x/.mac-mini", PushDelay: time.Hour}}
	f.syncState = map[string]SyncRepoState{"/Users/x/.mac-mini": state}
}

func TestACommitWellPastItsPushDelayIsReported(t *testing.T) {
	f := syncFixture(t)

	// An hour's delay and half an hour's margin, so anything under ninety minutes is a retry
	// ladder working rather than a sync that has silently stopped.
	behind(f, SyncRepoState{Read: true, Oldest: base.Add(-85 * time.Minute)})
	harness.Equal(t, f.sweep(), "", "the log of a check inside the margin")
	harness.Equal(t, f.sentCount(), 0, "messages sent")

	behind(f, SyncRepoState{Read: true, Oldest: base.Add(-100 * time.Minute)})
	out := f.sweep()

	harness.Wants(t, out, "/Users/x/.mac-mini has an unpushed commit 1h40m old, and sync is running")
	harness.Equal(t, f.sentCount(), 1, "messages sent")
	harness.Wants(t, f.lastSent(), "⚠️ A repository's commits are well past their push delay")
	harness.Wants(t, f.lastSent(), "**Repository:** `/Users/x/.mac-mini`")
	harness.Wants(t, f.lastSent(), "**Oldest unpushed commit:** 1 hour 40 minutes old")

	// One message while it stays late.
	f.at(300).sweep()
	harness.Equal(t, f.sentCount(), 1, "messages sent")
}

func TestACommitThatGoesUpInTheEndIsSaidToHaveCaughtUp(t *testing.T) {
	f := syncFixture(t)
	f.threads = true
	behind(f, SyncRepoState{Read: true, Oldest: base.Add(-100 * time.Minute)})
	f.sweep()

	behind(f, SyncRepoState{Read: true})
	out := f.at(600).sweep()

	harness.Wants(t, out, "/Users/x/.mac-mini has caught up")
	harness.Equal(t, f.sentCount(), 2, "messages sent")
	harness.Wants(t, f.lastSent(), "🟢 The repository whose commits were late has pushed them")
	harness.Equal(t, f.sentTo[1], "thread-late /Users/x/.mac-mini", "where the clear went")
}

// The debounce is seconds, so a tree still dirty after ten minutes is not one waiting out a
// burst of writes.
func TestATreeDirtyForLongerThanTheDebounceIsReported(t *testing.T) {
	f := syncFixture(t)
	behind(f, SyncRepoState{Read: true, Dirty: true})

	// The first check only records when it was first seen: how long a file has been written
	// is not something anything else on this Mac knows.
	harness.Equal(t, f.sweep(), "", "the log of the check that first saw it dirty")
	harness.Equal(t, f.sentCount(), 0, "messages sent")

	harness.Equal(t, f.at(540).sweep(), "", "the log of a check inside the ten minutes")
	harness.Equal(t, f.sentCount(), 0, "messages sent")

	out := f.at(900).sweep()
	harness.Wants(t, out, "/Users/x/.mac-mini has been uncommitted for 0h15m, and sync is running")
	harness.Equal(t, f.sentCount(), 1, "messages sent")
	harness.Wants(t, f.lastSent(), "⚠️ A repository has been uncommitted for far longer than its debounce")
	harness.Wants(t, f.lastSent(), "**Waiting:** 15 minutes")
}

func TestATreeThatGetsCommittedIsSaidToHaveCaughtUp(t *testing.T) {
	f := syncFixture(t)
	behind(f, SyncRepoState{Read: true, Dirty: true})
	f.sweep()
	f.at(900).sweep()

	behind(f, SyncRepoState{Read: true})
	out := f.at(1200).sweep()

	harness.Wants(t, out, "/Users/x/.mac-mini has caught up")
	harness.Wants(t, f.lastSent(), "🟢 The repository that would not commit has committed")
}

// A tree that goes clean inside the ten minutes was never worth saying anything about, so
// there is nothing to say is over either.
func TestATreeCommittedInsideTheDebounceWindowIsNeverMentioned(t *testing.T) {
	f := syncFixture(t)
	behind(f, SyncRepoState{Read: true, Dirty: true})
	f.sweep()

	behind(f, SyncRepoState{Read: true})
	harness.Equal(t, f.at(300).sweep(), "", "the log of a check after it committed")
	harness.Equal(t, f.sentCount(), 0, "messages sent")
}

// A sync that is down is already one message, and saying every repository it was syncing has
// fallen behind is the same news over again.
func TestNothingIsSaidAboutARepositoryWhileSyncIsDown(t *testing.T) {
	f := syncFixture(t)
	behind(f, SyncRepoState{Read: true, Oldest: base.Add(-10 * time.Hour), Dirty: true})

	f.syncBeat = base.Add(-time.Hour)
	f.sweep()

	harness.Equal(t, f.sentCount(), 1, "messages sent")
	harness.Wants(t, f.lastSent(), "Auto-sync has not run")
}

// A repository nothing could be read from is one the watch says nothing about: a missing
// clone and a sync that has fallen behind are not the same thing.
func TestARepositoryGitCannotAnswerForIsNotReported(t *testing.T) {
	f := syncFixture(t)
	behind(f, SyncRepoState{Oldest: base.Add(-10 * time.Hour), Dirty: true})

	harness.Equal(t, f.sweep(), "", "the log of a check on a repository git could not read")
	harness.Equal(t, f.sentCount(), 0, "messages sent")
}

func TestADryRunSaysWhatItWouldReportAboutARepositoryBehind(t *testing.T) {
	f := syncFixture(t)
	behind(f, SyncRepoState{Read: true, Oldest: base.Add(-100 * time.Minute)})

	out := f.dryRun()
	harness.Wants(t, out, "would report that /Users/x/.mac-mini has an unpushed commit 1h40m old")
	harness.Equal(t, f.sentCount(), 0, "messages sent")
}

// Both at once on one repository, because they are two different things gone wrong and each
// has its own thing for Tim to look at.
func TestARepositoryBothLateAndDirtyIsTwoMessages(t *testing.T) {
	f := syncFixture(t)
	behind(f, SyncRepoState{Read: true, Oldest: base.Add(-100 * time.Minute), Dirty: true})

	f.sweep()
	f.at(900).sweep()

	harness.Equal(t, f.sentCount(), 2, "messages sent")
}
