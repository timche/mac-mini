package usage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var now = time.Unix(1762000000, 0)

func written(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "latest.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The file as `claude-hooks statusline` writes it: the documented object, verbatim, with the
// time it was read beside it.
const written5h = 1762003600

func TestAReadingIsTheWindowsTheEventCarried(t *testing.T) {
	path := written(t, `{"read_at":1762000000,"rate_limits":{
		"five_hour":{"used_percentage":23.5,"resets_at":1762003600},
		"seven_day":{"used_percentage":81,"resets_at":1762500000}}}`)

	reading, ok := Read(path)
	if !ok {
		t.Fatal("the reading could not be read")
	}
	if !reading.ReadAt.Equal(now) {
		t.Errorf("it was read at %s", reading.ReadAt)
	}

	five, ok := reading.Window(FiveHour)
	if !ok || five.Used != 23.5 || five.Resets.Unix() != written5h {
		t.Errorf("the 5-hour window is %+v", five)
	}

	seven, _ := reading.Window(SevenDay)
	if seven.Used != 81 {
		t.Errorf("the 7-day window is %+v", seven)
	}
}

// Every way there is nothing to know, each of them ordinary: no session has drawn a status
// line since the state directory was made, or one is rewriting the file as this reads it.
func TestNothingKnownIsNotAFault(t *testing.T) {
	for _, one := range []struct {
		name string
		path string
	}{
		{name: "no file at all", path: filepath.Join(t.TempDir(), "latest.json")},
		{name: "a file that will not parse", path: written(t, "{half of it")},
		{name: "a file with no time on it", path: written(t, `{"rate_limits":{"five_hour":{"used_percentage":5}}}`)},
	} {
		t.Run(one.name, func(t *testing.T) {
			if _, ok := Read(one.path); ok {
				t.Error("it answered with a reading")
			}
		})
	}
}

// A window Claude Code did not say when it starts again, which has no reset time rather than
// one in 1970 — and so is nothing this says anything about.
func TestAWindowWithNoResetTimeIsNotLive(t *testing.T) {
	reading, ok := Read(written(t, `{"read_at":1762000000,"rate_limits":{"five_hour":{"used_percentage":95}}}`))
	if !ok {
		t.Fatal("the reading could not be read")
	}

	five, _ := reading.Window(FiveHour)
	if !five.Resets.IsZero() {
		t.Errorf("it resets at %s", five.Resets)
	}
	if five.Live(now) || five.Spent(80, now) {
		t.Error("a window with no reset time is being read as one that is running")
	}
}

// What a window being over the mark means, and the one thing that makes a reading about it
// worthless: Claude Code drops a window from the event once it resets, so a reading whose
// window has reset is only the last thing anybody read before it did.
func TestSpentIsOverTheMarkAndStillRunning(t *testing.T) {
	for _, one := range []struct {
		name  string
		used  float64
		after time.Duration
		want  bool
	}{
		{name: "under the mark", used: 79, after: time.Hour},
		{name: "on the mark", used: 80, after: time.Hour, want: true},
		{name: "over the mark", used: 96, after: time.Minute, want: true},
		{name: "over the mark but already reset", used: 96, after: -time.Minute},
	} {
		t.Run(one.name, func(t *testing.T) {
			window := Window{Used: one.used, Resets: now.Add(one.after)}
			if got := window.Spent(80, now); got != one.want {
				t.Errorf("spent is %v", got)
			}
		})
	}
}
