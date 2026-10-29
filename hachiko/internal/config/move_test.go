package config

import (
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
)

// What hachiko keeps in the four folders, as this test seeds it: every name in moves, the
// folders among them with something inside.
var (
	stateFiles = []string{
		"state.json", "host", "update-raised.json", "last-run", "failing.json", "heartbeat",
	}
	stateDirs = map[string]string{
		"reported":  "disk-1",
		"approvals": "cpu-2",
		"listen":    "heartbeat",
	}

	// The wrapper's, in the watch's old folder, which the move has no business touching.
	buildOutput = []string{
		"hachiko", "attempted", "build.log",
		filepath.Join("worktrees", "agent-1", "hachiko"),
	}
)

// A state file nobody seeded is one the move was never tried on.
func TestEveryStateNameIsSeededByTheseTests(t *testing.T) {
	for _, m := range moves {
		for _, name := range m.Names {
			if _, ok := stateDirs[name]; !ok && !slices.Contains(stateFiles, name) {
				t.Errorf("%s/%s is state the move is not tested on", m.Old, name)
			}
		}
	}
}

// A HOME with the four folders where hachiko kept them and the build output in the watch's,
// and nothing in hachiko's own folder yet. Library/Caches first, so the old root is worked
// out the way it is on this Mac rather than the way it would be anywhere else.
func oldFolders(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, m := range moves {
		t.Setenv(m.Var, "")
	}
	if err := os.MkdirAll(filepath.Join(home, "Library", "Caches"), 0o700); err != nil {
		t.Fatal(err)
	}

	for _, m := range moves {
		for _, name := range m.Names {
			path := filepath.Join(oldDir(home, m), name)
			if entry, ok := stateDirs[name]; ok {
				path = filepath.Join(path, entry)
			}
			put(t, path, seeded(m, name))
		}
	}
	for _, name := range buildOutput {
		put(t, filepath.Join(oldRoot(home), "hachiko", name), name)
	}
	return home
}

// Named for the folder as well as the file, so a file that arrives in another folder's new
// one reads as what it is rather than as the right contents.
func seeded(m moveSpec, name string) string { return m.Old + "/" + name }

func oldDir(home string, m moveSpec) string { return filepath.Join(oldRoot(home), m.Old) }
func newDir(home string, m moveSpec) string { return filepath.Join(stateRoot(home), m.New) }

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
	home := oldFolders(t)
	if err := MoveState(); err != nil {
		t.Fatal(err)
	}

	for _, m := range moves {
		for _, name := range m.Names {
			path := filepath.Join(newDir(home, m), name)
			if entry, ok := stateDirs[name]; ok {
				path = filepath.Join(path, entry)
			}
			if got := text(t, path); got != seeded(m, name) {
				t.Errorf("%s/%s = %q", m.New, name, got)
			}
			if _, err := os.Lstat(filepath.Join(oldDir(home, m), name)); err == nil {
				t.Errorf("%s is still in %s", name, m.Old)
			}
		}
	}

	for _, name := range buildOutput {
		if got := text(t, filepath.Join(oldRoot(home), "hachiko", name)); got != name {
			t.Errorf("the build output's %s = %q", name, got)
		}
		if _, err := os.Lstat(filepath.Join(stateRoot(home), "watch", name)); err == nil {
			t.Errorf("%s was taken for state", name)
		}
	}

	// Again, with the old folders holding the build output alone.
	if err := MoveState(); err != nil {
		t.Fatal(err)
	}
	if got := text(t, filepath.Join(stateRoot(home), "watch", "state.json")); got != "hachiko/state.json" {
		t.Errorf("the watch's state.json = %q after a second start", got)
	}
}

// What hachiko has been using is what it keeps: a file the new folder has is not written
// over, and a folder on both sides is merged rather than replaced.
func TestStateTheNewFolderAlreadyHasIsNotWrittenOver(t *testing.T) {
	home := oldFolders(t)
	watch := filepath.Join(stateRoot(home), "watch")
	put(t, filepath.Join(watch, "state.json"), "the newer state")
	put(t, filepath.Join(watch, "listen", "seen-1"), "a thread read since")

	if err := MoveState(); err != nil {
		t.Fatal(err)
	}
	if got := text(t, filepath.Join(watch, "state.json")); got != "the newer state" {
		t.Errorf("state.json = %q", got)
	}
	if got := text(t, filepath.Join(oldRoot(home), "hachiko", "state.json")); got != "hachiko/state.json" {
		t.Errorf("the old state.json = %q, where it should have been left alone", got)
	}
	if got := text(t, filepath.Join(watch, "listen", "heartbeat")); got != "hachiko/listen" {
		t.Errorf("the heartbeat from the old folder = %q", got)
	}
	if got := text(t, filepath.Join(watch, "listen", "seen-1")); got != "a thread read since" {
		t.Errorf("the thread read since = %q", got)
	}
	if _, err := os.Lstat(filepath.Join(oldRoot(home), "hachiko", "listen")); err == nil {
		t.Error("the old listen folder is still there, with nothing left in it")
	}
}

// The watch, the sweep, sync, the listener, the update and an on-call session's own commands
// all start on their own, so several moves can be under way at once: each file arrives once
// and whole.
func TestConcurrentStartsMoveTheStateOnce(t *testing.T) {
	home := oldFolders(t)

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
	for _, m := range moves {
		for _, name := range m.Names {
			path := filepath.Join(newDir(home, m), name)
			if entry, ok := stateDirs[name]; ok {
				path = filepath.Join(path, entry)
			}
			if got := text(t, path); got != seeded(m, name) {
				t.Errorf("%s/%s = %q", m.New, name, got)
			}
		}
	}
}

// A test, a fixture or a second hachiko told where to keep its state neither reads the live
// folders nor empties them, which is what lets the gate build and run this binary with this
// Mac's own HOME.
func TestStateFoldersOfItsOwnAreLeftAlone(t *testing.T) {
	home := oldFolders(t)
	for _, m := range moves {
		t.Setenv(m.Var, filepath.Join(home, "elsewhere", m.New))
	}

	if err := MoveState(); err != nil {
		t.Fatal(err)
	}
	for _, m := range moves {
		if _, err := os.Lstat(filepath.Join(oldDir(home, m), "state.json")); err != nil {
			t.Errorf("%s was moved out from under %s: %v", m.Old, m.Var, err)
		}
	}
	if _, err := os.Lstat(Dir(home)); err == nil {
		t.Errorf("%s was made anyway", Dir(home))
	}
}

// Each variable turns off its own folder's move and no other, so a run that names one of the
// four leaves the other three where they are rather than taking them over.
func TestOneFolderToldWhereToGoLeavesTheRestWhereTheyAre(t *testing.T) {
	home := oldFolders(t)
	t.Setenv(StateDirVar, filepath.Join(home, "elsewhere", "watch"))

	if err := MoveState(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(oldRoot(home), "hachiko", "state.json")); err != nil {
		t.Errorf("the watch's state was moved out from under %s: %v", StateDirVar, err)
	}
	if _, err := os.Lstat(filepath.Join(stateRoot(home), "watch")); err == nil {
		t.Error("the watch's new folder was made anyway")
	}
	for _, m := range moves[1:] {
		if got := text(t, filepath.Join(newDir(home, m), "state.json")); got != seeded(m, "state.json") {
			t.Errorf("%s/state.json = %q", m.New, got)
		}
	}
}
