package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/wording"
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
	wants(t, out, "written by fake-worker (pid 4242)")

	equal(t, f.oncallCalls, 1, "sessions opened")
	equal(t, f.oncallName, "disk", "the session's name")
	equal(t, f.sentCount(), 1, "messages sent")

	wants(t, f.lastSent(), "\U0001f4be Disk filling: worker.log is growing fast")
	wants(t, f.lastSent(), "**Growing fast:**\n- `"+path+"`")
	wants(t, f.lastSent(), "An agent is looking into it — attach in herdr: workspace `.mac-mini`, tab `disk-0000`. Details to follow.")
	wants(t, f.lastSent(), "-# Incident disk-")
	// The lead is the whole of the headline, and a headline carries no incident id.
	lacks(t, strings.SplitN(f.lastSent(), "\n", 2)[0], "disk-0000")

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

	incident := f.onlyPendingID()
	f.at(420).notify(incident)

	// The next sweep is what consumes the marker: it is the only thing that edits the
	// state, so a report cannot be lost to a sweep writing at the same moment.
	f.grow("tmp/worker.log", 4*mb)
	wants(t, f.at(900).sweep(), "the on-call session reported on "+incident)
	equal(t, len(f.state().Pending), 0, "pending incidents after a report")
	equal(t, f.sentCount(), 1, "messages sent")

	// And nothing of hachiko's own goes out afterwards however long the file keeps
	// growing.
	f.grow("tmp/worker.log", 4*mb)
	equal(t, f.at(1500).sweep(), "", "the log after the report was consumed")
	equal(t, f.sentCount(), 1, "messages sent after the report was consumed")
}

