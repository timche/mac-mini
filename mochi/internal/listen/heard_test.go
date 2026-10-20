package listen

import (
	"testing"
	"time"

	"github.com/timche/mac-mini/mochi/internal/event"
	"github.com/timche/mac-mini/mochi/internal/mark"
	"github.com/timche/mac-mini/mochi/internal/state"
)

// Tim answers in the session itself — the question picked, or a prompt typed: the wait is
// over and ⏳ is back, and the next time it stops he is pinged again.
func TestAnAnswerInTheSessionEndsTheWait(t *testing.T) {
	f, thread := startedSession(t)
	f.event(thread, event.Ask, 0)
	f.consume()
	f.inState(thread, mark.Asking)

	f.event(thread, event.Heard, 0)
	f.consume()
	f.inState(thread, mark.Running)

	f.event(thread, event.Stop, 0)
	f.consume()
	if f.pings(thread) != 2 {
		t.Errorf("pings = %d, want one per wait", f.pings(thread))
	}
}

// What the session set itself after the ping — a --review, a --done — is newer than the ping,
// and his answer leaves it.
func TestAnAnswerLeavesWhatTheSessionSetSince(t *testing.T) {
	for _, emoji := range []string{mark.Finished, mark.Asking} {
		f, thread := startedSession(t)
		f.event(thread, event.Stop, 0)
		f.consume()
		f.h.Marks.Settled(thread, emoji)

		f.event(thread, event.Heard, 0)
		f.consume()
		f.inState(thread, emoji)
	}
}

// A stop from before Tim's answer, read after it, is out of date and pings nobody.
func TestAStaleStopPingsNobody(t *testing.T) {
	f, thread := startedSession(t)
	f.event(thread, event.Stop, 0)  // the session's turn ended…
	f.event(thread, event.Heard, 0) // …and Tim had answered by the time it was read
	f.consume()
	if f.pings(thread) != 0 {
		t.Errorf("pings = %d", f.pings(thread))
	}

	// A stop written before an answer that is read later is stale as well.
	f.event(thread, event.Heard, 0)
	f.consume()
	waits := f.waits()
	waits[thread] = state.Wait{Heard: f.h.Now().Add(time.Hour).UnixNano()}
	f.h.Waits.Save(waits)
	f.event(thread, event.Stop, 0)
	f.consume()
	if f.pings(thread) != 0 {
		t.Errorf("a stop older than Tim's answer pinged")
	}
}

// And a stop from before the session's own record was last updated is stale too — a
// follow-up started it working again since.
func TestAStopFromBeforeTheSessionLastStartedIsStale(t *testing.T) {
	f, thread := startedSession(t)
	rec := f.record(thread)
	rec.Updated = f.h.Now().Add(time.Hour).Unix()
	f.h.Store.Put(rec)

	f.event(thread, event.Stop, 0)
	f.consume()
	if f.pings(thread) != 0 {
		t.Errorf("pings = %d", f.pings(thread))
	}
}
