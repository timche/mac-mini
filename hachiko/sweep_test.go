package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestQuietCheckSaysNothingAndSendsNothing(t *testing.T) {
	f := newFixture(t)
	f.grow("tmp/worker.log", 3*mb)

	equal(t, f.sweep(), "", "a quiet check's log")
	equal(t, f.sentCount(), 0, "messages sent")
	equal(t, f.oncallCalls, 0, "sessions opened")
}

// The order the alert goes out in: the session first, then one short message naming
// the tab it is waiting in, and nothing else from hachiko while that session has a
// report to make.
func TestNewIncidentOpensTheSessionThenSendsOneShortMessage(t *testing.T) {
	f := newFixture(t)
	f.grow("tmp/worker.log", 3*mb)
	f.at(0).sweep()

	path := f.grow("tmp/worker.log", 4*mb)
	out := f.at(300).sweep()

	wants(t, out, "growing fast: "+path)
	wants(t, out, "written by 4242 (fake-worker)")

	equal(t, f.oncallCalls, 1, "sessions opened")
	equal(t, f.oncallName, "disk", "the session's name")
	equal(t, f.sentCount(), 1, "messages sent")

	wants(t, f.lastSent(), "Disk: "+path+" growing")
	wants(t, f.lastSent(), "An agent is looking into it in herdr (workspace .mac-mini, tab disk-0000); details to follow.")
	// The short message is a headline; the detail is the session's to report.
	lacks(t, f.lastSent(), "fake-worker")

	wants(t, f.oncallBrief, "hachiko notify disk-")
	wants(t, f.oncallBrief, "fake-worker")
}

// The session reporting is what the incident was waiting for, so nothing of
// hachiko's own goes out afterwards however long the file keeps growing.
func TestAReportInsideTheDeadlineLeavesNothingMoreToSend(t *testing.T) {
	f := newFixture(t)
	f.grow("tmp/worker.log", 3*mb)
	f.at(0).sweep()
	f.grow("tmp/worker.log", 4*mb)
	f.at(300).sweep()

	f.at(420).notify(f.onlyPendingID())
	equal(t, len(f.state().Pending), 0, "pending incidents after a report")

	f.grow("tmp/worker.log", 4*mb)
	equal(t, f.at(900).sweep(), "", "the log after a report")
	equal(t, f.sentCount(), 1, "messages sent")
}

// The session needs herdr, a Claude login and usage left; a watch that only ever
// spoke through it would be silent exactly when that chain broke.
func TestASessionThatNeverReportsGetsTheRawDetailsAfterTheDeadline(t *testing.T) {
	f := newFixture(t)
	f.grow("tmp/worker.log", 3*mb)
	f.at(0).sweep()
	f.grow("tmp/worker.log", 4*mb)
	f.at(300).sweep()

	out := f.at(900).sweep()
	wants(t, out, "has not reported on disk-")

	equal(t, f.sentCount(), 2, "messages sent")
	wants(t, f.lastSent(), "The on-call agent has not reported after 10 minutes")
	wants(t, f.lastSent(), "written by 4242 (fake-worker)")
	equal(t, len(f.state().Pending), 0, "pending incidents after the fallback")

	equal(t, f.at(1200).sweep(), "", "the log after the fallback")
	equal(t, f.sentCount(), 2, "messages sent after the fallback")
}

func TestASessionThatCannotBeOpenedGetsTheRawDetailsOnTheSameRun(t *testing.T) {
	f := newFixture(t)
	f.oncallErr = errors.New("herdr agent list failed")

	f.grow("tmp/worker.log", 3*mb)
	f.at(0).sweep()
	f.grow("tmp/worker.log", 4*mb)
	out := f.at(300).sweep()

	wants(t, out, "no on-call session was opened")
	equal(t, f.sentCount(), 1, "messages sent")
	wants(t, f.lastSent(), "On-call session could not start.")
	wants(t, f.lastSent(), "written by 4242 (fake-worker)")
	equal(t, len(f.state().Pending), 0, "pending incidents with no session")
}

// 20 below 100, so a disk still filling after the first alert says so once more, and
// nothing is sent again until it is back over 100. Under 20 the first message carries
// the whole of it rather than a line: there is no time to wait for a reading.
func TestLowSpaceAlertsOnceThenAgainUnderTheCriticalThresholdThenReportsTheRecovery(t *testing.T) {
	f := newFixture(t)

	f.freeGB = 90
	wants(t, f.at(0).sweep(), "only 90.0 GB free, under the 100 GB threshold")
	equal(t, f.sentCount(), 1, "messages sent")
	wants(t, f.lastSent(), "Disk: only 90.0 GB free")

	equal(t, f.at(300).sweep(), "", "the log while nothing changed")
	equal(t, f.sentCount(), 1, "messages sent while nothing changed")

	f.freeGB = 15
	wants(t, f.at(600).sweep(), "under the 20 GB threshold")
	equal(t, f.sentCount(), 2, "messages sent after the escalation")
	wants(t, f.lastSent(), "under the 20 GB threshold")

	f.freeGB = 500
	wants(t, f.at(900).sweep(), "free space is back over 100 GB")
	equal(t, f.sentCount(), 2, "messages sent after the recovery")
	equal(t, f.state().LowSpaceLevel, int64(0), "the recorded threshold after the recovery")
}

