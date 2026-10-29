package listen

import (
	"reflect"
	"strings"
	"testing"

	"github.com/timche/mac-mini/discord"

	"github.com/timche/mac-mini/mochi/internal/chat"
	"github.com/timche/mac-mini/mochi/internal/mark"
	"github.com/timche/mac-mini/mochi/internal/split"
)

// The reactions a message shows now.
func (f *fixture) shows(id string, want ...string) {
	f.t.Helper()
	if got := f.api.Showing(id); !reflect.DeepEqual(got, want) {
		f.t.Errorf("%s shows %v, want %v", id, got, want)
	}
}

// A message mochi handled: 👀 while it did, taken back after, and no other reaction ever.
func (f *fixture) seenOnly(id string) bool {
	f.t.Helper()
	var added, taken []string
	for _, r := range f.api.Reactions {
		if strings.HasPrefix(r, id+" ") {
			added = append(added, r)
		}
	}
	for _, r := range f.api.Unreactions {
		if strings.HasPrefix(r, id+" ") {
			taken = append(taken, r)
		}
	}
	want := []string{id + " " + chat.Eyes}
	if !reflect.DeepEqual(added, want) || !reflect.DeepEqual(taken, want) {
		f.t.Errorf("%s: reactions %v, taken back %v", id, added, taken)
		return false
	}
	return true
}

// A message whose ask reached the session: 👀 while mochi handled it, then 👍, which stays.
func (f *fixture) receipt(id string) bool {
	f.t.Helper()
	var added, taken []string
	for _, r := range f.api.Reactions {
		if strings.HasPrefix(r, id+" ") {
			added = append(added, r)
		}
	}
	for _, r := range f.api.Unreactions {
		if strings.HasPrefix(r, id+" ") {
			taken = append(taken, r)
		}
	}
	if !reflect.DeepEqual(added, []string{id + " " + chat.Eyes, id + " " + chat.Delivered}) || !reflect.DeepEqual(taken, []string{id + " " + chat.Eyes}) {
		f.t.Errorf("%s: reactions %v, taken back %v", id, added, taken)
		return false
	}
	return true
}

// A session's state as mochi keeps it, which a thread or post mochi made shows in its name.
func (f *fixture) inState(thread, want string) {
	f.t.Helper()
	if got, _ := f.h.Marks.Session(thread); got != want {
		f.t.Errorf("%s is %q, want %q", thread, got, want)
	}
}

