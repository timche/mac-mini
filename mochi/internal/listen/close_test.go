package listen

import (
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/mochi/internal/event"
	"github.com/timche/mac-mini/mochi/internal/mark"
)

// What `mochi post --done --close` leaves for the listener.
func (f *fixture) closeRequested(thread string) {
	f.t.Helper()
	f.seq++
	at := f.h.Now().Add(time.Duration(f.seq) * time.Millisecond)
	if err := event.Put(f.cfg.EventsDir(), event.Event{Thread: thread, Kind: event.Close, At: at.Unix(), Nanos: at.UnixNano()}); err != nil {
		f.t.Fatal(err)
	}
}

// A heard written by the session's own hook: Tim typing in it, or one of mochi's own prompts
// arriving.
func (f *fixture) prompted(thread, prompt string) {
	f.t.Helper()
	f.seq++
	at := f.h.Now().Add(time.Duration(f.seq) * time.Millisecond)
	event.Write(strings.NewReader(`{"prompt":`+quoteJSON(prompt)+`}`), func(k string) string {
		if k == event.ThreadVar {
			return thread
		}
		return ""
	}, f.cfg.EventsDir(), event.Heard, func() time.Time { return at })
}

func quoteJSON(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

func (f *fixture) later(d time.Duration) {
	f.at(f.h.Now().Add(d))
	f.h.ConsumeEvents()
}

// A session Tim started from Discord, done and cleaned up: its tab closes a minute later, and
// not before; the record stays, so a later follow-up starts a new session.
func TestADoneSessionFromDiscordHasItsTabClosed(t *testing.T) {
	f, thread := startedSession(t)
	agent := "mochi-" + thread
	f.prompted(thread, openingPrompt("/brief.md")) // the brief arriving is mochi's own prompt
	f.h.Marks.Settled(thread, mark.Finished)
	f.closeRequested(thread)
	f.h.ConsumeEvents()

	f.later(closeAfter - time.Second)
	if len(f.herdr.closed) != 0 {
		t.Fatal("closed inside the minute")
	}
	f.later(2 * time.Second)
	if len(f.herdr.closed) != 1 || f.herdr.closed[0] != "tab:"+agent {
		t.Fatalf("closed = %v", f.herdr.closed)
	}
	f.later(time.Minute)
	if len(f.herdr.closed) != 1 {
		t.Error("closed twice")
	}
	if f.record(thread).Agent != agent {
		t.Error("the record went with the tab")
	}

	f.h.Handle(f.mention(thread, 9, tim, "one more thing", nil))
	if len(f.herdr.called("agent start")) != 2 {
		t.Error("a follow-up after the close did not start a new session")
	}
}

// Tim last typed in the session itself: he is at it, so it stays open.
func TestASessionTimLastTypedInStaysOpen(t *testing.T) {
	f, thread := startedSession(t)
	f.prompted(thread, "looks good, merge it")
	f.h.ConsumeEvents()
	f.h.Marks.Settled(thread, mark.Finished)
	f.closeRequested(thread)
	f.h.ConsumeEvents()
	f.later(2 * closeAfter)
	if len(f.herdr.closed) != 0 {
		t.Errorf("closed = %v", f.herdr.closed)
	}

	// A follow-up from Discord after that makes Discord the last word again.
	g, other := startedSession(t)
	g.prompted(other, "check this")
	g.h.ConsumeEvents()
	g.h.Handle(g.mention(other, 5, tim, "and merge", nil))
	g.prompted(other, relayPrompt("/relay.md", false))
	g.h.Marks.Settled(other, mark.Finished)
	g.closeRequested(other)
	g.h.ConsumeEvents()
	g.later(closeAfter + time.Second)
	if len(g.herdr.closed) != 1 {
		t.Errorf("a session last reached from Discord stayed open: %v", g.herdr.closed)
	}
}

// Tim saying anything to the session inside the minute keeps it open.
func TestAWordInsideTheMinuteCancelsTheClose(t *testing.T) {
	f, thread := startedSession(t)
	f.h.Marks.Settled(thread, mark.Finished)
	f.closeRequested(thread)
	f.h.ConsumeEvents()
	f.at(f.h.Now().Add(30 * time.Second))
	f.prompted(thread, "wait, one more thing")
	f.h.ConsumeEvents()
	f.later(2 * closeAfter)
	if len(f.herdr.closed) != 0 {
		t.Errorf("closed = %v", f.herdr.closed)
	}
}

// Without --close nothing closes; and a tab no longer labelled as mochi made it is left.
func TestNoCloseWithoutItAndNotATabMochiDidNotLabel(t *testing.T) {
	f, thread := startedSession(t)
	f.h.Marks.Settled(thread, mark.Finished)
	f.event(thread, event.Stop, 0)
	f.later(2 * closeAfter)
	if len(f.herdr.closed) != 0 {
		t.Errorf("closed without --close: %v", f.herdr.closed)
	}

	g, other := startedSession(t)
	g.herdr.agentTabs["mochi-"+other] = "renamed-by-tim"
	g.h.Marks.Settled(other, mark.Finished)
	g.closeRequested(other)
	g.h.ConsumeEvents()
	g.later(closeAfter + time.Second)
	if len(g.herdr.closed) != 0 {
		t.Errorf("closed a tab mochi did not label: %v", g.herdr.closed)
	}
}

// The prompts mochi pastes are recognised as its own, and Tim's typing is not.
func TestMochisOwnPromptsAreToldFromTims(t *testing.T) {
	for _, p := range []string{openingPrompt("/b.md"), relayPrompt("/r.md", false), relayPrompt("/r.md", true)} {
		if !event.FromMochi(p) {
			t.Errorf("not recognised: %q", p)
		}
	}
	if event.FromMochi("Tim says hi") {
		t.Error("Tim's own words taken for mochi's")
	}
}
