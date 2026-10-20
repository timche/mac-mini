package mark

import (
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/chat"
	"github.com/timche/mac-mini/mochi/internal/chat/chattest"
	"github.com/timche/mac-mini/mochi/internal/state"
)

type fixture struct {
	api *chattest.Fake
	k   Marker
	now time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{api: chattest.New(), now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	f.k = Marker{
		API:  f.api,
		Path: filepath.Join(t.TempDir(), "marks.json"),
		Now:  func() time.Time { return f.now },
		Log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return f
}

func (f *fixture) showing(t *testing.T, ref state.Ref, want ...string) {
	t.Helper()
	if got := f.api.Showing(ref.Message); !reflect.DeepEqual(got, want) {
		t.Errorf("%s shows %v, want %v", ref.Message, got, want)
	}
}

var (
	mention = state.Ref{Channel: "c", Message: "m1"}
	follow  = state.Ref{Channel: "t1", Message: "m2"}
)

// Exactly one of mochi's reactions at a time, the one before taken back as it moves on.
func TestOneReactionAtATimeThroughASession(t *testing.T) {
	f := newFixture(t)

	f.k.Set(mention, Seen)
	f.showing(t, mention, Seen)
	f.k.Set(mention, Asking)
	f.showing(t, mention, Asking)
	f.k.Started("t1", mention)
	f.showing(t, mention, Running)
	f.k.Settled("t1", Finished)
	f.showing(t, mention, Finished)

	if !reflect.DeepEqual(f.api.Unreactions, []string{"m1 " + Seen, "m1 " + Asking, "m1 " + Running}) {
		t.Errorf("taken back: %v", f.api.Unreactions)
	}

	// The same state twice is one reaction, not two.
	f.k.Set(mention, Finished)
	if len(f.api.Reactions) != 4 {
		t.Errorf("reactions = %v", f.api.Reactions)
	}
}

func TestAFailureIsACross(t *testing.T) {
	f := newFixture(t)
	f.k.Set(mention, Seen)
	f.k.Failed("t1", mention)
	f.showing(t, mention, Failed)
}

// A follow-up goes ⏳ itself and puts the session's first message back to ⏳ until the next
// finished post, which finishes both.
func TestAFollowUpReopensTheSession(t *testing.T) {
	f := newFixture(t)
	f.k.Started("t1", mention)
	f.k.Settled("t1", Finished)

	f.k.Set(follow, Seen)
	f.k.Started("t1", follow, mention)
	f.showing(t, follow, Running)
	f.showing(t, mention, Running)

	f.k.Settled("t1", Finished)
	f.showing(t, follow, Finished)
	f.showing(t, mention, Finished)
}

// Tim's mention for several sessions is ✅ only once every one of them is.
func TestAGroupFinishesWhenEverySessionHas(t *testing.T) {
	f := newFixture(t)
	starters := map[string]state.Ref{}
	for _, thread := range []string{"t1", "t2", "t3"} {
		starters[thread] = state.Ref{Channel: thread, Message: "s-" + thread}
		f.k.Started(thread, starters[thread])
	}
	f.k.Group(mention, []string{"t1", "t2", "t3"})
	f.showing(t, mention, Running)

	f.k.Settled("t1", Finished)
	f.k.Settled("t3", Finished)
	f.showing(t, starters["t1"], Finished)
	f.showing(t, mention, Running)

	f.k.Settled("t2", Finished)
	f.showing(t, mention, Finished)

	// One of them working again puts the mention back to ⏳.
	f.k.Started("t2", state.Ref{Channel: "t2", Message: "f2"})
	f.showing(t, mention, Running)
	f.k.Settled("t2", Finished)
	f.showing(t, mention, Finished)
}

// The state survives a restart, so the reaction taken back is the right one.
func TestTheStateIsKeptOnDisk(t *testing.T) {
	f := newFixture(t)
	f.k.Started("t1", mention)

	again := Marker{API: f.api, Path: f.k.Path, Now: f.k.Now, Log: f.k.Log}
	again.Settled("t1", Finished)
	f.showing(t, mention, Finished)
}

func TestTheThreadNameCarriesTheState(t *testing.T) {
	f := newFixture(t)
	f.api.AddChannel(&discordgo.Channel{ID: "t1", Name: ThreadName(Running, "FACEIT overlay hover")})
	f.k.Named("t1", "FACEIT overlay hover", ThreadName(Running, "FACEIT overlay hover"))

	f.k.Started("t1", mention)
	if len(f.api.Renames) != 0 {
		t.Errorf("a name already right was renamed: %v", f.api.Renames)
	}
	f.k.Settled("t1", Finished)
	if f.api.Channels["t1"].Name != "✅ FACEIT overlay hover" {
		t.Errorf("name = %q", f.api.Channels["t1"].Name)
	}
	f.k.Failed("t1")
	if f.api.Channels["t1"].Name != "❌ FACEIT overlay hover" {
		t.Errorf("name = %q", f.api.Channels["t1"].Name)
	}
}

// Two renames in ten minutes; a third waits for the window, and then shows only the latest
// state, never the ones in between.
func TestRenamesAreCoalescedUnderTheLimit(t *testing.T) {
	f := newFixture(t)
	f.api.AddChannel(&discordgo.Channel{ID: "t1"})
	f.k.Named("t1", "Map stats", ThreadName(Running, "Map stats"))

	f.k.Settled("t1", Finished) // rename 1: ✅
	f.now = f.now.Add(time.Minute)
	f.k.Started("t1", follow) // rename 2: ⏳
	f.now = f.now.Add(time.Minute)
	f.k.Settled("t1", Finished) // over the limit: deferred
	f.k.Started("t1", follow)   // still deferred, now wanting ⏳
	f.k.Settled("t1", Finished) // and ✅ again
	if len(f.api.Renames) != 2 {
		t.Fatalf("renames = %v", f.api.Renames)
	}

	f.now = f.now.Add(5 * time.Minute)
	f.k.Retry()
	if len(f.api.Renames) != 2 {
		t.Error("a rename went out inside the window")
	}

	f.now = f.now.Add(4 * time.Minute)
	f.k.Retry()
	if len(f.api.Renames) != 3 || f.api.Renames[2] != "t1 ✅ Map stats" {
		t.Errorf("renames = %v", f.api.Renames)
	}
}

func TestA429IsOneLaterRenameWithTheLatestState(t *testing.T) {
	f := newFixture(t)
	f.api.AddChannel(&discordgo.Channel{ID: "t1"})
	f.k.Named("t1", "Map stats", ThreadName(Running, "Map stats"))
	f.api.RenameErr["t1"] = chat.ErrRateLimited

	f.k.Failed("t1")
	f.k.Started("t1", follow)
	f.k.Settled("t1", Finished)
	delete(f.api.RenameErr, "t1")

	f.now = f.now.Add(9 * time.Minute)
	f.k.Retry()
	if len(f.api.Renames) != 0 {
		t.Fatal("the retry came before Discord's wait")
	}
	f.now = f.now.Add(2 * time.Minute)
	f.k.Retry()
	if !reflect.DeepEqual(f.api.Renames, []string{"t1 ✅ Map stats"}) {
		t.Errorf("renames = %v", f.api.Renames)
	}
}

// A thread mochi did not make, such as a forum post Tim mentioned it in, is tried once and
// then left as it is.
func TestAThreadThatIsNotMochisIsLeftAlone(t *testing.T) {
	f := newFixture(t)
	f.api.AddChannel(&discordgo.Channel{ID: "post", Name: "Bug: elo resets"})
	f.k.Adopted("post", "Elo resets", "Bug: elo resets")
	f.api.RenameErr["post"] = chat.ErrForbidden

	f.k.Started("post", mention)
	f.k.Settled("post", Finished)
	f.now = f.now.Add(time.Hour)
	f.k.Retry()

	if f.api.Channels["post"].Name != "Bug: elo resets" || len(f.api.Renames) != 0 {
		t.Errorf("name = %q, renames %v", f.api.Channels["post"].Name, f.api.Renames)
	}
	f.showing(t, mention, Finished)
}

// A session hands Tim a result: ❓ on what it was working on and on its thread, then ⏳ when
// he follows up, and ✅ when it says it is done.
func TestAReviewWaitsOnTimUntilTheSessionIsDone(t *testing.T) {
	f := newFixture(t)
	f.api.AddChannel(&discordgo.Channel{ID: "t1"})
	f.k.Named("t1", "Map stats", ThreadName(Running, "Map stats"))

	f.k.Set(mention, Seen)
	f.k.Started("t1", mention)
	f.k.Settled("t1", Asking)
	f.showing(t, mention, Asking)
	if f.api.Channels["t1"].Name != "❓ Map stats" {
		t.Errorf("name = %q", f.api.Channels["t1"].Name)
	}

	// "merge it"
	f.k.Set(follow, Seen)
	f.k.Started("t1", follow, mention)
	f.showing(t, follow, Running)
	f.showing(t, mention, Running)

	// A done right after a review, with no follow-up, still reaches the messages it was about.
	f.k.Settled("t1", Finished)
	f.showing(t, follow, Finished)
	f.showing(t, mention, Finished)
}

func TestADoneAfterAReviewWithNoFollowUpFinishesThem(t *testing.T) {
	f := newFixture(t)
	f.k.Started("t1", mention)
	f.k.Settled("t1", Asking)
	f.k.Settled("t1", Finished)
	f.showing(t, mention, Finished)
}

// A group's mention: ⏳ while any session works, ❓ when none does and one waits on Tim, ✅
// once all are done.
func TestAGroupShowsWhatItsSessionsAreWaitingOn(t *testing.T) {
	f := newFixture(t)
	for _, thread := range []string{"t1", "t2"} {
		f.k.Started(thread, state.Ref{Channel: thread, Message: "s-" + thread})
	}
	f.k.Group(mention, []string{"t1", "t2"})

	f.k.Settled("t1", Asking)
	f.showing(t, mention, Running)
	f.k.Settled("t2", Finished)
	f.showing(t, mention, Asking)
	f.k.Settled("t1", Finished)
	f.showing(t, mention, Finished)
}
