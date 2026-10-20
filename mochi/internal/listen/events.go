package listen

import (
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/timche/mac-mini/mochi/internal/chat"
	"github.com/timche/mac-mini/mochi/internal/event"
	"github.com/timche/mac-mini/mochi/internal/herdr"
	"github.com/timche/mac-mini/mochi/internal/mark"
	"github.com/timche/mac-mini/mochi/internal/state"
)

// A ping that fails waits this long before the next try, doubling up to the most.
const (
	pingBackoff    = 30 * time.Second
	pingBackoffMax = time.Hour

	pingGrace = 3 * time.Second

	// Long enough for Tim to see the done and say more in the session, which keeps it open.
	closeAfter = time.Minute
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

	var watches map[string]state.Watch
	for _, ev := range events {
		if ev.Kind != event.PR {
			h.waiting(ev, waits)
			event.Done(ev)
			continue
		}
		if watches == nil {
			var err error
			if watches, err = h.Watches.Load(); err != nil {
				// Left in the spool, for the next read.
				h.Log.Error("the watched pull requests could not be read", "error", err)
				watches = nil
				continue
			}
		}
		h.watch(ev, watches)
		event.Done(ev)
	}
	if watches != nil {
		if err := h.Watches.Save(watches); err != nil {
			h.Log.Error("the watched pull requests could not be kept", "error", err)
		}
	}
	h.pingWaiting(waits)
	h.closeDone(waits)
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
	switch ev.Kind {
	case event.Heard:
		h.answered(ev.Thread, waits, ev.Nanos, ev.Relayed)
		return
	case event.Close:
		h.closing(ev, rec, waits)
		return
	}
	// A report from before Tim last reached the session, or before it last started, is stale.
	if ev.Nanos <= w.Heard || ev.Nanos <= rec.Updated*int64(time.Second) {
		return
	}
	if ev.Kind == event.Stop && ev.Background > 0 {
		return
	}
	if shown, _ := h.Marks.Session(ev.Thread); settled(shown) {
		return
	}
	if w.Pinged || w.Pending || w.Dead {
		return
	}
	w.Pending, w.Due = true, ev.Nanos+int64(pingGrace)
	waits[ev.Thread] = w
}

// Tim reached the session, in it or through a follow-up: the wait is over, the next one pings
// again, and a close that was coming is not. The ❓ the ping set goes back to ⏳ unless
// something was set since.
func (h *Handler) answered(thread string, waits map[string]state.Wait, at int64, discord bool) {
	w := waits[thread]
	if w.Asked {
		if _, seq := h.Marks.Session(thread); seq == w.Seq {
			h.Marks.Settled(thread, mark.Running)
		}
	}
	if w.CloseAt != 0 {
		h.Log.Info("Tim reached a session before its tab closed, so it stays open", "thread", thread)
	}
	if at < w.Heard {
		discord = w.Discord
	}
	waits[thread] = state.Wait{Dead: w.Dead, Heard: max(at, w.Heard), Discord: discord}
}

// A session that finished and cleaned up has its tab closed a minute later, but only when
// Tim's last word to it came from Discord: one he last typed in is one he is sitting at.
func (h *Handler) closing(ev event.Event, rec state.Session, waits map[string]state.Wait) {
	w := waits[ev.Thread]
	if !w.Discord || ev.Nanos <= w.Heard {
		h.Log.Info("a session finished, and Tim last reached it in the session, so its tab stays open", "thread", ev.Thread, "workspace", rec.Workspace, "tab", rec.Tab)
		return
	}
	w.CloseAt = ev.Nanos + int64(closeAfter)
	waits[ev.Thread] = w
}

func (h *Handler) closeDone(waits map[string]state.Wait) {
	for thread, w := range waits {
		if w.CloseAt == 0 || h.Now().UnixNano() < w.CloseAt {
			continue
		}
		w.CloseAt = 0
		waits[thread] = w
		rec, ok, _ := h.Store.Get(thread)
		if !ok {
			continue
		}
		if err := herdr.CloseTab(h.Herdr, rec.Agent, rec.Tab); err != nil {
			h.Log.Warn("the tab of a finished session was not closed", "workspace", rec.Workspace, "tab", rec.Tab, "error", h.redact(err))
			continue
		}
		h.Log.Info("closed the tab of a session that finished from Discord", "workspace", rec.Workspace, "tab", rec.Tab, "thread", thread)
	}
}

func (h *Handler) pingWaiting(waits map[string]state.Wait) {
	for thread, w := range waits {
		if !w.Pending || w.Dead || h.Now().UnixNano() < w.Due || h.Now().Unix() < w.RetryAt {
			continue
		}
		if !w.Asked {
			// The session may have said it is done while the grace ran.
			if shown, _ := h.Marks.Session(thread); settled(shown) {
				waits[thread] = w.Lasting()
				continue
			}
			h.Marks.Settled(thread, mark.Asking)
			_, w.Seq = h.Marks.Session(thread)
			w.Asked = true
		}
		err := h.ping(thread)
		switch {
		case err == nil:
			seq := w.Seq
			w = w.Lasting()
			w.Pinged, w.Asked, w.Seq = true, true, seq
		case chat.Gone(err):
			w = w.Lasting()
			w.Dead = true
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
	h.answered(thread, waits, h.Now().UnixNano(), true)
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
	_, err := h.API.Send(thread, chat.Pinging(h.Cfg.User, fmt.Sprintf("The session is waiting on <@%s>.", h.Cfg.User)))
	if err != nil {
		h.Log.Error("Tim could not be told a session waits on him", "thread", thread, "error", h.redact(err))
	}
	return err
}

// A session done, or stopped by Tim, is waiting on nobody when its turn ends.
func settled(shown string) bool { return shown == mark.Finished || shown == mark.Stopped }
