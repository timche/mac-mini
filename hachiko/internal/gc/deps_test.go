package gc

import (
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// lsof prints a `p` line per process and an `n` line per file, and the sweep reads one pass
// of the whole machine rather than one call per pid.
func TestCWDsReadOnePassOfLsof(t *testing.T) {
	out := "p501\nfcwd\nn/Users/x/projects/app\np502\nfcwd\nn/Users/x/.herdr/worktrees/app/gone\n"

	got := parseCWDs(out)
	harness.Equal(t, len(got), 2, "the directories found")
	harness.Equal(t, got[0].PID, 501, "the first pid")
	harness.Equal(t, got[1].Dir, "/Users/x/.herdr/worktrees/app/gone", "the second directory")
}
