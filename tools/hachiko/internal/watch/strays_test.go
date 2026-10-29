package watch

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/harness"
	"github.com/timche/mac-mini/hachiko/internal/process"
)

// What the rule is: this account's, a parent of launchd, no launchd job and nothing macOS or
// an app ships, and a working directory a session would have been working in. Every
// exclusion here is one the live Mac needs — without them a ps of this account answers with
// a hundred and forty processes whose parent is launchd and which nothing is wrong with.
func TestWhatCountsAsLeftBehindBySession(t *testing.T) {
	cfg := config.Config{
		Home:         "/Users/x",
		HerdrRoot:    "/Users/x/.herdr/worktrees",
		ProjectsRoot: "/Users/x/projects",
		ScratchRoot:  "/Users/x/.cache/claude-tmp",
	}

	const (
		uid     = 501
		ownPID  = 999
		node    = "/usr/local/bin/node mcp-server.js"
		intree  = "/Users/x/.herdr/worktrees/meru/fix-ui/packages/app"
		scratch = "/Users/x/.cache/claude-tmp/claude-501/-Users-x-projects-meru/0192e4c1-1111-4222-8333-444455556666"
	)

	cases := []struct {
		name  string
		proc  process.Process
		dir   string
		jobs  map[int]bool
		allow []string

		// What the directory says started it, and "" for a process this reports nothing about.
		want string
	}{{
		name: "a worker left in a herdr worktree",
		proc: process.Process{PID: 7018, PPID: 1, UID: uid, Command: node},
		dir:  intree,
		want: "the meru worktree fix-ui",
	}, {
		name: "a worker left in one of Claude Code's own worktrees",
		proc: process.Process{PID: 7018, PPID: 1, UID: uid, Command: node},
		dir:  "/Users/x/projects/repeekgg/repeek/.claude/worktrees/agent-7",
		want: "a worktree of repeek, agent-7",
	}, {
		name: "a worker left in a plain checkout",
		proc: process.Process{PID: 7018, PPID: 1, UID: uid, Command: node},
		dir:  "/Users/x/projects/repeekgg/repeek/apps/web",
		want: "the repeekgg/repeek checkout",
	}, {
		name: "a worker left in the scratch folder a session is given",
		proc: process.Process{PID: 7018, PPID: 1, UID: uid, Command: node},
		dir:  scratch,
		want: "a session's own scratch folder",
	}, {
		// The test the whole rule turns on: every process whose parent dies is reparented to
		// launchd, so a parent of launchd on its own describes how macOS starts a daemon.
		name: "a launchd job of this account's",
		proc: process.Process{PID: 7018, PPID: 1, UID: uid, Command: node},
		dir:  intree,
		jobs: map[int]bool{7018: true},
	}, {
		name: "a daemon macOS ships",
		proc: process.Process{PID: 147, PPID: 1, UID: uid, Command: "/usr/libexec/dasd"},
		dir:  intree,
	}, {
		// The 5 GB helper that runs OrbStack's Linux guest is in the bundle beside the app
		// launchd is running, and has a parent of launchd and no job of its own.
		name: "a helper inside an app bundle",
		proc: process.Process{PID: 58117, PPID: 1, UID: uid,
			Command: "/Applications/OrbStack.app/Contents/Frameworks/OrbStack Helper.app/Contents/MacOS/OrbStack Helper vmgr"},
		dir: intree,
	}, {
		name: "a widget extension",
		proc: process.Process{PID: 667, PPID: 1, UID: uid,
			Command: "/System/Applications/FindMy.app/Contents/PlugIns/FindMyWidgetItems.appex/Contents/MacOS/FindMyWidgetItems"},
		dir: intree,
	}, {
		name: "a process another account owns",
		proc: process.Process{PID: 7018, PPID: 1, UID: 0, Command: node},
		dir:  intree,
	}, {
		name: "a process whose parent is still alive",
		proc: process.Process{PID: 7018, PPID: 4242, UID: uid, Command: node},
		dir:  intree,
	}, {
		name: "hachiko's own process",
		proc: process.Process{PID: ownPID, PPID: 1, UID: uid, Command: node},
		dir:  intree,
	}, {
		name:  "a command the allowlist names",
		proc:  process.Process{PID: 7018, PPID: 1, UID: uid, Command: node},
		dir:   intree,
		allow: []string{"node"},
	}, {
		// The test that makes this "left by a session" rather than any daemon of this
		// account's. op's daemon sits with a parent of launchd, outside every launchd job, in a
		// temporary folder of its own.
		name: "a daemon of this account's sitting somewhere else",
		proc: process.Process{PID: 712, PPID: 1, UID: uid, Command: "op daemon"},
		dir:  "/private/var/folders/js/53mxsrls6yg20t9zvx48z7mc0000gn/T/com.agilebits.op.501",
	}, {
		name: "a process lsof would not say the directory of",
		proc: process.Process{PID: 7018, PPID: 1, UID: uid, Command: node},
		dir:  "",
	}, {
		name: "a worker in another account's scratch folder",
		proc: process.Process{PID: 7018, PPID: 1, UID: uid, Command: node},
		dir:  "/Users/x/.cache/claude-tmp/claude-502/slug/0192e4c1-1111-4222-8333-444455556666",
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			procs := []process.Process{c.proc}
			cwd := func(int) string { return c.dir }

			found := straysAmong(cfg, procs, c.jobs, map[int]bool{ownPID: true}, uid, c.allow, cwd)

			if c.want == "" {
				harness.Equal(t, len(found), 0, "processes reported as left behind")
				return
			}
			harness.Equal(t, len(found), 1, "processes reported as left behind")
			harness.Equal(t, found[0].Where, c.want, "what the directory says started it")
			harness.Equal(t, found[0].Dir, c.dir, "the directory")
		})
	}
}

