package gc

import (
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

func TestWorktreeOfTheTwoRootsAndNothingElse(t *testing.T) {
	const root = "/Users/x/.herdr/worktrees"

	cases := []struct {
		path string
		want string
	}{
		{"/Users/x/.herdr/worktrees/app/fix-ui", "/Users/x/.herdr/worktrees/app/fix-ui"},
		{"/Users/x/.herdr/worktrees/app/fix-ui/src/deep", "/Users/x/.herdr/worktrees/app/fix-ui"},
		{"/Users/x/projects/app/.claude/worktrees/agent-1", "/Users/x/projects/app/.claude/worktrees/agent-1"},
		{"/Users/x/projects/app/.claude/worktrees/agent-1/src", "/Users/x/projects/app/.claude/worktrees/agent-1"},

		// A repository with no branch below it is the root's own listing rather than a
		// worktree, and the root itself is not one either.
		{"/Users/x/.herdr/worktrees/app", ""},
		{"/Users/x/.herdr/worktrees", ""},

		// The trailing separator is what keeps a folder that merely starts with the root's
		// name out of it.
		{"/Users/x/.herdr/worktrees-old/app/fix-ui", ""},

		{"/Users/x/projects/app", ""},
		{"/tmp/somewhere", ""},
	}

	for _, c := range cases {
		got, ok := worktreeOf(root, c.path)
		if !ok {
			got = ""
		}
		harness.Equal(t, got, c.want, "the worktree of "+c.path)
	}
}

// A path that holds the Claude Code root twice belongs to the outer worktree: the inner one
// is a checkout somebody made inside it, and removing the outer takes both.
func TestWorktreeOfTakesTheFirstNestedRoot(t *testing.T) {
	got, ok := worktreeOf("/none",
		"/Users/x/projects/app/.claude/worktrees/outer/sub/.claude/worktrees/inner")

	harness.Equal(t, ok, true, "a nested worktree was recognised")
	harness.Equal(t, got, "/Users/x/projects/app/.claude/worktrees/outer", "the worktree")
}
