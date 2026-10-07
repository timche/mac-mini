package sync

import (
	"strings"
	"testing"
	"time"
)

// A tick at a time with the clock in the test's hands, which is the only way to say that a
// burst of writes becomes one commit rather than five.
func tick(f *fixture, l *loop, after time.Duration) {
	f.now = f.now.Add(after)
	l.tick(f.now)
}

// A write, and then enough ticks for the debounce to run out on it: the tick that finds the
// status changed is the one that starts the wait, never the one that ends it.
func write(f *fixture, l *loop, status string, staged ...string) {
	f.tree.status, f.tree.staged = status, staged
	tick(f, l, time.Second)
	tick(f, l, f.repo.Debounce+time.Second)
}

func TestABurstOfWritesBecomesOneCommit(t *testing.T) {
	f := newFixture(t)
	_, l := f.loop()

	f.tree.status, f.tree.staged = " M a.md\x00", []string{"a.md"}
	tick(f, l, time.Second)

	// Another write before the debounce is up restarts it.
	f.tree.status, f.tree.staged = " M a.md\x00 M b.md\x00", []string{"a.md", "b.md"}
	tick(f, l, 4*time.Second)
	tick(f, l, 4*time.Second)

	if f.ranAny("commit ") {
		t.Fatalf("it committed inside the debounce: %v", f.calls)
	}

	tick(f, l, 2*time.Second)

	if !f.ran("commit -m Update a.md, b.md") {
		t.Errorf("the burst was not one commit: %v", f.calls)
	}
}

// A tree that stops changing and then does nothing is one pass, not a pass a second: the
// commit empties the status, and the status is the whole of what the debounce watches.
func TestACommittedTreeDoesNotPassAgain(t *testing.T) {
	f := newFixture(t)
	_, l := f.loop()

	write(f, l, " M a.md\x00", "a.md")
	f.calls = nil

	for i := 0; i < 20; i++ {
		tick(f, l, time.Second)
	}

	if f.ranAny("commit ") || f.ranAny("push ") {
		t.Errorf("an idle tree kept passing: %v", f.calls)
	}
}

// Without it a repository only ever pulls when it has something of its own to push, which
// is what brings a second machine's writes down at all.
func TestTheFetchIntervalRunsAPassWithNothingChangedLocally(t *testing.T) {
	f := newFixture(t)
	f.repo.FetchInterval = time.Minute
	_, l := f.loop()
	f.calls = nil

	tick(f, l, 30*time.Second)
	if f.ran("fetch origin") {
		t.Fatalf("it fetched before the interval: %v", f.calls)
	}

	tick(f, l, 31*time.Second)
	if !f.ran("fetch origin") {
		t.Errorf("the interval did not fetch: %v", f.calls)
	}
}

func TestAZeroFetchIntervalNeverFetchesOnItsOwn(t *testing.T) {
	f := newFixture(t)
	f.repo.FetchInterval = 0
	_, l := f.loop()
	f.calls = nil

	for i := 0; i < 10; i++ {
		tick(f, l, time.Minute)
	}
	if f.ran("fetch origin") {
		t.Errorf("calls: %v", f.calls)
	}
}

// Nothing need be written for a push delay to run out, so the loop has to come back to it by
// itself.
func TestAHeldPushGoesUpWhenItsDelayRunsOut(t *testing.T) {
	f := newFixture(t)
	f.repo.PushDelay = 10 * time.Minute
	f.repo.FetchInterval = time.Hour
	_, l := f.loop()

	write(f, l, " M a.md\x00", "a.md")

	if !f.ranAny("commit ") || f.ranAny("push ") {
		t.Fatalf("calls: %v", f.calls)
	}
	f.calls = nil

	tick(f, l, 5*time.Minute)
	if f.ranAny("push ") {
		t.Fatalf("it pushed before the delay was up: %v", f.calls)
	}

	tick(f, l, 6*time.Minute)
	if !f.ranAny("push ") {
		t.Errorf("the delay ran out and nothing pushed: %v", f.calls)
	}
}

// While commits remain unpushed the push is retried even with nothing written, which is what
// gets a repository upstream after an outage that nobody was watching.
func TestTheRecheckRetriesAFailedPushWithNothingWritten(t *testing.T) {
	f := newFixture(t)
	f.repo.FetchInterval, f.repo.Recheck = time.Hour, 10*time.Minute
	f.retry.Attempts = 1
	f.tree.unpushed = []string{"deadbee mine"}
	f.tree.oldest = base
	f.tree.pushRefused = 1
	f.tree.pushText = "fatal: Could not read from remote repository"

	_, l := f.loop()
	if !l.outstanding {
		t.Fatal("a failed push left nothing outstanding")
	}
	f.calls = nil

	tick(f, l, 5*time.Minute)
	if f.ranAny("push ") {
		t.Fatalf("it rechecked early: %v", f.calls)
	}

	tick(f, l, 6*time.Minute)
	if !f.ranAny("push ") {
		t.Errorf("the recheck did not retry: %v", f.calls)
	}
}

