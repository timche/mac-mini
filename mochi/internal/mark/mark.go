// Package mark is how a mochi session's state shows in Discord: one reaction of mochi's on
// each message it is about, and the same state at the front of the session thread's name.
// The listener and `mochi post --finished` both change it, from different processes, so it
// lives in one file taken under a lock.
package mark

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/timche/mac-mini/mochi/internal/chat"
	"github.com/timche/mac-mini/mochi/internal/state"
)

// 👀 picked up, ⏳ a session working, ❓ waiting on Tim — a question to answer or a result to
// review — ✅ done with nothing left for him, ❌ failed.
const (
	Seen     = "👀"
	Running  = "⏳"
	Asking   = "❓"
	Finished = "✅"
	Failed   = "❌"
)

// Discord lets a thread be renamed twice in ten minutes.
const (
	renameWindow   = 10 * time.Minute
	renamesAllowed = 2
	maxName        = 100
)

type Marker struct {
	API  chat.API
	Path string
	Now  func() time.Time
	Log  *slog.Logger
}

// Group is several sessions from one mention. Its reaction is ⏳ while any of them works, ❓
// when none does and one waits on Tim, and ✅ once all are done.
type Group struct {
	Mention state.Ref         `json:"mention"`
	Threads []string          `json:"threads"`
	State   map[string]string `json:"state"`
}

// Name is a session thread's name: the title, the state wanted at its front, what Discord
// has now, and the renames that count against the limit. Only the latest wanted name is ever
// sent, so states that came and went while a rename had to wait are never shown.
type Name struct {
	Title   string  `json:"title"`
	Want    string  `json:"want"`
	Have    string  `json:"have"`
	Renames []int64 `json:"renames,omitempty"`
	RetryAt int64   `json:"retry_at,omitempty"`
	Skip    bool    `json:"skip,omitempty"`
}

type marks struct {
	Shown   map[string]string      `json:"shown"`
	Running map[string][]state.Ref `json:"running"`
	Groups  map[string]*Group      `json:"groups"`
	Names   map[string]*Name       `json:"names"`
}

func key(r state.Ref) string { return r.Channel + "/" + r.Message }

// The emoji at the front of a session thread's name, and the title after it.
func ThreadName(emoji, title string) string {
	name := emoji + " " + title
	if r := []rune(name); len(r) > maxName {
		name = string(r[:maxName-1]) + "…"
	}
	return name
}

// The title in a thread name mochi gave, with the state at its front, or the older "mochi: "
// prefix, taken off.
func Title(name string) string {
	for _, prefix := range []string{Running + " ", Finished + " ", Failed + " ", Asking + " ", "mochi: "} {
		if rest, ok := strings.CutPrefix(name, prefix); ok {
			return rest
		}
	}
	return name
}

// Set shows one reaction on a message, taking back whichever of mochi's it showed before.
func (k Marker) Set(ref state.Ref, emoji string) {
	k.update(func(m *marks) { k.react(m, ref, emoji) })
}

// A thread mochi just made, under the name it gave it.
func (k Marker) Named(thread, title, name string) {
	k.update(func(m *marks) {
		m.Names[thread] = &Name{Title: title, Want: name, Have: name}
	})
}

// A thread mochi did not make, which a session is in all the same: its name is tried once,
// and left alone if Discord says it is not mochi's.
func (k Marker) Adopted(thread, title, have string) {
	k.update(func(m *marks) {
		if m.Names[thread] == nil {
			m.Names[thread] = &Name{Title: title, Have: have}
		}
	})
}

// A session is working: each message it is about shows ⏳ until the session says it waits on
// Tim or is done, and so do its thread's name and a group it belongs to.
func (k Marker) Started(thread string, refs ...state.Ref) {
	k.update(func(m *marks) {
		for _, ref := range refs {
			if ref.Message == "" {
				continue
			}
			k.react(m, ref, Running)
			if !contains(m.Running[thread], ref) {
				m.Running[thread] = append(m.Running[thread], ref)
			}
		}
		k.want(m, thread, Running)
		k.groups(m, thread, Running)
	})
}

// A session that could not start: its messages and its thread show ❌.
func (k Marker) Failed(thread string, refs ...state.Ref) {
	k.update(func(m *marks) {
		for _, ref := range refs {
			k.react(m, ref, Failed)
		}
		delete(m.Running, thread)
		k.want(m, thread, Failed)
	})
}

// The session posted that it waits on Tim (❓) or is done (✅): what it was working on shows
// that. Done is the end of those messages, and a later follow-up starts its own; a review
// keeps them, for the done that may follow it.
func (k Marker) Settled(thread, emoji string) {
	k.update(func(m *marks) {
		for _, ref := range m.Running[thread] {
			k.react(m, ref, emoji)
		}
		if emoji == Finished {
			delete(m.Running, thread)
		}
		k.want(m, thread, emoji)
		k.groups(m, thread, emoji)
	})
}

