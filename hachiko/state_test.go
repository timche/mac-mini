package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStateSurvivesASaveAndALoad(t *testing.T) {
	store := Store{dir: filepath.Join(t.TempDir(), "hachiko")}

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

	equal(t, got.Disk.Files["/tmp/a.log"], int64(2048), "a recorded size")
	equal(t, got.CPU.Procs["7018:x"].CPU, float64(90), "a recorded cpu time")
	equal(t, got.LowSpaceLevel, int64(20), "the recorded threshold")
	equal(t, got.Pending["disk-1"].Tab, "disk-0000", "the recorded tab")
	if !got.alertedFile("/tmp/a.log") || !got.alertedProc("7018:x") {
		t.Error("what was alerted on was not recorded")
	}
}

func TestAMissingStateFileIsAnEmptyStateRatherThanAnError(t *testing.T) {
	store := Store{dir: filepath.Join(t.TempDir(), "hachiko")}

	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.hasDiskSample() || state.hasCPUSample() {
		t.Error("an empty state claims to hold a sample")
	}
}

// A corrupt sample costs one interval of history; a refusal to read it would cost every
// interval after it.
func TestACorruptStateFileStartsOverRatherThanStopping(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hachiko")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	state, err := Store{dir: dir}.Load()
	if err != nil {
		t.Fatalf("a corrupt state file stopped the check: %v", err)
	}
	if state.hasDiskSample() {
		t.Error("a corrupt state file was read as a sample")
	}
}

func TestOnlyOneCheckHoldsTheLock(t *testing.T) {
	store := Store{dir: filepath.Join(t.TempDir(), "hachiko")}

	first, ok, _ := store.Acquire(5*time.Minute, base)
	if !ok {
		t.Fatal("the first check could not take the lock")
	}

	if _, ok, _ := store.Acquire(5*time.Minute, base); ok {
		t.Error("a second check took a lock the first one holds")
	}

	first.Release()
	if _, ok, _ := store.Acquire(5*time.Minute, base); !ok {
		t.Error("the lock was not released")
	}
}

// A lock left behind by a killed check must not stop every check after it.
func TestALockOlderThanTheIntervalIsTakenOver(t *testing.T) {
	store := Store{dir: filepath.Join(t.TempDir(), "hachiko")}

	if _, ok, _ := store.Acquire(5*time.Minute, base); !ok {
		t.Fatal("the first check could not take the lock")
	}

	_, ok, takenFrom := store.Acquire(5*time.Minute, base.Add(6*time.Minute))
	if !ok {
		t.Fatal("a stale lock was not taken over")
	}
	if takenFrom == "" {
		t.Error("taking over a stale lock went unsaid")
	}
}
