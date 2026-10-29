// Package statusline is the tap in front of whatever draws Claude Code's status line. It
// keeps the one thing in that event nothing on this Mac can read anywhere else — what
// Claude Code says is left of the account's usage limits — and then runs the program the
// line really comes from with the same event, handing back its output and its exit status
// untouched.
//
// Every session on this Mac spends one account's 5-hour and 7-day limits, so running out
// is the ceiling on how much work can be going at once, and nothing but the status line is
// told how close that is: there is no API for it that this may call. hachiko's five-minute
// check reads what this writes and is what says so in Discord.
//
// It is a claude-hooks command rather than a hachiko one because of where it runs: on every
// draw in every session, several times a minute. claude-hooks is already the binary a
// session runs directly, which checks its own sources rather than paying for a shell, a
// find, a shasum and a mise in front of each run.
//
// So every failure of its own is silence. A status line is redrawn for as long as a session
// is open, and nothing about writing a number down may cost a session its line or change
// what the line says.
package statusline

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Params is the whole of what the tap works with: the event, where the line is written, the
// reading's file and the program that draws the line.
type Params struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer

	// Where the reading goes, and the clock that stamps it. Empty means no reading is kept,
	// which is what this does on a Mac with no HOME to write one in.
	File string
	Now  func() time.Time

	// The program that draws the line and its arguments, as settings.json names them. The
	// tap draws nothing itself: what Tim sees is this program's output and no more.
	Command []string
}

// Draw answers with the exit status to leave with, which is the drawing program's own.
func Draw(p Params) int {
	// The whole event and never a bounded read: what is read here is what the program
	// behind this gets, and a truncated event is one it cannot parse at all.
	event, _ := io.ReadAll(p.In)

	record(p, event)
	return run(p, event)
}

// ReadingFile is where the reading goes: hachiko's state directory, in a folder of its own
// beside the four the four hachiko agents keep. The variable is hachiko's own, so the
// writer and the reader cannot be pointed at two different files.
func ReadingFile() string {
	if dir := os.Getenv("HACHIKO_USAGE_STATE_DIR"); dir != "" {
		return filepath.Join(dir, readingName)
	}

	home, err := os.UserHomeDir()
	if home == "" || err != nil {
		return ""
	}
	return filepath.Join(home, ".hachiko", "state", "usage", readingName)
}

const readingName = "latest.json"

// The `rate_limits` object exactly as Claude Code passed it, with the time it was read
// beside it. Verbatim because the shape is Claude Code's and documented, and which windows
// matter and what is worth saying about them is hachiko's: a format described here as well
// would be two descriptions of one file to keep in step.
//
// An event with no `rate_limits` in it leaves the last reading alone. The field is absent
// until the session's first API response and on an account that has no such limits at all,
// and a session that has just started is not news that the account is idle.
func record(p Params, event []byte) {
	if p.File == "" {
		return
	}

	var payload struct {
		RateLimits json.RawMessage `json:"rate_limits"`
	}
	if err := json.Unmarshal(event, &payload); err != nil {
		return
	}
	if len(bytes.TrimSpace(payload.RateLimits)) == 0 || bytes.Equal(bytes.TrimSpace(payload.RateLimits), []byte("null")) {
		return
	}

	body, err := json.Marshal(struct {
		ReadAt     int64           `json:"read_at"`
		RateLimits json.RawMessage `json:"rate_limits"`
	}{ReadAt: p.Now().Unix(), RateLimits: payload.RateLimits})
	if err != nil {
		return
	}

	write(p.File, body)
}

// Written whole or not at all, because every session on this Mac writes this file several
// times a minute and the one thing reading it is a five-minute check that may never see
// half of one.
func write(file string, body []byte) {
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}

	tmp, err := os.CreateTemp(dir, "latest-*.json")
	if err != nil {
		return
	}
	name := tmp.Name()

	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		os.Remove(name)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return
	}
	if err := os.Rename(name, file); err != nil {
		os.Remove(name)
	}
}

// The line itself, which is the program's and not this process's: its stdout is the status
// line, its stderr is what Claude Code reports as the command failing, and its exit status
// is the answer. A signal is not an exit status, and a line that was killed is a failure.
func run(p Params, event []byte) int {
	cmd := exec.Command(p.Command[0], p.Command[1:]...)
	cmd.Stdin = bytes.NewReader(event)
	cmd.Stdout, cmd.Stderr = p.Out, p.Err

	err := cmd.Run()
	if err == nil {
		return 0
	}

	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if code := exit.ExitCode(); code >= 0 {
			return code
		}
		return 1
	}

	fmt.Fprintf(p.Err, "claude-hooks: %s could not be run: %v\n", p.Command[0], err)
	return 1
}
