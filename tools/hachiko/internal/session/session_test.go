package session

import (
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/harness"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// The line the warning quotes is the one a session wrote, and hachiko has to be able to
// read its own warning back: the bold form is what it sends now.
func TestTheFallbackOptionSurvivesTheWarningsOwnFormat(t *testing.T) {
	warning := wording.Lead(wording.MarkerDegraded, "No answer on the disk incident — the agent decides in 15 minutes").
		Field(wording.LabelWaiting, "2 hours 45 minutes").
		Field(wording.LabelFallback, "stop pid 4242 and empty the log").
		Block("**Now:** 1 GB free").
		Can("Answer in herdr: workspace `.mac-mini`, tab `disk-1200`.").
		About("disk-1700000000", "mac-mini").
		String()

	harness.Equal(t, FallbackOption(warning), "stop pid 4242 and empty the log", "the option read back")

	// And every other spelling a session might reach for.
	for _, line := range []string{
		"If no answer: stop the worker",
		"**If no answer:** stop the worker",
		"- If no answer: stop the worker",
		"- **If no answer:** stop the worker",
		"> If no answer: stop the worker",
	} {
		harness.Equal(t, FallbackOption("some report\n"+line+"\nmore report"), "stop the worker", line)
	}
}
