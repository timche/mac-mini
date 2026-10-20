// Package mark is how a mochi session's state shows in Discord: one reaction of mochi's on
// each message it is about, and the same state at the front of the session thread's name.
// The listener and `mochi post --finished` both change it, from different processes, so it
// lives in one file taken under a lock.
package mark

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/timche/mac-mini/mochi/internal/chat"
	"github.com/timche/mac-mini/mochi/internal/split"
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

// A forum post that has been done this long is archived, and one whose archiving failed is
// tried again after the retry. Never deleted.
const (
	archiveAfter = 3 * 24 * time.Hour
	archiveRetry = time.Hour
)

// The tags a forum post carries, found on the forum by name: its state, and the kind of work.
var (
	stateTags = map[string]string{Running: "Running", Asking: "Waiting on you", Finished: "Done", Failed: "Failed"}
	typeTags  = map[string]string{split.Feature: "Feature", split.Bug: "Bug", split.Improvement: "Improvement", split.Research: "Research"}
)

// PostTags are the names of the tags a post in this state, of this type, carries.
func PostTags(emoji, typ string) []string {
	var tags []string
	if t := stateTags[emoji]; t != "" {
		tags = append(tags, t)
	}
	if t := typeTags[typ]; t != "" {
		tags = append(tags, t)
	}
	return tags
}

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

	// A forum post's: the forum its tags are found on, its type, the tags wanted and the ones
	// it has, by name, sent in the same edit as the name; NoTags once Discord has refused
	// them, after which the name goes alone. ArchiveAt is when a done post is archived.
	Forum     string   `json:"forum,omitempty"`
	Type      string   `json:"type,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	HaveTags  []string `json:"have_tags,omitempty"`
	NoTags    bool     `json:"no_tags,omitempty"`
	ArchiveAt int64    `json:"archive_at,omitempty"`
	Archived  bool     `json:"archived,omitempty"`
}

func (n *Name) pending() bool {
	return n.Want != n.Have || (n.Forum != "" && !n.NoTags && !slices.Equal(n.Tags, n.HaveTags))
}

type marks struct {
	Shown   map[string]string      `json:"shown"`
	Running map[string][]state.Ref `json:"running"`
	Groups  map[string]*Group      `json:"groups"`
	Names   map[string]*Name       `json:"names"`

	// Every message a session's state was shown on, by thread, so forgetting the session
	// forgets them too; and when each reaction was last set, for the ones no session holds.
	Threads map[string][]state.Ref `json:"threads"`
	At      map[string]int64       `json:"at"`

	// Each session's state, and how many times it has been set, so a change made by somebody
	// else since can be told apart from one's own.
	State map[string]string `json:"state"`
	Seq   map[string]int    `json:"seq"`
}

func (m *marks) set(thread, emoji string) {
	m.State[thread] = emoji
	m.Seq[thread]++
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

// Clear takes mochi's reaction off a message, leaving none: a question that expired is not
// waiting on anybody.
func (k Marker) Clear(ref state.Ref) {
	k.update(func(m *marks) {
		was := m.Shown[key(ref)]
		if was == "" {
			return
		}
		if err := k.API.Unreact(ref.Channel, ref.Message, was); err != nil {
			k.Log.Warn("a reaction could not be taken back", "emoji", was, "error", err)
			return
		}
		delete(m.Shown, key(ref))
	})
}

// A thread mochi just made, under the name it gave it. Only these carry the state in their
// name: a thread somebody else made keeps the title they gave it, so it is never named here.
func (k Marker) Named(thread, title, name string) {
	k.update(func(m *marks) {
		m.Names[thread] = &Name{Title: title, Want: name, Have: name}
	})
}

// A forum post mochi just made for a session, under the name and with the tags it gave it.
func (k Marker) Posted(thread, title, name, forum, typ string) {
	k.update(func(m *marks) {
		tags := PostTags(Running, typ)
		m.Names[thread] = &Name{Title: title, Want: name, Have: name, Forum: forum, Type: typ, Tags: tags, HaveTags: tags}
	})
}

// ErrNoType is a session whose thread is not a forum post mochi made, which has no type tag.
var ErrNoType = errors.New("this session's thread is not a forum post mochi made, so it has no type to change")

// Typed refiles a forum post under another kind of work, in the same edit as its name.
func (k Marker) Typed(thread, typ string) error {
	if !split.IsType(typ) {
		return fmt.Errorf("%s is not a type: feature, bug, improvement or research", typ)
	}
	err := ErrNoType
	k.update(func(m *marks) {
		n := m.Names[thread]
		if n == nil || n.Forum == "" {
			return
		}
		err = nil
		n.Type = typ
		n.Tags = PostTags(m.State[thread], typ)
		if n.RetryAt == 0 {
			k.rename(thread, n)
		}
	})
	return err
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
			if !contains(m.Threads[thread], ref) {
				m.Threads[thread] = append(m.Threads[thread], ref)
			}
		}
		k.want(m, thread, Running)
		k.groups(m, thread, Running)
		m.set(thread, Running)
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
		m.set(thread, Failed)
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
		m.set(thread, emoji)
	})
}

// Session is a session's state and how many times it has been set: what was set last, and
// whether anything has been set since a given time.
func (k Marker) Session(thread string) (string, int) {
	var shown string
	var seq int
	k.update(func(m *marks) { shown, seq = m.State[thread], m.Seq[thread] })
	return shown, seq
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

// Renames whose wait is over, with whatever state is wanted now, and done posts whose time to
// be archived has come.
func (k Marker) Retry() {
	k.update(func(m *marks) {
		now := k.Now().Unix()
		for thread, n := range m.Names {
			if n.RetryAt != 0 && now >= n.RetryAt {
				n.RetryAt = 0
				k.rename(thread, n)
			}
			if n.ArchiveAt != 0 && !n.Archived && now >= n.ArchiveAt {
				k.archive(thread, n)
			}
		}
	})
}

func (k Marker) archive(thread string, n *Name) {
	if err := k.API.Archive(thread); err != nil {
		n.ArchiveAt = k.Now().Add(archiveRetry).Unix()
		k.Log.Warn("a done post could not be archived", "thread", thread, "error", err)
		return
	}
	n.Archived = true
	k.Log.Info("archived a post done for three days", "thread", thread)
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
	m.At[key(ref)] = k.Now().Unix()
}

// Prune forgets the sessions whose record is gone, with the messages they were shown on, and
// any other reaction nobody has touched for keep. The reactions stay on Discord; only
// mochi's memory of them goes.
func (k Marker) Prune(alive func(thread string) bool, keep time.Duration) {
	k.update(func(m *marks) {
		forget := func(ref state.Ref) {
			delete(m.Shown, key(ref))
			delete(m.At, key(ref))
		}
		var gone []string
		for _, threads := range []map[string][]state.Ref{m.Running, m.Threads} {
			for t := range threads {
				if !alive(t) {
					gone = append(gone, t)
				}
			}
		}
		for t := range m.Names {
			if !alive(t) {
				gone = append(gone, t)
			}
		}
		for _, t := range gone {
			for _, ref := range m.Threads[t] {
				forget(ref)
			}
			delete(m.Threads, t)
			delete(m.Running, t)
			delete(m.Names, t)
			delete(m.State, t)
			delete(m.Seq, t)
		}

		for gk, g := range m.Groups {
			var still []string
			for _, t := range g.Threads {
				if alive(t) {
					still = append(still, t)
				} else {
					delete(g.State, t)
				}
			}
			g.Threads = still
			if len(still) == 0 {
				forget(g.Mention)
				delete(m.Groups, gk)
			}
		}

		held := map[string]bool{}
		for _, refs := range m.Threads {
			for _, ref := range refs {
				held[key(ref)] = true
			}
		}
		for _, g := range m.Groups {
			held[key(g.Mention)] = true
		}
		now := k.Now().Unix()
		for k2 := range m.Shown {
			if !held[k2] && now-m.At[k2] > int64(keep/time.Second) {
				delete(m.Shown, k2)
				delete(m.At, k2)
			}
		}
	})
}

func (k Marker) want(m *marks, thread, emoji string) {
	n := m.Names[thread]
	if n == nil || n.Title == "" {
		return
	}
	n.Want = ThreadName(emoji, n.Title)
	if n.Forum != "" {
		n.Tags = PostTags(emoji, n.Type)
		// A post somebody writes in is out of the archive again, which is the only way a
		// post's state changes after it is done.
		switch {
		case emoji != Finished:
			n.ArchiveAt, n.Archived = 0, false
		case n.ArchiveAt == 0:
			n.ArchiveAt = k.Now().Add(archiveAfter).Unix()
		}
	}
	if n.RetryAt == 0 {
		k.rename(thread, n)
	}
}

// One rename, or a single later one when the limit is used up or Discord says to wait. A
// forum post's name and tags go in one edit, which counts as one rename.
func (k Marker) rename(thread string, n *Name) {
	if n.Skip || n.Want == "" || !n.pending() {
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

	tagged := n.Forum != "" && !n.NoTags
	var err error
	if tagged {
		var ids []string
		if ids, err = chat.TagIDs(k.API, n.Forum, n.Tags); err != nil {
			n.RetryAt = now.Add(time.Minute).Unix()
			k.Log.Warn("the forum's tags could not be read, so the post is edited later", "thread", thread, "error", err)
			return
		}
		err = k.API.EditPost(thread, n.Want, ids)
	} else {
		err = k.API.RenameThread(thread, n.Want)
	}
	switch {
	case err == nil:
		n.Have = n.Want
		if tagged {
			n.HaveTags = n.Tags
		}
		n.Renames = append(n.Renames, now.Unix())
	case errors.Is(err, chat.ErrRateLimited):
		n.RetryAt = now.Add(renameWindow).Unix()
	case errors.Is(err, chat.ErrForbidden) && tagged:
		// Tags may need a permission a name does not; the name goes on alone.
		n.NoTags = true
		k.Log.Warn("the post's tags could not be set, so only its name follows its state", "thread", thread)
		k.rename(thread, n)
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
	if m.Threads == nil {
		m.Threads = map[string][]state.Ref{}
	}
	if m.At == nil {
		m.At = map[string]int64{}
	}
	if m.State == nil {
		m.State = map[string]string{}
	}
	if m.Seq == nil {
		m.Seq = map[string]int{}
	}
	before, _ := json.MarshalIndent(m, "", "  ")

	fn(m)

	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		k.Log.Error("the marks could not be written", "error", err)
		return
	}
	if bytes.Equal(body, before) {
		return
	}
	tmp := k.Path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		k.Log.Error("the marks could not be written", "error", err)
		return
	}
	if err := os.Rename(tmp, k.Path); err != nil {
		k.Log.Error("the marks could not be put in place", "error", err)
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
