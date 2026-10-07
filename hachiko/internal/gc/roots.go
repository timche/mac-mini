package gc

import (
	"path/filepath"
	"strings"
)

// The worktree folder a path belongs to, and false for a path outside both roots. herdr
// nests a worktree one level deeper than Claude Code, which puts its own inside the
// repository it branched from.
//
// Inside a root and nowhere else is the rule every sweep below starts from, and it is not
// the same as a folder that does not exist: a daemon shared with another machine, or a
// compose run from inside a container, leaves projects whose folder was never on this disk
// at all, and `down -v` on one of those deletes somebody's database over a path that only
// looks missing.
func worktreeOf(herdrRoot, path string) (string, bool) {
	if rest, ok := under(herdrRoot, path); ok {
		repo, rest := cut(rest)
		branch, _ := cut(rest)

		if repo != "" && branch != "" {
			return filepath.Join(herdrRoot, repo, branch), true
		}
	}

	const nested = "/.claude/worktrees/"
	if at := strings.Index(path, nested); at > 0 {
		name, _ := cut(path[at+len(nested):])
		if name != "" {
			return filepath.Join(path[:at], ".claude", "worktrees", name), true
		}
	}

	return "", false
}

// What is left of a path below a root, and false when it is not below it at all. The
// trailing separator matters: without it `~/.herdr/worktrees-old` reads as being inside
// `~/.herdr/worktrees`.
func under(root, path string) (string, bool) {
	if root == "" {
		return "", false
	}
	prefix := strings.TrimSuffix(root, "/") + "/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	return path[len(prefix):], true
}

func cut(path string) (string, string) {
	head, rest, _ := strings.Cut(path, "/")
	return head, rest
}
