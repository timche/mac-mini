package listen

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/chat/chattest"
	"github.com/timche/mac-mini/mochi/internal/gather"
)

func TestTheOrdersSayWhatTheFocusIs(t *testing.T) {
	f := newFixture(t)
	qol := f.api.Post(channel, 1, maker, "QOL list")
	m := f.mention(channel, 2, tim, "research", qol)

	f.h.Handle(m)

	prompt := onlyPrompt(t, f.herdr, "mochi-"+m.ID)
	if !strings.Contains(skill(t), "The FOCUS section at the top of that block is what Tim's instruction is about") {
		t.Error("the skill does not say what the focus is")
	}
	if !strings.HasPrefix(between(t, prompt, "DISCORD CONTEXT"), "Channel: #moderators\n\nFOCUS\n\n") {
		t.Errorf("the context does not open on the focus:\n%.200s", between(t, prompt, "DISCORD CONTEXT"))
	}
}

// Over the cap, the surrounding channel goes and the focus stays whole.
func TestTrimmingAPromptTakesTheSurroundingChannelNotTheFocus(t *testing.T) {
	was := maxPrompt
	maxPrompt = 20 << 10
	t.Cleanup(func() { maxPrompt = was })

	f := newFixture(t)
	parent := f.api.Post(channel, 1, other, "parent "+strings.Repeat("p", 1500))
	for n := 2; n <= 40; n++ {
		f.api.Post(channel, n, other, chattest.ID(n)+" "+strings.Repeat("x", 1500))
	}
	target := f.api.Post(channel, 41, maker, "QOL "+strings.Repeat("q", 1500))
	target.MessageReference = &discordgo.MessageReference{MessageID: parent.ID, ChannelID: channel}
	for n := 42; n <= 80; n++ {
		f.api.Post(channel, n, other, chattest.ID(n)+" "+strings.Repeat("y", 1500))
	}
	m := f.mention(channel, 81, tim, "research", target)

	f.h.Handle(m)

	context := between(t, onlyPrompt(t, f.herdr, "mochi-"+m.ID), "DISCORD CONTEXT")
	focusAt := strings.Index(context, gather.FocusHeading+"\n\n")
	surroundAt := strings.Index(context, gather.SurroundingHeading)
	if focusAt < 0 || surroundAt < 0 {
		t.Fatalf("sections missing:\n%.300s", context)
	}
	focus := context[focusAt:surroundAt]
	if !strings.Contains(focus, "parent ppp") || !strings.Contains(focus, "QOL qqq") {
		t.Error("the focus was trimmed")
	}
	if strings.Contains(context, chattest.ID(2)+" ") || !strings.Contains(context, "trimmed to keep this prompt") {
		t.Error("the oldest surrounding messages were not the ones trimmed")
	}
}

// A split session's TASK line stays at the very top, the focus after it.
func TestASplitSessionsTaskLineComesBeforeTheFocus(t *testing.T) {
	f := newFixture(t)
	f.h.Split = splitsInto(abc, nil, nil)
	qol := f.api.Post(channel, 1, maker, "QOL: a) b) c)")
	m := f.mention(channel, 2, tim, "research a, b and c separately", qol)

	f.h.Handle(m)

	context := between(t, onlyPrompt(t, f.herdr, "mochi-"+chanID(1)), "DISCORD CONTEXT")
	task := strings.Index(context, "TASK 2 of 3:")
	focus := strings.Index(context, "\nFOCUS\n")
	if task != 0 || focus < task || !strings.Contains(context[focus:], "QOL: a) b) c)") {
		t.Errorf("context:\n%.400s", context)
	}
}

// A follow-up carries what others said since, with no sections.
func TestARelayHasNoFocusSection(t *testing.T) {
	f := newFixture(t)
	first := f.mention(channel, 1, tim, "start", nil)
	f.h.Handle(first)
	f.api.Post(first.ID, 2, maker, "also b)")
	f.h.Handle(f.mention(first.ID, 3, tim, "and b)", nil))

	relay := f.herdr.prompts["mochi-"+first.ID][1]
	if strings.Contains(relay, gather.FocusHeading) || strings.Contains(relay, "SURROUNDING") {
		t.Errorf("relay:\n%s", relay)
	}
}
