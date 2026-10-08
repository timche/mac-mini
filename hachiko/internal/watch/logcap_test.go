package watch

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// Real files rather than a seam: what the cap is about is what the filesystem does to a log
// its writer is holding, and no fake answers that.
func logFile(t *testing.T, f *fixture, name string, kb int) string {
	t.Helper()

	path := filepath.Join(f.cfg.LogsRoot, name)
	harness.WriteFile(t, path, string(bytes.Repeat([]byte("x"), kb*1024)))
	return path
}

// The writer as every plist on the list opens one: `>>`, which is O_APPEND, held for the
// life of the process.
func appendingTo(t *testing.T, path string) *os.File {
	t.Helper()

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	return file
}

func sizeOf(t *testing.T, path string) int64 {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func TestALogUnderTheThresholdIsLeftAlone(t *testing.T) {
	f := newFixture(t)
	path := logFile(t, f, "herdr.log", logCapKB/2)

	harness.Equal(t, f.sweep(), "", "the log of a check with nothing to cap")
	harness.Equal(t, sizeOf(t, path), int64(logCapKB/2*1024), "the size of a log under the threshold")

	if _, err := os.Stat(path + ".0"); err == nil {
		t.Error("a log under the threshold was given a generation of its own")
	}
}

// The case newsyslog cannot do: the writer holds the descriptor, so the file is copied and
// then emptied under it rather than renamed out from under it.
func TestAHeldOpenLogIsCopiedAndThenEmptiedUnderItsWriter(t *testing.T) {
	f := newFixture(t)
	path := logFile(t, f, "herdr.log", 2*logCapKB)
	writer := appendingTo(t, path)

	out := f.sweep()
	harness.Wants(t, out, "capped "+path+" at 2 MB; what was there is in "+path+".0")

	harness.Equal(t, sizeOf(t, path+".0"), int64(2*logCapKB*1024), "bytes in the generation kept")
	harness.Equal(t, sizeOf(t, path), int64(0), "the size of the log after the cap")

	// The whole point of emptying it in place: the writer never knew, and its next line
	// lands at nothing rather than leaving a hole the size of what was emptied.
	if _, err := writer.Write([]byte("still writing\n")); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	harness.Equal(t, string(body), "still writing\n", "what the log holds after the writer's next line")

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("no stat behind the log")
	}
	// A truncate that had left a sparse file would be a log whose next line sat at two
	// megabytes, and whose allocated size said so.
	if st.Blocks > 16 {
		t.Errorf("the log is %d blocks after one short line, so the truncate left a hole", st.Blocks)
	}
}

// launchd opens this one itself, once per run of a job that exits between them, so a rename
// orphans nothing and has no window at all.
func TestAReopenedLogIsRenamed(t *testing.T) {
	f := newFixture(t)
	path := logFile(t, f, "hachiko-gc.log", 2*logCapKB)

	harness.Wants(t, f.sweep(), "capped "+path)

	harness.Equal(t, sizeOf(t, path+".0"), int64(2*logCapKB*1024), "bytes in the generation kept")
	if _, err := os.Stat(path); err == nil {
		t.Error("a renamed log was left behind at its own path")
	}
}

// One generation and no more, so a log on this list is bounded at twice the threshold rather
// than growing a file per cap for the life of the Mac.
func TestASecondCapReplacesTheOlderGeneration(t *testing.T) {
	f := newFixture(t)
	path := logFile(t, f, "hachiko-sync.log", 2*logCapKB)
	writer := appendingTo(t, path)
	f.sweep()

	if _, err := writer.Write(bytes.Repeat([]byte("y"), 3*logCapKB*1024)); err != nil {
		t.Fatal(err)
	}
	f.at(300).sweep()

	kept, err := os.ReadFile(path + ".0")
	if err != nil {
		t.Fatal(err)
	}
	harness.Equal(t, len(kept), 3*logCapKB*1024, "bytes in the generation kept")
	harness.Equal(t, kept[0], byte('y'), "which generation was kept")

	entries, err := os.ReadDir(f.cfg.LogsRoot)
	if err != nil {
		t.Fatal(err)
	}
	harness.Equal(t, len(entries), 2, "files left under ~/Library/Logs")
}

// Account-owned paths and hachiko runs as the account, so nothing here is a way to root —
// but a write through a link is still a write somewhere nobody asked for.
func TestALogThatIsASymlinkIsRefusedAndSaidSo(t *testing.T) {
	f := newFixture(t)
	elsewhere := logFile(t, f, "somebodys-file", 2*logCapKB)
	path := filepath.Join(f.cfg.LogsRoot, "herdr.log")

	if err := os.Symlink(elsewhere, path); err != nil {
		t.Fatal(err)
	}

	harness.Wants(t, f.sweep(), path+" is a symlink, so nothing of it was capped")
	harness.Equal(t, sizeOf(t, elsewhere), int64(2*logCapKB*1024), "the size of what the link pointed at")
}

// The second path a link could be waiting at, which the copy opens for writing and the
// rename replaces.
func TestAGenerationThatIsASymlinkIsRefusedAndSaidSo(t *testing.T) {
	f := newFixture(t)
	elsewhere := logFile(t, f, "somebodys-file", 1)
	path := logFile(t, f, "hachiko-listen.log", 2*logCapKB)

	if err := os.Symlink(elsewhere, path+".0"); err != nil {
		t.Fatal(err)
	}

	harness.Wants(t, f.sweep(), path+".0 is a symlink, so "+path+" was not capped")
	harness.Equal(t, sizeOf(t, elsewhere), int64(1024), "the size of what the link pointed at")
	harness.Equal(t, sizeOf(t, path), int64(2*logCapKB*1024), "the size of the log that was not capped")
}

// By exact path and never by pattern: a log under a worktree sits beside a build somebody is
// waiting on, and the free-space thresholds are what catch one of those.
func TestNothingOffTheListIsCapped(t *testing.T) {
	f := newFixture(t)
	path := logFile(t, f, "devbackend.log", 4*logCapKB)

	harness.Equal(t, f.sweep(), "", "the log of a check with a big file nobody named")
	harness.Equal(t, sizeOf(t, path), int64(4*logCapKB*1024), "the size of a log off the list")
}

func TestADryRunSaysWhatItWouldCapAndCapsNothing(t *testing.T) {
	f := newFixture(t)
	path := logFile(t, f, "ssh-agent.log", 2*logCapKB)

	out := f.dryRun()
	harness.Wants(t, out, "would cap "+path+" at 2 MB, keeping what is there now as "+path+".0")

	harness.Equal(t, sizeOf(t, path), int64(2*logCapKB*1024), "the size of the log after a dry run")
	if _, err := os.Stat(path + ".0"); err == nil {
		t.Error("a dry run wrote a generation")
	}
}
