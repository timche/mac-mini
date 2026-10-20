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
		"may be given up to ten times, at most 10 MB a file",
		"When you reach a result Tim would review",
		"saying it is finished and what changed or was found",
		"before-and-after screenshots, a recording or other images",
		"Post nothing else unprompted: no progress updates.",
		"often not technical and without GitHub access",
		"No pull request, GitHub or other links, no file paths, branch names, code or jargon, no secrets",
	} {
		if !strings.Contains(orders, want) {
			t.Errorf("the orders lack %q", want)
		}
	}
	if strings.Contains(orders, "Post only when Tim asks") || strings.Contains(orders, "a PR link") {
		t.Error("the old rule is still there")
	}
}
