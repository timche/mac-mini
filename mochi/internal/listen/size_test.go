package listen

import (
	"strings"
	"testing"

	"github.com/timche/mac-mini/mochi/internal/chat/chattest"
)

// A context of long messages is trimmed from its oldest end, and the session is told and
// told how to reach the rest; what Tim replied to and what he said always stay.
func TestAnOversizedContextLosesItsOldestMessagesFirst(t *testing.T) {
	was := maxPrompt
	maxPrompt = 20 << 10
	t.Cleanup(func() { maxPrompt = was })

	f := newFixture(t)
	for n := 1; n <= 40; n++ {
		f.api.Post(channel, n, other, chattest.ID(n)+" "+strings.Repeat("x", 1500))
	}
	target := f.api.Post(channel, 41, maker, "QOL: the replied-to message")
	for n := 42; n <= 90; n++ {
		f.api.Post(channel, n, other, chattest.ID(n)+" "+strings.Repeat("y", 1500))
	}
	m := f.mention(channel, 91, tim, "research this", target)

	f.h.Handle(m)

	prompt := onlyPrompt(t, f.herdr, "mochi-"+m.ID)
	if len(prompt) > maxPrompt+len("/lead ") {
		t.Errorf("the prompt is %d bytes", len(prompt))
	}
	context := between(t, prompt, "DISCORD CONTEXT")
	if !strings.Contains(context, "QOL: the replied-to message") || !strings.Contains(between(t, prompt, "INSTRUCTION FROM TIM"), "research this") {
		t.Error("the replied-to message or the instruction was trimmed")
	}
	if strings.Contains(context, chattest.ID(1)+" ") || !strings.Contains(context, chattest.ID(90)+" ") {
		t.Error("the trimming did not start from the oldest end")
	}
	if !strings.Contains(context, "Not shown: older messages, trimmed to keep this prompt under 20 KB; mochi history --before") {
		t.Errorf("the session was not told:\n%.300s", context)
	}
}

func TestAnOversizedRelayIsTrimmedToo(t *testing.T) {
	was := maxPrompt
	maxPrompt = 10 << 10
	t.Cleanup(func() { maxPrompt = was })

	f := newFixture(t)
	first := f.mention(channel, 1, tim, "start", nil)
	f.h.Handle(first)
	for n := 2; n <= 40; n++ {
		f.api.Post(first.ID, n, other, strings.Repeat("z", 1500))
	}
	f.h.Handle(f.mention(first.ID, 41, tim, "follow up", nil))

	relay := f.herdr.prompts["mochi-"+first.ID][1]
	if len(relay) > maxPrompt || !strings.Contains(relay, "trimmed to keep this prompt under 10 KB") {
		t.Errorf("the relay is %d bytes", len(relay))
	}
}