// Rebasing again over the conflict that stopped the last pass would only conflict again, so
// the fetch interval and the recheck both stop until either side moves.
func TestAPausedRepositoryStopsFetchingAndRechecking(t *testing.T) {
	f := newFixture(t)
	f.tree.behind = []string{"cafe123 theirs"}
	f.tree.rebaseText = "CONFLICT (content): Merge conflict in a.md"

	_, l := f.loop()
	if !l.paused {
		t.Fatal("the conflict did not pause the repository")
	}
	f.calls = nil

	// The fetch interval still comes round, but only to ask whether either side has moved:
	// no rebase, no push.
	tick(f, l, 2*time.Minute)

	if f.ranAny("pull ") || f.ranAny("push ") {
		t.Errorf("a paused repository rebased or pushed: %v", f.calls)
	}
	if !f.ran("fetch origin") {
		t.Errorf("it did not look to see whether the remote had moved: %v", f.calls)
	}
}

// The pause is in the state file rather than in memory, so a daemon that restarts does not
// rebase over the conflict that stopped the last one.
func TestAPauseSurvivesARestart(t *testing.T) {
	f := newFixture(t)
	f.tree.behind = []string{"cafe123 theirs"}
	f.tree.rebaseText = "CONFLICT (content): Merge conflict in a.md"

	f.loop()

	at, paused := (&Store{Dir: f.daemon().store.Dir}).PausedAt(f.repo.Path)
	if !paused {
		t.Fatal("the pause was not written down")
	}
	if at.Head == "" || at.Upstream == "" {
		t.Errorf("the pause recorded nothing to re-arm against: %+v", at)
	}

	f.tree.rebaseText = ""
	f.calls = nil
	_, l := f.loop()

	if !l.paused {
		t.Fatalf("a fresh loop started syncing a paused repository: %v", f.calls)
	}
	if f.ranAny("pull ") {
		t.Errorf("it rebased on startup: %v", f.calls)
	}
}

// Either sha moving means somebody has been here, which is the whole of what re-arms it.
func TestAPausedRepositoryResumesWhenHeadMoves(t *testing.T) {
	f := newFixture(t)
	f.tree.behind = []string{"cafe123 theirs"}
	f.tree.rebaseText = "CONFLICT (content): Merge conflict in a.md"

	_, l := f.loop()
	f.calls, f.sent = nil, nil

	// Nothing has moved, so it stays paused however many intervals come round.
	tick(f, l, 2*time.Minute)
	tick(f, l, 2*time.Minute)
	if !l.paused {
		t.Fatal("it resumed with neither side moved")
	}

	f.tree.head = "resolved-by-hand"
	f.tree.rebaseText, f.tree.behind = "", nil
	tick(f, l, 2*time.Minute)

	if l.paused {
		t.Fatalf("it stayed paused after HEAD moved: %v", f.calls)
	}
	if _, still := f.state().Paused[f.repo.Path]; still {
		t.Error("the pause is still in the state")
	}
	if len(f.sent) == 0 || !strings.Contains(f.lastSent(), "syncing again") {
		t.Errorf("nothing said the repository was back: %v", f.sent)
	}
}

func TestAPausedRepositoryResumesWhenTheUpstreamMoves(t *testing.T) {
	f := newFixture(t)
	f.tree.behind = []string{"cafe123 theirs"}
	f.tree.rebaseText = "CONFLICT (content): Merge conflict in a.md"

	_, l := f.loop()

	f.tree.upstream = "they-moved-on"
	f.tree.rebaseText, f.tree.behind = "", nil
	tick(f, l, 2*time.Minute)

	if l.paused {
		t.Error("it stayed paused after the upstream moved")
	}
}

// New work is not the same two sides, so it is committed and pushed while the pull stays
// paused.
func TestAPausedRepositoryStillCommitsNewWork(t *testing.T) {
	f := newFixture(t)
	f.tree.behind = []string{"cafe123 theirs"}
	f.tree.rebaseText = "CONFLICT (content): Merge conflict in a.md"

	_, l := f.loop()
	f.calls = nil

	write(f, l, " M a.md\x00", "a.md")

	if !f.ranAny("commit ") {
		t.Errorf("new work in a paused repository was not committed: %v", f.calls)
	}
}

// A fetch that cannot answer while a repository is paused says so and leaves it paused: not
// knowing whether either side moved is not the same as knowing neither did.
func TestAPausedRepositoryThatCannotFetchStaysPaused(t *testing.T) {
	f := newFixture(t)
	f.tree.behind = []string{"cafe123 theirs"}
	f.tree.rebaseText = "CONFLICT (content): Merge conflict in a.md"

	_, l := f.loop()
	f.log.Reset()

	f.tree.fetchFails, f.tree.fetchText = 1, "fatal: Could not read from remote repository"
	tick(f, l, 2*time.Minute)

	if !l.paused {
		t.Error("it resumed on a fetch that failed")
	}
	if !strings.Contains(f.log.String(), "could not be fetched") {
		t.Errorf("log: %s", f.log.String())
	}
}

// A catch-up pass before the first tick, so a commit that fell due while the Mac was off
// goes out at once rather than waiting for the next thing anybody writes.
func TestTheFirstPassHappensBeforeAnyTick(t *testing.T) {
	f := newFixture(t)
	f.tree.unpushed = []string{"deadbee from before the reboot"}
	f.tree.oldest = base.Add(-time.Hour)

	f.loop()

	if !f.ranAny("push ") {
		t.Errorf("nothing was caught up on startup: %v", f.calls)
	}
}
