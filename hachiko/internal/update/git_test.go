package update

import (
	"strings"
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// Sessions work in this checkout, so the one thing that has to be true of this step is that
// nothing but the lockfiles is read, staged, committed or pushed: every command that touches
// the tree ends in those three paths.
func TestOnlyTheLockfilesAreCommitted(t *testing.T) {
	b := newBox(t)
	b.current()
	b.moveTool(outdatedLine("node", "lts", "24.21.0", "24.22.0"))
	b.says(gitStatus, " M mise.lock\n")
	b.says(gitBranch, "main\n")
	b.says(gitDefault, "origin/main\n")

	b.update(false)

	for _, call := range b.matching("git -C " + repo) {
		switch {
		case strings.Contains(call, "status"), strings.Contains(call, "add"), strings.Contains(call, "commit"):
			if !strings.HasSuffix(call, "-- "+lockPaths) {
				t.Errorf("a command that touches the tree is not limited to the lockfiles: %q", call)
			}
		}
	}

	harness.Equal(t, b.ran(gitAdd), true, "staged the lockfiles")
	harness.Equal(t, b.ran(gitPush), true, "pushed")

	commits := b.matching("git -C " + repo + " commit")
	harness.Equal(t, len(commits), 1, "commits")
	harness.Wants(t, commits[0], `commit -m mise up moves node -- `+lockPaths)
	harness.Wants(t, b.summary(), "the lockfiles mise rewrote are committed")
}

func TestLockfilesThatDidNotMoveAreNotCommitted(t *testing.T) {
	b := newBox(t)
	b.current()
	b.says(gitStatus, "")

	b.update(false)

	harness.Equal(t, len(b.matching("git -C "+repo+" commit")), 0, "commits")
	harness.Equal(t, b.ran(gitPush), false, "pushed")
}

// A commit on a session's branch would go up inside their pull request, and one inside
// somebody's rebase would land in the rebase.
func TestLockfilesAreLeftAloneOnAnotherBranchOrMidRebase(t *testing.T) {
	t.Run("another branch", func(t *testing.T) {
		b := newBox(t)
		b.current()
		b.says(gitStatus, " M mise.lock\n")
		b.says(gitBranch, "worktree-agent-1\n")
		b.says(gitDefault, "origin/main\n")

		b.update(false)

		harness.Equal(t, len(b.matching("git -C "+repo+" commit")), 0, "commits")
		harness.Wants(t, b.log.String(), "worktree-agent-1 is checked out rather than main")
	})

	t.Run("mid-rebase", func(t *testing.T) {
		b := newBox(t)
		b.current()
		b.says(gitStatus, " M mise.lock\n")
		b.says(gitBranch, "HEAD\n")

		b.update(false)

		harness.Equal(t, len(b.matching("git -C "+repo+" commit")), 0, "commits")
		harness.Wants(t, b.log.String(), "HEAD is detached")
	})

	t.Run("mid-merge", func(t *testing.T) {
		b := newBox(t)
		b.current()
		b.says(gitStatus, " M mise.lock\n")
		b.says(gitBranch, "main\n")
		b.says(gitDefault, "origin/main\n")
		b.says("git -C "+repo+" rev-parse -q --verify MERGE_HEAD", "abc123\n")

		b.update(false)

		harness.Equal(t, len(b.matching("git -C "+repo+" commit")), 0, "commits")
		harness.Wants(t, b.log.String(), "MERGE_HEAD is there")
	})
}

// A push that is refused leaves the commit here, and nothing else on this Mac pushes it.
func TestAPushThatIsRefusedIsAnIncident(t *testing.T) {
	b := newBox(t)
	b.current()
	b.moveTool(outdatedLine("node", "lts", "24.21.0", "24.22.0"))
	b.says(gitStatus, " M mise.lock\n")
	b.says(gitBranch, "main\n")
	b.says(gitDefault, "origin/main\n")
	b.fails(gitPush, "! [rejected] main -> main (fetch first)")

	b.update(false)

	incidents := b.incidents()
	harness.Equal(t, len(incidents), 1, "incidents")
	harness.Wants(t, incidents[0].Text, "could not publish the lockfiles mise rewrote")
	harness.Wants(t, incidents[0].Text, "git -C ~/.mac-mini push")
}

func TestTheCommitSubjectNamesWhatMoved(t *testing.T) {
	harness.Equal(t, lockSubject([]string{"node", "bun"}), "mise up moves node, bun", "two tools")
	harness.Equal(t, lockSubject(nil), "mise up rewrites the lockfiles", "nothing named")

	many := []string{"node", "bun", "python", "go", "npm:portless", "npm:ccstatusline", "npm:chrome-devtools-mcp"}
	harness.Equal(t, lockSubject(many), "mise up moves 7 of the tools it locks", "a list too long for a subject")

	for _, subject := range []string{lockSubject(many), lockSubject([]string{"node"})} {
		if len(subject) > 72 {
			t.Errorf("a subject of %d characters: %q", len(subject), subject)
		}
	}
}
