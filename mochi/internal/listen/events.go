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

	if !reflect.DeepEqual(waits, before) {
		h.saveWaits(waits)
	}
}

func (h *Handler) waiting(ev event.Event, waits map[string]state.Wait) {
	if ev.Kind == event.Stop && ev.Background > 0 {
		return
	}
	if _, ok, _ := h.Store.Get(ev.Thread); !ok {
		return
	}
	if shown, _ := h.Marks.Session(ev.Thread); shown == mark.Finished {
		return
	}
	w := waits[ev.Thread]
	if w.Pinged || w.Pending || w.Dead {
		return
	}
	h.Marks.Settled(ev.Thread, mark.Asking)
	w.Pending = true
	waits[ev.Thread] = w
}

func (h *Handler) pingWaiting(waits map[string]state.Wait) {
	for thread, w := range waits {
		if !w.Pending || w.Dead || h.Now().Unix() < w.RetryAt {
			continue
		}
		err := h.ping(thread)
		switch {
		case err == nil:
			w = state.Wait{Pinged: true}
		case chat.Gone(err):
			w = state.Wait{Dead: true}
			h.Log.Warn("the thread of a session waiting on Tim is gone, so it is pinged no more", "thread", thread)
		default:
			wait := max(pingBackoff, min(time.Duration(w.Backoff)*time.Second*2, pingBackoffMax))
			w.Backoff, w.RetryAt = int64(wait/time.Second), h.Now().Add(wait).Unix()
		}
		waits[thread] = w
	}
}

// Heard is Tim's input reaching the session: the next time it waits on him he is pinged
// again. A thread Discord has lost stays lost.
func (h *Handler) heard(thread string) {
	waits, ok := h.loadWaits()
	if !ok {
		return
	}
	w, known := waits[thread]
	if !known || (w.Dead && w == (state.Wait{Dead: true})) {
		return
	}
	if w.Dead {
		waits[thread] = state.Wait{Dead: true}
	} else {
		delete(waits, thread)
	}
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
