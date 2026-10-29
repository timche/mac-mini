package gc

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// A projects root of its own, holding a checkout at <owner>/<repo>, the docs clone
// directly under the root, and a folder of notes that is neither. A .git directory rather
// than a .git file, which is what a worktree itself has.
func pruneFixture(t *testing.T) (*fixture, string) {
	t.Helper()

	f := newFixture(t)
	repo := filepath.Join(f.cfg.GCProjectsRoot, "acme", "app")

	f.mkdir(filepath.Join(repo, ".git"))
	f.mkdir(filepath.Join(f.cfg.GCProjectsRoot, "docs", ".git"))
	f.mkdir(filepath.Join(f.cfg.GCProjectsRoot, "notes"))
	f.write(filepath.Join(repo, ".claude", "worktrees", "live", ".git"), "gitdir: x")

	f.pruneOut[repo] = "Removing worktrees/gone: gitdir file points to non-existent location\n"

	return f, repo
}

func TestPruneReportsTheEntryAndTheReasonGitGave(t *testing.T) {
	f, repo := pruneFixture(t)

	out := f.sweep()
	harness.Wants(t, out, "pruning "+repo+"'s worktree entry worktrees/gone, whose folder is gone"+
		" (gitdir file points to non-existent location)")

	// The checkout one level in and the docs clone directly under the root; neither the
	// owner's folder nor a folder of notes is a repository.
	harness.Equal(t, len(f.pruned), 2, "repositories pruned")
}

func TestPruneReachesACheckoutUnderItsOwnerAndOneUnderTheRoot(t *testing.T) {
	f, repo := pruneFixture(t)

	f.sweep()
	pruned := strings.Join(f.pruned, " ")
	harness.Wants(t, pruned, repo)
	harness.Wants(t, pruned, filepath.Join(f.cfg.GCProjectsRoot, "docs"))
}

func TestPruneSaysWhatItWouldDoOnADryRun(t *testing.T) {
	f, repo := pruneFixture(t)

	out := f.dryRun()
	harness.Wants(t, out, "would prune "+repo+"'s worktree entry worktrees/gone")
}

func TestNothingIsPrunedWithNoGit(t *testing.T) {
	f, _ := pruneFixture(t)
	f.gitUp = false

	harness.Equal(t, f.sweep(), "", "the log of a sweep with no git")
	harness.Equal(t, len(f.pruned), 0, "repositories pruned")
}

func TestAPruneThatFailsIsOneMessage(t *testing.T) {
	f, repo := pruneFixture(t)
	f.pruneErr[repo] = "fatal: not a git repository: '.git'"

	out := f.sweep()
	harness.Wants(t, out, repo+"'s worktree entries could not be pruned: fatal: not a git repository")

	harness.Wants(t, f.lastSent(), "⚠️ A repository's worktree entries would not prune")
	harness.Wants(t, f.lastSent(), "**Repository:** `"+repo+"`")
	harness.Wants(t, f.lastSent(), "**Needs you:** run `git worktree prune -v`")
}
