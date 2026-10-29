package sync

import (
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
)

// A repository limited to paths: every command a pass runs against its tree carries them, and
// the ones that are about commits and the remote carry nothing, since a push publishes the
// branch rather than a path.
func TestEveryCommandThatTouchesALimitedTreeCarriesItsPaths(t *testing.T) {
	f := newFixture(t)
	f.repo.Paths = []string{"home/.claude"}
	f.repo.Pull = false
	f.tree.status = " M home/.claude/CLAUDE.md\x00"
	f.tree.staged = []string{"home/.claude/CLAUDE.md"}

	f.pass(pushNow)

	limited := map[string]string{
		"--no-optional-locks status --porcelain=v1 -z": "home/.claude",
		"add -A":                       "home/.claude",
		"diff --cached --name-only -z": "home/.claude",
		"commit -m Update home/.claude/CLAUDE.md --trailer " + config.SyncedTrailer: "home/.claude",

		// The commits and the remote are not the tree: a push publishes the branch, and which
		// commits on it are sync's is the trailer's question rather than a path's.
		"log --oneline @{upstream}..HEAD --fixed-strings --grep=" + config.SyncedTrailer: "",
		"rev-parse --abbrev-ref HEAD": "",
		"push origin main":            "",
	}
	seen := f.limits()

	for command, paths := range limited {
		got, ran := seen[command]
		if !ran {
			t.Errorf("`git %s` was never run: %v", command, f.calls)
			continue
		}
		if got != paths {
			t.Errorf("`git %s` was limited to %q, want %q", command, got, paths)
		}
	}
}

// The subject names what the commit carries, which is the paths' files alone: a change a
// session has staged elsewhere is not in the commit and may not be in its subject either.
func TestTheSubjectOfALimitedCommitNamesOnlyThePathsFiles(t *testing.T) {
	f := newFixture(t)
	f.repo.Paths = []string{"home/.claude"}
	f.repo.Pull = false
	f.tree.status = " M home/.claude/settings.json\x00"
	f.tree.staged = []string{"home/.claude/settings.json"}

	result, log := f.pass(pushNow)

	if result.Subject != "Update home/.claude/settings.json" {
		t.Errorf("subject %q", result.Subject)
	}
	if !strings.Contains(log, "pushed `Update home/.claude/settings.json`") {
		t.Errorf("log: %s", log)
	}
}

// Nothing inside the paths, so there is nothing to commit however dirty the rest of the
// checkout is — and `git add -A` on a pathspec that matches nothing is fatal, which is the
// other reason a clean status is where a pass stops.
func TestALimitedPassWithNothingInItsPathsAddsNothing(t *testing.T) {
	f := newFixture(t)
	f.repo.Paths = []string{"home/.claude"}
	f.repo.Pull = false

	result, _ := f.pass(pushNow)

	if result.Outcome != Nothing {
		t.Fatalf("outcome %v", result.Outcome)
	}
	for _, prefix := range []string{"add", "commit", "push", "pull", "fetch"} {
		if f.ranAny(prefix) {
			t.Errorf("ran %s: %v", prefix, f.calls)
		}
	}
}

// A commit on a branch somebody checked out here would carry Tim's own files onto it and the
// push would publish that branch, with nothing to bring them back to the branch they were
// meant for.
func TestALimitedRepoIsLeftAloneOnAnyOtherBranch(t *testing.T) {
	f := newFixture(t)
	f.repo.Paths = []string{"home/.claude"}
	f.repo.Pull = false
	f.onBranch = "main"
	f.tree.branch = "worktree-agent-1"
	f.tree.status = " M home/.claude/CLAUDE.md\x00"
	f.tree.staged = []string{"home/.claude/CLAUDE.md"}

	result, log := f.pass(pushNow)

	if result.Outcome != Nothing {
		t.Fatalf("outcome %v", result.Outcome)
	}
	for _, prefix := range []string{"add", "commit", "push"} {
		if f.ranAny(prefix) {
			t.Errorf("ran %s: %v", prefix, f.calls)
		}
	}
	if !strings.Contains(log, "worktree-agent-1 is checked out, and this repository is synced on main alone") {
		t.Errorf("log: %s", log)
	}
}

