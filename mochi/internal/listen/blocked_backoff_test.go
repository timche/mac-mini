package listen

import (
	"errors"
	"testing"
	"time"

	"github.com/timche/mac-mini/mochi/internal/chat"
	"github.com/timche/mac-mini/mochi/internal/mark"
	"github.com/timche/mac-mini/mochi/internal/state"
)

func (f *fixture) at(t time.Time) { f.h.Now = func() time.Time { return t } }

// A ping that does not land is tried again after 30 seconds, then 60, 120…, and the wait never
// grows past an hour.
func TestAFailedPingBacksOff(t *testing.T) {
	f, thread := startedSession(t)
	base := f.api.Tries[thread]
	tries := func() int { return f.api.Tries[thread] - base }
	f.herdr.agents["mochi-"+thread] = "blocked"
	f.api.SendErr[thread] = errors.New("503 Service Unavailable")
	start := time.Date(2026, 10, 20, 15, 4, 0, 0, time.UTC)

	f.at(start)
	f.h.WatchBlocked()
	f.h.WatchBlocked() // counted: the first try fails
	if tries() != 1 {
		t.Fatalf("tries = %d", tries())
	}

	for _, step := range []struct {
		after time.Duration
		want  int
	}{
		{10 * time.Second, 1}, // inside the first 30 s
		{30 * time.Second, 2}, // second try; the next waits 60 s
		{60 * time.Second, 2},
		{90 * time.Second, 3}, // third; the next waits 120 s
		{200 * time.Second, 3},
		{210 * time.Second, 4}, // fourth; the next waits 240 s
	} {
		f.at(start.Add(step.after))
		f.h.WatchBlocked()
		if tries() != step.want {
			t.Errorf("after %s: %d tries, want %d", step.after, tries(), step.want)
		}
	}
	if b := f.waits()[thread]; b.Backoff != 240 || b.Pinged {
		t.Errorf("wait = %+v", b)
	}

	// Many failures later the wait stops at an hour.
	for i := 0; i < 20; i++ {
		f.at(time.Unix(f.waits()[thread].RetryAt, 0))
		f.h.WatchBlocked()
	}
	if b := f.waits()[thread]; b.Backoff != 3600 {
		t.Errorf("backoff = %d, want 3600", b.Backoff)
	}

	// Discord answers again: the ping lands and the session shows ❓.
	delete(f.api.SendErr, thread)
	f.at(time.Unix(f.waits()[thread].RetryAt, 0))
	f.h.WatchBlocked()
	if f.pings(thread) != 1 {
		t.Errorf("pings = %d", f.pings(thread))
	}
	f.shows(thread, mark.Asking)
}

// A thread Discord no longer has is never pinged again, in this wait or any later one.
func TestADeletedThreadIsPingedNoMore(t *testing.T) {
	f, thread := startedSession(t)
	base := f.api.Tries[thread]
	tries := func() int { return f.api.Tries[thread] - base }
	f.herdr.agents["mochi-"+thread] = "blocked"
	f.api.SendErr[thread] = chat.ErrUnknownChannel

	f.h.WatchBlocked()
	f.h.WatchBlocked()
	f.at(time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC))
	f.h.WatchBlocked()
	if tries() != 1 {
		t.Errorf("tries = %d", tries())
	}

	f.herdr.agents["mochi-"+thread] = "working"
	f.h.WatchBlocked()
	f.herdr.agents["mochi-"+thread] = "blocked"
	f.h.WatchBlocked()
	f.h.WatchBlocked()
	if tries() != 1 {
		t.Errorf("a later wait tried a deleted thread again: %d tries", tries())
	}
}

func (f *fixture) waits() map[string]state.Blocked {
	f.t.Helper()
	all, err := f.h.Blocked.Load()
	if err != nil {
		f.t.Fatal(err)
	}
	return all
}
