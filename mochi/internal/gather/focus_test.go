package gather

import (
	"strings"
	"testing"

	"github.com/timche/mac-mini/discord"

	"github.com/timche/mac-mini/mochi/internal/chat/chattest"
)

// The two halves of a rendered context. Every message is in one of them.
func sections(t *testing.T, out string) (focus, surrounding string) {
	t.Helper()
	f := strings.Index(out, FocusHeading+"\n\n")
	if f < 0 {
		t.Fatalf("no focus section:\n%.400s", out)
	}
	s := strings.Index(out, "\n"+SurroundingHeading+"\n\n")
	if s < 0 {
		return out[f:], ""
	}
	return out[f:s], out[s:]
}

func TestAReplysFocusIsTheMessageItsChainAndItsReplies(t *testing.T) {
	f := server()
	for n := 1; n <= 30; n++ {
		f.Post(channel, n, other, "chatter "+chattest.ID(n))
	}
	parent := f.Post(channel, 31, other, "what do mods want next?")
	qol := reply(f.Post(channel, 32, maker, "QOL list"), parent)
	for n := 33; n <= 60; n++ {
		f.Post(channel, n, other, "later "+chattest.ID(n))
	}
	reply(f.Post(channel, 45, other, "+1"), qol)
	mention := reply(f.Post(channel, 61, tim, "<@"+mochi.ID+"> research"), qol)

	set, err := Reply(f, mention)
	if err != nil {
		t.Fatal(err)
	}
	focus, surrounding := sections(t, Render(set, nil))

	for _, want := range []string{"what do mods want next?", "QOL list", "+1"} {
		if !strings.Contains(focus, want) || strings.Contains(surrounding, want) {
			t.Errorf("%q is not in the focus alone", want)
		}
	}
	if !strings.Contains(surrounding, "chatter "+chattest.ID(20)) || !strings.Contains(surrounding, "later "+chattest.ID(40)) {
		t.Error("the discussion around it was dropped rather than kept as surrounding")
	}
	if strings.Contains(focus, "chatter") || strings.Contains(focus, "later") {
		t.Error("the surrounding channel is in the focus")
	}
	if strings.Index(focus, "what do mods") > strings.Index(focus, "QOL list") || strings.Index(focus, "QOL list") > strings.Index(focus, "+1") {
		t.Error("the focus is not in time order")
	}
	if strings.Index(surrounding, chattest.ID(20)) > strings.Index(surrounding, chattest.ID(40)) {
		t.Error("the surrounding channel is not in time order")
	}
}

func TestAThreadsFocusIsItsStarterAndTimsMention(t *testing.T) {
	f := server()
	f.AddChannel(&discord.Channel{ID: forum, GuildID: guild, Type: discord.ChannelTypeGuildForum})
	thread := &discord.Channel{ID: post, GuildID: guild, ParentID: forum, Type: discord.ChannelTypeGuildPublicThread}
	f.AddChannel(thread)
	starter := f.Post(post, 0, maker, "Bug: elo resets after a draw")
	starter.ID = post
	f.Msgs[post][0] = starter
	for n := 1; n <= 10; n++ {
		f.Post(post, n, other, "me too "+chattest.ID(n))
	}
	asked := f.Post(post, 11, other, "is it every draw?")
	mention := reply(f.Post(post, 12, tim, "<@"+mochi.ID+"> look into this"), asked)

	set, err := Thread(f, thread, mention)
	if err != nil {
		t.Fatal(err)
	}
	focus, surrounding := sections(t, Render(set, nil))

	for _, want := range []string{"Bug: elo resets", "look into this", "(" + NoteMention + ")", "is it every draw?"} {
		if !strings.Contains(focus, want) {
			t.Errorf("the focus lacks %q", want)
		}
	}
	if strings.Count(surrounding, "me too") != 10 || strings.Contains(focus, "me too") {
		t.Error("the rest of the thread is not the surrounding section")
	}
}

func TestAMentionThatRepliesToNothingFocusesOnTheLastFewMessages(t *testing.T) {
	f := server()
	for n := 1; n <= 30; n++ {
		f.Post(channel, n, other, "msg "+chattest.ID(n))
	}
	mention := f.Post(channel, 31, tim, "<@"+mochi.ID+"> what is this about?")

	set, err := Channel(f, mention)
	if err != nil {
		t.Fatal(err)
	}
	focus, surrounding := sections(t, Render(set, nil))

	if strings.Count(focus, "msg ") != FocusWindow || !strings.Contains(focus, chattest.ID(30)) || !strings.Contains(focus, chattest.ID(26)) {
		t.Errorf("focus:\n%s", focus)
	}
	if strings.Count(surrounding, "msg ") != 25 || strings.Contains(surrounding, chattest.ID(26)+" ") {
		t.Errorf("the surrounding section holds %d messages", strings.Count(surrounding, "msg "))
	}
	if strings.Contains(focus, "what is this about?") {
		t.Error("Tim's instruction is repeated in the context")
	}
}

func TestTrimmingNeverTakesTheFocus(t *testing.T) {
	f := server()
	for n := 1; n <= 20; n++ {
		f.Post(channel, n, other, "msg")
	}
	mention := f.Post(channel, 21, tim, "<@"+mochi.ID+"> go")
	set, err := Channel(f, mention)
	if err != nil {
		t.Fatal(err)
	}

	dropped := 0
	for set.DropOldest() {
		dropped++
	}
	if dropped != 15 || set.Len() != FocusWindow {
		t.Errorf("dropped %d, kept %d", dropped, set.Len())
	}
}

// Relays and history have no focus, and read as they did.
func TestASetWithNoFocusHasNoSections(t *testing.T) {
	f := server()
	f.Post(channel, 1, other, "hello")
	set, err := Paged(f, guild, channel, 10, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if out := Render(set, nil); strings.Contains(out, FocusHeading) || strings.Contains(out, "SURROUNDING") {
		t.Errorf("rendered:\n%s", out)
	}
}