// And it syncs again by itself once that branch is the one it was given, with nothing to
// re-arm: the branch is read on every pass.
func TestALimitedRepoSyncsAgainOnceItsOwnBranchIsBack(t *testing.T) {
	f := newFixture(t)
	f.repo.Paths = []string{"home/.claude"}
	f.repo.Pull = false
	f.onBranch = "main"
	f.tree.branch = "worktree-agent-1"

	_, l := f.loop()
	write(f, l, " M home/.claude/CLAUDE.md\x00", "home/.claude/CLAUDE.md")

	f.tree.branch = "main"
	f.log.Reset()
	tick(f, l, time.Second)

	if !strings.Contains(f.log.String(), "pushed `Update home/.claude/CLAUDE.md`") {
		t.Errorf("log: %s", f.log.String())
	}
}

// A tree left alone stays dirty, so every tick from then on is a pass that leaves it alone
// for the same reason: said once, or the log is a line a second for as long as it lasts.
func TestATreeLeftAloneIsSaidOnceRatherThanEveryTick(t *testing.T) {
	f := newFixture(t)
	f.repo.Paths = []string{"home/.claude"}
	f.repo.Pull = false
	f.onBranch = "main"
	f.tree.branch = "worktree-agent-1"

	// Nothing is reset: the pass the loop makes as it starts is already one that left the
	// tree alone, and the line it wrote is the one the ticks below may not write again.
	_, l := f.loop()

	write(f, l, " M home/.claude/CLAUDE.md\x00", "home/.claude/CLAUDE.md")
	for i := 0; i < 5; i++ {
		tick(f, l, time.Second)
	}

	if said := strings.Count(f.log.String(), "left alone:"); said != 1 {
		t.Errorf("said %d times:\n%s", said, f.log.String())
	}
}

// With pulling off there is nothing that rewrites the tree at all: no fetch on the interval,
// no rebase and no autostash, which is the whole reason a limited repository may not pull.
func TestALimitedRepoNeverFetchesOrRebases(t *testing.T) {
	f := newFixture(t)
	f.repo.Paths = []string{"home/.claude"}
	f.repo.Pull = false
	f.tree.behind = []string{"cafe123 theirs"}

	_, l := f.loop()
	write(f, l, " M home/.claude/CLAUDE.md\x00", "home/.claude/CLAUDE.md")

	// Several fetch intervals, which is what would bring another machine's commits down on a
	// repository that pulls.
	for i := 0; i < 3; i++ {
		tick(f, l, f.repo.FetchInterval+time.Second)
	}

	for _, prefix := range []string{"fetch", "pull", "rebase"} {
		if f.ranAny(prefix) {
			t.Errorf("ran %s: %v", prefix, f.calls)
		}
	}
}

// A push rejected on a repository that does not pull is a person's to look at: rebasing is
// what sync may not do here, so there is nothing left to try.
func TestARejectedPushOnALimitedRepoIsAFailureRatherThanARebase(t *testing.T) {
	f := newFixture(t)
	f.repo.Paths = []string{"home/.claude"}
	f.repo.Pull = false
	f.tree.pushRefused, f.tree.pushText = 1, "! [rejected] main -> main (fetch first)"
	f.tree.status = " M home/.claude/CLAUDE.md\x00"
	f.tree.staged = []string{"home/.claude/CLAUDE.md"}

	result, _ := f.pass(pushNow)

	if result.Outcome != NeedsHuman {
		t.Fatalf("outcome %v", result.Outcome)
	}
	for _, prefix := range []string{"pull", "rebase"} {
		if f.ranAny(prefix) {
			t.Errorf("ran %s: %v", prefix, f.calls)
		}
	}
	if got := f.lastSent(); !strings.Contains(got, "could not be pushed") {
		t.Errorf("sent: %s", got)
	}
}
