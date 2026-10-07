package gc

import (
	"path/filepath"
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// A projects root of its own, and a .git directory rather than a .git file, which is what a
// worktree itself has.
func pruneFixture(t *testing.T) (*fixture, string) {
	t.Helper()

	f := newFixture(t)
	repo := filepath.Join(f.cfg.GCProjectsRoot, "app")

	f.mkdir(filepath.Join(repo, ".git"))
	f.mkdir(filepath.Join(f.cfg.GCProjectsRoot, "notes"))
	f.write(filepath.Join(f.cfg.GCProjectsRoot, "app", ".claude", "worktrees", "live", ".git"), "gitdir: x")

	f.pruneOut[repo] = "Removing worktrees/gone: gitdir file points to non-existent location\n"

	return f, repo
}

func TestPruneReportsTheEntryAndTheReasonGitGave(t *testing.T) {
	f, repo := pruneFixture(t)

	out := f.sweep()
	harness.Wants(t, out, "pruning "+repo+"'s worktree entry worktrees/gone, whose folder is gone"+
		" (gitdir file points to non-existent location)")

	// Only a directory with a .git directory under the projects root; a folder of notes is
	// not a repository.
	harness.Equal(t, len(f.pruned), 1, "repositories pruned")
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

func TestAPruneThatFailsIsSaid(t *testing.T) {
	f, repo := pruneFixture(t)
	f.pruneErr[repo] = "fatal: not a git repository: '.git'"

	out := f.sweep()
	harness.Wants(t, out, repo+"'s worktree entries could not be pruned: exit status 128")
}
