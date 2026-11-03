// Package hc is every call mochi makes to herdr-core, through one Runner so a test drives it
// against the shapes the real CLI answers in. A session is one terminal of the core: the
// name addresses it, the labels are what the Herdr app shows Tim.
package hc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/timche/mac-mini/mochi/internal/fence"
	"github.com/timche/mac-mini/mochi/internal/op"
)

// Nothing here waits on a turn — a session is started in the background and every other call
// is one message to the core — so this is room for a core under load rather than for the work.
const timeout = 30 * time.Second

// It answers with whichever stream carried hc's JSON, and an error only when neither did: hc
// reports a refusal as JSON on stderr with a non-zero status, and the kind in it is the
// difference between a session that is gone and one waiting on a question.
type Runner func(args ...string) ([]byte, error)

func CLI(args ...string) ([]byte, error) {
	if _, err := exec.LookPath("hc"); err != nil {
		return nil, errors.New("no hc on PATH")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "hc", args...)
	cmd.Env = op.Scrub(os.Environ())
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()

	body := stdout.Bytes()
	if len(bytes.TrimSpace(body)) == 0 {
		body = stderr.Bytes()
	}
	if len(bytes.TrimSpace(body)) == 0 {
		if err == nil {
			err = errors.New("it said nothing at all")
		}
		return nil, fmt.Errorf("hc %s failed: %v", args[0], err)
	}
	return body, nil
}

const (
	kindBlocked   = "blocked"
	kindNotFound  = "not_found"
	kindFinished  = "finished"
	kindNameTaken = "name_taken"
)

var (
	ErrGone         = errors.New("the session is gone")
	ErrBlocked      = errors.New("the session is waiting on a question")
	ErrStillBlocked = errors.New("the session is still on its question after the esc")
	ErrNotBriefed   = errors.New("the session started without its first prompt")

	errNameTaken = errors.New("a live terminal already has that name")
)

// The words Claude Code reports for a turn in progress and for a question it waits on. hc
// passes a program's own status through untranslated.
const (
	Working = "working"
	Blocked = "blocked"
)

// Claude Code goes on reporting blocked for a fraction of a second after the esc has landed,
// so the status is read back rather than assumed. The window is far past what that takes,
// since giving up early leaves Tim's answer undelivered.
const (
	escWindow = 5 * time.Second
	escEvery  = 250 * time.Millisecond
)

const (
	// What the session's terminal carries in its environment, so `mochi post` and `mochi
	// history` know which session they are in without being told.
	ThreadVar = "MOCHI_THREAD"

	// mochi's own label on the terminal, which is how a session is told from whatever took
	// its place: the app's labels are the app's to rewrite, this one nobody else writes.
	ThreadLabel = "mochi.thread"
)

// Name is the terminal a thread's session runs in. A name is unique among live terminals, so
// deriving it from the thread is what makes one session per thread.
func Name(thread string) string { return "mochi-" + thread }

// TabLabel is the tab the Herdr app files the session under, in the space its server maps to.
func TabLabel(thread string) string { return "tb-" + Name(thread) }

// The program a session is. No model or effort: it runs on Tim's own defaults. A test stands
// its own program in for Claude Code here, since nothing else chooses it.
var Program = []string{"claude"}

// hc answers each command in a shape of its own; these are the fields mochi reads of any of
// them.
type reply struct {
	Address string            `json:"address"`
	Name    string            `json:"name"`
	State   string            `json:"state"`
	Labels  map[string]string `json:"labels"`

	Status *status `json:"status"`

	FirstMessage *struct {
		Sent   bool   `json:"sent"`
		Reason string `json:"reason"`
	} `json:"first_message"`

	Error *struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
	} `json:"error"`
}

// A program's own word for what it is doing. hc answers with the whole record where it
// answers with a terminal, and with the word alone where it answers for a keystroke, so both
// shapes are read here rather than split across two structs.
type status struct{ State string }

func (s *status) UnmarshalJSON(body []byte) error {
	if len(body) > 0 && body[0] == '"' {
		return json.Unmarshal(body, &s.State)
	}
	var record struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(body, &record); err != nil {
		return err
	}
	s.State = record.State
	return nil
}

func call(run Runner, args ...string) (*reply, error) {
	out, err := run(args...)
	if err != nil {
		return nil, err
	}
	r := &reply{}
	if err := json.Unmarshal(out, r); err != nil {
		return nil, fmt.Errorf("hc %s answered something that is not JSON", args[0])
	}
	if r.Error != nil {
		switch r.Error.Kind {
		case kindNotFound, kindFinished:
			return nil, ErrGone
		case kindBlocked:
			return nil, ErrBlocked
		case kindNameTaken:
			return nil, errNameTaken
		}
		return nil, fmt.Errorf("hc %s: %s (%s)", args[0], r.Error.Message, r.Error.Kind)
	}
	return r, nil
}

