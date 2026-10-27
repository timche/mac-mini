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

// One real night, end to end: four versions moved, the proxy daemon put back behind one of
// them, the lockfiles committed, and a macOS update that is still Tim's to deal with. The
// whole message rather than a line of it, because what is being pinned is how it reads —
// three sections in one order, and nothing in it that is only the Mac talking to itself.
func TestTheWholeSummaryOfANightThatMovedFourVersions(t *testing.T) {
	b := newBox(t)
	b.current()
	b.moveTool(outdatedLine("npm:portless", "latest", "0.15.11", "0.15.12"))
	b.lockfilesMoved()
	b.says("brew list --versions", "gh 2.103.2\nstripe-cli 1.55.1\ntuicr 0.27.0\n")
	b.saysNext("brew list --versions", "gh 2.103.3\nstripe-cli 1.55.2\ntuicr 0.28.0\n")
	b.says("brew list --cask --versions", "")
	b.says("softwareupdate --list", `Software Update Tool

Finding available software
Software Update found the following new or updated software:
* Label: macOS Sequoia 27.2-27C50
	Title: macOS 27.2, Version: 27.2, Size: 7101234KiB, Recommended: YES, Action: restart,
`)

	b.update(false)

	harness.Equal(t, b.summary(), strings.Join([]string{
		"⚠️ Nightly update: 4 versions moved, 1 needs you",
		"**Needs you:**\n" +
			"- `macOS 27.2` is waiting: macOS installs it by itself and restarts the Mac at a time it " +
			"picks, and you can install it sooner in System Settings › General › Software Update",
		"**Moved:**\n" +
			"- `npm:portless` 0.15.11 → 0.15.12 · dev-URL proxy restarted\n" +
			"- `gh` 2.103.2 → 2.103.3\n" +
			"- `stripe-cli` 1.55.1 → 1.55.2\n" +
			"- `tuicr` 0.27.0 → 0.28.0",
		"-# mac-mini",
	}, "\n\n"), "the whole summary")
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
		wants:  []string{"**Moved:**", "`node` 24.21.0 → 24.22.0 · dev-URL proxy restarted", "mac-mini"},
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
		name:   "a night with something waiting on Tim",
		marker: wording.MarkerDegraded,
		wants:  []string{"**Needs you:**", "the herdr server is running `0.9.5`", "needs you"},
		render: func(t *testing.T) string {
			b := newBox(t)
			b.current()
			b.moveHerdr("0.9.5", "0.9.6")
			b.herdrServerOn("0.9.5")
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
