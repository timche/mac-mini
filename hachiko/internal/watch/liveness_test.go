package watch

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/harness"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
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

// Ten minutes, which is longer than the whole of a retry ladder against an unreachable remote
// and shorter than an hour's writing.
func syncFixture(t *testing.T) *fixture {
	t.Helper()

	f := newFixture(t)
	f.cfg.SyncStaleAfter = 10 * time.Minute
	f.cfg.SyncDirtyAfter = 10 * time.Minute
	f.cfg.SyncLateAfter = 30 * time.Minute
	f.cfg.SyncPlist = "/Users/x/Library/LaunchAgents/io.github.timche.hachiko-sync.plist"
	f.syncBeat = base

	return f
}

// Sync's ten minutes, which also has to cover the five its agent throttles a restart by: a
// listener that exited for a new binary inside its first five minutes is away for the
// remainder of them.
func listenerFixture(t *testing.T) *fixture {
	t.Helper()

	f := newFixture(t)
	f.cfg.ListenStaleAfter = 10 * time.Minute
	f.cfg.ListenPlist = "/Users/x/Library/LaunchAgents/io.github.timche.hachiko-listen.plist"
	f.listenBeat = base
	f.listenBeating = true

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

// An agent removed while the watch had an alert open on it. Silence under a ⚠️ is the one
// thing this watch never leaves: without this the last word in the thread is a warning about
// an agent that has since ceased to exist.
func TestASweepAgentRemovedWhileAlertedGetsOneLastLine(t *testing.T) {
	f := gcFixture(t)
	f.threads = true
	f.at(7800).sweep()
	harness.Equal(t, f.sentCount(), 1, "messages sent")

	f.gcInstalled = false
	out := f.at(8100).sweep()

	harness.Wants(t, out, "the worktree sweep is no longer installed, so nothing further is said about it")
	harness.Equal(t, f.sentCount(), 2, "messages sent")
	harness.Wants(t, f.lastSent(), "ℹ️ The worktree sweep is no longer installed")
	harness.Equal(t, f.sentTo[1], "thread-io.github.timche.hachiko-gc", "where the last line went")

	// And nothing further, as it says.
	harness.Equal(t, f.at(8400).sweep(), "", "the log of the check after that")
	harness.Equal(t, len(f.state().Agents), 0, "what is left open about any agent")
}

func TestADryRunSaysWhatItWouldReportAboutTheSweep(t *testing.T) {
	f := gcFixture(t)

	out := f.at(7800).dryRun()
	harness.Wants(t, out, "would report that the worktree sweep has not run for 2h10m")
	harness.Equal(t, f.sentCount(), 0, "messages sent")
}

func TestASyncInsideTheHeartbeatSaysNothing(t *testing.T) {
	f := syncFixture(t)

	harness.Equal(t, f.at(540).sweep(), "", "the log of a check with sync beating")
	harness.Equal(t, f.sentCount(), 0, "messages sent")
}

func TestASyncThatHasStoppedIsOneMessageAndNoSession(t *testing.T) {
	f := syncFixture(t)
	out := f.at(3900).sweep()

	harness.Wants(t, out, "sync has not run for 1h05m, so nothing is committing or pushing what gets written")

	harness.Equal(t, f.sentCount(), 1, "messages sent")
	harness.Wants(t, f.lastSent(), "⚠️ Auto-sync has not run for 1 hour 5 minutes")
	harness.Wants(t, f.lastSent(), "**Last heartbeat:** ")
	harness.Wants(t, f.lastSent(), "**You can run:** `launchctl kickstart -k gui/501/io.github.timche.hachiko-sync`")
	harness.Wants(t, f.lastSent(), "~/Library/Logs/hachiko-sync.log")

	// Not an incident: the answer is already written in the message, and it is Tim's.
	harness.Equal(t, f.oncallCalls, 0, "sessions opened")

	// One message while it is still not running, not twelve an hour.
	f.at(4200).sweep()
	f.at(4500).sweep()
	harness.Equal(t, f.sentCount(), 1, "messages sent")
}

func TestASyncThatComesBackIsOneMoreLineInTheSameThread(t *testing.T) {
	f := syncFixture(t)
	f.threads = true
	f.at(3900).sweep()

	harness.Equal(t, f.opened[0], "io.github.timche.hachiko-sync", "the thread opened")

	f.syncBeat = base.Add(4200 * time.Second)
	out := f.at(4200).sweep()

	harness.Wants(t, out, "sync is running again")
	harness.Equal(t, f.sentCount(), 2, "messages sent")
	harness.Wants(t, f.lastSent(), "🟢 Auto-sync is running again")
	harness.Equal(t, f.sentTo[1], "thread-io.github.timche.hachiko-sync", "where the clear went")
}

// A fresh Mac has no heartbeat because sync has never run, and alerting on that would fire on
// every machine this repository provisions until install.sh reaches the agent.
func TestAMacWithNoSyncAgentIsNeverReportedAsHavingStopped(t *testing.T) {
	f := syncFixture(t)
	f.syncInstalled = false

	harness.Equal(t, f.at(100000).sweep(), "", "the log of a check on a Mac with no sync agent")
	harness.Equal(t, f.sentCount(), 0, "messages sent")
}

func TestAMessageAboutSyncThatDidNotSendIsSaidAgain(t *testing.T) {
	f := syncFixture(t)
	f.sendErr = errors.New("op run: 1Password is not reachable")

	out := f.at(3900).sweep()
	harness.Wants(t, out, "the message about sync did not send and is left to the next check")

	f.sendErr = nil
	f.at(4200).sweep()
	harness.Equal(t, f.sentCount(), 1, "messages sent")
}

func TestADryRunSaysWhatItWouldReportAboutSync(t *testing.T) {
	f := syncFixture(t)

	out := f.at(3900).dryRun()
	harness.Wants(t, out, "would report that sync has not run for 1h05m")
	harness.Equal(t, f.sentCount(), 0, "messages sent")
}

func TestAListenerInsideItsHeartbeatSaysNothing(t *testing.T) {
	f := listenerFixture(t)

	harness.Equal(t, f.at(540).sweep(), "", "the log of a check with the listener beating")
	harness.Equal(t, f.sentCount(), 0, "messages sent")
}

func TestAListenerThatHasStoppedIsOneMessageAndNoSession(t *testing.T) {
	f := listenerFixture(t)
	out := f.at(3900).sweep()

	harness.Wants(t, out, "the Discord listener has not run for 1h05m, so a reply in an incident thread is reaching nobody")

	harness.Equal(t, f.sentCount(), 1, "messages sent")
	harness.Wants(t, f.lastSent(), "⚠️ The Discord listener has not run for 1 hour 5 minutes")
	harness.Wants(t, f.lastSent(), "**Last heartbeat:** ")
	harness.Wants(t, f.lastSent(), "**You can run:** `launchctl kickstart -k gui/501/io.github.timche.hachiko-listen`")
	harness.Wants(t, f.lastSent(), "~/Library/Logs/hachiko-listen.log")

	harness.Equal(t, f.oncallCalls, 0, "sessions opened")

	f.at(4200).sweep()
	harness.Equal(t, f.sentCount(), 1, "messages sent")
}

func TestAListenerThatComesBackIsOneMoreLineInTheSameThread(t *testing.T) {
	f := listenerFixture(t)
	f.threads = true
	f.at(3900).sweep()

	harness.Equal(t, f.opened[0], "io.github.timche.hachiko-listen", "the thread opened")

	f.listenBeat = base.Add(4200 * time.Second)
	out := f.at(4200).sweep()

	harness.Wants(t, out, "the Discord listener is running again")
	harness.Wants(t, f.lastSent(), "🟢 The Discord listener is running again")
	harness.Equal(t, f.sentTo[1], "thread-io.github.timche.hachiko-listen", "where the clear went")
}

// Answering from Discord ships off, and a listener that is off idles and writes no
// heartbeat. Counting from the plist the way the other two do would alert on every Mac this
// repository provisions, for ever, about a feature nobody turned on.
func TestAListenerThatIsOffIsNeverReportedAsHavingStopped(t *testing.T) {
	f := listenerFixture(t)
	f.listenBeating = false

	harness.Equal(t, f.at(100000).sweep(), "", "the log of a check with answering from Discord off")
	harness.Equal(t, f.sentCount(), 0, "messages sent")
}

// Turning the feature off while an alert is open. Silence under a ⚠️ is the one thing this
// watch never leaves: the listener being off answers "it has stopped" as much as its coming
// back does.
func TestAListenerSwitchedOffClearsTheAlertOpenOnIt(t *testing.T) {
	f := listenerFixture(t)
	f.threads = true
	f.at(3900).sweep()
	harness.Equal(t, f.sentCount(), 1, "messages sent")

	f.listenBeating = false
	out := f.at(4200).sweep()

	harness.Wants(t, out, "answering from Discord is switched off, so nothing further is said about the listener")
	harness.Equal(t, f.sentCount(), 2, "messages sent")
	harness.Wants(t, f.lastSent(), "ℹ️ Answering from Discord is switched off")
	harness.Equal(t, f.sentTo[1], "thread-io.github.timche.hachiko-listen", "where the line went")

	// And nothing is left open about it, so turning it back on and stopping again is a fresh
	// alert rather than one nobody hears about.
	harness.Equal(t, len(f.state().Agents), 0, "what is left open about any agent")
	harness.Equal(t, f.at(4500).sweep(), "", "the log of the check after that")
}

func TestADryRunSaysWhatItWouldReportAboutTheListener(t *testing.T) {
	f := listenerFixture(t)

	out := f.at(3900).dryRun()
	harness.Wants(t, out, "would report that the Discord listener has not run for 1h05m")
	harness.Equal(t, f.sentCount(), 0, "messages sent")
}

// Every one of them tells Tim to read its own log. A message that named the agent's plist
// label instead sent him to a file that does not exist.
func TestEachAgentsMessageNamesItsOwnLog(t *testing.T) {
	for _, agent := range []struct {
		fixture func(*testing.T) *fixture
		log     string
	}{
		{gcFixture, "`~/Library/Logs/hachiko-gc.log`"},
		{syncFixture, "`~/Library/Logs/hachiko-sync.log`"},
		{listenerFixture, "`~/Library/Logs/hachiko-listen.log`"},
	} {
		f := agent.fixture(t)
		f.at(100000).sweep()

		harness.Equal(t, f.sentCount(), 1, "messages sent")
		harness.Wants(t, f.lastSent(), agent.log)
		harness.Lacks(t, f.lastSent(), ".plist")
	}
}

// A Mac is upgraded by the wrapper moving a new binary into place under the running agents,
// so the first state file a new binary reads is the last one's. An alert open at that moment
// has to clear into the thread that raised it rather than be forgotten and raised again.
func TestAnAlertOpenInTheOlderStateShapeStillClearsIntoItsThread(t *testing.T) {
	f := gcFixture(t)
	f.threads = true

	if err := os.MkdirAll(f.cfg.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	harness.WriteFile(t, filepath.Join(f.cfg.StateDir, "state.json"),
		`{"gc_stale":1700000000,"gc_thread":"thread-io.github.timche.hachiko-gc"}`)

	f.gcStamp = base.Add(300 * time.Second)
	out := f.at(300).sweep()

	harness.Wants(t, out, "the worktree sweep is running again")
	harness.Equal(t, f.sentCount(), 1, "messages sent")
	harness.Equal(t, f.sentTo[0], "thread-io.github.timche.hachiko-gc", "where the clear went")

	// And nothing of the older shape survives the save, so nothing below it has two places
	// to look.
	harness.Equal(t, f.state().GCStale, int64(0), "the stale field the older shape kept")
	harness.Equal(t, f.state().Agent(statedir.AgentGC).Thread, "", "the thread left open")
}