// The deadline and the report race: a sweep holds its lock across a herdr call and an
// `op run`, which together can outlast the five minutes an on-call session is told to
// report within — so the report may not depend on taking that lock, and a sweep that
// held it for the whole window must still see the report rather than sending the raw
// details over the top of it.
func TestAReportLandsWhileASweepHoldsTheLock(t *testing.T) {
	f := newFixture(t)
	f.grow("tmp/worker.log", 3*mb)
	f.at(0).sweep()
	f.grow("tmp/worker.log", 4*mb)
	f.at(300).sweep()

	incident := f.onlyPendingID()

	// The lock, held by a sweep that is still inside its callouts.
	lock, _, err := f.store.Acquire(5*time.Minute, f.now)
	if err != nil {
		t.Fatal(err)
	}

	f.at(420).notify(incident)
	lock.Release()

	out := f.at(900).sweep()
	wants(t, out, "the on-call session reported on "+incident)
	lacks(t, out, "has not reported")
	equal(t, f.sentCount(), 1, "messages sent")
	equal(t, len(f.state().Pending), 0, "pending incidents after the report")
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
	wants(t, f.lastSent(), "\u26a0\ufe0f No report from the agent on the disk incident after 10 minutes")
	wants(t, f.lastSent(), "written by fake-worker (pid 4242)")
	wants(t, f.lastSent(), "Attach in herdr: workspace `.mac-mini`, tab `disk-0000`.")
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
	wants(t, f.lastSent(), "No on-call session could be started, so nothing is being worked on it.")
	wants(t, f.lastSent(), "written by fake-worker (pid 4242)")
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
	wants(t, f.lastSent(), "\U0001f4be Low disk space: 90 GB free")
	wants(t, f.lastSent(), "**Free space:** 90 GB, under the 100 GB mark")

	equal(t, f.at(300).sweep(), "", "the log while nothing changed")
	equal(t, f.sentCount(), 1, "messages sent while nothing changed")

	f.freeGB = 15
	wants(t, f.at(600).sweep(), "under the 20 GB threshold")
	equal(t, f.sentCount(), 2, "messages sent after the escalation")
	wants(t, f.lastSent(), "\U0001f534 Disk critical: 15 GB free")
	wants(t, f.lastSent(), "**Free space:** 15 GB, under the 20 GB mark")

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

	wants(t, f.lastSent(), "**Emptied to keep the Mac going:** `"+log+"` — its writer was left running, so the space is back now")
	wants(t, f.lastSent(), "Disk critical: 15 GB free, and worker.log was emptied")
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
	wants(t, out, "would open an on-call session as disk and send: \U0001f534 Disk critical: 15 GB free")
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
	wants(t, f.lastSent(), "\U0001f525 node is busy: 80% of a core for 1 hour")
	wants(t, f.lastSent(), "**Busy processes:**\n- node (pid 7018) — 80% of a core for 1 hour, 512 MB memory, started ")

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
func TestAHotProcessLeftBehindInsideACheckoutIsNamedAsOne(t *testing.T) {
	f := newFixture(t)
	checkout := filepath.Join(f.cfg.ProjectsRoot, "repeek")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	f.cwd[7018] = checkout

	out := f.cpuRuns(13, 240)
	wants(t, out, "ppid 1")
	wants(t, out, "in the repeek checkout")
	wants(t, out, "left behind by the session that started it, which is gone")
	lacks(t, out, "system process")
}

// Every daemon launchd starts has ppid 1, so hachiko called root's dasd orphaned — which was
// a description of how macOS starts daemons and not of anything wrong with it. What is worth
// saying instead is that it is not the session's to stop.
func TestAHotSystemProcessIsNotCalledLeftBehind(t *testing.T) {
	f := newFixture(t)

	var out string
	for i := range 13 {
		f.systemProc(147, float64(i)*240, firstStart, "/usr/libexec/dasd")
		out = f.at(int64(i) * 300).sweep()
	}

	wants(t, out, "ppid 1, system process owned by root")
	lacks(t, out, "left behind")
	lacks(t, out, "orphaned")
}

// A hot process the session cannot touch is a message about Tim, not about the session: sudo
// is on its never list, so what he needs in the first line is what stopping it would take.
func TestASystemProcessSaysWhatStoppingItWouldTakeUpFront(t *testing.T) {
	f := newFixture(t)

	for i := range 13 {
		f.systemProc(147, float64(i)*240, firstStart, "/usr/libexec/dasd")
		f.at(int64(i) * 300).sweep()
	}

	equal(t, f.sentCount(), 1, "messages sent")
	const note = "**Needs you:** `sudo kill 147` \u2014 stopping a system process needs sudo, and launchd starts most daemons again."
	wants(t, f.lastSent(), note)
	wants(t, f.lastSent(), "\U0001f525 dasd is busy: 80% of a core for 1 hour")
	wants(t, f.lastSent(), "dasd (pid 147) \u2014 80% of a core for 1 hour, 512 MB memory, started ")
	wants(t, f.lastSent(), "a system process owned by root")

	// And in the brief, so the session knows before it writes a word that the fix it is
	// about to recommend is not one it may take.
	wants(t, f.oncallBrief, note)
	equal(t, f.oncallName, "cpu", "the kind of session opened")
}

// A daemon the root helper will restart with no password is the one system process Tim can
// deal with from his phone, so the message gives him that command rather than a sudo kill he
// has to sit down and think about.
func TestABusyDaemonTheRootHelperAllowsGivesTimTheOneCommand(t *testing.T) {
	f := newFixture(t)
	f.rootHelper("dasd")

	for i := range 13 {
		f.systemProc(147, float64(i)*240, firstStart, "/usr/libexec/dasd")
		f.at(int64(i) * 300).sweep()
	}

	wants(t, f.lastSent(), "**You can run:** `sudo "+f.cfg.RootHelper+
		" restart-daemon dasd` \u2014 it restarts the daemon with no password needed.")
	lacks(t, f.lastSent(), "sudo kill")
}

// And a daemon that is not on the helper's list gets the honest fallback, never a command
// the helper would refuse: the name is matched against that list and nothing looser.
func TestABusyDaemonTheRootHelperDoesNotAllowGetsTheSudoKill(t *testing.T) {
	f := newFixture(t)
	f.rootHelper("dasd")

	for i := range 13 {
		f.systemProc(314, float64(i)*240, firstStart, "/usr/libexec/syslogd")
		f.at(int64(i) * 300).sweep()
	}

	wants(t, f.lastSent(), "**Needs you:** `sudo kill 314`")
	lacks(t, f.lastSent(), "restart-daemon")
}

// Nothing of this happens to a process of Tim's own, which the session may stop inside its
// limits without anybody being woken up.
func TestAHotProcessOfThisAccountsSaysNothingAboutSudo(t *testing.T) {
	f := newFixture(t)
	f.cpuRuns(13, 240)

	lacks(t, f.lastSent(), "sudo")
	lacks(t, f.oncallBrief, "stopping it needs sudo")
}

// And this account's process with a parent of launchd outside a checkout is neither: there
// is nothing to say about whose work it was.
func TestAProcessOfThisAccountsOutsideACheckoutIsNeither(t *testing.T) {
	f := newFixture(t)
	f.cwd[7018] = filepath.Join(f.cfg.Home, "Downloads")

	out := f.cpuRuns(13, 240)
	wants(t, out, "ppid 1")
	lacks(t, out, "left behind")
	lacks(t, out, "system process")
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
	wants(t, f.oncallBrief, "node (pid 7018)")
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

// A directory that would not answer is named once and then remembered, so the next sweep
// hands it straight back as something to skip rather than paying the seconds again.
func TestADirectoryThatWouldNotAnswerIsNamedOnceAndThenSkipped(t *testing.T) {
	f := newFixture(t)
	hung := filepath.Join(f.cfg.Home, "Volumes", "dead-mount")
	f.stalls = []string{hung}

	wants(t, f.at(0).sweep(), hung+" did not answer a read within 3s, so it is skipped until it is tried again in 1h0m0s")
	equal(t, len(f.state().Stalled), 1, "directories remembered as stalled")

	// The second sweep is handed it to skip, and says nothing more about it.
	out := f.at(300).sweep()
	lacks(t, out, "did not answer a read")
	equal(t, len(f.skipped), 1, "directories the walk was told to skip")
	if len(f.skipped) == 1 {
		equal(t, f.skipped[0], hung, "the directory the walk was told to skip")
	}
	equal(t, len(f.state().Stalled), 1, "directories remembered after the second sweep")
}

// One slow read is not a reason to stop looking at a tree for good — and a read goes slow
// under disk pressure, which is exactly when a runaway writer is thrashing the volume and
// /private/tmp is the thing worth watching.
func TestADirectoryThatStalledIsTriedAgainAfterAnHour(t *testing.T) {
	f := newFixture(t)
	hung := filepath.Join(f.cfg.Home, "tmp")
	f.stalls = []string{hung}

	f.at(0).sweep()
	f.at(300).sweep()
	equal(t, len(f.skipped), 1, "directories skipped before the hour is up")

	// An hour later it is walked again rather than skipped.
	f.at(3600).sweep()
	equal(t, len(f.skipped), 0, "directories skipped once the hour is up")
	equal(t, len(f.state().Stalled), 1, "directories still remembered after a failed retry")

	// And having stalled again, it goes quiet for another hour rather than being retried
	// every five minutes.
	f.at(3900).sweep()
	equal(t, len(f.skipped), 1, "directories skipped after the retry stalled too")
}

func TestADirectoryThatAnswersAgainIsSaidOnceAndPutBackInTheWalk(t *testing.T) {
	f := newFixture(t)
	hung := filepath.Join(f.cfg.Home, "tmp")
	f.stalls = []string{hung}

	f.at(0).sweep()

	// The mount comes back, and the retry an hour later finds it.
	f.stalls = nil
	out := f.at(3600).sweep()

	wants(t, out, hung+" answered again after 1h00m, so it is back in the walk")
	equal(t, len(f.state().Stalled), 0, "directories still remembered after it recovered")

	// Said once, not every sweep after it.
	lacks(t, f.at(3900).sweep(), "answered again")
	equal(t, len(f.skipped), 0, "directories skipped once it recovered")
}

// A directory that is still stalling costs one line when it starts and nothing after,
// however many sweeps and retries it takes.
func TestADirectoryThatKeepsStallingIsNeverNamedTwice(t *testing.T) {
	f := newFixture(t)
	hung := filepath.Join(f.cfg.Home, "tmp")
	f.stalls = []string{hung}

	said := 0
	for i := range int64(30) {
		if strings.Contains(f.at(i*300).sweep(), "did not answer a read") {
			said++
		}
	}

	equal(t, said, 1, "times the stalled directory was named across two and a half hours")
	equal(t, len(f.state().Stalled), 1, "directories remembered at the end")
}

// A monitor that reports nothing because it is still counting is the failure this exists
// to avoid, so the CPU check and the alerts run on what the walk did manage to see.
func TestAWalkCutShortStillLetsTheRestOfTheCheckRun(t *testing.T) {
	f := newFixture(t)
	f.cutShort = true
	f.freeGB = 90

	out := f.at(0).sweep()
	wants(t, out, "the walk ran out of its 1m0s, so this check saw only part of the disk")
	wants(t, out, "only 90.0 GB free, under the 100 GB threshold")
	equal(t, f.sentCount(), 1, "messages sent")
}

// An escalation re-briefs the same session under a new incident id, but the id the
// session was first handed is the one in its scrollback — so it reports under that. The
// report is on the incident either way, and reading it any other way loses it and then
// says the agent went quiet about the very thing it just answered.
func TestAReportUnderTheIdAnEscalationSupersededStillCounts(t *testing.T) {
	f := newFixture(t)

	f.grow("tmp/worker.log", 3*mb)
	f.at(0).sweep()
	f.grow("tmp/worker.log", 4*mb)
	f.at(300).sweep()
	first := f.onlyPendingID()

	// The escalation: free space crosses the critical threshold, so the same session is
	// re-briefed under a second id and the first stops being pending.
	f.freeGB = 15
	f.at(600).sweep()
	second := f.onlyPendingID()
	if second == first {
		t.Fatalf("the escalation did not open a new incident: %s", second)
	}

	// The session answers with the id it was first given.
	f.at(660).notify(first)

	out := f.at(1500).sweep()
	wants(t, out, "the on-call session reported on "+first+", which "+second+" superseded")
	lacks(t, out, "has not reported")
	equal(t, len(f.state().Pending), 0, "pending incidents after the report")

	// And no marker is left behind to be read as a second report.
	equal(t, len(f.store.ReportedIDs()), 0, "markers left behind")
}

// A report for a kind nothing is pending on is not a report on something else of that
// kind that has not been raised yet.
func TestAMarkerWithNothingPendingIsForgotten(t *testing.T) {
	f := newFixture(t)
	f.at(0).notify("disk-1699999999")
	equal(t, len(f.store.ReportedIDs()), 1, "markers before the sweep")

	equal(t, f.at(0).sweep(), "", "the log for a report on nothing")
	equal(t, len(f.store.ReportedIDs()), 0, "markers after the sweep")
	equal(t, f.sentCount(), 0, "messages sent")
}

// The standing orders leave an on-call session waiting on a question, and herdr refuses
// a prompt to an agent in that state — so the commonest second alert of an incident
// reaches nobody. Reading that as "no session" would have claimed the Mac was
// unattended; waiting on it would have promised a report that cannot come.
func TestAnUpdateASessionCannotBeHandedSendsTheWholeOfItAndWaitsOnNothing(t *testing.T) {
	f := newFixture(t)
	f.grow("tmp/worker.log", 3*mb)
	f.at(0).sweep()

	f.oncallBlocked = true
	f.grow("tmp/worker.log", 4*mb)
	f.at(300).sweep()

	equal(t, f.sentCount(), 1, "messages sent")
	wants(t, f.lastSent(), "written by fake-worker (pid 4242)")
	wants(t, f.lastSent(), "This update did not reach it.")
	lacks(t, f.lastSent(), "No on-call session could be started")
	lacks(t, f.lastSent(), "Details to follow")

	// Nothing is going to report, so nothing waits ten minutes to say so.
	equal(t, len(f.state().Pending), 0, "pending incidents")

	// And the file is still one incident: the dedupe happened even though the session
	// never heard about it.
	f.grow("tmp/worker.log", 4*mb)
	equal(t, f.at(600).sweep(), "", "the log on the next check")
	equal(t, f.at(1200).sweep(), "", "the log after the deadline would have passed")
	equal(t, f.sentCount(), 1, "messages sent after the deadline would have passed")
}

// A truncate is the one thing here that changes somebody's disk. On every run but the
// first the file is already flagged and the threshold is already crossed, so nothing
// else fires — and the truncate went unreported.
func TestATruncateOnALaterRunIsStillReported(t *testing.T) {
	f := newFixture(t)
	f.freeGB = 15

	log := f.grow("tmp/worker.log", 2*mb)
	f.at(0).sweep()

	f.grow("tmp/worker.log", 4*mb)
	f.at(300).sweep()
	before := f.sentCount()

	// The file is in AlertedFiles now and the threshold has not moved, so the truncate is
	// the only thing this run has to say.
	f.grow("tmp/worker.log", 4*mb)
	out := f.at(600).sweep()

	wants(t, out, "truncated "+log)
	equal(t, f.sentCount(), before+1, "messages sent for the second truncate")
	wants(t, f.lastSent(), "**Emptied to keep the Mac going:** `"+log+"`")
	equal(t, size(t, log), int64(0), "the truncated log")
}

// A send that failed is an incident nobody has heard about, and the sample it was
// measured against is the only baseline that can still catch it: advancing it would let
// a writer that slows to under the threshold slip through for good, having already taken
// the disk.
func TestAFileNobodyHasHeardAboutKeepsTheSizeItWasMeasuredAgainst(t *testing.T) {
	f := newFixture(t)
	f.sendErr = errSendFailed

	f.grow("tmp/worker.log", 3*mb)
	f.at(0).sweep()
	f.grow("tmp/worker.log", 4*mb)
	f.at(300).sweep()
	equal(t, f.sentCount(), 0, "messages sent while the webhook was down")

	// Under the growth threshold for this interval alone, but far over it since the sample
	// the failed alert was about.
	f.sendErr = nil
	f.grow("tmp/worker.log", 1*mb)

	wants(t, f.at(600).sweep(), "growing fast")
	equal(t, f.sentCount(), 1, "messages sent once the webhook was back")
}

// A path is chosen by whatever filled the disk, and Discord caps a message at 2,000
// characters: one path at a megabyte would be the whole of it.
func TestALongPathIsClippedOutOfTheMessage(t *testing.T) {
	f := newFixture(t)

	long := "tmp/" + strings.Repeat("deep/", 120) + "worker.log"
	path := f.grow(long, 3*mb)
	f.at(0).sweep()

	f.grow(long, 4*mb)
	f.at(300).sweep()

	if len(path) <= wording.PathLimit {
		t.Fatalf("the path under test is only %d characters", len(path))
	}
	lacks(t, f.lastSent(), path)
	wants(t, f.lastSent(), wording.Clip(path, wording.PathLimit))
}

func size(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

// Every name in an alert was chosen by whatever filled the disk: the command line a worker
// was started with, and what lsof calls whoever holds a file. A name shaped like a link, or
// like one of hachiko's own labels, has to arrive as a name — in the lead, in the bullet,
// and in the post's title, which is the one place the escaping that does that would show.
func TestANameShapedLikeMarkdownArrivesAsAName(t *testing.T) {
	f := newFixture(t)
	// The shape lsof answers in, which is where a writer's own name is escaped: the pid and
	// the parentheses around it are hachiko's and stay as they are.
	f.writer = wording.PIDLabel("[Fix it](https://wherever)", "5073")

	for i := range 13 {
		f.proc(7018, float64(i)*240, firstStart, "/usr/local/bin/**node** worker.js")
		f.at(int64(i) * 300).sweep()
	}

	equal(t, f.sentCount(), 1, "messages sent")
	message := f.lastSent()

	wants(t, message, `\*\*node\*\* is busy: 80% of a core`)
	wants(t, message, `- \*\*node\*\* (pid 7018)`)
	// Nothing outside a code span arrives as emphasis of hachiko's own. Inside one it may:
	// the span is what stops the rendering, which is why the command line keeps its own
	// asterisks and needs no escape.
	for _, line := range strings.Split(message, "\n") {
		if !strings.Contains(line, "`") {
			lacks(t, line, "**node**")
		}
	}
	wants(t, message, wording.CodeSpan("/usr/local/bin/**node** worker.js"))

	// The post's title is plain text, so the escaping comes back out rather than being shown,
	// and the marker stays: it is what says at a glance which kind of alert this is.
	equal(t, threadName(message), "🔥 **node** is busy: 80% of a core for 1 hour",
		"the post's name")

	// A writer lsof named is the other half of it, in a bullet about the disk.
	path := f.grow("tmp/worker.log", bigKB)
	f.at(4200).sweep()
	f.grow("tmp/worker.log", growKB+bigKB)
	f.at(4500).sweep()

	wants(t, f.lastSent(), `written by \[Fix it\]\(https://wherever\) (pid 5073)`)
	lacks(t, f.lastSent(), "written by [Fix it]")
	wants(t, f.lastSent(), wording.CodeSpan(path))
}
