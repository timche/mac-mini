package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHistoryTakesNoChannel(t *testing.T) {
	for _, args := range [][]string{
		{"--channel", "1300000000000000002"},
		{"1300000000000000002"},
		{"--before", "1300000000000000002", "--after", "1300000000000000003"},
	} {
		if _, err := historyArgs(args); err == nil {
			t.Errorf("%v parsed", args)
		}
	}
	q, err := historyArgs([]string{"--around", "1300000000000000002", "--limit", "100"})
	if err != nil || q.Around != "1300000000000000002" || q.Limit != 100 {
		t.Errorf("%+v, %v", q, err)
	}
}

func TestPostTakesTextAndAFile(t *testing.T) {
	shot := filepath.Join(t.TempDir(), "shot.png")
	os.WriteFile(shot, []byte("png"), 0o644)

	file, text, err := postArgs([]string{"--file", shot, "see", "this"})
	if err != nil || file != shot || text != "see this" {
		t.Errorf("%q %q %v", file, text, err)
	}
	for _, args := range [][]string{{}, {"--file", shot}, {"--file", "/nowhere", "hi"}} {
		if _, _, err := postArgs(args); err == nil {
			t.Errorf("%v parsed", args)
		}
	}
}

// Outside a session there is no thread to post to, and saying so costs no 1Password read.
func TestPostAndHistoryOutsideASessionStopBeforeOp(t *testing.T) {
	t.Setenv("MOCHI_THREAD", "")
	t.Setenv("MOCHI_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	os.WriteFile(os.Getenv("MOCHI_CONFIG"), []byte("user = \"94004892942741504\"\n"), 0o644)
	t.Setenv("PATH", t.TempDir())

	for _, args := range [][]string{{"post", "hi"}, {"history"}} {
		if err := run(args); err == nil || err.Error() == "no op on PATH, so the bot token cannot be resolved" {
			t.Errorf("%v: %v", args, err)
		}
	}
}
