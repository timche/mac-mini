package update

import (
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/harness"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// Every message the update can send, rendered in full and printed:
//
//	go test -run TestMessages -v ./internal/update
//
// reads them the way Discord shows them, which is the only way to see that they read as the
// same voice as every other message hachiko sends. Each case asserts the few things that
// have to be true of it, and the printed message is the review.
func TestMessages(t *testing.T) {
	for _, kind := range messageKinds {
		t.Run(kind.name, func(t *testing.T) {
			text := kind.render(t)
			t.Logf("\n\n%s\n", text)

			if strings.HasSuffix(text, "\n") || strings.Contains(text, "\n\n\n") {
				t.Errorf("a blank line where there should be none:\n%q", text)
			}
			if len(text) > discord.Limit {
				t.Errorf("%d characters, which is over Discord's limit", len(text))
			}

			first, _, _ := strings.Cut(text, "\n")
			if !strings.HasPrefix(first, kind.marker+" ") {
				t.Errorf("the lead does not open with %s: %q", kind.marker, first)
			}
			if strings.HasSuffix(first, ".") || strings.Contains(first, "`") {
				t.Errorf("the lead is not a headline: %q", first)
			}
			for _, want := range kind.wants {
				harness.Wants(t, text, want)
			}
		})
	}
}

var messageKinds = []struct {
	name   string
	marker string
	wants  []string
	render func(t *testing.T) string
}{
	{
		name:   "a night that moved something",
		marker: wording.MarkerInfo,
		wants:  []string{"**Moved:**", "`node` 24.21.0 → 24.22.0", "**Also:**", "mac-mini"},
		render: func(t *testing.T) string {
			b := newBox(t)
			b.current()
			b.moveTool(outdatedLine("node", "lts", "24.21.0", "24.22.0"))
			b.says("tailscale status --json", `{"BackendState":"Running"}`)
			b.update(false)
			return b.summary()
		},
	},
	{
		name:   "a step that works again",
		marker: wording.MarkerRecovered,
		wants:  []string{"can update herdr again", "**Step:**", "**Failing since:**", "Incident update-1"},
		render: func(t *testing.T) string {
			b := newBox(t)
			b.current()
			b.fails("herdr update", "herdr: the release could not be downloaded")
			b.update(false)

			// What the five-minute check wrote back after raising it, which is the whole of how
			// the update knows where to say the step works again.
			b.raised(stepHerdr, "update-1", "thread-1")

			b.sent = nil
			b.now = b.now.Add(26 * time.Hour)
			delete(b.answers, "herdr update")
			b.update(false)

			cleared := b.incidents()
			if len(cleared) != 1 {
				t.Fatalf("%d messages", len(cleared))
			}
			return cleared[0].Text
		},
	},
}
