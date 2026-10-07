package process

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

const psSample = `    1     0     0  24320  33:41.67 Mon Sep 28 20:06:06 2026 root             /sbin/launchd
 7018     1   501 524288   2:03:04.12 Mon Sep  8 09:00:00 2026 timche           /usr/local/bin/node  worker.js --flag
  642     1   244   1616  1-02:00:00.00 Tue Sep 29 09:00:00 2026 _appstore        /usr/libexec/smd
`

func TestParseProcessesReadsTheFieldsPsPrints(t *testing.T) {
	procs := ParseProcesses(psSample)
	harness.Equal(t, len(procs), 3, "processes parsed")

	launchd := procs[0]
	harness.Equal(t, launchd.PID, 1, "pid")
	harness.Equal(t, launchd.PPID, 0, "ppid")
	harness.Equal(t, launchd.UID, 0, "uid")
	harness.Equal(t, launchd.User, "root", "owner")
	harness.Equal(t, launchd.Owner(), "root", "what to call the owner")
	harness.Equal(t, launchd.RSSKB, int64(24320), "rss")
	harness.Equal(t, launchd.CPU, 33*time.Minute+41*time.Second+670*time.Millisecond, "cumulative cpu")
	harness.Equal(t, launchd.Command, "/sbin/launchd", "command")

	harness.Equal(t, procs[1].UID, 501, "the uid of this account's process")
	harness.Equal(t, procs[2].Owner(), "_appstore", "a daemon account's name")

	// A uid with no name against it is still an owner, since the number is the identity
	// anything here decides by and the name is only what the message calls it.
	harness.Equal(t, Process{UID: 244}.Owner(), "244", "an owner ps resolved no name for")

	// The command keeps its own spacing, which is how an argument with two spaces in
	// it reads the way it was started.
	harness.Equal(t, procs[1].Command, "/usr/local/bin/node  worker.js --flag", "command with arguments")
	harness.Equal(t, procs[1].Name(), "node", "name")
	harness.Equal(t, procs[1].Path(), "/usr/local/bin/node", "path")
	harness.Equal(t, procs[1].Start, "Mon Sep 8 09:00:00 2026", "a single-digit day")
	harness.Equal(t, procs[1].Key(), "7018:Mon Sep 8 09:00:00 2026", "identity")
	harness.Equal(t, procs[1].CPU, (2*3600+3*60+4)*time.Second+120*time.Millisecond, "hours of cpu")

	harness.Equal(t, procs[2].CPU, 26*time.Hour, "a day of cpu")
	if procs[1].StartedAt.IsZero() {
		t.Error("the start date was not parsed")
	}
}

func TestParseProcessesIgnoresWhatIsNotALine(t *testing.T) {
	if procs := ParseProcesses("\n  \nnot a process line\n"); len(procs) != 0 {
		t.Errorf("rubbish was parsed as processes: %v", procs)
	}
}

// ps prints the start date with the locale's month and weekday names, and that date is
// half of an identity this compares as a string — so a Mac whose locale changed would
// otherwise reset every process's history.
func TestThePsSampleIsTakenInTheCLocale(t *testing.T) {
	cmd := psCommand(context.Background())

	if !slices.Contains(cmd.Env, "LC_ALL=C") {
		t.Error("the ps sample is not taken in the C locale")
	}
	if !slices.Contains(cmd.Args, "-ww") {
		t.Error("ps would truncate the command to a terminal width")
	}
	for _, arg := range cmd.Args {
		if arg == "pcpu" || arg == "%cpu" {
			t.Error("ps was asked for a per-cent figure of its own")
		}
	}

	// The owner's name is the one fixed field that could hold a space, so it goes after
	// lstart and immediately before the command: a name with one in it then costs a token off
	// the front of the command line rather than shifting every number behind it.
	if !slices.Contains(cmd.Args, "pid=,ppid=,uid=,rss=,time=,lstart=,user=,command=") {
		t.Errorf("the fields are not in the order the parser reads them: %v", cmd.Args)
	}
}

// A name with a space in it is not a name macOS will make, but a sample it broke would be a
// hot process hachiko never saw. The numbers it decides by stay where they are.
func TestAnOwnerNameWithASpaceInItDoesNotMoveTheNumbers(t *testing.T) {
	procs := ParseProcesses(" 7018     1   501 524288   2:03:04.12 Mon Sep  8 09:00:00 2026 odd name /usr/local/bin/node worker.js\n")

	harness.Equal(t, len(procs), 1, "processes parsed")
	harness.Equal(t, procs[0].PID, 7018, "pid")
	harness.Equal(t, procs[0].UID, 501, "uid")
	harness.Equal(t, procs[0].CPU, (2*3600+3*60+4)*time.Second+120*time.Millisecond, "cumulative cpu")
}

func TestOwnTreeIsEverythingBetweenAPidAndLaunchd(t *testing.T) {
	procs := []Process{
		{PID: 1, PPID: 0},
		{PID: 400, PPID: 1},
		{PID: 500, PPID: 400},
		{PID: 7018, PPID: 500},
		{PID: 9000, PPID: 1},
	}

	tree := OwnTree(procs, 7018)

	for _, pid := range []int{7018, 500, 400} {
		if !tree[pid] {
			t.Errorf("pid %d is not in its own tree", pid)
		}
	}
	for _, pid := range []int{1, 9000} {
		if tree[pid] {
			t.Errorf("pid %d was counted as hachiko's own", pid)
		}
	}
}

func TestOwnTreeSurvivesAPidItsSampleDoesNotHold(t *testing.T) {
	tree := OwnTree(nil, 7018)
	harness.Equal(t, len(tree), 1, "the tree of a pid nothing knows about")
	if !tree[7018] {
		t.Error("a process is not in its own tree")
	}
}