func oneStray(pid int, start string) []Stray {
	return []Stray{{Process: process.Process{PID: pid, Start: start}}}
}

// A session that has just exited leaves its children reparented to launchd for the moment it
// takes them to finish, so the span is the whole of what tells that apart from a leak.
func TestAProcessIsLeftBehindOnlyOnceTheWindowIsOut(t *testing.T) {
	const start = "Mon Sep 28 20:06:06 2026"
	key := "7018:" + start

	since, held := straysHeld(nil, oneStray(7018, start), base, time.Hour)
	harness.Equal(t, len(held), 0, "processes past the window on the check that first saw one")
	harness.Equal(t, since[key], base.Unix(), "when it was first seen left behind")

	since, held = straysHeld(since, oneStray(7018, start), base.Add(59*time.Minute), time.Hour)
	harness.Equal(t, len(held), 0, "processes past the window a minute early")

	_, held = straysHeld(since, oneStray(7018, start), base.Add(time.Hour), time.Hour)
	harness.Equal(t, len(held), 1, "processes past the window")
	harness.Equal(t, held[0].Since.Unix(), base.Unix(), "when it was first seen left behind")
}

// Because the window is a span of time rather than a count of checks, an interval the agent
// missed costs nothing: a Mac that was asleep for two hours comes back to a process that has
// been left behind for two hours.
func TestAMissedIntervalDoesNotRestartTheLeftBehindWindow(t *testing.T) {
	const start = "Mon Sep 28 20:06:06 2026"

	prev := map[string]int64{"7018:" + start: base.Unix()}

	_, held := straysHeld(prev, oneStray(7018, start), base.Add(2*time.Hour), time.Hour)
	harness.Equal(t, len(held), 1, "processes past the window after a missed interval")
	harness.Equal(t, int64(held[0].LeftFor(base.Add(2*time.Hour)).Hours()), int64(2), "hours left behind")
}

// A pid is reused, so what is kept is keyed by the start date as well — and what is not
// running any more is forgotten rather than carried as an age nothing can ever clear.
func TestAProcessThatHasGoneIsForgotten(t *testing.T) {
	const (
		old = "Mon Sep 28 20:06:06 2026"
		new = "Tue Sep 29 09:00:00 2026"
	)

	prev := map[string]int64{"7018:" + old: base.Unix(), "4242:" + old: base.Unix()}

	since, held := straysHeld(prev, oneStray(7018, new), base.Add(2*time.Hour), time.Hour)

	harness.Equal(t, len(since), 1, "processes remembered")
	harness.Equal(t, since["7018:"+new], base.Add(2*time.Hour).Unix(), "when the new holder of the pid was first seen")
	harness.Equal(t, len(held), 0, "processes past the window")
}

