package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
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

func TestBigFilesFindsWhatIsOverTheFloorAndNothingUnderIt(t *testing.T) {
	root := t.TempDir()
	big := filepath.Join(root, "logs", "worker.log")
	small := filepath.Join(root, "logs", "small.log")

	writeKB(t, big, 2048)
	writeKB(t, small, 16)

	files := bigFiles([]string{root}, nil, 1024)

	if !found(files, big) {
		t.Errorf("the file over the floor was not found: %v", files)
	}
	if found(files, small) {
		t.Errorf("a file under the floor was reported: %v", files)
	}
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

	if files := bigFiles([]string{root}, nil, 1024); found(files, sparse) {
		t.Errorf("a sparse file was reported for space it is not occupying: %v", files)
	}
}

// .git and node_modules hold most of the files on this machine and none of the big
// ones, and the two Containers folders hold the disk images.
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

	files := bigFiles([]string{root}, []string{containers}, 1024)

	for _, pruned := range []string{inGit, inModules, inContainers} {
		if found(files, pruned) {
			t.Errorf("%s was not pruned: %v", pruned, files)
		}
	}
	if !found(files, kept) {
		t.Errorf("the file worth watching was pruned with the rest: %v", files)
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

	if files := bigFiles([]string{root}, nil, 1024); len(files) != 0 {
		t.Errorf("a symlink was followed: %v", files)
	}
}

func TestTheWalkSkipsARootThatIsNotThere(t *testing.T) {
	if files := bigFiles([]string{filepath.Join(t.TempDir(), "gone")}, nil, 1024); len(files) != 0 {
		t.Errorf("a missing root produced files: %v", files)
	}
}
