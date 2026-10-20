package listen

import (
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

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

// A session herdr has lost is not waiting on anybody: its wait ends, and it is not asked
// after again until its record changes, as it does when a follow-up starts a new session.
func TestAGoneSessionEndsItsWaitAndIsNotAskedAgain(t *testing.T) {
	f, thread := startedSession(t)
	f.herdr.agents["mochi-"+thread] = "blocked"
	f.h.WatchBlocked()
	f.h.WatchBlocked()

	delete(f.herdr.agents, "mochi-"+thread)
	f.h.WatchBlocked()
	all, _ := f.h.Blocked.Load()
	if b := all[thread]; b.Pinged || b.Polls != 0 || b.Gone == 0 {
		t.Errorf("remembered %+v", b)
	}

	asked := f.herdr.gets
	f.h.WatchBlocked()
	f.h.WatchBlocked()
	if f.herdr.gets != asked {
		t.Errorf("a session last seen gone was asked after %d more times", f.herdr.gets-asked)
	}

	// A follow-up later starts a new session in the thread, and it is watched again.
	f.h.Now = func() time.Time { return time.Date(2026, 10, 20, 16, 0, 0, 0, time.UTC) }
	f.h.Handle(f.mention(thread, 5, tim, "again", nil))
	asked = f.herdr.gets
	f.h.WatchBlocked()
	if f.herdr.gets != asked+1 {
		t.Errorf("the new session was not asked after")
	}
}

// herdr is asked without the handler's lock, so a mention is never kept waiting on a poll.
func TestTheWatchDoesNotHoldTheLockWhileAskingHerdr(t *testing.T) {
	f, thread := startedSession(t)
	f.herdr.agents["mochi-"+thread] = "blocked"
	f.herdr.getStarted, f.herdr.getRelease = make(chan struct{}), make(chan struct{})

	done := make(chan struct{})
	go func() { f.h.WatchBlocked(); close(done) }()
	<-f.herdr.getStarted

	locked := make(chan struct{})
	go func() { f.h.Lock(); f.h.Unlock(); close(locked) }()
	select {
	case <-locked:
	case <-time.After(2 * time.Second):
		t.Error("the handler's lock was held while herdr was asked")
	}
	close(f.herdr.getRelease)
	<-done
}

// herdr not answering ends the pass rather than reading every session as moved on.
func TestAHerdrThatDoesNotAnswerEndsThePass(t *testing.T) {
	f, first := startedSession(t)
	second := f.mention(channel, 2, tim, "another", nil)
	f.h.Handle(second)
	f.herdr.agents["mochi-"+first] = "blocked"
	f.herdr.agents["mochi-"+second.ID] = "blocked"
	f.h.WatchBlocked()
	f.h.WatchBlocked()
	if f.pings(first) != 1 || f.pings(second.ID) != 1 {
		t.Fatal("not pinged to begin with")
	}

	f.herdr.getErr = errors.New("herdr: connection refused")
	asked := f.herdr.gets
	f.h.WatchBlocked()
	if f.herdr.gets != asked+1 {
		t.Errorf("asked %d times after herdr stopped answering", f.herdr.gets-asked)
	}
	f.shows(first, mark.Asking)
	f.shows(second.ID, mark.Asking)
}
