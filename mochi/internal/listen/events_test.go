package listen

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/mochi/internal/chat"
	"github.com/timche/mac-mini/mochi/internal/event"
	"github.com/timche/mac-mini/mochi/internal/mark"
	"github.com/timche/mac-mini/mochi/internal/state"
)

const ping = "The session is waiting on <@94004892942741504>."

func startedSession(t *testing.T) (*fixture, string) {
	t.Helper()
	f := newFixture(t)
	m := f.mention(channel, 1, tim, "research", nil)
	f.h.Handle(m)
	return f, m.ID
}

// What the session's hook leaves when its turn ends or it asks a question.
func (f *fixture) event(thread, kind string, background int) {
	f.t.Helper()
	input := `{"background_tasks":[` + strings.TrimSuffix(strings.Repeat(`{"id":"x"},`, background), ",") + `]}`
	f.seq++
	at := f.h.Now().Add(time.Duration(f.seq) * time.Millisecond)
	event.Write(strings.NewReader(input), func(k string) string {
		if k == event.ThreadVar {
			return thread
		}
		return ""
	}, f.cfg.EventsDir(), kind, func() time.Time { return at })
}

func (f *fixture) pings(thread string) int {
	n := 0
	for _, text := range f.sentTo(thread) {
		if text == ping {
			n++
		}
	}
	return n
}

func (f *fixture) at(t time.Time) { f.h.Now = func() time.Time { return t } }

// The events read, and read again once the grace before a ping has run out.
func (f *fixture) consume() {
	f.h.ConsumeEvents()
	f.at(f.h.Now().Add(pingGrace + time.Second))
	f.h.ConsumeEvents()
}

func (f *fixture) waits() map[string]state.Wait {
	f.t.Helper()
	all, err := f.h.Waits.Load()
	if err != nil {
		f.t.Fatal(err)
	}
	return all
}

// A turn that ends waiting on Tim, or a question asked in the session: ❓, and one ping that
// can reach him alone.
func TestAStopOrAQuestionPingsTimOnce(t *testing.T) {
	for _, kind := range []string{event.Stop, event.Ask} {
		f, thread := startedSession(t)
		f.event(thread, kind, 0)
		f.consume()

		if f.pings(thread) != 1 {
			t.Fatalf("%s: pings = %d", kind, f.pings(thread))
		}
		am := f.api.Sent[len(f.api.Sent)-1].Msg.AllowedMentions
		if am == nil || len(am.Parse) != 0 || !reflect.DeepEqual(am.Users, []string{tim.ID}) || am.RepliedUser {
			t.Errorf("%s: allowed mentions = %+v", kind, am)
		}
		f.shows(thread, mark.Asking)
		if f.api.Channels[thread].Name != "❓ research" {
			t.Errorf("%s: thread = %q", kind, f.api.Channels[thread].Name)
		}
		if left := event.Read(f.cfg.EventsDir()); len(left) != 0 {
			t.Errorf("%s: events left: %v", kind, left)
		}
	}
}

// Until Tim sends the session something, it is not pinged again, however often it stops.
func TestNoSecondPingUntilTimHasSaidSomething(t *testing.T) {
	f, thread := startedSession(t)
	f.event(thread, event.Stop, 0)
	f.consume()
	f.event(thread, event.Ask, 0)
	f.event(thread, event.Stop, 0)
	f.consume()
	if f.pings(thread) != 1 {
		t.Fatalf("pings = %d", f.pings(thread))
	}

	// His follow-up reaches the session; the next time it waits, he is pinged again.
	f.h.Handle(f.mention(thread, 5, tim, "go with b", nil))
	f.shows(thread, mark.Running)
	f.event(thread, event.Stop, 0)
	f.consume()
	if f.pings(thread) != 2 {
		t.Errorf("pings = %d after his follow-up", f.pings(thread))
	}
}

// A session that said it is done is not waiting on anybody when its turn ends; one that
// handed Tim something to review is.
func TestDoneIsQuietAndReviewIsNot(t *testing.T) {
	f, thread := startedSession(t)
	f.h.Marks.Settled(thread, mark.Finished)
	f.event(thread, event.Stop, 0)
	f.consume()
	if f.pings(thread) != 0 {
		t.Error("a done session pinged")
	}
	f.shows(thread, mark.Finished)

	f2, review := startedSession(t)
	f2.h.Marks.Settled(review, mark.Asking)
	f2.event(review, event.Stop, 0)
	f2.consume()
	if f2.pings(review) != 1 {
		t.Error("a stop after a review did not ping")
	}
}

// A turn that ended with subagents or shells still running paused for them, not for Tim.
func TestAStopWithWorkInFlightIsNotAWait(t *testing.T) {
	f, thread := startedSession(t)
	f.event(thread, event.Stop, 2)
	f.consume()
	if f.pings(thread) != 0 {
		t.Error("pinged while work was still running")
	}
	f.shows(thread, mark.Running)
}

// An event from a thread mochi keeps no session for is dropped.
func TestAnEventForNoSessionIsDropped(t *testing.T) {
	f := newFixture(t)
	f.event("1300000000000000077", event.Stop, 0)
	f.consume()
	if len(f.api.Sent) != 0 || len(event.Read(f.cfg.EventsDir())) != 0 {
		t.Errorf("sent %d, events left %d", len(f.api.Sent), len(event.Read(f.cfg.EventsDir())))
	}
}

