package main

import (
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

// FileSize is a file worth watching and the space it is actually occupying.
type FileSize struct {
	Path string
	KB   int64
}

// WalkResult is what one walk found, and what it could not look at. A directory that
// did not answer is remembered rather than retried every five minutes, and a walk that
// ran out of time says so instead of being mistaken for a disk with nothing on it.
type WalkResult struct {
	Files    []FileSize
	Stalled  []string
	CutShort bool
}

// Directories named here hold most of the files on this machine and none of the big
// ones.
var prunedNames = map[string]bool{".git": true, "node_modules": true}

// Walk is everything one sweep needs to read the disk, with the two seams that matter:
// the directory read itself, and the clock the deadlines are measured on.
type Walk struct {
	Roots   []string
	Pruned  []string
	MinKB   int64
	ReadDir func(string) ([]os.DirEntry, error)
	Now     func() time.Time

	// What one directory may take before it is abandoned, and what the whole walk may
	// take before the sweep goes on to the CPU and the alerts without it.
	DirTimeout  time.Duration
	WalkTimeout time.Duration
}

// Allocated size rather than apparent: a VM's sparse disk image reads as hundreds of
// gigabytes it is not occupying. The two Containers folders holding those images are
// pruned by path anyway, so this is about the next one nobody has thought of.
func (w Walk) Run() WalkResult {
	readDir := w.ReadDir
	if readDir == nil {
		readDir = os.ReadDir
	}
	now := w.Now
	if now == nil {
		now = time.Now
	}

	pruned := make(map[string]bool, len(w.Pruned))
	for _, p := range w.Pruned {
		pruned[p] = true
	}

	deadline := time.Time{}
	if w.WalkTimeout > 0 {
		deadline = now().Add(w.WalkTimeout)
	}

	state := &walkState{
		readDir:    readDir,
		now:        now,
		pruned:     pruned,
		minKB:      w.MinKB,
		dirTimeout: w.DirTimeout,
		deadline:   deadline,
		found:      map[string]int64{},
		stalled:    map[string]bool{},
	}

	for _, root := range w.Roots {
		info, err := os.Lstat(root)
		if err != nil || !info.IsDir() {
			continue
		}

		// -xdev: a volume mounted under a root is somebody else's disk, and its free
		// space is not the number this is about.
		var device int32 = -1
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			device = st.Dev
		}

		state.walkTree(root, device)
	}

	return state.result()
}

type walkState struct {
	readDir    func(string) ([]os.DirEntry, error)
	now        func() time.Time
	pruned     map[string]bool
	minKB      int64
	dirTimeout time.Duration
	deadline   time.Time

	mu       sync.Mutex
	found    map[string]int64
	stalled  map[string]bool
	cutShort bool
}

func (s *walkState) result() WalkResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := WalkResult{Files: make([]FileSize, 0, len(s.found)), CutShort: s.cutShort}
	for path, kb := range s.found {
		out.Files = append(out.Files, FileSize{Path: path, KB: kb})
	}
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Path < out.Files[j].Path })

	for dir := range s.stalled {
		out.Stalled = append(out.Stalled, dir)
	}
	sort.Strings(out.Stalled)

	return out
}

func (s *walkState) outOfTime() bool {
	return !s.deadline.IsZero() && s.now().After(s.deadline)
}

// A worker per directory, bounded: the cost here is one lstat per file over a few
// hundred thousand of them, and they are all waiting on the disk rather than on a core.
const walkWorkers = 16

func (s *walkState) walkTree(root string, device int32) {
	sem := make(chan struct{}, walkWorkers)
	var wg sync.WaitGroup

	var walk func(dir string)
	walk = func(dir string) {
		defer wg.Done()

		if s.outOfTime() {
			s.markCutShort()
			return
		}

		sem <- struct{}{}
		entries, ok := s.readDirWithin(dir)
		children := make([]string, 0, 8)

		if ok {
			for _, entry := range entries {
				path := filepath.Join(dir, entry.Name())
				if s.pruned[path] {
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
					// A symlink is never followed: the file it names is either under a
					// root already or on somebody else's volume.
					if kb, err := allocatedKB(entry); err == nil && kb >= s.minKB {
						s.mu.Lock()
						s.found[path] = kb
						s.mu.Unlock()
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

// A directory read that has not answered in time is abandoned rather than waited on.
// The goroutine holding the open stays blocked until the kernel lets it go, which on a
// consent-protected folder or a dead mount is never — but this process exits every five
// minutes, and a sweep that finished is worth more than a complete one.
func (s *walkState) readDirWithin(dir string) ([]os.DirEntry, bool) {
	// The walk's own deadline bounds a read that is already waiting, not just the next
	// one to start: with enough hung directories, every read is inside its own limit and
	// the walk would still never end.
	limit := s.dirTimeout
	outOfTime := false

	if !s.deadline.IsZero() {
		remaining := s.deadline.Sub(s.now())
		if remaining <= 0 {
			s.markCutShort()
			return nil, false
		}
		if limit <= 0 || remaining < limit {
			limit, outOfTime = remaining, true
		}
	}

	if limit <= 0 {
		entries, err := s.readDir(dir)
		return entries, err == nil
	}

	type answer struct {
		entries []os.DirEntry
		err     error
	}

	// Buffered, so the goroutine this abandons can finish its send and go away rather
	// than blocking on a reader that has left.
	done := make(chan answer, 1)
	go func() {
		entries, err := s.readDir(dir)
		done <- answer{entries, err}
	}()

	timer := time.NewTimer(limit)
	defer timer.Stop()

	select {
	case got := <-done:
		return got.entries, got.err == nil
	case <-timer.C:
		// Out of time is not the same as a directory that will not answer: one is this
		// walk's budget and the other is a folder to stop asking about.
		if outOfTime {
			s.markCutShort()
		} else {
			s.markStalled(dir)
		}
		return nil, false
	}
}

func (s *walkState) markStalled(dir string) {
	s.mu.Lock()
	s.stalled[dir] = true
	s.mu.Unlock()
}

func (s *walkState) markCutShort() {
	s.mu.Lock()
	s.cutShort = true
	s.mu.Unlock()
}

func allocatedKB(entry os.DirEntry) (int64, error) {
	info, err := entry.Info()
	if err != nil {
		return 0, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, os.ErrInvalid
	}
	// st_blocks is counted in 512-byte blocks whatever the filesystem's own block size,
	// which is what stat -f %b reports too.
	return st.Blocks / 2, nil
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
