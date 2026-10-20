package listen

import (
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/timche/mac-mini/mochi/internal/chat"
	"github.com/timche/mac-mini/mochi/internal/event"
	"github.com/timche/mac-mini/mochi/internal/mark"
	"github.com/timche/mac-mini/mochi/internal/state"
)

// A ping that fails waits this long before the next try, doubling up to the most.
const (
	pingBackoff    = 30 * time.Second
	pingBackoffMax = time.Hour

	pingGrace = 3 * time.Second
)

// ConsumeEvents runs every second: a session whose turn ended, or that asked a question, is
// waiting on Tim in the session itself, so it shows ❓ and he is pinged in its thread, the
// one ping mochi ever sends. Once only until he next sends the session something; not at
// all for a session that said it is done, or one that only paused for work still running.
func (h *Handler) ConsumeEvents() {
	events := event.Read(h.Cfg.EventsDir())

	h.mu.Lock()
	defer h.mu.Unlock()
	waits, ok := h.loadWaits()
	if !ok {
		return
	}
	before := map[string]state.Wait{}
	for k, v := range waits {
		before[k] = v
	}

	for _, ev := range events {
		h.waiting(ev, waits)
		event.Done(ev)
	}
	h.pingWaiting(waits)
	if sessions, err := h.Store.Load(); err == nil {
		for thread := range waits {
			if _, ok := sessions[thread]; !ok {
				delete(waits, thread)
			}
		}
	}

	if !reflect.DeepEqual(waits, before) {
		h.saveWaits(waits)
	}
}

func (h *Handler) waiting(ev event.Event, waits map[string]state.Wait) {
	rec, ok, _ := h.Store.Get(ev.Thread)
	if !ok {
		return
	}
	w := waits[ev.Thread]
	if ev.Kind == event.Heard {
		h.answered(ev.Thread, waits, ev.Nanos)
		return
	}
	// A report from before Tim last reached the session, or before it last started, is stale.
	if ev.Nanos <= w.Heard || ev.Nanos <= rec.Updated*int64(time.Second) {
		return
	}
	if ev.Kind == event.Stop && ev.Background > 0 {
		return
	}
	if shown, _ := h.Marks.Session(ev.Thread); shown == mark.Finished {
		return
	}
	if w.Pinged || w.Pending || w.Dead {
		return
	}
	w.Pending, w.Due = true, ev.Nanos+int64(pingGrace)
	waits[ev.Thread] = w
}

// Tim reached the session, in it or through a follow-up: the wait is over, and the next one
// pings again. The ❓ the ping set goes back to ⏳ unless something was set since.
func (h *Handler) answered(thread string, waits map[string]state.Wait, at int64) {
	w := waits[thread]
	if w.Asked {
		if _, seq := h.Marks.Session(thread); seq == w.Seq {
			h.Marks.Settled(thread, mark.Running)
		}
	}
	waits[thread] = state.Wait{Dead: w.Dead, Heard: max(at, w.Heard)}
}

func (h *Handler) pingWaiting(waits map[string]state.Wait) {
	for thread, w := range waits {
		if !w.Pending || w.Dead || h.Now().UnixNano() < w.Due || h.Now().Unix() < w.RetryAt {
			continue
		}
		if !w.Asked {
			// The session may have said it is done while the grace ran.
			if shown, _ := h.Marks.Session(thread); shown == mark.Finished {
				waits[thread] = state.Wait{Heard: w.Heard}
				continue
			}
			h.Marks.Settled(thread, mark.Asking)
			_, w.Seq = h.Marks.Session(thread)
			w.Asked = true
		}
		err := h.ping(thread)
		switch {
		case err == nil:
			w = state.Wait{Pinged: true, Asked: true, Seq: w.Seq, Heard: w.Heard}
		case chat.Gone(err):
			w = state.Wait{Dead: true, Heard: w.Heard}
			h.Log.Warn("the thread of a session waiting on Tim is gone, so it is pinged no more", "thread", thread)
		default:
			wait := max(pingBackoff, min(time.Duration(w.Backoff)*time.Second*2, pingBackoffMax))
			w.Backoff, w.RetryAt = int64(wait/time.Second), h.Now().Add(wait).Unix()
		}
		waits[thread] = w
	}
}

// heard is a follow-up of Tim's delivered to the session from Discord.
func (h *Handler) heard(thread string) {
	waits, ok := h.loadWaits()
	if !ok {
		return
	}
	h.answered(thread, waits, h.Now().UnixNano())
	h.saveWaits(waits)
}

func (h *Handler) loadWaits() (map[string]state.Wait, bool) {
	waits, err := h.Waits.Load()
	switch {
	case errors.Is(err, state.ErrCorrupt):
		h.Log.Error("what is known of waiting sessions was corrupt", "error", err)
	case err != nil:
		h.Log.Error("what is known of waiting sessions could not be read", "error", err)
		return nil, false
	}
	return waits, true
}

func (h *Handler) saveWaits(waits map[string]state.Wait) {
	if err := h.Waits.Save(waits); err != nil {
		h.Log.Error("what is known of waiting sessions could not be kept", "error", err)
	}
}

// Tim alone may be pinged, and only here.
func (h *Handler) ping(thread string) error {
	_, err := h.API.Send(thread, chat.Pinging(h.Cfg.User, fmt.Sprintf("<@%s> the session is waiting on you.", h.Cfg.User)))
	if err != nil {
		h.Log.Error("Tim could not be told a session waits on him", "thread", thread, "error", h.redact(err))
	}
	return err
}
