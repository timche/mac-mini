package listen

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/timche/mac-mini/mochi/internal/op"
)

// Nothing else runs mochi's wrapper on a timer, and the wrapper is the only thing that
// builds an edit. So the listener runs it itself, for nothing but the build: a new binary
// moved into place is then what the inode check sees, and KeepAlive starts it.
const refreshEvery = 5 * time.Minute

type refresher struct {
	every time.Duration
	last  time.Time
	run   func() error
}

// The first run is one interval after the start: the wrapper has just built the binary that
// is running.
func newRefresher(start time.Time, run func() error) *refresher {
	return &refresher{every: refreshEvery, last: start, run: run}
}

func (r *refresher) tick(now time.Time) error {
	if now.Sub(r.last) < r.every {
		return nil
	}
	r.last = now
	return r.run()
}

// Through the link in ~/.local/bin, which is the wrapper launchd started; `version` does
// nothing once it is built. Its own two bounded steps fit well inside the five minutes, and
// it is handed no secret.
func runWrapper(home string) func() error {
	return func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		cmd := exec.CommandContext(ctx, filepath.Join(home, ".local", "bin", "mochi"), "version")
		cmd.Env = op.Scrub(os.Environ())
		cmd.Stdout, cmd.Stderr = io.Discard, os.Stderr
		return cmd.Run()
	}
}
