package listen

import (
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/chat"
	"github.com/timche/mac-mini/mochi/internal/herdr"
	"github.com/timche/mac-mini/mochi/internal/mark"
	"github.com/timche/mac-mini/mochi/internal/state"
)

// A session counts as waiting on Tim after this many polls in a row find it so, so a
// question answered in seconds pings nobody.
const blockedPolls = 2

// A ping that fails waits this long before the next try, doubling up to the most.
const (
	pingBackoff    = 30 * time.Second
	pingBackoffMax = time.Hour
)

// WatchBlocked runs every half minute: a session that has stopped on a question or a
// permission is one Tim has to answer in the session, and he is told once per episode, in
// its thread, with the one ping mochi ever sends. Its state is ❓ until it moves again.
//
// herdr is asked without the handler's lock, which a mention in progress holds for up to a
// minute; a session last seen gone is not asked again until its record changes, and herdr not
// answering ends the pass rather than reading every session as moved.
func (h *Handler) WatchBlocked() {
	h.mu.Lock()
	sessions, seen, ok := h.loadWaits()
	h.mu.Unlock()
	if !ok {
		return
	}

	const gone = "gone"
	statuses := map[string]string{}
	for thread, rec := range sessions {
		if b := seen[thread]; b.Gone != 0 && b.Gone == rec.Updated {
			continue
		}
		status, err := herdr.Status(h.Herdr, rec.Agent)
		if errors.Is(err, herdr.ErrGone) {
			statuses[thread] = gone
			continue
		}
		if err != nil {
			h.Log.Warn("herdr could not be asked which sessions wait on Tim, so this pass stops", "error", h.redact(err))
			break
		}
		statuses[thread] = status
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	before := map[string]state.Blocked{}
	for k, v := range seen {
		before[k] = v
	}
	for thread, status := range statuses {
		b := seen[thread]
		switch status {
		case gone:
			seen[thread] = state.Blocked{Gone: sessions[thread].Updated}
			continue
		case herdr.StatusBlocked:
		default:
			if b.Pinged {
				h.restore(thread, b)
			}
			if b.Dead {
				seen[thread] = state.Blocked{Dead: true}
			} else {
				delete(seen, thread)
			}
			continue
		}
		// Counted only as far as it decides anything, so a long wait is one write, not one a poll.
		if b.Polls < blockedPolls {
			b.Polls++
		}
		if b.Polls >= blockedPolls && !b.Pinged && !b.Dead && h.Now().Unix() >= b.RetryAt {
			err := h.ping(thread)
			switch {
			case err == nil:
				b.Pinged, b.Backoff, b.RetryAt = true, 0, 0
				b.Prev, _ = h.Marks.Session(thread)
				h.Marks.Settled(thread, mark.Asking)
				_, b.Seq = h.Marks.Session(thread)
			case chat.Gone(err):
				b.Dead = true
				h.Log.Warn("the thread of a session waiting on Tim is gone, so it is pinged no more", "thread", thread)
			default:
				wait := time.Duration(b.Backoff) * time.Second * 2
				wait = max(pingBackoff, min(wait, pingBackoffMax))
				b.Backoff, b.RetryAt = int64(wait/time.Second), h.Now().Add(wait).Unix()
			}
		}
		seen[thread] = b
	}
	for thread := range seen {
		if _, ok := sessions[thread]; !ok {
			delete(seen, thread)
		}
	}

	if !reflect.DeepEqual(seen, before) {
		if err := h.Blocked.Save(seen); err != nil {
			h.Log.Error("what is known of waiting sessions could not be kept", "error", err)
		}
	}
}

func (h *Handler) loadWaits() (map[string]state.Session, map[string]state.Blocked, bool) {
	sessions, err := h.Store.Load()
	if err != nil {
		h.Log.Error("the sessions could not be read to see which wait on Tim", "error", err)
		return nil, nil, false
	}
	seen, err := h.Blocked.Load()
	switch {
	case errors.Is(err, state.ErrCorrupt):
		h.Log.Error("what is known of waiting sessions was corrupt", "error", err)
	case err != nil:
		h.Log.Error("what is known of waiting sessions could not be read", "error", err)
		return nil, nil, false
	}
	return sessions, seen, true
}

// The wait is over: the state from before the ping comes back, unless the session or a
// follow-up has set one since, which is newer than anything the ping knew.
func (h *Handler) restore(thread string, b state.Blocked) {
	if _, seq := h.Marks.Session(thread); seq != b.Seq {
		return
	}
	prev := b.Prev
	if prev == "" || prev == mark.Asking {
		prev = mark.Running
	}
	h.Marks.Settled(thread, prev)
}

// Tim alone may be pinged, and only here.
func (h *Handler) ping(thread string) error {
	msg := &discordgo.MessageSend{
		Content: fmt.Sprintf("<@%s> the session is waiting on you — pick an option in the session.", h.Cfg.User),
		AllowedMentions: &discordgo.MessageAllowedMentions{
			Parse: []discordgo.AllowedMentionType{},
			Users: []string{h.Cfg.User},
		},
	}
	_, err := h.API.Send(thread, msg)
	if err != nil {
		h.Log.Error("Tim could not be told a session waits on him", "thread", thread, "error", h.redact(err))
	}
	return err
}
