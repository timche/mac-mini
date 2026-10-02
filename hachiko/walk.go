package main

import (
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
)

// FileSize is a file worth watching and the space it is actually occupying.
type FileSize struct {
	Path string
	KB   int64
}

// Directories named here hold most of the files on this machine and none of the
// big ones.
var prunedNames = map[string]bool{".git": true, "node_modules": true}

// Allocated size rather than apparent: a VM's sparse disk image reads as hundreds
// of gigabytes it is not occupying. The two Containers folders holding those images
// are pruned by path anyway, so this is about the next one nobody has thought of.
func bigFiles(roots, prunedPaths []string, minKB int64) []FileSize {
	pruned := make(map[string]bool, len(prunedPaths))
	for _, p := range prunedPaths {
		pruned[p] = true
	}

	found := make(map[string]int64)
	var mu sync.Mutex

	for _, root := range roots {
		info, err := os.Lstat(root)
		if err != nil || !info.IsDir() {
			continue
		}

		// -xdev: a volume mounted under a root is somebody else's disk, and its
		// free space is not the number this is about.
		var device int32 = -1
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			device = st.Dev
		}

		walkTree(root, device, pruned, minKB, func(path string, kb int64) {
			mu.Lock()
			found[path] = kb
			mu.Unlock()
		})
	}

	out := make([]FileSize, 0, len(found))
	for path, kb := range found {
		out = append(out, FileSize{Path: path, KB: kb})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// A worker per directory, bounded: the cost here is one lstat per file over a few
// hundred thousand of them, and they are all waiting on the disk rather than on a
// core.
const walkWorkers = 16

func walkTree(root string, device int32, pruned map[string]bool, minKB int64, emit func(string, int64)) {
	sem := make(chan struct{}, walkWorkers)
	var wg sync.WaitGroup

	var walk func(dir string)
	walk = func(dir string) {
		defer wg.Done()

		sem <- struct{}{}
		entries, err := os.ReadDir(dir)
		children := make([]string, 0, 8)

		if err == nil {
			for _, entry := range entries {
				path := filepath.Join(dir, entry.Name())
				if pruned[path] {
					continue
				}

				switch {
				case entry.IsDir():
					if prunedNames[entry.Name()] {
						continue
					}
					if sameDevice(path, device) {
						children = append(children, path)
					}
				case entry.Type().IsRegular():
					// A symlink is never followed: the file it names is either under
					// a root already or on somebody else's volume.
					if kb, ok := allocatedKB(entry); ok && kb >= minKB {
						emit(path, kb)
					}
				}
			}
		}
		<-sem

		for _, child := range children {
			wg.Add(1)
			go walk(child)
		}
	}

	wg.Add(1)
	walk(root)
	wg.Wait()
}

func allocatedKB(entry os.DirEntry) (int64, bool) {
	info, err := entry.Info()
	if err != nil {
		return 0, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	// st_blocks is counted in 512-byte blocks whatever the filesystem's own block
	// size, which is what stat -f %b reports too.
	return st.Blocks / 2, true
}

func sameDevice(path string, device int32) bool {
	if device < 0 {
		return true
	}
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Dev == device
}