// A process left behind, this account's, with a parent of launchd and a working directory
// inside a worktree. A gigabyte resident and nothing on the CPU, which is the shape neither
// of the other two watches sees.
func strayProc(f *fixture, pid int, command, dir string) process.Process {
	f.t.Helper()

	start := f.now.Add(-26 * time.Hour)
	f.cwd[pid] = dir

	return process.Process{
		PID:       pid,
		PPID:      1,
		UID:       accountUID,
		User:      "timche",
		RSSKB:     config.GiB,
		Start:     start.Format("Mon Jan 2 15:04:05 2006"),
		StartedAt: start,
		Command:   command,
	}
}

func (f *fixture) worktree() string { return filepath.Join(f.cfg.HerdrRoot, "meru", "fix-ui") }

const mcpServer = "/usr/local/bin/node /Users/timche/mcp/server.js --stdio"

func TestAProcessLeftBehindForAnHourIsOneIncident(t *testing.T) {
	f := newFixture(t)
	f.setProcs(strayProc(f, 7018, mcpServer, f.worktree()))

	f.at(0).sweep()
	harness.Equal(t, f.sentCount(), 0, "messages before the window is out")

	f.at(3600).sweep()
	harness.Equal(t, f.sentCount(), 1, "messages once the window is out")
	harness.Equal(t, f.oncallName, "stray", "the on-call session's kind")

	message := f.lastSent()
	harness.Wants(t, message, "⚠️ node was left behind by a session that has ended")
	harness.Wants(t, message, "**Left behind:**")
	harness.Wants(t, message, "- node (pid 7018) — left behind for 1 hour, 1 GB memory, started ")
	harness.Wants(t, message, "(the meru worktree fix-ui)")
	harness.Wants(t, message, "\n  `"+mcpServer+"`")

	// Free space belongs in a message the disk is part of: a worker nobody is waiting on says
	// nothing about how much room is left.
	harness.Lacks(t, message, "**Free space:**")

	// One message per incident: the same process still sitting there is the same incident.
	f.at(3900).sweep()
	harness.Equal(t, f.sentCount(), 1, "messages on the check after it was reported")
	harness.Equal(t, f.oncallCalls, 1, "on-call sessions opened")
}

// The brief the session is handed is where the reading that matters goes, since what it has
// to work out is which session left the process and whether anybody still wants it.
func TestTheBriefNamesEverythingTheSessionNeeds(t *testing.T) {
	f := newFixture(t)
	f.setProcs(strayProc(f, 7018, mcpServer, f.worktree()))

	f.at(0).sweep()
	f.at(3600).sweep()

	for _, want := range []string{
		"pid 7018", mcpServer, f.worktree(), "the meru worktree fix-ui",
		"1 GB memory", "left behind for 1 hour",
	} {
		harness.Wants(t, f.oncallBrief, want)
	}
}

// Nothing here kills anything, which is the whole of the difference between this and the
// worktree sweep: a worker in a worktree somebody is still working in is as likely to be
// wanted as not, and only the session that reads the transcripts can tell.
func TestNothingIsSignalledAboutAProcessLeftBehind(t *testing.T) {
	f := newFixture(t)
	f.setProcs(strayProc(f, 7018, mcpServer, f.worktree()))

	f.at(0).sweep()
	log := f.at(3600).sweep()

	harness.Wants(t, log, "left behind by a session that has ended: pid 7018")
	harness.Lacks(t, log, "kill")
}

// Several of them is the news rather than the first one's name, and what they are holding
// together is the reason this watch exists at all.
func TestSeveralLeftBehindAreCountedAndWeighedInTheLead(t *testing.T) {
	f := newFixture(t)
	f.setProcs(
		strayProc(f, 7018, mcpServer, f.worktree()),
		strayProc(f, 7019, "/usr/local/bin/node /Users/timche/mcp/other.js", f.worktree()),
	)

	f.at(0).sweep()
	f.at(3600).sweep()

	harness.Wants(t, f.lastSent(), "⚠️ 2 processes left behind by sessions that have ended, holding 2 GB")
}

