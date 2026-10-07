package watch

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

// Walk is everything one sweep needs to read the disk, with the seams that matter: the
// two calls that touch the filesystem, and the clock the deadlines are measured on.
//
// Lstat is a seam and not an implementation detail because it is the other call that can
// hang: ~/OrbStack is an NFS mount, and asking which volume a directory is on blocks on a
// server that has gone away exactly as reading it would.
type Walk struct {
	Roots   []string
	Pruned  []string
	MinKB   int64
	ReadDir func(string) ([]os.DirEntry, error)
	Lstat   func(string) (os.FileInfo, error)
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
	lstat := w.Lstat
	if lstat == nil {
		lstat = os.Lstat
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
		lstat:      lstat,
		now:        now,
		pruned:     pruned,
		minKB:      w.MinKB,
		dirTimeout: w.DirTimeout,
		deadline:   deadline,
		found:      map[string]int64{},
		stalled:    map[string]bool{},
	}

	for _, root := range w.Roots {
		// -xdev: a volume mounted under a root is somebody else's disk, and its free
		// space is not the number this is about. Timed like everything else, because a
		// root can be a mount too.
		device, ok := state.deviceOf(root)
		if !ok {
			continue
		}
		state.walkTree(root, device)
	}

	return state.result()
}

type walkState struct {
	readDir    func(string) ([]os.DirEntry, error)
	lstat      func(string) (os.FileInfo, error)
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
		entries, ok := s.openDir(dir, device)
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
					// Which volume it is on is settled in its own timed read rather than
					// here: asking costs an lstat, and an lstat of a mount whose server has
					// gone away never comes back.
					children = append(children, path)
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

// Everything that touches a directory happens here, under one deadline: which volume it
// is on, and what is in it. Both are calls that never come back on a mount whose server
// has gone away or a folder behind a consent prompt — so neither may happen anywhere
// else.
//
// What does not answer in time is abandoned rather than waited on. The goroutine holding
// it stays blocked until the kernel lets it go, which may be never, but this process
// exits every five minutes and a sweep that finished is worth more than a complete one.
func (s *walkState) openDir(dir string, device int32) ([]os.DirEntry, bool) {
	type answer struct {
		entries []os.DirEntry
		ok      bool
	}

	// Buffered, so the goroutine this abandons can finish its send and go away rather
	// than blocking on a reader that has left.
	done := make(chan answer, 1)
	work := func() {
		if !s.onDevice(dir, device) {
			done <- answer{}
			return
		}
		entries, err := s.readDir(dir)
		done <- answer{entries, err == nil}
	}

	if !s.within(dir, work) {
		return nil, false
	}
	got := <-done
	return got.entries, got.ok
}

// The device of a root, which every directory under it is then measured against.
func (s *walkState) deviceOf(root string) (int32, bool) {
	type answer struct {
		device int32
		ok     bool
	}

	done := make(chan answer, 1)
	work := func() {
		info, err := s.lstat(root)
		if err != nil || info == nil || !info.IsDir() {
			done <- answer{}
			return
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			// Unmeasurable rather than missing: walk it, and let every child match.
			done <- answer{device: -1, ok: true}
			return
		}
		done <- answer{device: st.Dev, ok: true}
	}

	if !s.within(root, work) {
		return 0, false
	}
	got := <-done
	return got.device, got.ok
}

func (s *walkState) onDevice(dir string, device int32) bool {
	if device < 0 {
		return true
	}
	info, err := s.lstat(dir)
	if err != nil || info == nil {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Dev == device
}

// Runs work and says whether it finished in time. The walk's own deadline bounds work
// that is already waiting, not just the next piece to start: with enough hung
// directories, every one of them is inside its own limit and the walk still never ends.
func (s *walkState) within(dir string, work func()) bool {
	limit := s.dirTimeout
	outOfTime := false

	if !s.deadline.IsZero() {
		remaining := s.deadline.Sub(s.now())
		if remaining <= 0 {
			s.markCutShort()
			return false
		}
		if limit <= 0 || remaining < limit {
			limit, outOfTime = remaining, true
		}
	}

	if limit <= 0 {
		work()
		return true
	}

	finished := make(chan struct{})
	go func() {
		work()
		close(finished)
	}()

	timer := time.NewTimer(limit)
	defer timer.Stop()

	select {
	case <-finished:
		return true
	case <-timer.C:
		// Out of time is not the same as a directory that will not answer: one is this
		// walk's budget and the other is a folder to stop asking about.
		if outOfTime {
			s.markCutShort()
		} else {
			s.markStalled(dir)
		}
		return false
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
