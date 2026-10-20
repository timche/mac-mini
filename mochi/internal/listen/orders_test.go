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
		"post it with `mochi post --review`",
		"show ❓ until he answers",
		"before-and-after screenshots, a recording or other images",
		"post that with `mochi post --done`: they show ✅",
		"add `--pr <url>` whenever there is a pull request",
		"so do not write it",
		"Give it the text on stdin",
		"**What changed**",
		"Keep it brief: its readers are not technical, and Tim reads the details in this session and the pull request.",
		"At most three bullets under What changed",
		"1. Before — <what it shows>",
		"**Still to check**",
		"number the screenshots in the order the files are attached",
		"No technical detail — no pixel values, config or release mechanics, file or function names",
		"A post without either flag changes neither.",
		"Post nothing else unprompted: no progress updates.",
		"often not technical and without GitHub access",
		"No links but the pull request --pr adds, no file paths, branch names, code or jargon, no secrets",
		"Name none of the tools or services behind the work",
	} {
		if !strings.Contains(orders, want) {
			t.Errorf("the orders lack %q", want)
		}
	}
	if strings.Contains(orders, "Post only when Tim asks") || strings.Contains(orders, "a PR link") {
		t.Error("the old rule is still there")
	}
}
