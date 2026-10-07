package gc

import (
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// A sweep whose compose down keeps failing, which is the shape every rule below is about.
func failingFixture(t *testing.T) *fixture {
	t.Helper()

	f := newFixture(t)
	gone := f.herdrWorktree("app", "gone", false)
	f.containers = []Container{{Project: "gc-gone", WorkingDir: gone}}
	f.downErr["gc-gone"] = "Error response from daemon: conflict"

	return f
}

// Six messages an hour about a volume docker will not remove is a notification Tim turns
// off, and then the next one is one he never sees.
func TestAFailureThatPersistsIsOneMessage(t *testing.T) {
	f := failingFixture(t)

	f.at(0).sweep()
	harness.Equal(t, len(f.sent), 1, "messages sent")

	f.at(600).sweep()
	f.at(1200).sweep()
	harness.Equal(t, len(f.sent), 1, "messages sent")
}

// One failure, one thread: the line that says it cleared lands under the one that said it
// had not.
func TestAFailureThatClearsIsOneMoreLineInTheSameThread(t *testing.T) {
	f := failingFixture(t)
	f.at(0).sweep()

	harness.Equal(t, f.sent[0].OpenThread, "compose-down gc-gone", "the thread opened")
	harness.Equal(t, f.sent[0].Thread, "", "the thread the first message went into")

	delete(f.downErr, "gc-gone")
	f.at(3900).sweep()

	harness.Equal(t, len(f.sent), 2, "messages sent")
	harness.Equal(t, f.sent[1].Thread, "thread-1", "the thread the clear went into")
	harness.Wants(t, f.lastSent(), "🟢 A removed worktree's containers are gone now")
	harness.Wants(t, f.lastSent(), "**Failing since:** 1 hour 5 minutes")

	// Nothing of it is left to say anything about.
	harness.Equal(t, len(f.state().Posted), 0, "failures on record")
}

// A send that failed is a failure nobody has heard about, so the next sweep says it again.
func TestASendThatFailedIsTriedAgainNextSweep(t *testing.T) {
	f := failingFixture(t)
	f.sendErr = "op run: 1Password is not reachable"

	out := f.at(0).sweep()
	harness.Wants(t, out, "the message about this did not send and is left to the next sweep")
	harness.Equal(t, len(f.sent), 0, "messages sent")

	f.sendErr = ""
	f.at(600).sweep()
	harness.Equal(t, len(f.sent), 1, "messages sent")

	// The clock on it runs from the sweep that found it rather than from the one that
	// managed to say so.
	harness.Wants(t, f.lastSent(), "**Failing since:** 10 minutes")
}

// A failure that clears before anybody heard about it is nothing to announce.
func TestAFailureNobodyHeardAboutClearsInSilence(t *testing.T) {
	f := failingFixture(t)
	f.sendErr = "op run: 1Password is not reachable"
	f.at(0).sweep()

	f.sendErr = ""
	delete(f.downErr, "gc-gone")
	f.at(600).sweep()

	harness.Equal(t, len(f.sent), 0, "messages sent")
	harness.Equal(t, len(f.state().Posted), 0, "failures on record")
}

// Two failures are two notifications, because each is a different thing left undone.
func TestTwoFailuresAreTwoMessages(t *testing.T) {
	f := failingFixture(t)
	f.containers = append(f.containers, Container{
		Project: "gc-also", WorkingDir: f.herdrWorktree("app", "also", false),
	})
	f.downErr["gc-also"] = "Error response from daemon: conflict"

	f.at(0).sweep()
	harness.Equal(t, len(f.sent), 2, "messages sent")
	harness.Equal(t, f.sent[0].OpenThread, "compose-down gc-also", "the first thread opened")
	harness.Equal(t, f.sent[1].OpenThread, "compose-down gc-gone", "the second thread opened")
}

// Routine cleanup is a log and nothing else: Tim hears about a sweep only when it could not
// do what it set out to.
func TestASweepThatWorkedSendsNothing(t *testing.T) {
	f := newFixture(t)
	removed := f.herdrWorktree("app", "gone", false)
	f.containers = []Container{{Project: "gc-gone", WorkingDir: removed}}
	f.sitting(4242, accountUID, removed, "node")

	out := f.sweep()
	harness.Wants(t, out, "removing compose project gc-gone")
	harness.Wants(t, out, "killing 4242")
	harness.Equal(t, len(f.sent), 0, "messages sent")
}

// A project name the daemon could have been given anything for is not one to put in a
// command Tim pastes.
func TestACommandIsOnlyOfferedForANameDockerWouldTake(t *testing.T) {
	f := failingFixture(t)
	f.containers[0].Project = "gc gone; rm -rf /"
	f.downErr["gc gone; rm -rf /"] = "Error response from daemon: conflict"

	f.at(0).sweep()
	harness.Lacks(t, f.lastSent(), "docker compose -p")
	harness.Wants(t, f.lastSent(), "**Needs you:**")
}
