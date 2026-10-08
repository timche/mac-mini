package statedir

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

var base = time.Unix(1700000000, 0)

func TestStateSurvivesASaveAndALoad(t *testing.T) {
	store := Store{Dir: filepath.Join(t.TempDir(), "hachiko")}

	want := &State{
		Disk:          DiskSample{At: base.Unix(), Files: map[string]int64{"/tmp/a.log": 2048}},
		CPU:           CPUSample{At: base.Unix(), Procs: map[string]ProcSample{"7018:x": {CPU: 90, HotSince: 1}}},
		AlertedFiles:  []string{"/tmp/a.log"},
		AlertedProcs:  []string{"7018:x"},
		LowSpaceLevel: 20,
		Pending:       map[string]Pending{"disk-1": {OpenedAt: 1, Tab: "disk-0000", Details: "the lot"}},
	}

	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}

	harness.Equal(t, got.Disk.Files["/tmp/a.log"], int64(2048), "a recorded size")
	harness.Equal(t, got.CPU.Procs["7018:x"].CPU, float64(90), "a recorded cpu time")
	harness.Equal(t, got.LowSpaceLevel, int64(20), "the recorded threshold")
	harness.Equal(t, got.Pending["disk-1"].Tab, "disk-0000", "the recorded tab")
	if !got.AlertedFile("/tmp/a.log") || !got.AlertedProc("7018:x") {
		t.Error("what was alerted on was not recorded")
	}
}

func TestAMissingStateFileIsAnEmptyStateRatherThanAnError(t *testing.T) {
	store := Store{Dir: filepath.Join(t.TempDir(), "hachiko")}

	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.HasDiskSample() || state.HasCPUSample() {
		t.Error("an empty state claims to hold a sample")
	}
}

func TestOnlyOneCheckHoldsTheLock(t *testing.T) {
	store := Store{Dir: filepath.Join(t.TempDir(), "hachiko")}

	first, _, err := store.Acquire(5*time.Minute, base)
	if err != nil {
		t.Fatalf("the first check could not take the lock: %v", err)
	}

	// A live holder is the one failure that is not a fault, and it has a sentinel of its
	// own so that everything else can be said out loud.
	if _, _, err := store.Acquire(5*time.Minute, base); !errors.Is(err, ErrLockHeld) {
		t.Errorf("a second check saw %v rather than a held lock", err)
	}

	first.Release()
	if _, _, err := store.Acquire(5*time.Minute, base); err != nil {
		t.Errorf("the lock was not released: %v", err)
	}
}

// A lock left behind by a killed check must not stop every check after it.
func TestALockOlderThanTheIntervalIsTakenOver(t *testing.T) {
	store := Store{Dir: filepath.Join(t.TempDir(), "hachiko")}

	if _, _, err := store.Acquire(5*time.Minute, base); err != nil {
		t.Fatalf("the first check could not take the lock: %v", err)
	}

	_, takenFrom, err := store.Acquire(5*time.Minute, base.Add(6*time.Minute))
	if err != nil {
		t.Fatalf("a stale lock was not taken over: %v", err)
	}
	if takenFrom == "" {
		t.Error("taking over a stale lock went unsaid")
	}
}

// A sweep slow enough to have its lock taken over must not then delete the lock of the
// check that took it, or a third would run beside both.
func TestReleasingALockSomebodyElseNowHoldsDoesNothing(t *testing.T) {
	store := Store{Dir: filepath.Join(t.TempDir(), "hachiko")}

	slow, _, err := store.Acquire(5*time.Minute, base)
	if err != nil {
		t.Fatal(err)
	}

	// The takeover, as another process: the pid in the lock is no longer the slow one's.
	if err := os.WriteFile(filepath.Join(store.Dir, "lock", "pid"), []byte("999999"), 0o644); err != nil {
		t.Fatal(err)
	}

	slow.Release()

	if _, _, err := store.Acquire(5*time.Minute, base); !errors.Is(err, ErrLockHeld) {
		t.Errorf("a released lock was somebody else's: %v", err)
	}
}

