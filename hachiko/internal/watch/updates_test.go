package watch

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/harness"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
	"github.com/timche/mac-mini/hachiko/internal/update"
)

// A step the nightly update left for the check to raise, in the shape the update writes one.
func handed(step, phrase, detail, action string, since time.Time) update.Handed {
	return update.Handed{Step: step, Phrase: phrase, Detail: detail, Action: action, Since: since.Unix()}
}

func failingUpdate(t *testing.T) *fixture {
	t.Helper()

	f := newFixture(t)
	f.cfg.RemindAfter = remindAt * time.Second
	f.cfg.WarnAfter = warnAt * time.Second
	f.cfg.HandoverAfter = handoverAt * time.Second
	f.threads = true
	// The action is the one the update writes for a step with nothing specific to suggest,
	// since the hand-off carries that line rather than leaving it to be written here.
	f.updateFailing = []update.Handed{handed("mise up", "upgrade the tools mise manages",
		"mise ERR failed to install node@24.22.0",
		"**Needs you:** nothing here tries it again before tomorrow night. The step is `mise up` in `hachiko update`.",
		f.now)}

	return f
}

// One night's failures are one incident with one post and one session on it, raised by the
// check rather than by the update: the update would be a second post in the feed about the
// same thing, and nothing it could post would have an agent behind it.
func TestANewlyFailingUpdateStepIsOneIncident(t *testing.T) {
	f := failingUpdate(t)

	out := f.at(0).sweep()

	harness.Equal(t, f.oncallName, statedir.KindUpdate, "the on-call agent's kind")
	harness.Equal(t, f.oncallCalls, 1, "sessions opened")
	harness.Equal(t, f.sentCount(), 1, "messages sent")

	incident := f.onlyPendingID()
	harness.Equal(t, incident, "update-"+strconv.FormatInt(base.Unix(), 10), "the incident id")
	harness.Equal(t, statedir.KindOf(incident), statedir.KindUpdate, "the kind the id splits into")
	harness.Equal(t, len(f.opened), 1, "posts opened")
	harness.Equal(t, f.state().Threads[incident], "thread-"+incident, "the post's thread")
	harness.Wants(t, out, "raised 1 step the nightly update could not do as "+incident)

	// Everything the agent has to work from, as data it was handed rather than as something
	// it has to go and find.
	for _, want := range []string{
		"⚠️ The nightly update could not upgrade the tools mise manages",
		"**Step:** `mise up`",
		"mise ERR failed to install node@24.22.0",
		"**Failing since:** less than a minute",
		"**Needs you:** nothing here tries it again before tomorrow night",
		"Incident id: " + incident,
		"hachiko notify " + incident,
	} {
		harness.Wants(t, f.oncallBrief, want)
	}
	harness.Wants(t, f.lastSent(), "⚠️ The nightly update could not upgrade the tools mise manages")
	harness.Wants(t, f.lastSent(), "An agent is looking into it")

	// Written back, which is the whole of how the next check knows it has already said this
	// and how the update knows where to say the step works again.
	harness.Equal(t, f.updateRaised["mise up"].Incident, incident, "the incident the step was raised as")
	harness.Equal(t, f.updateRaised["mise up"].Thread, "thread-"+incident, "the thread it was raised in")
}

// A step that keeps failing is one incident for as long as it does, the same rule every
// other thing hachiko says once obeys.
func TestAStepThatKeepsFailingIsNotRaisedAgain(t *testing.T) {
	f := failingUpdate(t)
	f.at(0).sweep()
	f.notify(f.onlyPendingID())

	for at := int64(300); at <= 1800; at += 300 {
		harness.Lacks(t, f.at(at).sweep(), "raised 1 step")
	}
	harness.Equal(t, f.sentCount(), 1, "messages sent")
	harness.Equal(t, f.oncallCalls, 1, "sessions opened")
}

// The guarantee behind the session holds here too: it needs herdr, a Claude login and usage
// left, and the raw details are what Tim gets when that chain breaks.
func TestAnUpdateSessionThatNeverReportsSendsTheRawDetails(t *testing.T) {
	f := failingUpdate(t)
	f.at(0).sweep()
	incident := f.onlyPendingID()

	out := f.at(600).sweep()

	harness.Wants(t, out, "the on-call session has not reported on "+incident)
	harness.Wants(t, f.lastSent(), "⚠️ No report from the agent on the nightly update after 10 minutes")
	harness.Wants(t, f.lastSent(), "**Step:** `mise up`")
	harness.Equal(t, f.sentTo[len(f.sentTo)-1], "thread-"+incident, "the thread it went into")
}

