package hc

import (
	"os"
	"testing"
	"time"
)

// The whole round trip against a real core, with a sleeper reporting Claude Code's statuses in
// its place. Skipped unless a test is pointed at a core of its own: the live one holds Tim's
// sessions.
func TestARoundTripAgainstARealCore(t *testing.T) {
	if session := os.Getenv("HC_SESSION"); session == "" || session == "default" {
		t.Skip("set HC_SESSION and the XDG paths of a scratch core to run this")
	}
	thread := "9000000000000000001"
	// It dies unless it was handed the thread, so a status read is the proof the session's own
	// commands can tell which session they are in.
	Program = []string{"sh", "-c", `[ "$MOCHI_THREAD" = "` + thread + `" ] || exit 1
printf "\033]7501;state=idle\033\\"
sleep 300`}

	target := Target{Name: Name(thread), Space: "sp-smoke", Tab: TabLabel(thread), Thread: thread, Cwd: t.TempDir()}
	if err := Start(CLI, target, "the opening"); err != nil {
		t.Fatal(err)
	}
	defer Close(CLI, target.Name, thread)

	status, err := Status(CLI, target.Name)
	if err != nil || status != "idle" {
		t.Fatalf("status = %q, %v", status, err)
	}
	if err := Prompt(CLI, target.Name, "a follow-up"); err != nil {
		t.Error(err)
	}
	if err := Interrupt(CLI, target.Name); err != nil {
		t.Error(err)
	}
	if err := Answer(CLI, target.Name, "an answer", func(time.Duration) {}); err != nil {
		t.Error(err)
	}
	if err := Close(CLI, target.Name, thread); err != nil {
		t.Fatal(err)
	}
	if _, err := Status(CLI, target.Name); err != ErrGone {
		t.Errorf("after the close: %v", err)
	}
}
