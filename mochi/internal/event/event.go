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

	// The session posted --done --close: its tab may be closed. Written by `mochi post`, never
	// by a hook.
	Close = "close"

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

	// A prompt mochi itself pasted into the session, which carries Tim's word from Discord
	// rather than anything he typed there.
	Relayed bool `json:"relayed,omitempty"`

	file string
}

// How the prompts mochi pastes begin, which is how a submitted prompt is told to be one of
// mochi's rather than Tim's own typing.
const (
	OpeningPrefix  = "/lead Tim mentioned mochi in Discord"
	FollowedPrefix = "Tim followed up in this session's Discord thread"
	AnsweredPrefix = "Tim answered in Discord"
)

var relayed = []string{OpeningPrefix, FollowedPrefix, AnsweredPrefix}

func FromMochi(prompt string) bool {
	for _, prefix := range relayed {
		if strings.HasPrefix(prompt, prefix) {
			return true
		}
	}
	return false
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
		Prompt          string            `json:"prompt"`
	}
	if body, err := io.ReadAll(io.LimitReader(stdin, 1<<20)); err == nil {
		json.Unmarshal(body, &input)
	}

	at := now()
	Put(dir, Event{Thread: thread, Kind: kind, Background: len(input.BackgroundTasks), Relayed: kind == Heard && FromMochi(input.Prompt), At: at.Unix(), Nanos: at.UnixNano()})
}

// Put leaves one event in the spool, renamed into place so the listener never reads half.
func Put(dir string, ev Event) error {
	if !safeThread.MatchString(ev.Thread) {
		return fmt.Errorf("%q is not a thread", ev.Thread)
	}
	body, _ := json.Marshal(ev)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%d.json", ev.Thread, ev.Nanos))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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
