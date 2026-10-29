package gc

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

const (
	over    = "11111111-1111-4111-8111-111111111111"
	touched = "22222222-2222-4222-8222-222222222222"
	running = "33333333-3333-4333-8333-333333333333"
	lonely  = "44444444-4444-4444-8444-444444444444"
)

// Five folders, because the rule is narrower than "the session is over": a live session
// claims the third, the second was touched inside the week a resume is given,
// `bash-edit-diff` is named like no session at all, and the project folder only goes when
// the last session folder in it did.
func scratchFixture(t *testing.T) (*fixture, string, string) {
	t.Helper()

	f := newFixture(t)
	root := filepath.Join(f.cfg.ScratchRoot, "claude-501")
	app, empty := filepath.Join(root, "-Users-x-app"), filepath.Join(root, "-Users-x-gone")

	for _, dir := range []string{over, touched, running, "bash-edit-diff"} {
		f.write(filepath.Join(app, dir, "scratchpad", "f"), "x")
	}
	f.write(filepath.Join(empty, lonely, "f"), "x")

	// The record outlives the process it describes, so the pid in the filename is what says
	// a session is live.
	f.write(filepath.Join(f.home, ".claude", "sessions", "4321.json"),
		`{"pid":4321,"sessionId":"`+running+`"}`)
	f.alive[4321] = true

	age(t, root, base.Add(-30*24*time.Hour))
	age(t, filepath.Join(app, touched), base.Add(-time.Hour))

	return f, app, empty
}

// Depth first: a directory aged before the files in it is dated now again by the write that
// made them.
func age(t *testing.T, root string, at time.Time) {
	t.Helper()

	var paths []string
	err := filepath.Walk(root, func(path string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for i := len(paths) - 1; i >= 0; i-- {
		if err := os.Chtimes(paths[i], at, at); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScratchReportsTheFolderOfASessionLongOverAndOnlyThatOne(t *testing.T) {
	f, app, empty := scratchFixture(t)
	out := f.dryRun()

	harness.Wants(t, out, "would remove scratch folder "+filepath.Join(app, over)+", whose session is over")
	harness.Wants(t, out, "would remove the empty project folder "+empty)

	for _, kept := range []string{touched, running, "bash-edit-diff"} {
		harness.Lacks(t, out, kept)
	}
	harness.Lacks(t, out, "project folder "+app)
}

func TestScratchRemovesTheDeadFoldersAndTheProjectFolderTheyEmptied(t *testing.T) {
	f, app, empty := scratchFixture(t)
	out := f.sweep()

	harness.Wants(t, out, "removing scratch folder "+filepath.Join(app, over))
	harness.Wants(t, out, "removed the empty project folder "+empty)

	gone(t, filepath.Join(app, over))
	gone(t, empty)

	for _, kept := range []string{touched, running, "bash-edit-diff"} {
		there(t, filepath.Join(app, kept))
	}
}

// A .DS_Store Finder left behind keeps the folder too, which is what rmdir would do with it
// anyway.
func TestADotfileKeepsTheProjectFolder(t *testing.T) {
	f, _, empty := scratchFixture(t)
	f.write(filepath.Join(empty, ".DS_Store"), "")
	age(t, empty, base.Add(-30*24*time.Hour))

	out := f.sweep()
	harness.Wants(t, out, "removing scratch folder "+filepath.Join(empty, lonely))
	harness.Lacks(t, out, "project folder "+empty)
	there(t, empty)
}

// The one thing that makes rmdir fail here is something still in the folder, which is the
// folder staying and is what the rule is for — so it is not worth a message.
func TestAFolderThatWouldNotGoIsOneMessage(t *testing.T) {
	f, app, _ := scratchFixture(t)

	// A folder the account cannot write is one whose contents it cannot remove.
	dead := filepath.Join(app, over)
	if err := os.Chmod(app, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(app, 0o755) })

	out := f.sweep()
	harness.Wants(t, out, dead+" could not be removed")
	harness.Wants(t, f.lastSent(), "⚠️ A finished session's scratch folder would not go")
	harness.Wants(t, f.lastSent(), "**Folder:** `"+dead+"`")
}

func gone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err == nil {
		t.Errorf("%s is still there", path)
	}
}

func there(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Errorf("%s was removed", path)
	}
}
