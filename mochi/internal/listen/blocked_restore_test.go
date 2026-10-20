package listen

import (
	"testing"
	"time"

	"github.com/timche/mac-mini/mochi/internal/mark"
)

func (f *fixture) waitThenMove(thread string, meanwhile func()) {
	f.herdr.agents["mochi-"+thread] = "blocked"
	f.h.WatchBlocked()
	f.h.WatchBlocked()
	if meanwhile != nil {
		meanwhile()
	}
	f.herdr.agents["mochi-"+thread] = "working"
	f.h.WatchBlocked()
}

// The ping's ❓ goes when the wait ends, and the state from before it comes back.
func TestTheWaitEndingRestoresWhatWasThere(t *testing.T) {
	f, thread := startedSession(t)
	f.waitThenMove(thread, nil)
	f.shows(thread, mark.Running)

	// A session already done that then waits on a permission is done again after it.
	f2, done := startedSession(t)
	f2.h.Marks.Settled(done, mark.Finished)
	f2.waitThenMove(done, nil)
	f2.shows(done, mark.Finished)
	// The name has had its two renames for these ten minutes (✅, then ❓), so the third waits.
	f2.at(f2.h.Now().Add(11 * time.Minute))
	f2.h.Marks.Retry()
	if f2.api.Channels[done].Name != "✅ research" {
		t.Errorf("thread = %q", f2.api.Channels[done].Name)
	}
}

// What the session set itself while it waited is newer than the ping, and stays.
func TestWhatTheSessionSetWhileWaitingStays(t *testing.T) {
	for emoji, name := range map[string]string{mark.Finished: "--done", mark.Asking: "--review"} {
		f, thread := startedSession(t)
		f.waitThenMove(thread, func() { f.h.Marks.Settled(thread, emoji) })
		if got := f.api.Showing(thread); len(got) != 1 || got[0] != emoji {
			t.Errorf("after %s while waiting: %v", name, got)
		}
	}
}

// A session that vanished while it waited is not shown as working.
func TestAVanishedSessionIsNotShownWorking(t *testing.T) {
	f, thread := startedSession(t)
	f.herdr.agents["mochi-"+thread] = "blocked"
	f.h.WatchBlocked()
	f.h.WatchBlocked()

	delete(f.herdr.agents, "mochi-"+thread)
	f.h.WatchBlocked()
	f.shows(thread, mark.Asking)
}
