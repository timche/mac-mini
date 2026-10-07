package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
)

func writeKB(t *testing.T, path string, kb int64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), int(kb*1024)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func found(files []FileSize, path string) bool {
	for _, f := range files {
		if f.Path == path {
			return true
		}
	}
	return false
}

func walkOf(roots []string, pruned []string, minKB int64) WalkResult {
	return Walk{Roots: roots, Pruned: pruned, MinKB: minKB}.Run()
}

func TestBigFilesFindsWhatIsOverTheFloorAndNothingUnderIt(t *testing.T) {
	root := t.TempDir()
	big := filepath.Join(root, "logs", "worker.log")
	small := filepath.Join(root, "logs", "small.log")

	writeKB(t, big, 2048)
	writeKB(t, small, 16)

	got := walkOf([]string{root}, nil, 1024)

	if !found(got.Files, big) {
		t.Errorf("the file over the floor was not found: %v", got.Files)
	}
	if found(got.Files, small) {
		t.Errorf("a file under the floor was reported: %v", got.Files)
	}
	equal(t, got.CutShort, false, "whether the walk ran out of time")
	equal(t, len(got.Stalled), 0, "stalled directories")
}

// A VM's sparse disk image is apparently hundreds of gigabytes it is not occupying,
// which is why the size that decides is the allocated one.
func TestASparseFileIsMeasuredByWhatItOccupies(t *testing.T) {
	root := t.TempDir()
	sparse := filepath.Join(root, "disk.img")

	file, err := os.Create(sparse)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(512 << 20); err != nil {
		t.Fatal(err)
	}
	file.Close()

	if info, err := os.Stat(sparse); err != nil || info.Size() < 512<<20 {
		t.Fatalf("the sparse file is not apparently large: %v %v", info, err)
	}

	if got := walkOf([]string{root}, nil, 1024); found(got.Files, sparse) {
		t.Errorf("a sparse file was reported for space it is not occupying: %v", got.Files)
	}
}

// .git and node_modules hold most of the files on this machine and none of the big ones,
// and the two Containers folders hold the disk images.
func TestTheWalkPrunesTheFoldersThatCostTimeAndLie(t *testing.T) {
	root := t.TempDir()
	containers := filepath.Join(root, "Library", "Containers")

	inGit := filepath.Join(root, "repo", ".git", "pack.idx")
	inModules := filepath.Join(root, "repo", "node_modules", "big.bin")
	inContainers := filepath.Join(containers, "com.example", "disk.img")
	kept := filepath.Join(root, "repo", "build.log")

	for _, path := range []string{inGit, inModules, inContainers, kept} {
		writeKB(t, path, 2048)
	}

	got := walkOf([]string{root}, []string{containers}, 1024)

	for _, pruned := range []string{inGit, inModules, inContainers} {
		if found(got.Files, pruned) {
			t.Errorf("%s was not pruned: %v", pruned, got.Files)
		}
	}
	if !found(got.Files, kept) {
		t.Errorf("the file worth watching was pruned with the rest: %v", got.Files)
	}
}

// A symlink's target is either under a root already or on somebody else's volume, and
// counting it here would count it twice or count what this is not about.
func TestTheWalkFollowsNoSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "elsewhere.log")
	writeKB(t, outside, 2048)

	link := filepath.Join(root, "linked.log")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	if got := walkOf([]string{root}, nil, 1024); len(got.Files) != 0 {
		t.Errorf("a symlink was followed: %v", got.Files)
	}
}

func TestTheWalkSkipsARootThatIsNotThere(t *testing.T) {
	if got := walkOf([]string{filepath.Join(t.TempDir(), "gone")}, nil, 1024); len(got.Files) != 0 {
		t.Errorf("a missing root produced files: %v", got.Files)
	}
}

