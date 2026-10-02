package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// The timeline every test below walks: an incident, a session, its report, and then the
// question nobody answers. Minutes rather than hours, because the thresholds are
// environment variables for exactly this reason.
//
// Twelve minutes to the reminder, thirty-three to the warning and thirty-six to the
// handover: the same three-to-eleven-to-twelve shape the real hour, 2h45 and three hours
// have, so the order the chain fires in is the real one.
const (
	remindAt   = 720
	warnAt     = 1980
	handoverAt = 2160
)

func waiting(t *testing.T) *fixture {
	t.Helper()

	f := newFixture(t)
	f.cfg.RemindAfter = remindAt * time.Second
	f.cfg.WarnAfter = warnAt * time.Second
	f.cfg.HandoverAfter = handoverAt * time.Second

	// The incident, the session, and the report that leaves the session on its question.
	f.grow("tmp/worker.log", 3*mb)
	f.at(0).sweep()
	f.grow("tmp/worker.log", 4*mb)
	f.at(300).sweep()

	// The file is still going on the run the question goes up on, which is what gives the
	// question numbers and a writer to be measured against later.
	f.notifyWithFallback(f.onlyPendingID(), "stop pid 4242 and empty the log")
	f.blocks("disk")
	f.grow("tmp/worker.log", 3*mb)
	f.at(600).sweep()

	return f
}

// The clock starts at the question rather than at the brief: the wait being measured is
// Tim's, and until the agent has asked him something there is nothing for him to answer.
func TestTheClockStartsWhenTheAgentIsFirstSeenOnItsQuestion(t *testing.T) {
	f := waiting(t)

	w := f.state().Waiting["disk"]
	equal(t, w.Since, base.Unix()+600, "when the question went up")
	equal(t, w.Default, "stop pid 4242 and empty the log", "the option read out of the report")
	equal(t, w.Asked.FreeKB, 500*gib, "the free space the question was asked with")
	equal(t, f.sentCount(), 1, "messages sent while the question is new")
}

func TestAReminderGoesOutAtAnHourAndOnlyOnce(t *testing.T) {
	f := waiting(t)

	equal(t, f.at(600+remindAt-300).sweep(), "", "the log before the reminder is due")
	equal(t, f.sentCount(), 1, "messages sent before the reminder is due")

	out := f.at(600 + remindAt).sweep()
	wants(t, out, "reminded about disk-")
	equal(t, f.sentCount(), 2, "messages sent once the reminder is due")

	wants(t, f.lastSent(), "is still waiting for you after 0h12m")
	wants(t, f.lastSent(), "still waiting for you in herdr (workspace .mac-mini, tab disk-0000)")
	// Re-measured this run rather than quoted from the question.
	wants(t, f.lastSent(), "Now on mac-mini: 500.0 GB free")

	// Never twice, however many checks go by.
	for at := 600 + remindAt + 300; at < 600+warnAt; at += 300 {
		lacks(t, f.at(int64(at)).sweep(), "reminded about")
	}
	equal(t, f.sentCount(), 2, "messages sent before the warning is due")
}

// A quarter of an hour's notice, with the option the session itself named: Tim answers
// that message by doing nothing if he agrees with it.
func TestTheWarningQuotesTheOptionTheSessionWouldFallBackOn(t *testing.T) {
	f := waiting(t)
	f.at(600 + remindAt).sweep()

	out := f.at(600 + warnAt).sweep()
	wants(t, out, "from the handover")
	equal(t, f.sentCount(), 3, "messages sent once the warning is due")

	wants(t, f.lastSent(), "In 0h03m the agent will decide and act on its own")
	wants(t, f.lastSent(), "If no answer: stop pid 4242 and empty the log")
	wants(t, f.lastSent(), "Now on mac-mini: 500.0 GB free")

	lacks(t, f.at(600+warnAt+60).sweep(), "from the handover")
	equal(t, f.sentCount(), 3, "messages sent after the warning")
}

