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

// A session's state and how often it was set survive a restart, and no message is ever given
// a reaction for it: a name is where the state shows.
func TestTheStateIsKeptOnDiskAndShownOnNoMessage(t *testing.T) {
	f := newFixture(t)
	f.k.Started("t1")
	f.k.Settled("t1", Asking)

	again := Marker{API: f.api, Path: f.k.Path, Now: f.k.Now, Log: f.k.Log}
	if state, seq := again.Session("t1"); state != Asking || seq != 2 {
		t.Errorf("session = %q, %d", state, seq)
	}
	again.Settled("t1", Finished)
	if len(f.api.Reactions) != 0 || len(f.api.Unreactions) != 0 {
		t.Errorf("reactions %v, taken back %v", f.api.Reactions, f.api.Unreactions)
	}
}

func TestTheThreadNameCarriesTheState(t *testing.T) {
	f := newFixture(t)
	f.api.AddChannel(&discordgo.Channel{ID: "t1", Name: ThreadName(Running, "FACEIT overlay hover")})
	f.k.Named("t1", "FACEIT overlay hover", ThreadName(Running, "FACEIT overlay hover"))

	f.k.Started("t1")
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
	f.k.Started("t1") // rename 2: ⏳
	f.now = f.now.Add(time.Minute)
	f.k.Settled("t1", Finished) // over the limit: deferred
	f.k.Started("t1")           // still deferred, now wanting ⏳
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
	f.k.Started("t1")
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

// A thread mochi did not make, such as a forum post Tim mentioned it in, keeps the title its
// author gave it: nothing even tries to rename it.
func TestAThreadThatIsNotMochisIsNeverRenamed(t *testing.T) {
	f := newFixture(t)
	f.api.AddChannel(&discordgo.Channel{ID: "post", Name: "Bug: elo resets"})

	f.k.Started("post")
	f.k.Settled("post", Asking)
	f.k.Settled("post", Finished)
	f.now = f.now.Add(time.Hour)
	f.k.Retry()

	if f.api.Channels["post"].Name != "Bug: elo resets" || len(f.api.Renames) != 0 {
		t.Errorf("name = %q, renames %v", f.api.Channels["post"].Name, f.api.Renames)
	}
}
