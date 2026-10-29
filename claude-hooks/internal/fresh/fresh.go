// Package fresh answers the one question the binary asks about itself before it reads an
// event: are the sources it was built from newer than it is?
//
// It is asked here because settings.json registers the built binary and not the wrapper
// that builds it. A shell, a find, a shasum and a mise before every Bash call in every
// session cost several times the check the hook exists for, so the watching the wrapper
// did on the way past is done by the binary instead: when the sources have moved it hands
// the event to the wrapper, the only part of this that can build.
//
// So this compares modification times and reads no file's contents. Hashing the tree is
// the wrapper's, once it has been handed the event and a session is already waiting on a
// build.
package fresh

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LoopGuard is set in the environment by whichever side execs the other, since an exec
// that came back here would be an exec before every command in every session.
const LoopGuard = "CLAUDE_HOOKS_FRESH"

type Params struct {
	// Source is the folder the binary was built from, written in at build time. It is
	// empty under `go run`, in a test binary and in a build by hand, where there is no
	// wrapper and nothing to compare against.
	Source string

	// Binary is the running executable and Marker the file the wrapper writes after
	// every build attempt, successful or not. The newer of the two is what the sources
	// are compared against: a build that failed leaves a binary older than the sources
	// that broke it, and without the marker every command afterwards would hand the
	// event over for a build that is going to fail the same way.
	Binary string
	Marker string

	// Wrapped says the wrapper is what started this process, so it has already decided
	// what to build.
	Wrapped bool
}

// Stale reports whether the sources have moved since the last build attempt. Everything
// it cannot read it reports as fresh: it runs in front of a check whose answer decides
// whether a session's command runs at all, and an unreadable folder is not an edit.
func Stale(p Params) bool {
	if p.Wrapped || p.Source == "" || p.Binary == "" {
		return false
	}

	binary, err := os.Stat(p.Binary)
	if err != nil {
		return false
	}
	since := binary.ModTime()
	if marker, err := os.Stat(p.Marker); err == nil && marker.ModTime().After(since) {
		since = marker.ModTime()
	}

	root, err := os.Stat(p.Source)
	if err != nil || !root.IsDir() {
		return false
	}
	return newer(p.Source, root, since)
}

// A folder's own time counts as much as the sources in it: a deletion or a rename changes
// nothing else, the file whose time would have said so being the file that is gone.
func newer(dir string, info os.FileInfo, since time.Time) bool {
	if info.ModTime().After(since) {
		return true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() && !isSource(entry.Name()) {
			continue
		}
		stat, err := entry.Info()
		if err != nil {
			continue
		}
		if !entry.IsDir() {
			if stat.ModTime().After(since) {
				return true
			}
			continue
		}
		if newer(filepath.Join(dir, entry.Name()), stat, since) {
			return true
		}
	}
	return false
}

func isSource(name string) bool {
	return strings.HasSuffix(name, ".go") || name == "go.mod" || name == "go.sum"
}
