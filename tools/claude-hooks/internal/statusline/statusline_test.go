package statusline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

var read = time.Unix(1762000000, 0)

// The event Claude Code writes to a status line command, as the documented shape has it:
// enough of it to be real JSON, and the two usage windows this is here for.
const event = `{
  "session_id": "abc123",
  "model": {"id": "claude-opus-5", "display_name": "Opus"},
  "rate_limits": {
    "five_hour": {"used_percentage": 23.5, "resets_at": 1762003600},
    "seven_day": {"used_percentage": 41.2, "resets_at": 1762500000}
  }
}`

type tap struct {
	t *testing.T

	file string
	out  strings.Builder
	err  strings.Builder
	code int

	// Where the stand-in for the drawing program wrote what it was given, so a test reads
	// the event that reached it rather than believing it arrived.
	passed string
}

// A stand-in for ccstatusline: it draws a line, records the event it was handed, and exits
// however the case wants it to.
func newTap(t *testing.T, line string, exit int) *tap {
	t.Helper()

	dir := t.TempDir()
	one := &tap{
		t:      t,
		file:   filepath.Join(dir, "state", "usage", "latest.json"),
		passed: filepath.Join(dir, "passed"),
	}

	script := "#!/bin/sh\ncat >" + one.passed + "\nprintf '%s' " + line + "\nexit " + strconv.Itoa(exit) + "\n"
	program := filepath.Join(dir, "draw")
	if err := os.WriteFile(program, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	one.code = Draw(Params{
		In:      strings.NewReader(event),
		Out:     &one.out,
		Err:     &one.err,
		File:    one.file,
		Now:     func() time.Time { return read },
		Command: []string{program},
	})
	return one
}

// What the reading holds: the time it was taken and the `rate_limits` object as Claude Code
// passed it, which is what hachiko reads back.
type reading struct {
	ReadAt     int64 `json:"read_at"`
	RateLimits struct {
		FiveHour struct {
			Used   float64 `json:"used_percentage"`
			Resets int64   `json:"resets_at"`
		} `json:"five_hour"`
		SevenDay struct {
			Used   float64 `json:"used_percentage"`
			Resets int64   `json:"resets_at"`
		} `json:"seven_day"`
	} `json:"rate_limits"`
}

func (one *tap) reading() reading {
	one.t.Helper()

	body, err := os.ReadFile(one.file)
	if err != nil {
		one.t.Fatalf("no reading was written: %v", err)
	}

	var out reading
	if err := json.Unmarshal(body, &out); err != nil {
		one.t.Fatalf("the reading is not JSON: %v", err)
	}
	return out
}

func TestTheLineIsTheDrawingProgramSOwn(t *testing.T) {
	one := newTap(t, "'ctx 42% | main'", 0)

	if one.out.String() != "ctx 42% | main" {
		t.Errorf("the line is %q and not the program's own", one.out.String())
	}
	if one.code != 0 {
		t.Errorf("the exit status is %d and not the program's own", one.code)
	}
}

// The program behind this parses the event itself, so what reaches it has to be the whole of
// what arrived rather than whatever was left after reading a field out of it.
func TestTheWholeEventReachesTheDrawingProgram(t *testing.T) {
	one := newTap(t, "'line'", 0)

	passed, err := os.ReadFile(one.passed)
	if err != nil {
		t.Fatalf("the program was handed no event: %v", err)
	}
	if string(passed) != event {
		t.Errorf("the program was handed %q", passed)
	}
}

// Claude Code says nothing at all about a status line command that fails, so the status is
// the drawing program's rather than anything of the tap's.
func TestTheExitStatusPassesThrough(t *testing.T) {
	one := newTap(t, "'half a line'", 3)

	if one.code != 3 {
		t.Errorf("the exit status is %d and not the program's 3", one.code)
	}
	if one.out.String() != "half a line" {
		t.Errorf("the line is %q, so a failing program's output did not pass through", one.out.String())
	}
}

func TestTheReadingIsTheUsageLimitsAndTheTimeTheyWereRead(t *testing.T) {
	got := newTap(t, "'line'", 0).reading()

	if got.ReadAt != read.Unix() {
		t.Errorf("the reading is stamped %d and not when it was read", got.ReadAt)
	}
	if got.RateLimits.FiveHour.Used != 23.5 || got.RateLimits.FiveHour.Resets != 1762003600 {
		t.Errorf("the 5-hour window came through as %+v", got.RateLimits.FiveHour)
	}
	if got.RateLimits.SevenDay.Used != 41.2 || got.RateLimits.SevenDay.Resets != 1762500000 {
		t.Errorf("the 7-day window came through as %+v", got.RateLimits.SevenDay)
	}
}

// Nothing of the reading is in front of the line: a Mac where the state directory cannot be
// written, an event that is not JSON and an event with no usage in it all draw their line.
func TestTheLineIsDrawnWhateverTheReadingDoes(t *testing.T) {
	dir := t.TempDir()

	program := filepath.Join(dir, "draw")
	if err := os.WriteFile(program, []byte("#!/bin/sh\nprintf 'line'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// A file where the folder has to go, so every way of making the directory fails.
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, one := range []struct {
		name  string
		file  string
		event string
	}{
		{name: "a state directory that cannot be made", file: filepath.Join(blocked, "usage", "latest.json"), event: event},
		{name: "no file to write at all", file: "", event: event},
		{name: "an event that is not JSON", file: filepath.Join(dir, "a", "latest.json"), event: "not json"},
		{name: "an event with no usage limits in it", file: filepath.Join(dir, "b", "latest.json"), event: `{"session_id":"abc"}`},
		{name: "an event whose usage limits are null", file: filepath.Join(dir, "c", "latest.json"), event: `{"rate_limits":null}`},
	} {
		t.Run(one.name, func(t *testing.T) {
			out := &strings.Builder{}
			code := Draw(Params{
				In:      strings.NewReader(one.event),
				Out:     out,
				Err:     &strings.Builder{},
				File:    one.file,
				Now:     func() time.Time { return read },
				Command: []string{program},
			})

			if code != 0 || out.String() != "line" {
				t.Errorf("the line is %q and the status %d", out.String(), code)
			}
			if one.file != "" {
				if _, err := os.Stat(one.file); err == nil {
					t.Error("a reading was written where there was nothing to record")
				}
			}
		})
	}
}

// A reading that is already there is the account's usage as of when it was taken, and a
// session that has just started is not news that the account is idle: the field is absent
// until a session's first API response.
func TestAnEventWithNoUsageLeavesTheLastReadingAlone(t *testing.T) {
	one := newTap(t, "'line'", 0)
	was := one.reading()

	program := filepath.Join(filepath.Dir(one.passed), "draw")
	Draw(Params{
		In:      strings.NewReader(`{"session_id":"fresh"}`),
		Out:     &strings.Builder{},
		Err:     &strings.Builder{},
		File:    one.file,
		Now:     func() time.Time { return read.Add(time.Hour) },
		Command: []string{program},
	})

	if now := one.reading(); now != was {
		t.Errorf("the reading became %+v", now)
	}
}

// A line that cannot be drawn at all is a failure, said on stderr where Claude Code reports
// it, rather than a tap that prints something of its own into the status line.
func TestADrawingProgramThatCannotBeRunSaysSoOnStderr(t *testing.T) {
	out, err := &strings.Builder{}, &strings.Builder{}

	code := Draw(Params{
		In:      strings.NewReader(event),
		Out:     out,
		Err:     err,
		Now:     func() time.Time { return read },
		Command: []string{filepath.Join(t.TempDir(), "nothing-here")},
	})

	if code == 0 {
		t.Error("a program that could not be run exited zero")
	}
	if out.String() != "" {
		t.Errorf("the status line carries %q", out.String())
	}
	if !strings.Contains(err.String(), "could not be run") {
		t.Errorf("stderr is %q", err.String())
	}
}

// The writer and the reader are two tools, so the one variable that moves the file has to
// move it for both: this is the half hachiko reads.
func TestTheStateDirectoryVariableMovesTheReading(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HACHIKO_USAGE_STATE_DIR", dir)

	if got := ReadingFile(); got != filepath.Join(dir, "latest.json") {
		t.Errorf("the reading would go to %q", got)
	}

	t.Setenv("HACHIKO_USAGE_STATE_DIR", "")
	t.Setenv("HOME", dir)

	if got := ReadingFile(); got != filepath.Join(dir, ".hachiko", "state", "usage", "latest.json") {
		t.Errorf("the reading would go to %q", got)
	}
}