// The handover itself: the question goes first, because herdr refuses a prompt to an
// agent still on one, and then the session is told to decide. Nothing of hachiko's own
// reaches the channel — the session's own message is what says what it did.
func TestTheDecisionIsHandedOverAtTheDeadlineAndOnlyOnce(t *testing.T) {
	f := waiting(t)
	f.at(600 + remindAt).sweep()
	f.at(600 + warnAt).sweep()

	out := f.at(600 + handoverAt).sweep()
	wants(t, out, "handed the decision on disk-")
	wants(t, out, "with no answer")
	equal(t, len(f.interrupts), 1, "questions cancelled")
	equal(t, f.sentCount(), 3, "messages sent by hachiko for the handover itself")

	handover := f.lastInterrupt()
	wants(t, handover, "Tim has not answered for 0h36m")
	wants(t, handover, "least destructive option that resolves it")
	wants(t, handover, "Spawn the oncall-partner agent")
	wants(t, handover, "Re-check the situation from scratch")
	wants(t, handover, "hachiko notify disk-")
	wants(t, handover, "What you said you would do if nobody answered: stop pid 4242 and empty the log")
	wants(t, handover, "Now on mac-mini: 500.0 GB free")

	// Once per incident. The agent is working on what it was handed, and a second
	// handover would be a second decision on one question.
	f.blocks("disk")
	for at := 600 + handoverAt + 300; at < 600+handoverAt+1500; at += 300 {
		lacks(t, f.at(int64(at)).sweep(), "handed the decision")
	}
	equal(t, len(f.interrupts), 1, "questions cancelled after the deadline")
}

// An answer stops the clock, and everything on it: the session is working on what he
// picked, and a reminder about a question he has already answered is noise.
func TestAnAnswerBeforeTheDeadlineStopsTheClock(t *testing.T) {
	f := waiting(t)

	// He picks an option, so herdr shows the agent working rather than blocked — and
	// hachiko did not cancel anything, so that is him.
	f.status["disk"] = statusWorking
	out := f.at(600 + remindAt).sweep()

	wants(t, out, "was answered after 0h12m")
	equal(t, len(f.state().Waiting), 0, "waits still being counted")
	equal(t, f.sentCount(), 1, "messages sent after the answer")

	for at := 600 + warnAt; at <= 600+handoverAt+600; at += 300 {
		equal(t, f.at(int64(at)).sweep(), "", "the log after the answer")
	}
	equal(t, f.sentCount(), 1, "messages sent after the deadline would have passed")
	equal(t, len(f.interrupts), 0, "questions cancelled after the answer")
}

// The session's second report is the outcome, which is the other way a wait ends.
func TestTheOutcomeReportEndsTheWait(t *testing.T) {
	f := waiting(t)
	incident := f.state().Waiting["disk"].Incident

	f.notify(incident)
	out := f.at(900).sweep()

	wants(t, out, "reported the outcome of "+incident)
	equal(t, len(f.state().Waiting), 0, "waits still being counted")
	equal(t, len(f.store.ReportedIDs()), 0, "markers left behind")
}

// The session depends on herdr and on Tim not having closed the tab, and a wait that
// handed a decision to nobody would be an incident reported as resolved and not touched.
func TestASessionClosedBeforeTheDeadlineGetsAMessageSayingNothingWasDone(t *testing.T) {
	f := waiting(t)

	f.status["disk"] = statusGone
	equal(t, f.at(600+warnAt).sweep(), "", "the log before the deadline with no agent")
	equal(t, f.sentCount(), 1, "messages sent before the deadline with no agent")

	out := f.at(600 + handoverAt).sweep()
	wants(t, out, "has no on-call agent left after 0h36m")
	equal(t, f.sentCount(), 2, "messages sent once the deadline passed with no agent")
	wants(t, f.lastSent(), "no on-call agent left to decide")
	wants(t, f.lastSent(), "nothing was done about it")
	wants(t, f.lastSent(), "Now on mac-mini: 500.0 GB free")

	equal(t, len(f.interrupts), 0, "questions cancelled with no agent")
	equal(t, len(f.state().Waiting), 0, "waits still being counted")
}

