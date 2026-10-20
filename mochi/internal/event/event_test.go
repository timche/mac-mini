package event

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

var now = func() time.Time { return time.Unix(1792500000, 123) }

func TestOutsideAMochiSessionNothingIsWritten(t *testing.T) {
	dir := t.TempDir()
	Write(strings.NewReader(`{"hook_event_name":"Stop"}`), env(nil), dir, Stop, now)
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("wrote %v", entries)
	}
}

func TestAStopAndAnAskAreSpooled(t *testing.T) {
	dir := t.TempDir()
	vars := env(map[string]string{ThreadVar: "1562147652450394154"})

	Write(strings.NewReader(`{"hook_event_name":"Stop","stop_hook_active":false,"background_tasks":[]}`), vars, dir, Stop, now)
	Write(strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"AskUserQuestion","tool_input":{}}`), vars, dir,
		Ask, func() time.Time { return time.Unix(1792500001, 0) })

	files, _ := filepath.Glob(filepath.Join(dir, "1562147652450394154-*.json"))
	if len(files) != 2 {
		t.Fatalf("files = %v", files)
	}
	info, _ := os.Stat(files[0])
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode())
	}
	events := Read(dir)
	if len(events) != 2 || events[0].Kind != Stop || events[1].Kind != Ask || events[0].Thread != "1562147652450394154" {
		t.Fatalf("events = %+v", events)
	}
	Done(events[0])
	if left := Read(dir); len(left) != 1 || left[0].Kind != Ask {
		t.Errorf("left = %+v", left)
	}
}

// A turn that ended with work still running has not stopped on Tim, and says so.
func TestAStopWithWorkInFlightCountsIt(t *testing.T) {
	dir := t.TempDir()
	Write(strings.NewReader(`{"background_tasks":[{"id":"a","type":"subagent"},{"id":"b","type":"shell"}]}`),
		env(map[string]string{ThreadVar: "t1"}), dir, Stop, now)
	if events := Read(dir); len(events) != 1 || events[0].Background != 2 {
		t.Errorf("events = %+v", events)
	}
}

func TestAnythingOddWritesNothing(t *testing.T) {
	dir := t.TempDir()
	Write(strings.NewReader("{}"), env(map[string]string{ThreadVar: "../etc"}), dir, Stop, now)
	Write(strings.NewReader("{}"), env(map[string]string{ThreadVar: "t1"}), dir, "kill", now)
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("wrote %v", entries)
	}
	// Unreadable input still records the stop: the hook never needs it to.
	Write(strings.NewReader("not json"), env(map[string]string{ThreadVar: "t1"}), dir, Stop, now)
	if events := Read(dir); len(events) != 1 {
		t.Errorf("events = %+v", events)
	}
}

// A hook killed between its write and its rename leaves a .tmp, which the listener clears.
func TestCleanTakesAwayHalfWrittenEvents(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "t1-1.json.tmp"), []byte("{"), 0o600)
	Write(strings.NewReader("{}"), env(map[string]string{ThreadVar: "t1"}), dir, Heard, now)

	Clean(dir)
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".json") {
		t.Errorf("left %v", entries)
	}
	if events := Read(dir); len(events) != 1 || events[0].Kind != Heard || events[0].Nanos != now().UnixNano() {
		t.Errorf("events = %+v", events)
	}
}
