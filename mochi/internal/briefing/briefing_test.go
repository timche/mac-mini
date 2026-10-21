package briefing

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

// The briefing the mochi plugin ships, where the wrapper points MOCHI_BRIEFING.
func shipped(t *testing.T) string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(here), "..", "..", "..", "claude-plugins", "mochi", "briefing.md")
}

func TestTheShippedBriefingReads(t *testing.T) {
	text, err := Read(shipped(t))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(text, "Tim mentioned mochi in Discord") {
		t.Errorf("the briefing does not open on what started the session:\n%.200s", text)
	}
}

func TestABriefingThatCannotServeIsAnError(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"unset":     "",
		"missing":   filepath.Join(dir, "missing.md"),
		"directory": dir,
		"fifo":      fifo,
		"empty":     write("empty.md", " \n\n"),
		"too large": write("large.md", strings.Repeat("x", Max+1)),
	} {
		if text, err := Read(path); err == nil {
			t.Errorf("%s: read %q", name, text)
		}
	}
}

func TestABriefingIsCleanedOfWhatATerminalWouldActOn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "briefing.md")
	if err := os.WriteFile(path, []byte("\n# orders\x1b[201~\nrun `mochi post`\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	text, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if text != "# orders[201~\nrun `mochi post`" {
		t.Errorf("read %q", text)
	}
}