// A question whose options were written against a file half this size is the wrong
// question, so it goes and the session is asked again rather than being handed an update
// herdr would refuse.
func TestAFileDoublingWhileTheAgentWaitsCancelsTheQuestionAndAsksAgain(t *testing.T) {
	f := waiting(t)
	before := f.state().Waiting["disk"].Since

	path := f.grow("tmp/worker.log", 11*mb)
	out := f.at(900).sweep()

	wants(t, out, "has doubled to")
	wants(t, out, "its question was cancelled and it was asked again")
	equal(t, len(f.interrupts), 1, "questions cancelled")
	wants(t, f.lastInterrupt(), "your question has been cancelled")
	wants(t, f.lastInterrupt(), clip(path, pathLimit)+" has doubled to")
	wants(t, f.lastInterrupt(), "ask again with options that fit what it is now")

	// The clock keeps running from the first question: he has been unanswered since
	// then, and a writer that worsens every hour would otherwise push the deadline out
	// for ever.
	equal(t, f.state().Waiting["disk"].Since, before, "when the question went up")
	wants(t, f.lastInterrupt(), "still counted from the first question")

	// And nothing of hachiko's own went out about it: the session is going to ask again.
	equal(t, f.sentCount(), 1, "messages sent for the refresh")
}

// A threshold crossed either way is a question whose options were written for a disk
// with more room on it, or for one that has since recovered. Measured against the
// question rather than against the last check, because the question is what went stale.
func TestWhatCountsAsTheIncidentHavingMoved(t *testing.T) {
	asked := Waiting{Asked: Asked{
		At:      base.Unix(),
		FreeKB:  500 * gib,
		Level:   100,
		Sizes:   map[string]int64{"/private/tmp/a.log": 10 * mb, "/private/tmp/b.log": 4 * mb},
		Writers: []string{"4242 (worker)"},
	}}
	same := nowReading{
		free:    500 * gib,
		level:   100,
		sizes:   map[string]int64{"/private/tmp/a.log": 12 * mb, "/private/tmp/b.log": 4 * mb},
		writers: []string{"4242 (worker)"},
	}

	equal(t, materialChange(asked, same), "", "a question about an incident that has not moved")

	crossed := same
	crossed.level = 20
	wants(t, materialChange(asked, crossed), "free space crossed the 20 GB threshold")

	recovered := same
	recovered.level = 0
	wants(t, materialChange(asked, recovered), "free space is back over every threshold")

	doubled := same
	doubled.sizes = map[string]int64{"/private/tmp/a.log": 20 * mb, "/private/tmp/b.log": 4 * mb}
	wants(t, materialChange(asked, doubled), "/private/tmp/a.log has doubled to")

	joined := same
	joined.writers = []string{"4242 (worker)", "5151 (another)"}
	wants(t, materialChange(asked, joined), "a writer that was not there when the question was asked: 5151 (another)")

	// Nobody was recorded holding it when the question was asked, so there is nothing for
	// a writer found later to be new against — and reading it as new would cancel the
	// question on every incident whose file was between writes at that moment.
	unknown := asked
	unknown.Asked.Writers = nil
	equal(t, materialChange(unknown, joined), "", "a writer with none recorded to compare against")
}

// A fresh alert for the same kind while the agent is on its question: the brief cannot be
// delivered to a blocked agent, so the question goes and the session is asked again. The
// clock is Tim's and does not restart with it; the steps do, because the handover is a
// decision about an incident and this is a new one.
func TestANewIncidentWhileTheAgentWaitsCancelsTheQuestionAndKeepsTheClock(t *testing.T) {
	f := waiting(t)
	f.at(600 + remindAt).sweep()

	w := f.state().Waiting["disk"]
	first, since := w.Incident, w.Since
	equal(t, len(w.Steps), 1, "steps recorded before the new incident")

	f.freeGB = 90
	out := f.at(600 + remindAt + 300).sweep()

	wants(t, out, "only 90.0 GB free, under the 100 GB threshold")
	equal(t, len(f.interrupts), 1, "questions cancelled")
	wants(t, f.lastInterrupt(), "The situation changed")

	w = f.state().Waiting["disk"]
	if w.Incident == first {
		t.Fatal("the new incident did not replace the one the question was about")
	}
	equal(t, w.Since, since, "when Tim was first asked something")
	equal(t, len(w.Steps), 0, "steps carried over to the new incident")

	// And the working state that follows is hachiko's own doing, so the clock is not read
	// as having been answered.
	equal(t, f.at(600+remindAt+600).sweep(), "", "the log while the agent works on the new brief")
	equal(t, len(f.state().Waiting), 1, "waits still being counted")
}