// A truncate and never an rm or a kill, and only the fastest grower: the log under
// the tmp root goes, the file beside it that is not named like a log stays, and so
// does the one outside the three roots a log may live in.
func TestUnderTheCriticalThresholdTheFastestGrowingLogIsTruncatedAndNothingElse(t *testing.T) {
	f := newFixture(t)
	f.freeGB = 15

	log := f.grow("tmp/worker.log", 2*mb)
	bin := f.grow("tmp/worker.bin", 2*mb)
	other := f.grow("elsewhere/other.log", 2*mb)
	f.at(0).sweep()

	f.grow("tmp/worker.log", 9*mb)
	f.grow("tmp/worker.bin", 6*mb)
	f.grow("elsewhere/other.log", 6*mb)

	wants(t, f.at(300).sweep(), "truncated "+log)

	equal(t, size(t, log), int64(0), "the truncated log")
	if size(t, bin) == 0 || size(t, other) == 0 {
		t.Error("a file that is not a log this may truncate was truncated")
	}

	wants(t, f.lastSent(), "truncated "+log)
	wants(t, f.lastSent(), "An agent is looking into it")
}

func TestNothingIsTruncatedWhenTheFastestGrowingFileIsNotALog(t *testing.T) {
	f := newFixture(t)
	f.freeGB = 15

	log := f.grow("tmp/worker.log", 2*mb)
	data := f.grow("elsewhere/data.bin", 2*mb)
	f.at(0).sweep()

	f.grow("tmp/worker.log", 4*mb)
	f.grow("elsewhere/data.bin", 9*mb)

	wants(t, f.at(300).sweep(), "not a log this may truncate")
	if size(t, log) == 0 || size(t, data) == 0 {
		t.Error("something was truncated")
	}
	equal(t, len(f.truncated), 0, "truncations")
}

// A send that failed is an incident nobody has heard about, so it may not be recorded
// as one: the next check has to try again.
func TestASendThatFailsLeavesTheIncidentUnraisedForTheNextCheck(t *testing.T) {
	f := newFixture(t)
	f.sendErr = errSendFailed

	f.grow("tmp/worker.log", 3*mb)
	f.at(0).sweep()
	f.grow("tmp/worker.log", 4*mb)

	wants(t, f.at(300).sweep(), "did not send and is left to the next check")
	equal(t, len(f.state().AlertedFiles), 0, "files recorded as alerted")
	equal(t, len(f.state().Pending), 0, "pending incidents")

	f.grow("tmp/worker.log", 4*mb)
	f.at(600).sweep()
	equal(t, f.sendAttempts, 2, "send attempts")
	equal(t, f.oncallCalls, 2, "sessions opened")
}

func TestADryRunReportsWhatItSeesAlertsNothingAndLeavesNoState(t *testing.T) {
	f := newFixture(t)
	f.freeGB = 15
	f.grow("tmp/worker.log", 4*mb)

	out := f.dryRun()
	wants(t, out, "would report 15.0 GB free, under the 20 GB threshold")
	wants(t, out, "would open an on-call session as disk and send: Disk: only")
	wants(t, out, "dry run over")

	if _, err := os.Stat(filepath.Join(f.cfg.StateDir, "state.json")); err == nil {
		t.Error("a dry run wrote state")
	}
	equal(t, f.sentCount(), 0, "messages sent")
	equal(t, f.oncallCalls, 0, "sessions opened")
}

func TestADryRunSaysWhatItWouldTruncateAndTruncatesNothing(t *testing.T) {
	f := newFixture(t)
	log := f.grow("tmp/worker.log", 2*mb)
	f.at(0).sweep()

	f.freeGB = 15
	f.grow("tmp/worker.log", 4*mb)

	wants(t, f.at(300).dryRun(), "would truncate "+log)
	if size(t, log) == 0 {
		t.Error("a dry run truncated the file")
	}
	equal(t, f.state().Disk.At, base.Unix(), "the sample a dry run left behind")
}

// Twelve samples at 80% of a core, which is the hour the rule is about, then a
// thirteenth that must say nothing: the process is the same incident.
func TestAProcessOverHalfACoreForTheWholeWindowAlertsOnceAndNotAgain(t *testing.T) {
	f := newFixture(t)
	out := f.cpuRuns(13, 240)

	wants(t, out, "hot for an hour: pid 7018 node")
	wants(t, out, "80% of a core")
	equal(t, f.oncallCalls, 1, "sessions opened")
	equal(t, f.oncallName, "cpu", "the session's name")
	equal(t, f.sentCount(), 1, "messages sent")
	wants(t, f.lastSent(), "CPU: node pid 7018 at 80% of a core")

	f.proc(7018, 3360, firstStart, "/usr/local/bin/node worker.js")
	equal(t, f.at(3900).sweep(), "", "the log on the next check")
	equal(t, f.oncallCalls, 1, "sessions opened after the next check")
}