// The disk has a deadline on it and this does not, so one run is one message and one
// session, and the session it goes to is the disk's.
func TestLowSpaceAndAProcessLeftBehindAreOneIncident(t *testing.T) {
	f := newFixture(t)
	f.setProcs(strayProc(f, 7018, mcpServer, f.worktree()))

	f.at(0).sweep()
	f.freeGB = 46
	f.at(3600).sweep()

	harness.Equal(t, f.sentCount(), 1, "messages")
	harness.Equal(t, f.oncallName, "disk", "the on-call session's kind")

	message := f.lastSent()
	harness.Wants(t, message, "💾 Low disk space: 46 GB free")
	harness.Wants(t, message, "**Left behind:**")
}

// A process launchd is running is not one anything was abandoned to it, and that list is
// what the whole rule turns on — so a check that could not read it watches for nothing and
// says so where something off this Mac can see it.
func TestWithoutLaunchctlNothingIsCalledLeftBehind(t *testing.T) {
	f := newFixture(t)
	f.setProcs(strayProc(f, 7018, mcpServer, f.worktree()))
	f.jobsErr = errors.New("launchctl list: exit status 1")

	f.at(0).sweep()
	log := f.at(3600).sweep()

	harness.Equal(t, f.sentCount(), 0, "messages")
	harness.Wants(t, log, "launchctl would not say which processes are its own jobs")

	last := f.checkins[len(f.checkins)-1]
	harness.Equal(t, last.Failed, true, "whether the sweep reported a fault")
	harness.Wants(t, last.Reason, "nothing was watching for what a session left behind")
}

// And a check that could not read it says nothing new either: the one already reported is
// still the same incident, not a fresh one the moment launchctl answers again.
func TestACheckThatCouldNotReadLaunchctlRaisesNothingAgain(t *testing.T) {
	f := newFixture(t)
	f.setProcs(strayProc(f, 7018, mcpServer, f.worktree()))

	f.at(0).sweep()
	f.at(3600).sweep()
	harness.Equal(t, f.oncallCalls, 1, "on-call sessions opened")

	f.jobsErr = errors.New("launchctl list: exit status 1")
	f.at(3660).sweep()
	harness.Equal(t, len(f.state().AlertedStrays), 1, "processes still recorded as reported")

	f.jobsErr = nil
	f.at(3720).sweep()
	harness.Equal(t, f.oncallCalls, 1, "on-call sessions opened once launchctl answered again")
}

func TestALaunchdJobIsNeverCalledLeftBehind(t *testing.T) {
	f := newFixture(t)
	f.setProcs(strayProc(f, 7018, mcpServer, f.worktree()))
	f.jobs[7018] = true

	f.at(0).sweep()
	f.at(3600).sweep()

	harness.Equal(t, f.sentCount(), 0, "messages")
}

// What the on-call session needs on top of the reading it was briefed with: the process it
// is waiting on an answer about has gone, which is a reason to close the incident rather
// than a reason to go on asking.
func TestAProcessThatHasGoneClearsTheIncident(t *testing.T) {
	f := newFixture(t)
	f.cfg.RemindAfter = remindAt * time.Second
	f.cfg.WarnAfter = warnAt * time.Second
	f.cfg.HandoverAfter = handoverAt * time.Second
	f.setProcs(strayProc(f, 7018, mcpServer, f.worktree()))

	f.at(0).sweep()
	f.at(3600).sweep()

	f.notifyWithFallback(f.onlyPendingID(), "stop pid 7018")
	f.blocks("stray")
	f.at(3900).sweep()

	f.setProcs()
	log := f.at(4200).sweep()
	harness.Wants(t, log, "nothing that fired stray-")

	log = f.at(4500).sweep()
	harness.Wants(t, log, "so its question was cancelled and it was asked again")
	harness.Wants(t, f.lastInterrupt(), "Nothing is left behind by a session that has ended any more.")
}

// A dry run reports what it sees and changes nothing, the state included: it is the command
// a session runs to find out what the watch would do.
func TestADryRunSaysWhatIsLeftBehindAndRecordsNothing(t *testing.T) {
	f := newFixture(t)
	f.setProcs(strayProc(f, 7018, mcpServer, f.worktree()))

	f.at(0).sweep()
	log := f.at(3600).dryRun()

	harness.Wants(t, log, "left behind by a session that has ended: pid 7018")
	harness.Wants(t, log, "0 process(es) hot and 1 left behind of 1 sampled")
	harness.Equal(t, f.sentCount(), 0, "messages")
	harness.Equal(t, len(f.state().AlertedStrays), 0, "processes recorded as reported")
}
