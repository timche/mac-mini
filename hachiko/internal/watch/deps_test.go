package watch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

func TestTruncateEmptiesTheLogAndLeavesItThere(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "worker.log")
	if err := os.WriteFile(log, []byte("a few hundred thousand connection errors"), 0o644); err != nil {
		t.Fatal(err)
	}

	device, ok := deviceOf(dir)
	if !ok {
		t.Fatal("the test directory has no device")
	}

	if err := truncateLog(log, device); err != nil {
		t.Fatal(err)
	}

	// Never an rm: the writer keeps its descriptor and its offset, so the file it appends
	// to has to still be there.
	info, err := os.Stat(log)
	if err != nil {
		t.Fatalf("the log was removed rather than emptied: %v", err)
	}
	harness.Equal(t, info.Size(), int64(0), "the truncated log")
}

// Minutes pass between the walk that chose a path and the decision to empty it, and the
// walk follows no symlink — so a link found here is one that arrived in between, and
// following it would empty a file nothing ever looked at.
func TestTruncateRefusesALinkSwappedInForTheLog(t *testing.T) {
	dir := t.TempDir()

	precious := filepath.Join(dir, "precious")
	if err := os.WriteFile(precious, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}

	log := filepath.Join(dir, "worker.log")
	if err := os.Symlink(precious, log); err != nil {
		t.Fatal(err)
	}

	device, _ := deviceOf(dir)
	if err := truncateLog(log, device); err == nil {
		t.Error("a symlink was followed and something else was emptied")
	}

	info, err := os.Stat(precious)
	if err != nil {
		t.Fatal(err)
	}
	harness.Equal(t, info.Size(), int64(7), "the file the link pointed at")
}

// The volume free space is being counted on, and nothing else: a path that has become a
// file on another one is not the file this measured.
func TestTruncateRefusesAFileOnAnotherVolume(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "worker.log")
	if err := os.WriteFile(log, []byte("still here"), 0o644); err != nil {
		t.Fatal(err)
	}

	device, ok := deviceOf(dir)
	if !ok {
		t.Fatal("the test directory has no device")
	}

	if err := truncateLog(log, device+1); err == nil {
		t.Error("a file on another volume was emptied")
	}

	info, err := os.Stat(log)
	if err != nil {
		t.Fatal(err)
	}
	harness.Equal(t, info.Size(), int64(10), "the log on the other volume")
}

func TestTruncateRefusesWhatIsNotAFile(t *testing.T) {
	dir := t.TempDir()
	device, _ := deviceOf(dir)

	if err := truncateLog(dir, device); err == nil {
		t.Error("a directory was emptied")
	}
	if err := truncateLog(filepath.Join(dir, "absent.log"), device); err == nil {
		t.Error("a path with nothing at it was emptied")
	}
}