// The window has to be unbroken: a process that goes quiet for one sample has not
// been busy for an hour, whatever it did before.
func TestAProcessThatDropsBelowHalfACoreBeforeTheWindowIsOutDoesNotAlert(t *testing.T) {
	f := newFixture(t)
	f.cpuRuns(12, 240)
	equal(t, f.sentCount(), 0, "messages sent before the window is out")

	f.proc(7018, 2670, firstStart, "/usr/local/bin/node worker.js")
	equal(t, f.at(3600).sweep(), "", "the log after a quiet sample")
	equal(t, f.sentCount(), 0, "messages sent after a quiet sample")
}

func TestAnAllowlistedCommandBurningAWholeCoreIsNeverReported(t *testing.T) {
	f := newFixture(t)
	f.allow("# the docker VM\nnode\n")

	out := f.cpuRuns(13, 300)
	lacks(t, out, "hot for an hour")
	equal(t, f.sentCount(), 0, "messages sent")
}

// A pid is reused, and the history behind one belongs to whoever held it: the same
// number with a different start time is a different process and starts again.
func TestAReusedPidStartsItsHourAgain(t *testing.T) {
	f := newFixture(t)
	f.cpuRuns(12, 240)
	equal(t, f.sentCount(), 0, "messages sent before the reuse")

	const reused = "Tue Sep 29 09:00:00 2026"
	f.proc(7018, 30, reused, "/usr/local/bin/node worker.js")
	equal(t, f.at(3600).sweep(), "", "the log on the first sample of the new process")

	f.proc(7018, 270, reused, "/usr/local/bin/node worker.js")
	equal(t, f.at(3900).sweep(), "", "the log on the second sample of the new process")
	equal(t, f.sentCount(), 0, "messages sent after the reuse")
}

// The shape that caused the incident this exists for: a worker whose session ended,
// reparented to launchd and still spending a core on work nobody wants.
func TestAHotProcessOrphanedInsideACheckoutIsNamedAsOne(t *testing.T) {
	f := newFixture(t)
	checkout := filepath.Join(f.cfg.ProjectsRoot, "repeek")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	f.cwd[7018] = checkout

	out := f.cpuRuns(13, 240)
	wants(t, out, "ppid 1 (orphaned)")
	wants(t, out, "in the repeek checkout")
	wants(t, out, "the session that started it is gone")
}

// One message and one session for a run, however many things fired in it: two agents
// on one machine would be two sets of options for Tim to reconcile.
func TestADiskAndACPUIncidentInOneRunMakeOneMessageAndOneSession(t *testing.T) {
	f := newFixture(t)
	f.grow("tmp/worker.log", 2*mb)
	f.cpuRuns(12, 240)
	equal(t, f.sentCount(), 0, "messages sent before either fired")

	f.proc(7018, 2880, firstStart, "/usr/local/bin/node worker.js")
	f.grow("tmp/worker.log", 4*mb)
	f.at(3600).sweep()

	equal(t, f.oncallCalls, 1, "sessions opened")
	equal(t, f.oncallName, "disk", "the session's name")
	equal(t, f.sentCount(), 1, "messages sent")
	wants(t, f.oncallBrief, "worker.log")
	wants(t, f.oncallBrief, "pid 7018 node")
}

// hachiko is itself a process burning a core for a second every five minutes, and a
// watch that reported itself would be the only thing it ever reported.
func TestHachikoNeverReportsItsOwnProcessTree(t *testing.T) {
	f := newFixture(t)
	f.pid = 7018

	out := f.cpuRuns(13, 300)
	lacks(t, out, "hot for an hour")
	equal(t, f.sentCount(), 0, "messages sent")
}

// A regression: the first process of a sample used to be dropped when there was no
// previous sample to join it to, so a machine that had just restarted lost one
// process from its history on every first check.
func TestTheFirstProcessIsRecordedWhenThereIsNoPreviousSample(t *testing.T) {
	f := newFixture(t)
	f.setProcs(
		Process{PID: 101, PPID: 1, CPU: 60e9, Start: firstStart, Command: "/usr/local/bin/first"},
		Process{PID: 102, PPID: 1, CPU: 60e9, Start: firstStart, Command: "/usr/local/bin/second"},
	)

	f.sweep()

	procs := f.state().CPU.Procs
	equal(t, len(procs), 2, "processes recorded on the first check")
	if _, ok := procs["101:"+firstStart]; !ok {
		t.Error("the first process of the sample was dropped")
	}
}

func size(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}
