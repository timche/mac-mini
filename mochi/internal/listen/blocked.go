package listen

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/herdr"
	"github.com/timche/mac-mini/mochi/internal/mark"
	"github.com/timche/mac-mini/mochi/internal/state"
)

// A session counts as waiting on Tim after this many polls in a row find it so, so a
// question answered in seconds pings nobody.
const blockedPolls = 2

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
				h.Marks.Settled(thread, mark.Running)
			}
			delete(seen, thread)
			continue
		}
		// Counted only as far as it decides anything, so a long wait is one write, not one a poll.
		if b.Polls < blockedPolls {
			b.Polls++
		}
		if b.Polls >= blockedPolls && !b.Pinged {
			b.Pinged = h.ping(thread)
			if b.Pinged {
				h.Marks.Settled(thread, mark.Asking)
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

// Tim alone may be pinged, and only here.
func (h *Handler) ping(thread string) bool {
	msg := &discordgo.MessageSend{
		Content: fmt.Sprintf("<@%s> the session is waiting on you — pick an option in the session.", h.Cfg.User),
		AllowedMentions: &discordgo.MessageAllowedMentions{
			Parse: []discordgo.AllowedMentionType{},
			Users: []string{h.Cfg.User},
		},
	}
	if _, err := h.API.Send(thread, msg); err != nil {
		h.Log.Error("Tim could not be told a session waits on him", "thread", thread, "error", h.redact(err))
		return false
	}
	return true
}
