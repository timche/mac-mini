package watch

import (
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/harness"
	"github.com/timche/mac-mini/hachiko/internal/usage"
)

// A Mac with a reading of the account's usage: the 5-hour window nearly spent and two hours
// of it to go, which is the state the warning is for.
func usageFixture(t *testing.T) *fixture {
	t.Helper()

	f := newFixture(t)
	f.threads = true
	f.usageAt(usage.FiveHour, 85, base.Add(2*time.Hour))

	return f
}

func TestAWindowUnderTheMarkSaysNothing(t *testing.T) {
	f := newFixture(t)
	f.usageAt(usage.FiveHour, 79, base.Add(2*time.Hour))

	harness.Equal(t, f.at(0).sweep(), "", "the log of a check with usage to spare")
	harness.Equal(t, f.sentCount(), 0, "messages sent")
}

// A Mac where nobody has drawn a status line since the state was made, which is every Mac
// until a session runs: there is nothing to read and nothing to say.
func TestNoReadingSaysNothing(t *testing.T) {
	f := newFixture(t)

	harness.Equal(t, f.at(0).sweep(), "", "the log of a check with no reading")
	harness.Equal(t, f.sentCount(), 0, "messages sent")
}

func TestAWindowOverTheMarkIsOneMessageAndNoSession(t *testing.T) {
	f := usageFixture(t)
	out := f.at(0).sweep()

	harness.Wants(t, out, "85% of the 5-hour usage limit is gone, and it resets in 2h00m")

	harness.Equal(t, f.sentCount(), 1, "messages sent")
	harness.Wants(t, f.lastSent(), "⚠️ 85% of the account's 5-hour Claude usage is gone")
	harness.Wants(t, f.lastSent(), "**Used:** 85% of the 5-hour window")
	harness.Wants(t, f.lastSent(), ", in 2 hours")
	harness.Wants(t, f.lastSent(), "**Needs you:** hold new work back until it resets")

	// Not an incident: starting a session over this would spend the very thing that is
	// running out, and the one answer is Tim's.
	harness.Equal(t, f.oncallCalls, 0, "sessions opened")

	// One message while the window is the window it was warned about, not twelve an hour.
	f.usageAt(usage.FiveHour, 97, base.Add(2*time.Hour))
	harness.Equal(t, f.at(300).sweep(), "", "the log of a check about a window already warned about")
	harness.Equal(t, f.sentCount(), 1, "messages sent after a second check")
}

// What ends the warning is the reset time it was sent about passing, and nothing else: the
// reading only moves while somebody is running a session, which is what the warning asked
// him to stop doing.
func TestAWindowThatHasResetIsSaidInTheSameThread(t *testing.T) {
	f := usageFixture(t)
	f.at(0).sweep()

	out := f.at(7260).sweep()
	harness.Wants(t, out, "the 5-hour usage limit has reset")

	harness.Equal(t, f.sentCount(), 2, "messages sent")
	harness.Wants(t, f.lastSent(), "🟢 The account's 5-hour Claude usage has reset")
	harness.Wants(t, f.lastSent(), "**Was at:** 85% of the 5-hour window")
	harness.Equal(t, f.sentTo[1], "thread-usage-five_hour", "the thread the 🟢 went into")
	harness.Equal(t, f.outcomes[1], true, "whether the 🟢 closed the post")

	// And nothing is left behind: the window that has started gets its own warning if it
	// goes the same way.
	harness.Equal(t, len(f.state().Usage), 0, "windows still recorded as warned about")

	f.usageAt(usage.FiveHour, 88, base.Add(9*time.Hour))
	f.at(7560).sweep()
	harness.Equal(t, f.sentCount(), 3, "messages sent after the next window went the same way")
	harness.Wants(t, f.lastSent(), "⚠️ 88% of the account's 5-hour Claude usage is gone")
}

// A reading taken before the window it describes reset says nothing about the window running
// now: Claude Code drops a window from the event once its reset time passes, so what is left
// in the file is the last thing anybody read.
func TestAReadingOlderThanItsWindowIsIgnored(t *testing.T) {
	f := newFixture(t)
	f.usageAt(usage.FiveHour, 96, base.Add(-time.Minute))

	harness.Equal(t, f.at(0).sweep(), "", "the log of a check with a reading of a window that is over")
	harness.Equal(t, f.sentCount(), 0, "messages sent")
}

// The 7-day window is the one that cannot be waited out, and it is warned about on its own:
// two windows over the mark are two pieces of news with two reset times.
func TestTheTwoWindowsAreWarnedAboutSeparately(t *testing.T) {
	f := newFixture(t)
	f.threads = true
	f.usageAt(usage.SevenDay, 82, base.Add(50*time.Hour))
	f.at(0).sweep()

	harness.Equal(t, f.sentCount(), 1, "messages sent")
	harness.Wants(t, f.lastSent(), "⚠️ 82% of the account's 7-day Claude usage is gone")
	harness.Wants(t, f.lastSent(), "**Used:** 82% of the 7-day window")

	f.usageAt(usage.FiveHour, 91, base.Add(time.Hour))
	f.at(300).sweep()

	harness.Equal(t, f.sentCount(), 2, "messages sent")
	harness.Wants(t, f.lastSent(), "⚠️ 91% of the account's 5-hour Claude usage is gone")
	harness.Equal(t, f.opened[1], "usage-five_hour", "the post the second window opened")
}

// A send that failed leaves the warning unsaid, so the next check says it rather than
// recording news nobody has had.
func TestAWarningThatDidNotSendIsLeftToTheNextCheck(t *testing.T) {
	f := usageFixture(t)
	f.sendErr = errSendFailed
	f.at(0).sweep()

	harness.Equal(t, len(f.state().Usage), 0, "windows recorded as warned about")

	f.sendErr = nil
	f.at(300).sweep()
	harness.Equal(t, f.sentCount(), 1, "messages sent once the channel took one")
	harness.Wants(t, f.lastSent(), "⚠️ 85% of the account's 5-hour Claude usage is gone")
}

// The mark is a knob for the same reason every other one is: a test says what it is about by
// the one number it moves.
func TestTheMarkIsAKnob(t *testing.T) {
	f := newFixture(t)
	f.cfg.UsageWarnPercent = 95
	f.usageAt(usage.FiveHour, 85, base.Add(time.Hour))

	harness.Equal(t, f.at(0).sweep(), "", "the log of a check under a mark of 95%")

	f.usageAt(usage.FiveHour, 95.5, base.Add(time.Hour))
	f.at(300).sweep()
	harness.Wants(t, f.lastSent(), "⚠️ 95.5% of the account's 5-hour Claude usage is gone")
}

// A dry run says what it would report and changes nothing, the state included.
func TestADryRunSaysWhatItWouldReportAboutAWindow(t *testing.T) {
	f := usageFixture(t)

	harness.Wants(t, f.at(0).dryRun(), "would report that 85% of the 5-hour usage limit is gone, with 2h00m to go")
	harness.Equal(t, f.sentCount(), 0, "messages sent")
	harness.Equal(t, len(f.state().Usage), 0, "windows recorded as warned about")

	f.at(0).sweep()
	harness.Wants(t, f.at(7260).dryRun(), "would report that the 5-hour usage limit has reset")
	harness.Equal(t, len(f.state().Usage), 1, "windows still recorded as warned about")
}