// All of a run's new failures in one incident: four posts and four agents on one night's
// update is four sets of options for Tim to reconcile.
func TestEveryStepOfOneRunGoesInOneIncident(t *testing.T) {
	f := failingUpdate(t)
	f.updateFailing = append(f.updateFailing, handed("brew cleanup", "clear what Homebrew replaced",
		"Permission denied @ apply2files", "**You can run:** `sudo rm -rf /opt/homebrew/Cellar/tailscale/1.104.1`", f.now))

	f.at(0).sweep()

	harness.Equal(t, f.sentCount(), 1, "messages sent")
	harness.Equal(t, f.oncallCalls, 1, "sessions opened")
	harness.Wants(t, f.lastSent(), "⚠️ The nightly update failed 2 steps")
	harness.Wants(t, f.lastSent(), "**Step:** `brew cleanup` — could not clear what Homebrew replaced")
	harness.Wants(t, f.lastSent(), "**Step:** `mise up` — could not upgrade the tools mise manages")
	harness.Wants(t, f.lastSent(), "sudo rm -rf /opt/homebrew/Cellar/tailscale/1.104.1")

	incident := f.onlyPendingID()
	harness.Equal(t, f.updateRaised["mise up"].Incident, incident, "the first step's incident")
	harness.Equal(t, f.updateRaised["brew cleanup"].Incident, incident, "the second step's incident")
}

// A step that failed on a later night is its own news, and it supersedes the one before it
// the way a second disk alert does: one agent per kind, one question in front of Tim.
func TestAStepThatFailsOnALaterNightIsItsOwnIncident(t *testing.T) {
	f := failingUpdate(t)
	f.at(0).sweep()
	first := f.onlyPendingID()

	f.updateFailing = append(f.updateFailing, handed("herdr update", "update herdr",
		"herdr: the release could not be downloaded", "", f.now))
	f.at(300).sweep()

	second := f.onlyPendingID()
	if second == first {
		t.Fatalf("the second night reused %s", first)
	}
	harness.Equal(t, f.sentCount(), 2, "messages sent")
	harness.Wants(t, f.lastSent(), "⚠️ The nightly update could not update herdr")
	harness.Equal(t, f.updateRaised["mise up"].Incident, first, "the first step's incident")
	harness.Equal(t, f.updateRaised["herdr update"].Incident, second, "the second step's incident")
}

// A send that failed leaves the incident unraised, so the next check tries again rather than
// recording a post nobody ever saw.
func TestARaiseThatDidNotSendIsTriedAgain(t *testing.T) {
	f := failingUpdate(t)
	f.sendErr = errSendFailed

	harness.Wants(t, f.at(0).sweep(), "the alert did not send and is left to the next check")
	harness.Equal(t, len(f.updateRaised), 0, "steps recorded as raised")

	f.sendErr = nil
	f.at(300).sweep()

	harness.Equal(t, f.sentCount(), 1, "messages sent")
	harness.Equal(t, len(f.updateRaised), 1, "steps recorded as raised")
}

// The update says in the post's own thread that the step works again and closes it, so what
// is left here is to stop waiting on it: a wait left open has the listener polling an
// archived post and `hachiko status` calling a resolved incident open for ever.
func TestAStepThatStopsFailingEndsTheWaitOnIt(t *testing.T) {
	f := failingUpdate(t)
	f.at(0).sweep()

	incident := f.onlyPendingID()
	f.notify(incident)
	f.blocks(statedir.KindUpdate)
	f.at(300).sweep()
	harness.Equal(t, f.state().Waiting[statedir.KindUpdate].Incident, incident, "the incident being waited on")

	f.updateFailing = nil
	out := f.at(600).sweep()

	harness.Wants(t, out, "the nightly update is no longer failing on mise up")
	harness.Wants(t, out, "nothing is failing under "+incident+" any more, so the wait on it ends")
	harness.Equal(t, len(f.state().Waiting), 0, "waits still open")
	harness.Equal(t, len(f.updateRaised), 0, "steps recorded as raised")
	harness.Equal(t, len(f.state().Threads), 0, "threads the listener still polls")
}