// Tim's mention shows 👀 while mochi handles it, then 👍 once what he asked reached a session —
// one, several, or the ones his answer to a question started — and nothing after a failure,
// which says why. The state is in the thread's name.
func TestAMentionShowsEyesThenAReceipt(t *testing.T) {
	flows := map[string]func(f *fixture) *discord.Message{
		"a session": func(f *fixture) *discord.Message {
			m := f.mention(channel, 1, tim, "research", nil)
			f.h.Handle(m)
			if f.api.Threads[0] != m.ID+" ⏳ research" {
				t.Errorf("thread = %q", f.api.Threads[0])
			}
			f.h.Marks.Settled(m.ID, mark.Asking)
			if f.api.Channels[m.ID].Name != "❓ research" {
				t.Errorf("thread = %q", f.api.Channels[m.ID].Name)
			}
			return m
		},
		"a forum post": func(f *fixture) *discord.Message {
			f.api.AddChannel(&discord.Channel{ID: sessions, GuildID: guild, Type: discord.ChannelTypeGuildForum, AvailableTags: forumTags})
			f.useForum(sessions)
			m := f.mention(channel, 1, tim, "research", nil)
			f.h.Handle(m)
			f.shows(postID(1))
			return m
		},
		"a question": func(f *fixture) *discord.Message {
			f.h.Split = answers(split.Answer{Decision: split.Ask, Title: "QOL", Tasks: abc}, nil, nil)
			m := f.mention(channel, 1, tim, "take a look", nil)
			f.h.Handle(m)
			press(f, tim, buttonsOf(t, f.api.Sent[0].Msg.Components)[1].CustomID)
			return m
		},
		"several sessions": func(f *fixture) *discord.Message {
			f.h.Split = splitsInto(abc, nil, nil)
			m := f.mention(channel, 1, tim, "research a, b and c separately", nil)
			f.h.Handle(m)
			for i := range abc {
				for _, s := range f.api.Sent {
					if s.Channel == chanID(i) {
						f.shows(s.ID)
					}
				}
			}
			return m
		},
		"a failure": func(f *fixture) *discord.Message {
			f.herdr.startFails = true
			m := f.mention(channel, 1, tim, "research", nil)
			f.h.Handle(m)
			if f.api.Channels[m.ID].Name != "❌ research" {
				t.Errorf("thread = %q", f.api.Channels[m.ID].Name)
			}
			if posts := f.sentTo(m.ID); len(posts) == 0 || !strings.HasPrefix(posts[len(posts)-1], "❌ ") {
				t.Errorf("the failure was not said: %q", posts)
			}
			return m
		},
		"a split that fails in part": func(f *fixture) *discord.Message {
			f.h.Split = answers(split.Answer{Decision: split.Many, Tasks: abc}, nil, nil)
			f.herdr.failFrom = 3
			m := f.mention(channel, 1, tim, "research a, b and c separately", nil)
			f.h.Handle(m)
			return m
		},
	}
	failed := map[string]bool{"a failure": true, "a split that fails in part": true}
	for name, flow := range flows {
		f := newFixture(t)
		m := flow(f)
		if failed[name] && !f.seenOnly(m.ID) || !failed[name] && !f.receipt(m.ID) {
			t.Errorf("%s", name)
		}
	}
}

// Tim's follow-up shows 👀 until the session has it and 👍 after, inside a thread mochi made and
// inside one it did not; the session's state moves in the name.
func TestAFollowUpShowsAReceiptOnceTheSessionHasIt(t *testing.T) {
	f := newFixture(t)
	first := f.mention(channel, 1, tim, "start", nil)
	f.h.Handle(first)
	f.h.Marks.Settled(first.ID, mark.Asking)

	follow := f.mention(first.ID, 2, tim, "merge it", nil)
	f.h.Handle(follow)
	f.receipt(follow.ID)
	f.inState(first.ID, mark.Running)
	if f.api.Channels[first.ID].Name != "⏳ start" {
		t.Errorf("thread = %q", f.api.Channels[first.ID].Name)
	}

	g := newFixture(t)
	g.api.AddChannel(&discord.Channel{ID: forum, GuildID: guild, Type: discord.ChannelTypeGuildForum})
	g.api.AddChannel(&discord.Channel{ID: post, GuildID: guild, ParentID: forum, Name: "Ideas", Type: discord.ChannelTypeGuildPublicThread})
	g.h.Handle(g.mention(post, 1, tim, "look at this", nil))
	again := g.mention(post, 2, tim, "and this", nil)
	g.h.Handle(again)
	g.receipt(again.ID)
}

// A forum post Tim mentioned mochi in is the session's thread but not mochi's: its title stays.
func TestAForumPostKeepsItsTitle(t *testing.T) {
	f := newFixture(t)
	f.api.AddChannel(&discord.Channel{ID: forum, GuildID: guild, Type: discord.ChannelTypeGuildForum})
	f.api.AddChannel(&discord.Channel{ID: post, GuildID: guild, ParentID: forum, Name: "Bug: elo resets", Type: discord.ChannelTypeGuildPublicThread})
	m := f.mention(post, 1, tim, "look into this", nil)

	f.h.Handle(m)
	f.h.Marks.Settled(post, mark.Finished)

	if len(f.api.Renames) != 0 || f.api.Channels[post].Name != "Bug: elo resets" {
		t.Errorf("renames %v", f.api.Renames)
	}
	f.receipt(m.ID)
}