// Several sessions from one mention, all started: the mention shows ⏳ while any works.
func (k Marker) Group(mention state.Ref, threads []string) {
	k.update(func(m *marks) {
		g := &Group{Mention: mention, Threads: threads, State: map[string]string{}}
		for _, t := range threads {
			g.State[t] = Running
		}
		m.Groups[key(mention)] = g
		k.react(m, mention, Running)
	})
}

func (k Marker) groups(m *marks, thread, emoji string) {
	for _, g := range m.Groups {
		if _, in := g.State[thread]; !in {
			continue
		}
		g.State[thread] = emoji
		shown := Finished
		for _, t := range g.Threads {
			switch g.State[t] {
			case Running:
				shown = Running
			case Asking:
				if shown != Running {
					shown = Asking
				}
			}
		}
		k.react(m, g.Mention, shown)
	}
}

// Renames whose wait is over, with whatever state is wanted now.
func (k Marker) Retry() {
	k.update(func(m *marks) {
		now := k.Now().Unix()
		for thread, n := range m.Names {
			if n.RetryAt != 0 && now >= n.RetryAt {
				n.RetryAt = 0
				k.rename(thread, n)
			}
		}
	})
}

func (k Marker) react(m *marks, ref state.Ref, emoji string) {
	was := m.Shown[key(ref)]
	if was == emoji {
		return
	}
	if was != "" {
		if err := k.API.Unreact(ref.Channel, ref.Message, was); err != nil {
			k.Log.Warn("a reaction could not be taken back", "emoji", was, "error", err)
		}
	}
	if err := k.API.React(ref.Channel, ref.Message, emoji); err != nil {
		k.Log.Warn("could not react", "emoji", emoji, "error", err)
		return
	}
	m.Shown[key(ref)] = emoji
}

func (k Marker) want(m *marks, thread, emoji string) {
	n := m.Names[thread]
	if n == nil || n.Title == "" {
		return
	}
	n.Want = ThreadName(emoji, n.Title)
	if n.RetryAt == 0 {
		k.rename(thread, n)
	}
}

// One rename, or a single later one when the limit is used up or Discord says to wait.
func (k Marker) rename(thread string, n *Name) {
	if n.Skip || n.Want == "" || n.Want == n.Have {
		return
	}
	now := k.Now()
	recent := n.Renames[:0]
	for _, at := range n.Renames {
		if now.Sub(time.Unix(at, 0)) < renameWindow {
			recent = append(recent, at)
		}
	}
	n.Renames = recent
	if len(recent) >= renamesAllowed {
		n.RetryAt = time.Unix(recent[0], 0).Add(renameWindow).Unix()
		return
	}

	err := k.API.RenameThread(thread, n.Want)
	switch {
	case err == nil:
		n.Have = n.Want
		n.Renames = append(n.Renames, now.Unix())
	case errors.Is(err, chat.ErrRateLimited):
		n.RetryAt = now.Add(renameWindow).Unix()
	case errors.Is(err, chat.ErrForbidden):
		n.Skip = true
		k.Log.Info("the thread is not mochi's to rename, so its name is left as it is", "thread", thread)
	default:
		k.Log.Warn("the thread could not be renamed", "thread", thread, "error", err)
	}
}

// Under an exclusive lock on a file beside the state, so the listener and a `mochi post` in
// a session never write over each other.
func (k Marker) update(fn func(*marks)) {
	// A marker nobody gave a file shows nothing, rather than writing into the working
	// directory.
	if k.Path == "" || k.API == nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(k.Path), 0o700); err != nil {
		k.Log.Error("the marks could not be kept", "error", err)
		return
	}
	lock, err := os.OpenFile(k.Path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		k.Log.Error("the marks could not be locked", "error", err)
		return
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		k.Log.Error("the marks could not be locked", "error", err)
		return
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	m := &marks{}
	if body, err := os.ReadFile(k.Path); err == nil {
		if err := json.Unmarshal(body, m); err != nil {
			k.Log.Error("the marks file is not JSON, so it starts again", "error", err)
			m = &marks{}
		}
	}
	if m.Shown == nil {
		m.Shown = map[string]string{}
	}
	if m.Running == nil {
		m.Running = map[string][]state.Ref{}
	}
	if m.Groups == nil {
		m.Groups = map[string]*Group{}
	}
	if m.Names == nil {
		m.Names = map[string]*Name{}
	}

	fn(m)

	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return
	}
	tmp := k.Path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err == nil {
		os.Rename(tmp, k.Path)
	}
}

func contains(refs []state.Ref, ref state.Ref) bool {
	for _, r := range refs {
		if r == ref {
			return true
		}
	}
	return false
}
