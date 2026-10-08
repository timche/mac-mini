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

// The failure this is built around, at the one moment it matters: the copy cannot finish,
// and what is already in the generation is the only copy of the log there is. A non-empty
// directory at the path the copy fills is a real failure at exactly that point — the
// exclusive create cannot go round it and the leftover sweep cannot remove it — and under a
// copy that wrote the generation directly it would have destroyed it on the way past.
func TestACopyThatCannotFinishLeavesTheGenerationBeforeItAlone(t *testing.T) {
	f := newFixture(t)
	path := logFile(t, f, "hachiko-sync.log", 2*logCapKB)
	harness.WriteFile(t, path+".0", "the generation before")

	inTheWay := path + ".0.tmp"
	if err := os.MkdirAll(filepath.Join(inTheWay, "not-empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	out := f.sweep()
	harness.Wants(t, out, path+" is 2 MB and could not be capped")

	kept, err := os.ReadFile(path + ".0")
	if err != nil {
		t.Fatal(err)
	}
	harness.Equal(t, string(kept), "the generation before", "what the generation holds after a failed cap")
	harness.Equal(t, sizeOf(t, path), int64(2*logCapKB*1024), "the size of the log after a failed cap")

	// Said once. The disk that made the copy fail is still full five minutes later, and this
	// runs twelve times an hour for ever.
	harness.Equal(t, f.at(300).sweep(), "", "the log of the check after a cap that failed")
	harness.Equal(t, f.at(600).sweep(), "", "the log of the check after that")

	// And once what was in the way has gone, the cap happens and is said.
	if err := os.RemoveAll(inTheWay); err != nil {
		t.Fatal(err)
	}
	harness.Wants(t, f.at(900).sweep(), "capped "+path)
	harness.Equal(t, sizeOf(t, path+".0"), int64(2*logCapKB*1024), "bytes in the generation kept")

	// The complaint is forgotten with it, so the next thing to go wrong is said rather than
	// swallowed.
	harness.Equal(t, len(f.state().CapTrouble), 0, "logs still owing a complaint")
}

// A copy left behind by a sweep that was killed between filling it and renaming it. The name
// is hachiko's own and nothing else writes it, so it is this sweep's to take back.
func TestACopyLeftBehindByAKilledSweepIsTakenBack(t *testing.T) {
	f := newFixture(t)
	path := logFile(t, f, "herdr.log", 2*logCapKB)
	harness.WriteFile(t, path+".0.tmp", "half of a copy from a sweep that died")

	harness.Wants(t, f.sweep(), "capped "+path)

	harness.Equal(t, sizeOf(t, path+".0"), int64(2*logCapKB*1024), "bytes in the generation kept")
	if _, err := os.Stat(path + ".0.tmp"); err == nil {
		t.Error("the copy was left behind after the cap")
	}
}

// The third path a link could be waiting at, and the one the copy creates rather than
// replaces.
func TestACopyPathThatIsASymlinkIsRefusedAndSaidSo(t *testing.T) {
	f := newFixture(t)
	elsewhere := logFile(t, f, "somebodys-file", 1)
	path := logFile(t, f, "hachiko.log", 2*logCapKB)

	if err := os.Symlink(elsewhere, path+".0.tmp"); err != nil {
		t.Fatal(err)
	}

	harness.Wants(t, f.sweep(), path+".0.tmp is a symlink, so "+path+" was not capped")
	harness.Equal(t, sizeOf(t, elsewhere), int64(1024), "the size of what the link pointed at")
	harness.Equal(t, sizeOf(t, path), int64(2*logCapKB*1024), "the size of the log that was not capped")
}

// A generation is readable by exactly whoever could read the log it came out of, which for
// every one of these is the account and nobody else.
func TestAGenerationKeepsTheLogsOwnPermissions(t *testing.T) {
	f := newFixture(t)
	path := logFile(t, f, "ssh-agent.log", 2*logCapKB)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}

	f.sweep()

	info, err := os.Stat(path + ".0")
	if err != nil {
		t.Fatal(err)
	}
	harness.Equal(t, info.Mode().Perm(), os.FileMode(0o600), "the generation's permissions")
}

// The sweep's own stdout is on the list, opened with `>>` by its plist for each run, so the
// process doing the capping is the one holding it. A rename would have sent the rest of this
// sweep's lines to the archive, the line saying it had capped anything among them.
func TestTheSweepsOwnLogIsCappedUnderItself(t *testing.T) {
	f := newFixture(t)
	path := filepath.Join(f.cfg.LogsRoot, "hachiko.log")

	writer := appendingTo(t, path)
	if _, err := writer.Write(bytes.Repeat([]byte("a line from an earlier check\n"), 2*logCapKB*1024/29)); err != nil {
		t.Fatal(err)
	}

	deps := f.deps()
	deps.Log = writer

	if err := (sweeper{cfg: f.cfg, deps: deps, store: f.store}).run(); err != nil {
		t.Fatal(err)
	}

	kept, err := os.ReadFile(path + ".0")
	if err != nil {
		t.Fatal(err)
	}
	harness.Wants(t, string(kept), "a line from an earlier check")

	// The line saying it capped is the first byte of the file and the only thing in it: a
	// truncate that had kept the writer's offset would have left a megabyte of holes in front
	// of it.
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	harness.Wants(t, string(body), "capped "+path)
	harness.Lacks(t, string(body), "a line from an earlier check")
	harness.Lacks(t, string(body), "\x00")
	harness.Equal(t, sizeOf(t, path), int64(len(body)), "the size of the log after it capped itself")
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