// A disk with nothing left is the fault this watch exists to catch, so `mkdir` failing
// for want of space may not look like another check holding the lock.
func TestALockThatCannotBeMadeIsNotMistakenForAHeldOne(t *testing.T) {
	// A file where the state directory should be: mkdir fails with something that is not
	// EEXIST on the lock itself.
	dir := filepath.Join(t.TempDir(), "hachiko")
	if err := os.WriteFile(dir, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := Store{Dir: dir}.Acquire(5*time.Minute, base)
	if err == nil {
		t.Fatal("a state directory that is a file was taken as a lock")
	}
	if errors.Is(err, ErrLockHeld) {
		t.Error("a broken state directory was reported as another check holding the lock")
	}
}

// The marker `hachiko notify` leaves, which needs no lock: an id that is not one a
// sweep makes may not become a filename.
func TestOnlyAnIncidentIdCanBeMarkedReported(t *testing.T) {
	store := Store{Dir: filepath.Join(t.TempDir(), "hachiko")}

	if err := store.MarkReported("disk-1700000000", "stop the worker", false); err != nil {
		t.Fatal(err)
	}
	if !store.Reported("disk-1700000000") {
		t.Error("a report was not recorded")
	}
	harness.Equal(t, store.ReportedFallback("disk-1700000000"), "stop the worker", "the option read back off the marker")

	store.ClearReported("disk-1700000000")
	if store.Reported("disk-1700000000") {
		t.Error("a cleared report is still recorded")
	}

	for _, bad := range []string{"../../etc/passwd", "disk", "disk-", "disk-1/x", "", "Disk-1"} {
		if err := store.MarkReported(bad, "", false); err == nil {
			t.Errorf("%q was accepted as an incident id", bad)
		}
		if store.Reported(bad) {
			t.Errorf("%q was read back as a report", bad)
		}
	}
}

// A corrupt sample costs one interval of history; going quiet about it would cost every
// interval after it, since a file that cannot be read every run is a sweep that can
// never measure growth.
func TestACorruptStateFileIsSaidOutLoud(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hachiko")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	state, err := Store{Dir: dir}.Load()
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("a corrupt state file answered %v", err)
	}
	if state.HasDiskSample() {
		t.Error("a corrupt state file was read as a sample")
	}
}

// A new binary's first state file is the last one's, written when the worktree sweep's and
// sync's liveness each had two fields of their own. An alert open at that moment has to go
// on being one alert and clear into the thread that raised it.
func TestTheLivenessFieldsOfAnOlderStateAreReadAndThenDropped(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hachiko")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	older := `{"gc_stale":1700000000,"gc_thread":"thread-gc","sync_stale":1700000300,"sync_thread":"thread-sync"}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(older), 0o644); err != nil {
		t.Fatal(err)
	}

	store := Store{Dir: dir}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}

	if got := state.Agent(AgentGC); got.Stale != 1700000000 || got.Thread != "thread-gc" {
		t.Errorf("the sweep's open alert: %+v", got)
	}
	if got := state.Agent(AgentSync); got.Stale != 1700000300 || got.Thread != "thread-sync" {
		t.Errorf("sync's open alert: %+v", got)
	}

	// Nothing below the load has two places to look, and the next save drops them.
	if state.GCStale != 0 || state.GCThread != "" || state.SyncStale != 0 || state.SyncThread != "" {
		t.Errorf("the older fields survived the load: %+v", state)
	}

	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "gc_stale") || strings.Contains(string(saved), "sync_stale") {
		t.Errorf("the older fields were written again: %s", saved)
	}

	// And a listener, which the older shape never knew about, starts from nothing.
	if state.Agent(AgentListen).Stale != 0 {
		t.Error("the listener came out of an older state with an alert open")
	}
}
