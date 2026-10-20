// Package event is how a mochi session tells the listener it has stopped on Tim: Claude Code
// runs `mochi session-event` from a Stop hook and from a PreToolUse hook on AskUserQuestion,
// which writes one small file into a spool folder; the listener reads the folder. The hook
// holds no token, prints nothing, and changes nothing about the turn or the tool.
package event

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	Stop = "stop"
	Ask  = "ask"

	// Tim answered in the session: a question answered, or a prompt submitted.
	Heard = "heard"

	ThreadVar = "MOCHI_THREAD"
)

type Event struct {
	Thread string `json:"thread"`
	Kind   string `json:"kind"`

	// Work still in flight when the turn ended — subagents, shells — which means the session
	// has paused for that work rather than for Tim.
	Background int `json:"background,omitempty"`

	At    int64 `json:"at"`
	Nanos int64 `json:"nanos"`

	file string
}

var safeThread = regexp.MustCompile(`\A[A-Za-z0-9_-]+\z`)

// Write is `mochi session-event <kind>`. Outside a mochi session it returns at once, and
// nothing that goes wrong is shown: the session's turn goes on exactly as without it.
func Write(stdin io.Reader, env func(string) string, dir, kind string, now func() time.Time) {
	thread := env(ThreadVar)
	if thread == "" || !safeThread.MatchString(thread) || (kind != Stop && kind != Ask && kind != Heard) {
		return
	}
	var input struct {
		BackgroundTasks []json.RawMessage `json:"background_tasks"`
	}
	if body, err := io.ReadAll(io.LimitReader(stdin, 1<<20)); err == nil {
		json.Unmarshal(body, &input)
	}

	at := now()
	body, _ := json.Marshal(Event{Thread: thread, Kind: kind, Background: len(input.BackgroundTasks), At: at.Unix(), Nanos: at.UnixNano()})
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%d.json", thread, at.UnixNano()))
	tmp := path + ".tmp"
	if os.WriteFile(tmp, body, 0o600) != nil {
		return
	}
	os.Rename(tmp, path)
}

// Read is every event waiting, oldest first; Done takes one away once handled.
func Read(dir string) []Event {
	entries, _ := os.ReadDir(dir)
	var out []Event
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		body, err := os.ReadFile(path)
		var ev Event
		if err != nil || json.Unmarshal(body, &ev) != nil || ev.Thread == "" {
			os.Remove(path)
			continue
		}
		ev.file = path
		out = append(out, ev)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].file < out[j].file })
	return out
}

func Done(ev Event) { os.Remove(ev.file) }

// Clean takes away what a hook killed between its write and its rename left behind.
func Clean(dir string) {
	stale, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	for _, path := range stale {
		os.Remove(path)
	}
}