func TestANewWriterWhileTheAgentWaitsCancelsTheQuestion(t *testing.T) {
	f := waiting(t)

	f.writer = "5151 (another-worker)"
	f.grow("tmp/worker.log", 3*mb)
	out := f.at(900).sweep()

	wants(t, out, "a writer that was not there when the question was asked: 5151 (another-worker)")
	equal(t, len(f.interrupts), 1, "questions cancelled")
}

// A file that grows at the rate it was growing when the question went up is the incident
// the question is about, so it is left alone.
func TestAnIncidentThatHasNotMovedLeavesTheQuestionAlone(t *testing.T) {
	f := waiting(t)

	f.grow("tmp/worker.log", 3*mb)
	equal(t, f.at(900).sweep(), "", "the log while the incident has not moved")
	equal(t, len(f.interrupts), 0, "questions cancelled")
	equal(t, f.sentCount(), 1, "messages sent while the incident has not moved")
}

// The three hours are for Tim being asleep, not for a disk that will be full before he
// wakes: when the projection says the critical threshold comes first, the decision goes
// over early and the session judges whether waiting was ever safe.
func TestADiskThatWillBeCriticalBeforeTheDeadlineHandsOverEarly(t *testing.T) {
	f := waiting(t)

	// Falling slowly enough at first that the threshold is hours away, and then fast
	// enough that it comes before the handover would.
	f.freeGB = 490
	equal(t, f.at(900).sweep(), "", "the log while the threshold is still hours away")

	f.freeGB = 300
	out := f.at(1200).sweep()

	wants(t, out, "handed the decision on disk-")
	wants(t, out, "early: free space reaches 20 GB in about")
	equal(t, len(f.interrupts), 1, "questions cancelled")

	early := f.lastInterrupt()
	wants(t, early, "getting worse rapidly")
	wants(t, early, "the three-hour handover would come too late")
	wants(t, early, "If you judge instead that it is about to stop by itself")
	wants(t, early, "with the oncall-partner agent's agreement first")

	// Early or not, it is one handover per incident.
	f.blocks("disk")
	f.freeGB = 200
	lacks(t, f.at(1500).sweep(), "handed the decision")
	equal(t, len(f.interrupts), 1, "questions cancelled after the early handover")
}

// The other trigger, which needs no rate at all: a quarter of what was free when he was
// asked is gone, so the options in front of him are about a different disk.
func TestAQuarterOfTheFreeSpaceGoingHandsOverEarly(t *testing.T) {
	f := waiting(t)

	// Not a rate the projection can reach the threshold on inside the horizon, but a
	// quarter of what the question was asked with.
	f.freeGB = 374
	out := f.at(600 + remindAt).sweep()

	wants(t, out, "handed the decision on disk-")
	wants(t, out, "GB of the 500.0 GB free when the question was asked is already gone")
	equal(t, len(f.interrupts), 1, "questions cancelled")
}

// Slowly is what the three hours are for. A file still growing at the rate it was and a
// disk with room on it is a question to leave in front of him.
func TestSlowGrowthDoesNotHandOverEarly(t *testing.T) {
	f := waiting(t)

	for at := int64(900); at < 600+warnAt; at += 300 {
		f.grow("tmp/worker.log", 1*mb)
		lacks(t, f.at(at).sweep(), "handed the decision")
	}
	equal(t, len(f.interrupts), 0, "questions cancelled")
}

