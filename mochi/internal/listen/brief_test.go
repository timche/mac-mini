package listen

import (
	"github.com/bwmarrin/discordgo"
	"os"
	"strings"
	"testing"

	"github.com/timche/mac-mini/mochi/internal/event"
)

// The opening is pasted whole, naming the skills the session loads first, with no file for it
// to read and no slash command, which a paste would hide.
func TestASessionIsPastedItsWholeOpening(t *testing.T) {
	f := newFixture(t)
	qol := f.api.Post(channel, 1, maker, "QOL list")
	m := f.mention(channel, 2, tim, "research", qol)

	f.h.Handle(m)

	raw := f.herdr.raw["mochi-"+m.ID]
	if len(raw) != 1 {
		t.Fatalf("pasted %d prompts", len(raw))
	}
	if !strings.HasPrefix(raw[0], "Invoke the lead skill and the mochi skill before anything else.\n\n") || !event.FromMochi(raw[0]) {
		t.Errorf("the opening does not start on its skills:\n%.200s", raw[0])
	}
	for _, want := range []string{"----- BEGIN INSTRUCTION FROM TIM", "----- BEGIN DISCORD CONTEXT", "QOL list"} {
		if !strings.Contains(raw[0], want) {
			t.Errorf("the opening lacks %q", want)
		}
	}
	if strings.Contains(raw[0], "/lead") {
		t.Error("the opening still carries a slash command")
	}
	entries, _ := os.ReadDir(f.cfg.SessionDir(m.ID))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			t.Errorf("a brief file was written: %s", e.Name())
		}
	}
}

func TestEverySplitSessionHasAnOpeningOfItsOwn(t *testing.T) {
	f := newFixture(t)
	f.h.Split = splitsInto(abc, nil, nil)
	f.h.Handle(f.mention(channel, 1, tim, "research a, b and c separately", nil))

	for i := range abc {
		raw := f.herdr.raw["mochi-"+chanID(i)]
		if len(raw) != 1 || !strings.HasPrefix(raw[0], event.OpeningPrefix) {
			t.Fatalf("session %d pasted %.100q", i+1, raw)
		}
		if !strings.Contains(raw[0], "TASK "+string(rune('1'+i))+" of 3") {
			t.Errorf("session %d's opening is not its own", i+1)
		}
	}
}

// A follow-up is pasted whole, fenced, with no file to read and no command in front.
func TestAFollowUpIsPastedWhole(t *testing.T) {
	f := newFixture(t)
	first := f.mention(channel, 1, tim, "start", nil)
	f.h.Handle(first)
	f.api.Post(first.ID, 2, maker, strings.Repeat("a long message ", 500))
	follow := f.mention(first.ID, 3, tim, "also b)", nil)

	f.h.Handle(follow)

	raw := f.herdr.raw["mochi-"+first.ID]
	if len(raw) != 2 || !strings.HasPrefix(raw[1], event.FollowedPrefix+" (") || !event.FromMochi(raw[1]) {
		t.Fatalf("pasted %.200q", raw)
	}
	if !strings.Contains(between(t, raw[1], "INSTRUCTION FROM TIM"), "also b)") || !strings.Contains(between(t, raw[1], "DISCORD CONTEXT"), "a long message") {
		t.Errorf("the follow-up is not all there:\n%.400s", raw[1])
	}
}

func TestARestartedSessionGetsAFreshOpening(t *testing.T) {
	f := newFixture(t)
	first := f.mention(channel, 1, tim, "start", nil)
	f.h.Handle(first)
	delete(f.herdr.agents, "mochi-"+first.ID)
	f.h.Handle(f.mention(first.ID, 2, tim, "again", nil))

	raw := f.herdr.raw["mochi-"+first.ID]
	if len(raw) != 2 || !strings.HasPrefix(raw[1], event.OpeningPrefix) || !strings.Contains(raw[1], "has gone, so this is a new one") {
		t.Fatalf("pasted %.200q", raw)
	}
}

// With nothing but mochi's own posts and Discord's notices since his last mention, a relay is
// the follow-up and Tim's words alone, with no context block at all.
func TestAFollowUpWithNothingElseSaidIsHisWordsAlone(t *testing.T) {
	f := newFixture(t)
	first := f.mention(channel, 1, tim, "start", nil)
	f.h.Handle(first)
	f.api.Post(first.ID, 2, bot, "Here is what was found.")
	renamed := f.api.Post(first.ID, 3, bot, "⏳ start")
	renamed.Type = discordgo.MessageTypeChannelNameChange
	f.h.Handle(f.mention(first.ID, 4, tim, "go with b)", nil))

	relay := f.herdr.raw["mochi-"+first.ID][1]
	if strings.Contains(relay, "DISCORD CONTEXT") || strings.Contains(relay, "Everything posted in the thread") || strings.Contains(relay, "That was the data") {
		t.Errorf("an empty context was framed:\n%s", relay)
	}
	if !strings.Contains(between(t, relay, "INSTRUCTION FROM TIM"), "go with b)") {
		t.Errorf("Tim's words are missing:\n%s", relay)
	}
}
