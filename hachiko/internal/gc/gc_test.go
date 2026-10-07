package gc

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

func TestAQuietSweepSaysNothing(t *testing.T) {
	f := newFixture(t)

	harness.Equal(t, f.sweep(), "", "the log of a quiet sweep")
}

// A dry run changes nothing at all, the lock included.
func TestADryRunLeavesNoLock(t *testing.T) {
	f := newFixture(t)
	removed := f.herdrWorktree("app", "gone", false)
	f.containers = []Container{{Project: "gc-gone", WorkingDir: removed}}

	out := f.dryRun()
	harness.Wants(t, out, "dry run over")
	harness.Equal(t, len(f.downs), 0, "projects taken down")
	gone(t, f.cfg.GCLock)
}

// A sweep already running is one this one has nothing to say about.
func TestALockSomebodyElseHoldsStopsTheSweep(t *testing.T) {
	f := newFixture(t)
	removed := f.herdrWorktree("app", "gone", false)
	f.containers = []Container{{Project: "gc-gone", WorkingDir: removed}}

	if err := os.Mkdir(f.cfg.GCLock, 0o755); err != nil {
		t.Fatal(err)
	}
	harness.Equal(t, f.sweep(), "", "the log of a sweep that could not take the lock")
	harness.Equal(t, len(f.downs), 0, "projects taken down")
}

// A lock left by a killed run would otherwise stop every sweep after it.
func TestALockOlderThanTheIntervalIsTakenOver(t *testing.T) {
	f := newFixture(t)

	if err := os.Mkdir(f.cfg.GCLock, 0o755); err != nil {
		t.Fatal(err)
	}
	harness.WriteFile(t, filepath.Join(f.cfg.GCLock, "pid"), "777")

	stale := base.Add(-11 * time.Minute)
	if err := os.Chtimes(f.cfg.GCLock, stale, stale); err != nil {
		t.Fatal(err)
	}

	out := f.sweep()
	harness.Wants(t, out, "taking over a lock left behind by 777")
}

// A sweep slow enough to have its lock taken over is one whose own exit would otherwise
// delete the lock of the sweep that took it.
func TestALockIsReleasedOnlyByTheRunThatHoldsIt(t *testing.T) {
	f := newFixture(t)
	f.sweep()

	gone(t, f.cfg.GCLock)
}
