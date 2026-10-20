package listen

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/event"
	"github.com/timche/mac-mini/mochi/internal/mark"
	"github.com/timche/mac-mini/mochi/internal/split"
)

// A classifier that counts its calls and always says one task.
func counting(n *int) func(string, *discordgo.Message) (split.Answer, error) {
	return func(string, *discordgo.Message) (split.Answer, error) {
		*n++
		return split.Answer{}, nil
	}
}

// Tim stops a working session: an esc and nothing prompted, ⏸ in the thread's name and a line
// saying how to go on, and no classifier; his next word puts it back to ⏳.
func TestStopInterruptsAWorkingSession(t *testing.T) {
	f := newFixture(t)
	classified := 0
	f.h.Split = counting(&classified)
	first := f.mention(channel, 1, tim, "research", nil)
	f.h.Handle(first)
	agent := "mochi-" + first.ID
	f.herdr.agents[agent] = "working"

	stop := f.mention(first.ID, 2, tim, "Stop.", nil)
	f.h.Handle(stop)

	if got := f.herdr.called("agent send-keys " + agent + " esc"); len(got) != 1 {
		t.Errorf("escs = %v", got)
	}
	if len(f.herdr.prompts[agent]) != 1 {
		t.Errorf("the stop was prompted: %d prompts", len(f.herdr.prompts[agent]))
	}
	if classified != 1 {
		t.Errorf("the classifier ran %d times, want only for the session", classified)
	}
	if f.api.Channels[first.ID].Name != "⏸ research" {
		t.Errorf("thread = %q", f.api.Channels[first.ID].Name)
	}
	if posts := f.sentTo(first.ID); posts[len(posts)-1] != "Stopped. Mention mochi to continue." {
		t.Errorf("posts = %q", posts)
	}
	if f.record(first.ID).LastMention != stop.ID {
		t.Error("the stop did not move the window on")
	}
	f.seenOnly(stop.ID)

	// The turn it ended is no wait on Tim.
	f.event(first.ID, event.Stop, 0)
	f.consume()
	if f.pings(first.ID) != 0 {
		t.Error("a stopped session pinged Tim")
	}

	f.h.Handle(f.mention(first.ID, 3, tim, "go on with b) only", nil))
	if len(f.herdr.prompts[agent]) != 2 {
		t.Error("the follow-up after a stop was not relayed")
	}
	f.inState(first.ID, mark.Running)
	if f.api.Channels[first.ID].Name != "⏳ research" {
		t.Errorf("thread after the follow-up = %q", f.api.Channels[first.ID].Name)
	}
}

// An idle session is only marked; "cancel" is the same word; a question it is on is cancelled.
func TestStopOnAnIdleOrAskingSession(t *testing.T) {
	f := newFixture(t)
	first := f.mention(channel, 1, tim, "research", nil)
	f.h.Handle(first)
	f.h.Handle(f.mention(first.ID, 2, tim, "cancel", nil))
	if got := f.herdr.called("agent send-keys"); len(got) != 0 {
		t.Errorf("an esc to an idle session: %v", got)
	}
	f.inState(first.ID, mark.Stopped)

	g := newFixture(t)
	other := g.mention(channel, 1, tim, "research", nil)
	g.h.Handle(other)
	g.herdr.agents["mochi-"+other.ID] = "blocked"
	g.h.Handle(g.mention(other.ID, 2, tim, "STOP!", nil))
	if got := g.herdr.called("agent send-keys"); len(got) != 1 {
		t.Errorf("escs = %v", got)
	}
}

// Stop is a command only when it is the whole instruction.
func TestStopInsideAnInstructionIsAnInstruction(t *testing.T) {
	for _, words := range []string{"stop showing the old elo", "cancel the old match export and add csv", "please stop"} {
		f := newFixture(t)
		first := f.mention(channel, 1, tim, "research", nil)
		f.h.Handle(first)
		f.h.Handle(f.mention(first.ID, 2, tim, words, nil))
		if prompts := f.herdr.prompts["mochi-"+first.ID]; len(prompts) != 2 || !strings.Contains(prompts[1], words) {
			t.Errorf("%q was not relayed", words)
		}
		f.inState(first.ID, mark.Running)
	}
	for _, words := range []string{"stop", "Stop", " cancel. ", "STOP!", "stop."} {
		if !stopping(words) {
			t.Errorf("%q is not a stop", words)
		}
	}
}

// Stop where there is no session starts nothing and says so, with no classifier.
func TestStopWithNoSessionStartsNothing(t *testing.T) {
	f := newFixture(t)
	classified := 0
	f.h.Split = counting(&classified)
	m := f.mention(channel, 1, tim, "stop", nil)
	f.h.Handle(m)
	if classified != 0 || len(f.herdr.called("agent start")) != 0 || len(f.api.Threads) != 0 {
		t.Errorf("a stop started something: classified %d, starts %v", classified, f.herdr.called("agent start"))
	}
	if replies := f.sentTo(channel); len(replies) != 1 || replies[0] != "❌ There is no session here to stop." {
		t.Errorf("replies = %q", replies)
	}
}
