package main

import (
	"testing"
	"time"
)

func TestRepoOfNamesTheCheckoutOrWorktreeADirectoryBelongsTo(t *testing.T) {
	cfg := Config{
		HerdrRoot:    "/Users/x/.herdr/worktrees",
		ProjectsRoot: "/Users/x/projects",
	}

	cases := []struct {
		dir  string
		want string
	}{
		{"/Users/x/.herdr/worktrees/meru/fix-ui", "the meru worktree fix-ui"},
		{"/Users/x/.herdr/worktrees/meru/fix-ui/packages/app", "the meru worktree fix-ui"},
		{"/Users/x/projects/repeek/.claude/worktrees/agent-7", "a worktree of repeek, agent-7"},
		{"/Users/x/projects/repeek", "the repeek checkout"},
		{"/Users/x/projects/repeek/apps/web", "the repeek checkout"},
		{"/Users/x/Downloads", ""},
		{"/Users/x/projects", ""},
	}

	for _, c := range cases {
		if got := repoOf(cfg, c.dir); got != c.want {
			t.Errorf("repoOf(%s) = %q, want %q", c.dir, got, c.want)
		}
	}
}

// Half a core is the threshold, so exactly half of it counts.
func TestHalfACoreExactlyCountsAsBusy(t *testing.T) {
	start := "Mon Sep 28 20:06:06 2026"
	prev := CPUSample{At: base.Unix(), Procs: map[string]ProcSample{
		"7018:" + start: {CPU: 0},
	}}
	procs := []Process{{PID: 7018, Start: start, CPU: 150 * time.Second}}

	sample, hot := cpuHot(prev, procs, base.Add(300*time.Second), 50, time.Hour)

	equal(t, len(hot), 0, "processes hot before the window is out")
	equal(t, sample.Procs["7018:"+start].HotSince, base.Unix(), "the start of the busy run")
}

func TestJustUnderHalfACoreIsNotBusy(t *testing.T) {
	start := "Mon Sep 28 20:06:06 2026"
	prev := CPUSample{At: base.Unix(), Procs: map[string]ProcSample{
		"7018:" + start: {CPU: 0},
	}}
	procs := []Process{{PID: 7018, Start: start, CPU: 149 * time.Second}}

	sample, _ := cpuHot(prev, procs, base.Add(300*time.Second), 50, time.Hour)
	equal(t, sample.Procs["7018:"+start].HotSince, int64(0), "the start of the busy run")
}

// Because the window is a span of time rather than a count of runs, an interval the
// agent missed costs nothing: a Mac that was asleep for half an hour and comes back
// still busy has been busy for the whole hour.
func TestAMissedIntervalDoesNotRestartTheWindow(t *testing.T) {
	start := "Mon Sep 28 20:06:06 2026"
	key := "7018:" + start

	prev := CPUSample{At: base.Add(1800 * time.Second).Unix(), Procs: map[string]ProcSample{
		key: {CPU: 1000, HotSince: base.Unix(), CPUAtHotSince: 0},
	}}
	procs := []Process{{PID: 7018, Start: start, CPU: 2400 * time.Second}}

	// An hour after the run began, but only half an hour after the previous sample.
	_, hot := cpuHot(prev, procs, base.Add(3600*time.Second), 50, time.Hour)

	equal(t, len(hot), 1, "processes hot after a missed interval")
	equal(t, hot[0].HotSince.Unix(), base.Unix(), "the start of the busy run")
	// 2400 seconds of CPU over the 3600 the run has lasted.
	if share := hot[0].Share; share < 66.6 || share > 66.7 {
		t.Errorf("the average over the window is %.2f%%", share)
	}
}
