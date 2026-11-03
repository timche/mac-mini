package gc

import (
	"path/filepath"
	"strings"
)

// The worktree folder a path belongs to, and false for a path outside the root: Claude Code
// makes a worktree inside the repository it branched from, and that is the one root this
// machine has.
//
// Inside it and nowhere else is the rule every sweep below starts from, and it is not the
// same as a folder that does not exist: a daemon shared with another machine, or a compose
// run from inside a container, leaves projects whose folder was never on this disk at all,
// and `down -v` on one of those deletes somebody's database over a path that only looks
// missing.
func worktreeOf(path string) (string, bool) {
	const nested = "/.claude/worktrees/"
	if at := strings.Index(path, nested); at > 0 {
		name, _, _ := strings.Cut(path[at+len(nested):], "/")
		if name != "" {
			return filepath.Join(path[:at], ".claude", "worktrees", name), true
		}
	}

	return "", false
}