// Where a session runs and what Tim finds it under: the terminal's name, the space and tab
// ids the Herdr app shows it at, the thread it belongs to and the checkout it starts in.
type Target struct {
	Name   string
	Space  string
	Tab    string
	Thread string
	Cwd    string
}

// Start runs the session and hands it the opening in the same call, because a send to a
// program that is still booting can be dropped while an argument cannot.
//
// The name is the thread's, so a live terminal already holding it is a session of mochi's
// whose Claude Code has gone — a core that went down leaves the shell `--keep` put in its
// place. That terminal is taken off the list rather than worked around, since a name is the
// only address a thread has and the thread would otherwise never get a session again.
func Start(run Runner, t Target, text string) error {
	err := start(run, t, text)
	if !errors.Is(err, errNameTaken) {
		return err
	}
	if err := Close(run, t.Name, t.Thread); err != nil && !errors.Is(err, ErrGone) {
		return err
	}
	return start(run, t, text)
}

// Never a child of whatever started mochi, and kept, so a core that goes down leaves Tim a
// shell where the session was rather than nothing. `run` is the one command whose piped
// output is text unless JSON is asked for.
func start(run Runner, t Target, text string) error {
	args := []string{
		"run", "--bg", "--no-parent", "--keep",
		"--name", t.Name,
		"--cwd", t.Cwd,
		"--label", "herdr.space=" + t.Space,
		"--label", "herdr.tab=" + t.Tab,
		"--label", ThreadLabel + "=" + t.Thread,
		"--json", "--",
		"env", ThreadVar + "=" + t.Thread,
	}
	args = append(args, Program...)
	args = append(args, fence.Clean(text))

	r, err := call(run, args...)
	if err != nil {
		return err
	}
	if r.Address == "" {
		return fmt.Errorf("hc started %s but gave it no address", t.Name)
	}
	// Only a template reports on its first message; one passed as an argument is the program's
	// before it runs. hc says to read the report wherever there is one, and the terminal is
	// there either way, so the caller can keep it and say what is missing.
	if fm := r.FirstMessage; fm != nil && !fm.Sent {
		return fmt.Errorf("%w: %s", ErrNotBriefed, fm.Reason)
	}
	return nil
}

// Cleaned here as well as in the fence, so no part of a prompt, whoever wrote it, can end the
// paste: hc pastes the text into the terminal and presses Enter.
func Prompt(run Runner, name, text string) error {
	_, err := call(run, "send", name, fence.Clean(text))
	return err
}

// Interrupt is hc's stop key, an esc for a program that reports its status: it ends the turn a
// session is in, or cancels the question it waits on.
func Interrupt(run Runner, name string) error {
	_, err := call(run, "interrupt", name)
	return err
}

// Answer cancels the question a session is waiting on and prompts it in the question's place.
// The esc goes first because hc refuses a send to a blocked program, and forcing one would
// answer the dialog rather than the question.
func Answer(run Runner, name, text string, nap func(time.Duration)) error {
	if err := Interrupt(run, name); err != nil {
		return err
	}
	status, err := Status(run, name)
	for left := escWindow; err == nil && status == Blocked && left > 0; left -= escEvery {
		nap(escEvery)
		status, err = Status(run, name)
	}
	switch {
	case err != nil:
		return err
	case status == Blocked:
		return ErrStillBlocked
	}
	return Prompt(run, name, text)
}

// Close ends a session's terminal and takes it off the list, and only while it is still the
// terminal mochi opened for that thread and nobody is using it: mochi's own label on it, and a
// session neither working nor waiting on a question. Whether Tim is looking at it has no
// answer in hc — focus is the app's, and the herdr.seen label it writes is the last turn
// somebody read, which a terminal nobody has opened carries too — so what keeps a session he
// is sitting at open is the caller's rule that his last word to it came from Discord.
func Close(run Runner, name, thread string) error {
	r, err := call(run, "get", name)
	if err != nil {
		return err
	}
	if label := r.Labels[ThreadLabel]; label != thread {
		return fmt.Errorf("%s carries %s %q rather than %q, so it is not the terminal mochi opened", name, ThreadLabel, label, thread)
	}
	if r.Status != nil {
		switch status := r.Status.State; status {
		case Working, Blocked:
			return fmt.Errorf("%s is %s, so its terminal is left open", name, status)
		}
	}
	if _, err := call(run, "kill", name); err != nil {
		return err
	}
	_, err = call(run, "rm", name)
	return err
}

// ErrGone for a terminal hc has never heard of, which is a session Tim closed; for one whose
// program has ended; and for one whose program says nothing about itself, since that is the
// shell `--keep` puts in a lost session's place and a prompt sent to a shell would run as a
// command.
func Status(run Runner, name string) (string, error) {
	r, err := call(run, "get", name)
	if err != nil {
		return "", err
	}
	if r.State != "live" || r.Status == nil {
		return "", ErrGone
	}
	if r.Status.State == "" {
		return "", fmt.Errorf("hc said nothing about what %s is doing", name)
	}
	return r.Status.State, nil
}