// A step is done only once its message has left the machine: a webhook that was down for
// the one check the hour came up on would otherwise lose the reminder for good.
func TestAStepThatCouldNotSendIsTriedAgainOnTheNextCheck(t *testing.T) {
	f := waiting(t)

	f.sendErr = errSendFailed
	wants(t, f.at(600+remindAt).sweep(), "the remind step on disk-")
	equal(t, len(f.state().Waiting["disk"].Steps), 0, "steps recorded while the webhook was down")

	f.sendErr = nil
	wants(t, f.at(600+remindAt+300).sweep(), "reminded about disk-")
	equal(t, f.sentCount(), 2, "messages sent once the webhook was back")

	// And having sent, it is not sent again.
	lacks(t, f.at(600+remindAt+600).sweep(), "reminded about")
}

// The same for the handover, which is a prompt rather than a message: a herdr that could
// not be reached leaves the decision to the next check rather than recording it as made.
func TestAHandoverThatCouldNotBeDeliveredIsTriedAgainOnTheNextCheck(t *testing.T) {
	f := waiting(t)
	f.at(600 + remindAt).sweep()
	f.at(600 + warnAt).sweep()

	f.interruptErr = errors.New("the agent is still on its question after the esc")
	wants(t, f.at(600+handoverAt).sweep(), "could not be handed to the disk on-call agent")
	equal(t, len(f.state().Waiting["disk"].Steps), 2, "steps recorded while herdr refused")

	f.interruptErr = nil
	wants(t, f.at(600+handoverAt+300).sweep(), "handed the decision on disk-")
	equal(t, len(f.interrupts), 1, "questions cancelled")
}

// herdr not answering at all is not an answer from Tim, so the wait is left exactly as
// it was rather than being read as either.
func TestHerdrNotAnsweringLeavesTheWaitAsItWas(t *testing.T) {
	f := waiting(t)
	f.statusErr = errors.New("no server is listening")

	wants(t, f.at(600+handoverAt).sweep(), "herdr did not say what the disk on-call agent is doing")
	equal(t, len(f.state().Waiting), 1, "waits still being counted")
	equal(t, len(f.interrupts), 0, "questions cancelled")
	equal(t, f.sentCount(), 1, "messages sent")
}

// A dry run changes nothing and says nothing to anybody, the wait included.
func TestADryRunDoesNothingAboutAQuestionNobodyAnswered(t *testing.T) {
	f := waiting(t)

	out := f.at(600 + handoverAt).dryRun()
	for _, unwanted := range []string{"reminded about", "handed the decision", "was cancelled"} {
		lacks(t, out, unwanted)
	}
	equal(t, f.sentCount(), 1, "messages sent by a dry run")
	equal(t, len(f.interrupts), 0, "questions cancelled by a dry run")
	equal(t, len(f.state().Waiting["disk"].Steps), 0, "steps recorded by a dry run")
}

// The line the session puts in its own report, which is the only thing about the report
// hachiko keeps: it is a line Tim reads too, so saying it to him and saying it to hachiko
// are one act.
func TestTheFallbackOptionIsReadOutOfTheReport(t *testing.T) {
	for _, tc := range []struct{ report, want string }{
		{"Disk filling.\n\nIf no answer: stop pid 4242\n\nAttach: tab disk-1200", "stop pid 4242"},
		{"- If no answer: empty /private/tmp/devbackend.log", "empty /private/tmp/devbackend.log"},
		{"**If no answer:** nothing, it stops by itself", "nothing, it stops by itself"},
		{"if no answer:   trailing space   ", "trailing space"},
		{"Disk filling. Nothing else.", ""},
		{"If no answer:", ""},
	} {
		equal(t, fallbackOption(tc.report), tc.want, "the option read out of "+tc.report)
	}

	// Agent-written text about an incident whose paths were chosen by whatever filled the
	// disk, so it is clipped like every other such string.
	long := fallbackOption("If no answer: " + strings.Repeat("x", fallbackLimit+50))
	equal(t, len(long), fallbackLimit+3, "the length of a clipped option")
}
