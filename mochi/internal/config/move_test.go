package config

import (
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
)

// What mochi keeps, as this test seeds it: every name in stateNames, the folders among them
// with something inside.
var (
	stateFiles = []string{
		"state.json", "marks.json", "waits.json", "pending.json", "prs.json",
		"heartbeat", "identify", "op-attempt",
	}
	stateDirs = map[string]string{
		"events":   "t1-1.json",
		"sessions": filepath.Join("t1", "shot.png"),
	}

	// The wrapper's, in the same folder, which the move has no business touching.
	buildOutput = []string{"mochi", "source.sha256", "build.log", filepath.Join("worktrees", "agent-1", "mochi")}
)

// A state file nobody seeded is one the move was never tried on.
func TestEveryStateFileIsSeededByTheseTests(t *testing.T) {
	for _, name := range stateNames {
		if _, ok := stateDirs[name]; !ok && !slices.Contains(stateFiles, name) {
			t.Errorf("%s is state the move is not tested on", name)
		}
	}
}

// A HOME with mochi's state and its build output where mochi kept both, and nothing in
// mochi's own folder yet.
func oldFolder(t *testing.T) (home, old string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(StateDirVar, "")
	old = oldStateDir(home)
	for _, name := range stateFiles {
		put(t, filepath.Join(old, name), name)
	}
	for dir, entry := range stateDirs {
		put(t, filepath.Join(old, dir, entry), entry)
	}
	for _, name := range buildOutput {
		put(t, filepath.Join(old, name), name)
	}
	return home, old
}

func put(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func text(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTheStateComesOutOfTheBuildCache(t *testing.T) {
	home, old := oldFolder(t)
	if err := MoveState(); err != nil {
		t.Fatal(err)
	}

	for _, name := range stateFiles {
		if got := text(t, filepath.Join(Dir(home), name)); got != name {
			t.Errorf("%s = %q", name, got)
		}
		if _, err := os.Lstat(filepath.Join(old, name)); err == nil {
			t.Errorf("%s is still in the old folder", name)
		}
	}
	for dir, entry := range stateDirs {
		if got := text(t, filepath.Join(Dir(home), dir, entry)); got != entry {
			t.Errorf("%s/%s = %q", dir, entry, got)
		}
		if _, err := os.Lstat(filepath.Join(old, dir)); err == nil {
			t.Errorf("%s is still in the old folder", dir)
		}
	}
	for _, name := range buildOutput {
		if got := text(t, filepath.Join(old, name)); got != name {
			t.Errorf("the build output's %s = %q", name, got)
		}
		if _, err := os.Lstat(filepath.Join(Dir(home), name)); err == nil {
			t.Errorf("%s was taken for state", name)
		}
	}

	// Again, with the old folder holding the build output alone.
	if err := MoveState(); err != nil {
		t.Fatal(err)
	}
	if got := text(t, filepath.Join(Dir(home), "marks.json")); got != "marks.json" {
		t.Errorf("marks.json = %q after a second start", got)
	}
}

// What mochi has been using is what it keeps: a file the new folder has is not written over,
// and a folder on both sides is merged rather than replaced.
func TestStateTheNewFolderAlreadyHasIsNotWrittenOver(t *testing.T) {
	home, old := oldFolder(t)
	put(t, filepath.Join(Dir(home), "marks.json"), "the newer marks")
	put(t, filepath.Join(Dir(home), "events", "t1-2.json"), "an event since")

	if err := MoveState(); err != nil {
		t.Fatal(err)
	}
	if got := text(t, filepath.Join(Dir(home), "marks.json")); got != "the newer marks" {
		t.Errorf("marks.json = %q", got)
	}
	if got := text(t, filepath.Join(old, "marks.json")); got != "marks.json" {
		t.Errorf("the old marks.json = %q, where it should have been left alone", got)
	}
	if got := text(t, filepath.Join(Dir(home), "events", "t1-1.json")); got != "t1-1.json" {
		t.Errorf("the event from the old folder = %q", got)
	}
	if got := text(t, filepath.Join(Dir(home), "events", "t1-2.json")); got != "an event since" {
		t.Errorf("the event written since = %q", got)
	}
	if _, err := os.Lstat(filepath.Join(old, "events")); err == nil {
		t.Error("the old events folder is still there, with nothing left in it")
	}
}

// The listener and every hook-run `mochi session-event` start on their own, so several moves
// can be under way at once: each file arrives once and whole.
func TestConcurrentStartsMoveTheStateOnce(t *testing.T) {
	home, _ := oldFolder(t)

	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = MoveState()
		}()
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range stateFiles {
		if got := text(t, filepath.Join(Dir(home), name)); got != name {
			t.Errorf("%s = %q", name, got)
		}
	}
	for dir, entry := range stateDirs {
		if got := text(t, filepath.Join(Dir(home), dir, entry)); got != entry {
			t.Errorf("%s/%s = %q", dir, entry, got)
		}
	}
}

func TestAStateFolderOfItsOwnIsLeftAlone(t *testing.T) {
	home, old := oldFolder(t)
	t.Setenv(StateDirVar, filepath.Join(home, "elsewhere"))

	if err := MoveState(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(old, "marks.json")); err != nil {
		t.Errorf("the marks were moved out from under %s: %v", StateDirVar, err)
	}
	if _, err := os.Lstat(Dir(home)); err == nil {
		t.Errorf("%s was made anyway", Dir(home))
	}
}