// A ping that does not land is tried again after 30 seconds, doubling to an hour, until it
// lands; one into a thread Discord no longer has is never tried again.
func TestAFailedPingBacksOffAndADeletedThreadIsLeft(t *testing.T) {
	f, thread := startedSession(t)
	base := f.api.Tries[thread]
	tries := func() int { return f.api.Tries[thread] - base }
	f.api.SendErr[thread] = errors.New("503 Service Unavailable")
	start := time.Date(2026, 10, 20, 15, 4, 0, 0, time.UTC)
	f.at(start)

	f.event(thread, event.Stop, 0)
	f.consume()
	for _, step := range []struct {
		after time.Duration
		want  int
	}{
		{10 * time.Second, 1},
		{30 * time.Second, 2},
		{60 * time.Second, 2},
		{90 * time.Second, 3},
	} {
		f.at(start.Add(step.after))
		f.consume()
		if tries() != step.want {
			t.Errorf("after %s: %d tries, want %d", step.after, tries(), step.want)
		}
	}
	for i := 0; i < 20; i++ {
		f.at(time.Unix(f.waits()[thread].RetryAt, 0))
		f.consume()
	}
	if b := f.waits()[thread].Backoff; b != 3600 {
		t.Errorf("backoff = %d", b)
	}
	delete(f.api.SendErr, thread)
	f.at(time.Unix(f.waits()[thread].RetryAt, 0))
	f.consume()
	if f.pings(thread) != 1 {
		t.Errorf("pings = %d once Discord answered", f.pings(thread))
	}

	g, gone := startedSession(t)
	gbase := g.api.Tries[gone]
	g.api.SendErr[gone] = chat.ErrUnknownChannel
	g.event(gone, event.Stop, 0)
	g.consume()
	g.h.Handle(g.mention(gone, 5, tim, "again", nil))
	g.event(gone, event.Stop, 0)
	g.at(time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC))
	g.h.ConsumeEvents()
	if n := g.api.Tries[gone] - gbase; n > 3 { // the ping, and the follow-up's own posts
		t.Errorf("a deleted thread was tried %d times", n)
	}
	if !g.waits()[gone].Dead {
		t.Error("the deleted thread was forgotten")
	}
}

// A follow-up queued behind the turn starts the moment it ends: the heard it brings inside the
// grace cancels the wait before anything shows.
func TestAHeardInsideTheGraceCancelsThePing(t *testing.T) {
	f, thread := startedSession(t)
	f.event(thread, event.Stop, 0)
	f.h.ConsumeEvents()
	f.at(f.h.Now().Add(pingGrace / 2))
	f.event(thread, event.Heard, 0)
	f.consume()
	f.at(f.h.Now().Add(time.Minute))
	f.h.ConsumeEvents()

	if f.pings(thread) != 0 {
		t.Errorf("pings = %d", f.pings(thread))
	}
	f.shows(thread, mark.Running)
	if w := f.waits()[thread]; w.Pending || w.Asked {
		t.Errorf("wait = %+v", w)
	}
}

// Nothing shows inside the grace, and the ping follows once it has run out.
func TestThePingWaitsOutTheGrace(t *testing.T) {
	f, thread := startedSession(t)
	f.event(thread, event.Ask, 0)
	f.h.ConsumeEvents()
	if f.pings(thread) != 0 {
		t.Error("pinged inside the grace")
	}
	f.shows(thread, mark.Running)

	f.at(f.h.Now().Add(pingGrace + time.Second))
	f.h.ConsumeEvents()
	if f.pings(thread) != 1 {
		t.Errorf("pings = %d after the grace", f.pings(thread))
	}
	f.shows(thread, mark.Asking)
}

// Whether Tim has been pinged outlives a restart.
func TestAPingOutlivesARestart(t *testing.T) {
	f, thread := startedSession(t)
	f.event(thread, event.Stop, 0)
	f.consume()

	restarted := &Handler{
		Cfg: f.h.Cfg, API: f.api, Herdr: f.herdr.run,
		Store: f.h.Store, Pending: f.h.Pending, Waits: f.h.Waits, Marks: f.h.Marks,
		Now: f.h.Now, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	restarted.SetBot(bot.ID)
	f.event(thread, event.Stop, 0)
	restarted.ConsumeEvents()
	if f.pings(thread) != 1 {
		t.Errorf("pinged again after a restart: %d", f.pings(thread))
	}
}

// A file that will not parse is said once, moved aside, and the watch carries on.
func TestACorruptWaitingFileIsMovedAsideOnce(t *testing.T) {
	f, thread := startedSession(t)
	var logged strings.Builder
	f.h.Log = slog.New(slog.NewTextHandler(&logged, nil))
	os.WriteFile(f.h.Waits.Path, []byte("{not json"), 0o600)

	f.event(thread, event.Stop, 0)
	f.consume()
	f.consume()

	if n := strings.Count(logged.String(), "was corrupt"); n != 1 {
		t.Errorf("said %d times", n)
	}
	if aside, _ := filepath.Glob(f.h.Waits.Path + ".corrupt-*"); len(aside) != 1 {
		t.Errorf("moved aside: %v", aside)
	}
	if f.pings(thread) != 1 {
		t.Errorf("pings = %d", f.pings(thread))
	}
}
