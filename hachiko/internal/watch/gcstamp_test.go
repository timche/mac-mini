package watch

import (
	"errors"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// An hour is six of the sweep's own ten-minute intervals.
func gcFixture(t *testing.T) *fixture {
	t.Helper()

	f := newFixture(t)
	f.cfg.GCStaleAfter = time.Hour
	f.cfg.GCPlist = "/Users/x/Library/LaunchAgents/io.github.timche.hachiko-gc.plist"
	f.gcStamp = base

	return f
}

func TestASweepInsideTheHourSaysNothing(t *testing.T) {
	f := gcFixture(t)

	harness.Equal(t, f.at(3540).sweep(), "", "the log of a check with a sweep that ran")
	harness.Equal(t, f.sentCount(), 0, "messages sent")
}

func TestASweepThatHasStoppedIsOneMessageAndNoSession(t *testing.T) {
	f := gcFixture(t)
	out := f.at(7800).sweep()

	harness.Wants(t, out, "the worktree sweep has not run for 2h10m, so nothing is cleaning up after a removed worktree")

	harness.Equal(t, f.sentCount(), 1, "messages sent")
	harness.Wants(t, f.lastSent(), "⚠️ The worktree sweep has not run for 2 hours 10 minutes")
	harness.Wants(t, f.lastSent(), "**Last sweep:** ")
	harness.Wants(t, f.lastSent(), "**You can run:** `launchctl kickstart -k gui/501/io.github.timche.hachiko-gc`")

	// Not an incident: the answer is already written in the message, and it is Tim's.
	harness.Equal(t, f.oncallCalls, 0, "sessions opened")

	// One message while it is still not running, not twelve an hour.
	f.at(8100).sweep()
	f.at(8400).sweep()
	harness.Equal(t, f.sentCount(), 1, "messages sent")
}

func TestASweepThatComesBackIsOneMoreLineInTheSameThread(t *testing.T) {
	f := gcFixture(t)
	f.threads = true
	f.at(7800).sweep()

	harness.Equal(t, f.opened[0], "io.github.timche.hachiko-gc", "the thread opened")

	f.gcStamp = base.Add(8100 * time.Second)
	out := f.at(8100).sweep()

	harness.Wants(t, out, "the worktree sweep is running again")
	harness.Equal(t, f.sentCount(), 2, "messages sent")
	harness.Wants(t, f.lastSent(), "🟢 The worktree sweep is running again")
	harness.Equal(t, f.sentTo[1], "thread-io.github.timche.hachiko-gc", "where the clear went")
}

// A fresh Mac has no stamp because nothing has ever swept, and alerting on that would fire
// on every machine this repository provisions until install.sh reaches the agent.
func TestAMacWithNoSweepAgentIsNeverReportedAsHavingStopped(t *testing.T) {
	f := gcFixture(t)
	f.gcInstalled = false

	harness.Equal(t, f.at(100000).sweep(), "", "the log of a check on a Mac with no sweep agent")
	harness.Equal(t, f.sentCount(), 0, "messages sent")
}

// A send that failed leaves it unreported, so the next check says it again rather than
// going quiet about a sweep that is not running.
func TestAMessageAboutTheSweepThatDidNotSendIsSaidAgain(t *testing.T) {
	f := gcFixture(t)
	f.sendErr = errors.New("op run: 1Password is not reachable")

	out := f.at(7800).sweep()
	harness.Wants(t, out, "the message about the worktree sweep did not send and is left to the next check")

	f.sendErr = nil
	f.at(8100).sweep()
	harness.Equal(t, f.sentCount(), 1, "messages sent")
}

func TestADryRunSaysWhatItWouldReportAboutTheSweep(t *testing.T) {
	f := gcFixture(t)

	out := f.at(7800).dryRun()
	harness.Wants(t, out, "would report that the worktree sweep has not run for 2h10m")
	harness.Equal(t, f.sentCount(), 0, "messages sent")
}
