package gc

import (
	"path/filepath"
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// Three projects, because the rule is narrower than "the folder is gone": only a working
// directory inside a worktree root counts. A daemon shared with another machine reports
// projects whose folder was never on this disk, and `down -v` on one of those would delete
// a database over a path that only looks missing.
func composeFixture(t *testing.T) *fixture {
	f := newFixture(t)

	gone := f.herdrWorktree("app", "gone", false)
	kept := f.herdrWorktree("app", "kept", true)

	f.containers = []Container{
		{Project: "gc-gone", WorkingDir: gone, ConfigFiles: []string{filepath.Join(gone, "compose.yaml")}},
		{Project: "gc-kept", WorkingDir: kept, ConfigFiles: []string{filepath.Join(kept, "compose.yaml")}},
		{Project: "gc-elsewhere", WorkingDir: filepath.Join(f.home, "elsewhere"), ConfigFiles: nil},
	}
	return f
}

func TestComposeTakesDownOnlyTheProjectOfARemovedWorktree(t *testing.T) {
	f := composeFixture(t)
	out := f.sweep()

	harness.Wants(t, out, "removing compose project gc-gone, whose worktree ")
	harness.Lacks(t, out, "gc-kept")
	harness.Lacks(t, out, "gc-elsewhere")

	harness.Equal(t, len(f.downs), 1, "projects taken down")
	harness.Equal(t, f.downs[0], "gc-gone", "the project taken down")

	// What docker said about it, under the line that says what was being done.
	harness.Wants(t, out, "  Container gc-gone-db  Removed")
}

func TestComposeSaysWhatItWouldDoAndDoesNothingOnADryRun(t *testing.T) {
	f := composeFixture(t)
	out := f.dryRun()

	harness.Wants(t, out, "would remove compose project gc-gone, whose worktree ")
	harness.Equal(t, len(f.downs), 0, "projects taken down")
	harness.Wants(t, out, "dry run over")
}

// A compose file still on disk is the second opinion on a folder that is gone: the project
// keeps one, so something of it is still there.
func TestComposeLeavesAProjectWhoseConfigFileIsStillThere(t *testing.T) {
	f := composeFixture(t)
	f.write(filepath.Join(f.home, "somewhere", "compose.yaml"), "services: {}\n")
	f.containers[0].ConfigFiles = append(f.containers[0].ConfigFiles,
		filepath.Join(f.home, "somewhere", "compose.yaml"))

	harness.Equal(t, f.sweep(), "", "the log of a sweep with nothing to do")
	harness.Equal(t, len(f.downs), 0, "projects taken down")
}

// A Mac with OrbStack stopped has no containers to sweep, and a line about it every ten
// minutes would bury the lines that matter.
func TestComposeSaysNothingWithNoDaemon(t *testing.T) {
	f := composeFixture(t)
	f.dockerUp = false

	harness.Equal(t, f.sweep(), "", "the log of a sweep with no daemon")
	harness.Equal(t, len(f.downs), 0, "projects taken down")
}

// One container per service, so a project with three of them is one decision and one
// `down`.
func TestComposeDecidesOncePerProject(t *testing.T) {
	f := newFixture(t)
	gone := f.herdrWorktree("app", "gone", false)

	f.containers = []Container{
		{Project: "gc-gone", WorkingDir: gone},
		{Project: "gc-gone", WorkingDir: gone},
		{Project: "gc-gone", WorkingDir: gone},
	}

	out := f.sweep()
	harness.Equal(t, len(f.downs), 1, "projects taken down")
	harness.Equal(t, len(lines(out)), 2, "lines about one project")
}
