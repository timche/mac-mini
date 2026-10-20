package listen

import (
	"strings"
	"testing"
)

// What a session is told about posting: when to announce it is finished, with proof, and what
// a channel of moderators can read.
func TestTheOrdersSayWhenAndHowToPost(t *testing.T) {
	f := newFixture(t)
	m := f.mention(channel, 1, tim, "fix the overlay", nil)
	f.h.Handle(m)

	prompt := onlyPrompt(t, f.herdr, "mochi-"+m.ID)
	orders := prompt[:strings.Index(prompt, "----- BEGIN INSTRUCTION FROM TIM")]
	for _, want := range []string{
		"up to ten, at most 10 MB each",
		"Give it the text on stdin",
		"Two audiences. The thread is for the team",
		"questions the team can weigh in on (scope, priority, behaviour, what users see)",
		"This session is for Tim alone: anything internal",
		"post its product framing in the thread and keep the technical detail here",
		"`--review` is a post that needs an answer from Tim or the team — a result ready for review, or a product question",
		"`--done` is for a task complete with nothing left for Tim",
		"A post with neither flag is an update about the product that needs no answer",
		"no progress chatter",
		"mochi tells him in the thread that the session is waiting on him",
		"add `--pr <url>` to a --review or --done post whenever there is a pull request",
		"so do not write it",
		"**What changed**",
		"1. Before — <what it shows>",
		"**Still to check**",
		"**What was found**",
		"**Question**",
		"Keep it brief: Tim reads the details in this session and the pull request.",
		"At most three bullets under a heading",
		"number the screenshots in the order the files are attached",
		"No technical detail — no pixel values, config or release mechanics, file or function names",
		"No links but the pull request --pr adds, no file paths, branch names, code or jargon, no secrets",
		"Name none of the tools or services behind the work",
	} {
		if !strings.Contains(orders, want) {
			t.Errorf("the orders lack %q", want)
		}
	}
	if strings.Contains(orders, "Post only when Tim asks") || strings.Contains(orders, "a PR link") || strings.Contains(orders, "result, proof or a question") || strings.Contains(orders, "never in Discord") {
		t.Error("the old rule is still there")
	}
}