// The folders macOS guards with a consent prompt, which under launchd an unsigned binary
// cannot open and cannot be refused either — it waits for a dialog on a screen nobody is
// looking at. The list is the fix; this is the assertion that it is the list.
func TestEveryFolderAConsentPromptGuardsIsNeverOpened(t *testing.T) {
	home := t.TempDir()
	cfg := config.Config{Home: home}

	guarded := []string{
		"Desktop", "Documents", "Downloads", "Movies", "Music", "Pictures", "Public",
		"Library",
		filepath.Join("Library", "Photos"),
		filepath.Join("Library", "Mobile Documents"),
		filepath.Join("Library", "CloudStorage"),
		filepath.Join("Library", "Containers"),
		filepath.Join("Library", "Group Containers"),
		filepath.Join("Library", "Messages"),
	}

	for _, name := range guarded {
		writeKB(t, filepath.Join(home, name, "big.log"), 2048)
	}
	writeKB(t, filepath.Join(home, "projects", "app", "build.log"), 2048)

	// The two inside ~/Library that are this machine's own output rather than anybody's
	// data, and that a runaway log and a truncate both need.
	writeKB(t, filepath.Join(home, "Library", "Logs", "worker.log"), 2048)
	writeKB(t, filepath.Join(home, "Library", "Caches", "hachiko", "build.log"), 2048)

	// Any read below one of these folders is the bug, whether the walk would have found a
	// file in it or not.
	var opened []string
	readDir := func(dir string) ([]os.DirEntry, error) {
		opened = append(opened, dir)
		return os.ReadDir(dir)
	}

	got := Walk{
		Roots:   cfg.Roots(),
		Pruned:  cfg.PrunedPaths(),
		MinKB:   1024,
		ReadDir: readDir,
	}.Run()

	for _, name := range guarded {
		dir := filepath.Join(home, name)
		for _, open := range opened {
			if open == dir {
				t.Errorf("%s was opened", dir)
			}
		}
		if found(got.Files, filepath.Join(dir, "big.log")) {
			t.Errorf("%s was walked", dir)
		}
	}

	// Nothing under ~/Library at all, except the two that come back as roots.
	for _, open := range opened {
		rest, under := under(filepath.Join(home, "Library"), open)
		if !under {
			continue
		}
		switch first, _, _ := strings.Cut(rest, "/"); first {
		case "Logs", "Caches":
		default:
			t.Errorf("%s was opened under ~/Library", open)
		}
	}

	for _, wanted := range []string{
		filepath.Join(home, "projects", "app", "build.log"),
		filepath.Join(home, "Library", "Logs", "worker.log"),
		filepath.Join(home, "Library", "Caches", "hachiko", "build.log"),
	} {
		if !found(got.Files, wanted) {
			t.Errorf("%s was not walked: %v", wanted, got.Files)
		}
	}
}

// Defence in depth behind that list: the next folder macOS decides to guard, a dead
// mount, a filesystem that stops answering. One open may not cost the machine its only
// monitor, so it is abandoned and the walk goes on.
func TestADirectoryThatNeverAnswersIsAbandonedAndNamed(t *testing.T) {
	root := t.TempDir()
	writeKB(t, filepath.Join(root, "fine", "worker.log"), 2048)
	if err := os.MkdirAll(filepath.Join(root, "hangs"), 0o755); err != nil {
		t.Fatal(err)
	}

	hangs := filepath.Join(root, "hangs")
	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })

	readDir := func(dir string) ([]os.DirEntry, error) {
		if dir == hangs {
			// What a consent-protected open does under launchd: it neither answers nor
			// fails.
			<-blocked
			return nil, nil
		}
		return os.ReadDir(dir)
	}

	started := time.Now()
	got := Walk{
		Roots:      []string{root},
		MinKB:      1024,
		ReadDir:    readDir,
		DirTimeout: 50 * time.Millisecond,
	}.Run()
	took := time.Since(started)

	if took > 5*time.Second {
		t.Fatalf("the walk waited %s on a directory that never answers", took)
	}

	equal(t, len(got.Stalled), 1, "stalled directories")
	if len(got.Stalled) == 1 {
		equal(t, got.Stalled[0], hangs, "the directory that stalled")
	}

	// And the rest of the disk was still read, which is the whole point of abandoning it.
	if !found(got.Files, filepath.Join(root, "fine", "worker.log")) {
		t.Errorf("the walk gave up on the rest of the disk: %v", got.Files)
	}
}

