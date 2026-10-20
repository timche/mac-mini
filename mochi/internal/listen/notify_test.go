package listen

import (
	"reflect"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/chat/chattest"
)

// Every thread mochi opens mentions Tim in its first post, with a mention that can reach him
// alone and no preview, and makes him a member, so a thread reaches him though he is notified
// of mentions only.
func TestEveryThreadMochiOpensReachesTim(t *testing.T) {
	f := newFixture(t)
	m := f.mention(channel, 1, tim, "research", nil)
	f.h.Handle(m)

	first := f.api.Sent[0].Msg
	if f.api.Sent[0].Channel != m.ID || first.Content != startedPost(m.ID) {
		t.Errorf("first post %q in %s", first.Content, f.api.Sent[0].Channel)
	}
	if am := first.AllowedMentions; am == nil || !reflect.DeepEqual(am.Users, []string{tim.ID}) || len(am.Parse) != 0 {
		t.Errorf("allowed mentions = %+v", am)
	}
	if first.Flags&discordgo.MessageFlagsSuppressEmbeds == 0 {
		t.Error("the first post would show a preview")
	}
	if !reflect.DeepEqual(f.api.Members, []string{m.ID + " " + tim.ID}) {
		t.Errorf("members = %v", f.api.Members)
	}

	g := newFixture(t)
	g.h.Split = splitsInto(abc, nil, nil)
	g.h.Handle(g.mention(channel, 1, tim, "research a, b and c separately", nil))
	if len(g.api.Members) != 3 {
		t.Errorf("members = %v", g.api.Members)
	}
	for i := range abc {
		if posts := g.sentTo(chanID(i)); len(posts) == 0 || posts[0] != startedPost(chattest.ID(1)) {
			t.Errorf("thread %d first post = %q", i+1, posts)
		}
	}
	g.quiet()
}

// A thread Tim is already in — one he mentioned mochi inside — gets no mention and no member.
func TestAnAdoptedThreadIsNotAnnounced(t *testing.T) {
	f := newFixture(t)
	f.api.AddChannel(&discordgo.Channel{ID: forum, GuildID: guild, Type: discordgo.ChannelTypeGuildForum})
	f.api.AddChannel(&discordgo.Channel{ID: post, GuildID: guild, ParentID: forum, Name: "Ideas", Type: discordgo.ChannelTypeGuildPublicThread})
	f.h.Handle(f.mention(post, 1, tim, "look into this", nil))

	if got := f.sentTo(post); len(got) != 1 || got[0] != "Session started." {
		t.Errorf("posts = %q", got)
	}
	if len(f.api.Members) != 0 {
		t.Errorf("members = %v", f.api.Members)
	}
}
