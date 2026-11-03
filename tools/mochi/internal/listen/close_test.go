package listen

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/discord"

	"github.com/timche/mac-mini/mochi/internal/event"
	"github.com/timche/mac-mini/mochi/internal/gather"
	"github.com/timche/mac-mini/mochi/internal/hc"
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
	b, _ := json.Marshal(s)
	return string(b)
}

func (f *fixture) later(d time.Duration) {
	f.at(f.h.Now().Add(d))
	f.h.ConsumeEvents()
}

// A session Tim started from Discord, done and cleaned up: its terminal closes a minute later,
// and not before; the record stays, so a later follow-up starts a new session.
func TestADoneSessionFromDiscordHasItsTerminalClosed(t *testing.T) {
	f, thread := startedSession(t)
	agent := "mochi-" + thread
	f.prompted(thread, f.hc.raw[agent][0]) // the opening arriving is mochi's own prompt
	f.h.Marks.Settled(thread, mark.Finished)
	f.closeRequested(thread)
	f.h.ConsumeEvents()

	f.later(closeAfter - time.Second)
	if len(f.hc.closed) != 0 {
		t.Fatal("closed inside the minute")
	}
	f.later(2 * time.Second)
	if len(f.hc.closed) != 1 || f.hc.closed[0] != agent {
		t.Fatalf("closed = %v", f.hc.closed)
	}
	f.later(time.Minute)
	if len(f.hc.closed) != 1 {
		t.Error("closed twice")
	}
	if f.record(thread).Agent != agent {
		t.Error("the record went with the terminal")
	}

	f.h.Handle(f.mention(thread, 9, tim, "one more thing", nil))
	if len(f.hc.called("run")) != 2 {
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
	if len(f.hc.closed) != 0 {
		t.Errorf("closed = %v", f.hc.closed)
	}

	// A follow-up from Discord after that makes Discord the last word again.
	g, other := startedSession(t)
	g.prompted(other, "check this")
	g.h.ConsumeEvents()
	g.h.Handle(g.mention(other, 5, tim, "and merge", nil))
	g.prompted(other, g.hc.raw["mochi-"+other][len(g.hc.raw["mochi-"+other])-1])
	g.h.Marks.Settled(other, mark.Finished)
	g.closeRequested(other)
	g.h.ConsumeEvents()
	g.later(closeAfter + time.Second)
	if len(g.hc.closed) != 1 {
		t.Errorf("a session last reached from Discord stayed open: %v", g.hc.closed)
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
	if len(f.hc.closed) != 0 {
		t.Errorf("closed = %v", f.hc.closed)
	}
}

// Without --close nothing closes; and a terminal no longer carrying mochi's own label is left.
func TestNoCloseWithoutItAndNotATerminalMochiDidNotLabel(t *testing.T) {
	f, thread := startedSession(t)
	f.h.Marks.Settled(thread, mark.Finished)
	f.event(thread, event.Stop, 0)
	f.later(2 * closeAfter)
	if len(f.hc.closed) != 0 {
		t.Errorf("closed without --close: %v", f.hc.closed)
	}

	g, other := startedSession(t)
	g.hc.live["mochi-"+other].labels[hc.ThreadLabel] = "another-thread"
	g.h.Marks.Settled(other, mark.Finished)
	g.closeRequested(other)
	g.h.ConsumeEvents()
	g.later(closeAfter + time.Second)
	if len(g.hc.closed) != 0 {
		t.Errorf("closed a terminal mochi did not open: %v", g.hc.closed)
	}
}

// A session somebody may be in the middle of is left open: one still working, or one on a
// question. The request is dropped, not tried again once it would pass.
func TestASessionInUseIsNotClosedAndNotTriedAgain(t *testing.T) {
	for name, inUse := range map[string]func(f *fixture, agent string){
		"working": func(f *fixture, agent string) { f.hc.say(agent, "working") },
		"blocked": func(f *fixture, agent string) { f.hc.say(agent, "blocked") },
	} {
		f, thread := startedSession(t)
		agent := "mochi-" + thread
		f.h.Marks.Settled(thread, mark.Finished)
		f.closeRequested(thread)
		f.h.ConsumeEvents()
		inUse(f, agent)
		f.later(closeAfter + time.Second)
		if len(f.hc.closed) != 0 {
			t.Errorf("%s: closed = %v", name, f.hc.closed)
		}

		f.hc.say(agent, "done")
		f.later(closeAfter)
		if len(f.hc.closed) != 0 {
			t.Errorf("%s: the close was tried again", name)
		}
		if w := f.waits()[thread]; w.CloseAt != 0 {
			t.Errorf("%s: the close request was kept: %+v", name, w)
		}
	}
}

// The prompts mochi pastes are recognised as its own, and Tim's typing is not.
func TestMochisOwnPromptsAreToldFromTims(t *testing.T) {
	m := &discord.Message{ID: "3", ChannelID: "2", GuildID: "1"}
	for _, p := range []string{skillFirst + "\n\nTim mentioned mochi", followUp(m, "x", gather.NewSet("1"), nil, false), followUp(m, "x", gather.NewSet("1"), nil, true)} {
		if !event.FromMochi(p) {
			t.Errorf("not recognised: %q", p)
		}
	}
	if event.FromMochi("Tim says hi") {
		t.Error("Tim's own words taken for mochi's")
	}
}