// Nothing of the machine's own autonomy reaches one of these, however long Tim takes: a step
// is retried by tomorrow night's run and by nothing else, and retrying one moves a version
// with nobody watching.
func TestAnUpdateIncidentIsNeverHandedOver(t *testing.T) {
	f := failingUpdate(t)
	f.at(0).sweep()

	f.notifyWithFallback(f.onlyPendingID(), "leave it to tomorrow night's run")
	f.blocks(statedir.KindUpdate)
	f.at(300).sweep()

	out := f.at(300 + remindAt).sweep()
	harness.Wants(t, out, "reminded about update-")
	harness.Wants(t, f.lastSent(), "⚠️ Still no answer on the nightly update after 12 minutes")
	// A reading of the disk says nothing about a step that failed.
	harness.Lacks(t, f.lastSent(), "**Now:**")

	out = f.at(300 + warnAt).sweep()
	harness.Wants(t, out, "said in the channel that nothing retries update-")
	harness.Wants(t, f.lastSent(), "⚠️ No answer on the nightly update, and nothing retries it")
	harness.Wants(t, f.lastSent(), "**If no answer:** leave it to tomorrow night's run")
	harness.Lacks(t, f.lastSent(), "the agent decides in")

	// Past the deadline, and nothing is handed to the agent and nothing more is said.
	for at := int64(300 + handoverAt); at <= 300+handoverAt+1800; at += 300 {
		out := f.at(at).sweep()
		harness.Lacks(t, out, "handed the decision")
	}
	harness.Equal(t, len(f.interrupts), 0, "questions cancelled")
	harness.Equal(t, len(f.prompts), 0, "prompts sent")
	harness.Equal(t, f.sentCount(), 3, "messages sent")

	w := f.state().Waiting[statedir.KindUpdate]
	harness.Equal(t, statedir.Contains(w.Steps, stepHandover), false, "the handover step")
	harness.Equal(t, statedir.Contains(w.Steps, stepEarly), false, "the early handover step")
}

// The free space falling away under an update incident is not that incident getting worse,
// and the projection behind an early handover is about a disk rather than about a step.
func TestAnUpdateIncidentIsNeverHandedOverEarly(t *testing.T) {
	f := failingUpdate(t)
	f.at(0).sweep()

	f.notify(f.onlyPendingID())
	f.blocks(statedir.KindUpdate)
	f.at(300).sweep()

	// A quarter of the free space gone since the question went up, which is the measurement
	// that cuts the three hours short on a disk incident.
	f.freeGB = 100
	harness.Lacks(t, f.at(600).sweep(), "handed the decision")
	harness.Lacks(t, f.at(900).sweep(), "handed the decision")

	harness.Equal(t, len(f.interrupts), 0, "questions cancelled")
	harness.Equal(t, f.state().Waiting[statedir.KindUpdate].Worsening, int64(0), "the worsening clock")
}

// A question about a step is not one a disk can make stale: a free-space mark crossed in
// either direction, a file that doubled or a writer that was not there before are all about
// the other incident.
func TestADiskThatMovesDoesNotCancelAnUpdateQuestion(t *testing.T) {
	f := failingUpdate(t)
	f.at(0).sweep()

	f.notify(f.onlyPendingID())
	f.blocks(statedir.KindUpdate)
	f.at(300).sweep()

	f.freeGB = 46
	harness.Lacks(t, f.at(600).sweep(), "changed while the update on-call agent was waiting")
	harness.Equal(t, len(f.interrupts), 0, "questions cancelled")
}

// A dry run reaches the hand-off and changes nothing in it.
func TestADryRunRaisesNoUpdateIncident(t *testing.T) {
	f := failingUpdate(t)

	out := f.dryRun()

	harness.Wants(t, out, "would raise 1 step the nightly update could not do as one incident")
	harness.Equal(t, f.oncallCalls, 0, "sessions opened")
	harness.Equal(t, f.sentCount(), 0, "messages sent")
	harness.Equal(t, len(f.updateRaised), 0, "steps recorded as raised")
}

// Writing back which incident a step was raised as is what stops the next check raising it a
// second time, so a write that failed says so rather than going quiet.
func TestAWriteBackThatFailedIsSaid(t *testing.T) {
	f := failingUpdate(t)
	f.updateRaisedErr = errors.New("read-only file system")

	out := f.at(0).sweep()
	harness.Wants(t, out, "could not be written down, so the next check may raise one of them a second time")
}
