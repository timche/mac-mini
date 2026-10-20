package listen

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTheWrapperRunsEveryFiveMinutesAndNotBefore(t *testing.T) {
	start := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	runs := 0
	r := newRefresher(start, func() error { runs++; return nil })

	for _, step := range []struct {
		after time.Duration
		want  int
	}{
		{30 * time.Second, 0},
		{4*time.Minute + 59*time.Second, 0},
		{5 * time.Minute, 1},
		{6 * time.Minute, 1},
		{10 * time.Minute, 2},
	} {
		if err := r.tick(start.Add(step.after)); err != nil {
			t.Fatal(err)
		}
		if runs != step.want {
			t.Errorf("after %s: %d runs, want %d", step.after, runs, step.want)
		}
	}
}

// A failed build is the wrapper's to log; the listener says so and keeps listening.
func TestAFailedRunIsReportedAndTheTimerMovesOn(t *testing.T) {
	start := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	runs := 0
	r := newRefresher(start, func() error { runs++; return errors.New("exit status 1") })

	if err := r.tick(start.Add(5 * time.Minute)); err == nil {
		t.Error("a failed run was not reported")
	}
	if r.tick(start.Add(6*time.Minute)) != nil || runs != 1 {
		t.Error("a failed run was retried before its interval")
	}
}

// The real call, against a stand-in wrapper at ~/.local/bin/mochi that records how it was
// run.
func TestTheWrapperIsRunFromHomeWithNoSecret(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	os.MkdirAll(bin, 0o755)
	out := filepath.Join(home, "ran")
	script := "#!/bin/sh\n{ echo \"$@\"; env; } > " + out + "\n"
	if err := os.WriteFile(filepath.Join(bin, "mochi"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOCHI_DISCORD_BOT_TOKEN", "secret-token")

	if err := runWrapper(home)(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	if !strings.HasPrefix(string(got), "version\n") || strings.Contains(string(got), "secret-token") {
		t.Errorf("the wrapper ran as:\n%s", got)
	}
}
