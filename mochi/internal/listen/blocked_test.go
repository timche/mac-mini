package listen

import (
	"io"
	"log/slog"
	"reflect"
	"testing"

	"github.com/timche/mac-mini/mochi/internal/mark"
)

const ping = "<@94004892942741504> the session is waiting on you — pick an option in the session."

func (f *fixture) pings(thread string) int {
	n := 0
	for _, text := range f.sentTo(thread) {
		if text == ping {
			n++
		}
	}
	return n
}

func startedSession(t *testing.T) (*fixture, string) {
	t.Helper()
	f := newFixture(t)
	m := f.mention(channel, 1, tim, "research", nil)
	f.h.Handle(m)
	return f, m.ID
}

// Blocked on two polls running pings Tim once, with the one mention mochi ever makes, and
// shows ❓ until the session moves; staying blocked does not ping again.
func TestASessionWaitingOnTimPingsHimOnce(t *testing.T) {
	f, thread := startedSession(t)
	f.herdr.agents["mochi-"+thread] = "blocked"

	f.h.WatchBlocked()
	if f.pings(thread) != 0 {
		t.Fatal("pinged on the first poll")
	}
	f.h.WatchBlocked()
	if f.pings(thread) != 1 {
		t.Fatalf("pings = %d", f.pings(thread))
	}
	sent := f.api.Sent[len(f.api.Sent)-1].Msg
	am := sent.AllowedMentions
	if am == nil || len(am.Parse) != 0 || !reflect.DeepEqual(am.Users, []string{tim.ID}) || am.RepliedUser {
		t.Errorf("allowed mentions = %+v", am)
	}
	f.shows(thread, mark.Asking)
	if f.api.Channels[thread].Name != "❓ research" {
		t.Errorf("thread = %q", f.api.Channels[thread].Name)
	}

	f.h.WatchBlocked()
	f.h.WatchBlocked()
	if f.pings(thread) != 1 {
		t.Errorf("pinged again while still blocked: %d", f.pings(thread))
	}

	// It moves again: ⏳.
	f.herdr.agents["mochi-"+thread] = "working"
	f.h.WatchBlocked()
	f.shows(thread, mark.Running)
	if f.api.Channels[thread].Name != "⏳ research" {
		t.Errorf("thread = %q", f.api.Channels[thread].Name)
	}

	// A later wait is a new episode, and a new ping.
	f.herdr.agents["mochi-"+thread] = "blocked"
	f.h.WatchBlocked()
	f.h.WatchBlocked()
	if f.pings(thread) != 2 {
		t.Errorf("pings = %d, want a second for the second wait", f.pings(thread))
	}
}

// A question answered between two polls pings nobody.
func TestABriefWaitPingsNobody(t *testing.T) {
	f, thread := startedSession(t)
	for _, status := range []string{"blocked", "working", "blocked", "idle", "blocked"} {
		f.herdr.agents["mochi-"+thread] = status
		f.h.WatchBlocked()
	}
	if f.pings(thread) != 0 {
		t.Errorf("pings = %d", f.pings(thread))
	}
	f.shows(thread, mark.Running)
}

// The episode survives a restart: no second ping for the same wait, and a count already
// begun is finished rather than started again.
func TestAWaitOutlivesARestart(t *testing.T) {
	f, thread := startedSession(t)
	f.herdr.agents["mochi-"+thread] = "blocked"
	f.h.WatchBlocked()
	f.h.WatchBlocked()

	restarted := &Handler{
		Cfg: f.h.Cfg, API: f.api, Herdr: f.herdr.run,
		Store: f.h.Store, Pending: f.h.Pending, Blocked: f.h.Blocked, Marks: f.h.Marks,
		Now: f.h.Now, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	restarted.SetBot(bot.ID)
	restarted.WatchBlocked()
	if f.pings(thread) != 1 {
		t.Errorf("pinged again after a restart: %d", f.pings(thread))
	}

	f.herdr.agents["mochi-"+thread] = "working"
	restarted.WatchBlocked()
	f.shows(thread, mark.Running)
}

// A session herdr has lost is not waiting on anybody, and one herdr cannot be asked about is
// left as it was.
func TestAGoneSessionEndsItsWait(t *testing.T) {
	f, thread := startedSession(t)
	f.herdr.agents["mochi-"+thread] = "blocked"
	f.h.WatchBlocked()
	f.h.WatchBlocked()

	delete(f.herdr.agents, "mochi-"+thread)
	f.h.WatchBlocked()
	if all, _ := f.h.Blocked.Load(); len(all) != 0 {
		t.Errorf("still remembered: %v", all)
	}
}