// Asking which volume a directory is on is an lstat, and an lstat of a mount whose server
// has gone away never comes back — ~/OrbStack is NFS. It used to happen in the parent's
// loop, outside every deadline, so one dead mount hung the whole sweep.
func TestAMountWhoseLstatNeverAnswersIsAbandonedLikeAnyOtherDirectory(t *testing.T) {
	root := t.TempDir()
	writeKB(t, filepath.Join(root, "fine", "worker.log"), 2048)
	dead := filepath.Join(root, "dead-mount")
	if err := os.MkdirAll(dead, 0o755); err != nil {
		t.Fatal(err)
	}

	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })

	lstat := func(path string) (os.FileInfo, error) {
		if path == dead {
			<-blocked
			return nil, nil
		}
		return os.Lstat(path)
	}

	started := time.Now()
	got := Walk{
		Roots:      []string{root},
		MinKB:      1024,
		Lstat:      lstat,
		DirTimeout: 50 * time.Millisecond,
	}.Run()
	took := time.Since(started)

	if took > 5*time.Second {
		t.Fatalf("the walk waited %s on a mount whose lstat never answers", took)
	}

	equal(t, len(got.Stalled), 1, "stalled directories")
	if len(got.Stalled) == 1 {
		equal(t, got.Stalled[0], dead, "the directory that stalled")
	}
	if !found(got.Files, filepath.Join(root, "fine", "worker.log")) {
		t.Errorf("the walk gave up on the rest of the disk: %v", got.Files)
	}
}

// And the same for a root, which can be a mount too.
func TestARootWhoseLstatNeverAnswersIsNotWaitedOn(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	writeKB(t, filepath.Join(other, "worker.log"), 2048)

	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })

	lstat := func(path string) (os.FileInfo, error) {
		if path == root {
			<-blocked
			return nil, nil
		}
		return os.Lstat(path)
	}

	started := time.Now()
	got := Walk{
		Roots:      []string{root, other},
		MinKB:      1024,
		Lstat:      lstat,
		DirTimeout: 50 * time.Millisecond,
	}.Run()

	if took := time.Since(started); took > 5*time.Second {
		t.Fatalf("the walk waited %s on a root whose lstat never answers", took)
	}
	if !found(got.Files, filepath.Join(other, "worker.log")) {
		t.Errorf("the root after it was not walked: %v", got.Files)
	}
}

// A directory already known not to answer costs nothing on the next run: the sweep hands
// it back as something to skip, so there is no second three seconds to pay.
func TestADirectoryAlreadyKnownToStallIsNotOpenedAgain(t *testing.T) {
	root := t.TempDir()
	hangs := filepath.Join(root, "hangs")
	if err := os.MkdirAll(hangs, 0o755); err != nil {
		t.Fatal(err)
	}

	opened := false
	readDir := func(dir string) ([]os.DirEntry, error) {
		if dir == hangs {
			opened = true
		}
		return os.ReadDir(dir)
	}

	got := Walk{
		Roots:      []string{root},
		Pruned:     []string{hangs},
		MinKB:      1024,
		ReadDir:    readDir,
		DirTimeout: time.Second,
	}.Run()

	if opened {
		t.Error("a directory known to stall was opened again")
	}
	equal(t, len(got.Stalled), 0, "stalled directories on the second run")
}

// Enough hung directories and the walk itself has to give up, so that the CPU check and
// the alerts still run: a monitor that reports nothing because it is still counting is
// the failure this exists to avoid.
func TestAWalkThatRunsOutOfTimeSaysSoRatherThanWaiting(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b", "c", "d"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })

	readDir := func(dir string) ([]os.DirEntry, error) {
		if dir == root {
			return os.ReadDir(dir)
		}
		<-blocked
		return nil, nil
	}

	started := time.Now()
	got := Walk{
		Roots:       []string{root},
		MinKB:       1024,
		ReadDir:     readDir,
		DirTimeout:  80 * time.Millisecond,
		WalkTimeout: 40 * time.Millisecond,
	}.Run()
	took := time.Since(started)

	if took > 5*time.Second {
		t.Fatalf("the walk waited %s with every directory hung", took)
	}
	equal(t, got.CutShort, true, "whether the walk said it ran out of time")
}
