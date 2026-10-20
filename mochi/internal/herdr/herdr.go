// Package herdr is every call mochi makes to herdr, through one Runner so a test drives it
// against the shapes the real CLI answers in. Copied in part from hachiko's on-call
// session rather than imported, the two being separate modules.
package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/timche/mac-mini/mochi/internal/fence"
	"github.com/timche/mac-mini/mochi/internal/op"
)

// Starting Claude Code is the longest call, and herdr is given 20 of these 30 seconds for
// it so the wait ends in herdr's own answer rather than in this process killing it.
const timeout = 30 * time.Second

// It answers with whichever stream carried herdr's JSON, and an error only when neither
// did: herdr reports a refusal as JSON on stderr with a non-zero status, and the code in it
// is the difference between an agent that is gone and one that is busy.
type Runner func(args ...string) ([]byte, error)

func CLI(args ...string) ([]byte, error) {
	if _, err := exec.LookPath("herdr"); err != nil {
		return nil, errors.New("no herdr on PATH")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "herdr", args...)
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
		return nil, fmt.Errorf("herdr %s failed: %v", args[0], err)
	}
	return body, nil
}

const (
	codeBlocked  = "agent_blocked"
	codeNotFound = "agent_not_found"
)

var (
	ErrGone         = errors.New("the agent is gone")
	ErrBlocked      = errors.New("the agent is waiting on a question in herdr")
	ErrStillBlocked = errors.New("the agent is still on its question after the esc")
)

// The status herdr gives an agent waiting on a question.
const Blocked = "blocked"

// Measured against the real herdr for hachiko's on-call: `agent get` still answered blocked
// on the first read after the esc and done on the second, about 150 ms later. The window is
// far past that, since giving up early leaves Tim's answer undelivered.
const (
	escWindow = 5 * time.Second
	escEvery  = 250 * time.Millisecond
)

type reply struct {
	Result struct {
		Agent struct {
			Status string `json:"agent_status"`
			TabID  string `json:"tab_id"`
		} `json:"agent"`
		Tab struct {
			Label     string `json:"label"`
			PaneCount int    `json:"pane_count"`
			Focused   bool   `json:"focused"`
		} `json:"tab"`
		Workspaces []struct {
			Label       string `json:"label"`
			WorkspaceID string `json:"workspace_id"`
		} `json:"workspaces"`
		Workspace struct {
			WorkspaceID string `json:"workspace_id"`
		} `json:"workspace"`
		RootPane struct {
			PaneID string `json:"pane_id"`
		} `json:"root_pane"`
		Tabs []struct {
			Label string `json:"label"`
		} `json:"tabs"`
	} `json:"result"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func call(run Runner, args ...string) (*reply, error) {
	out, err := run(args...)
	if err != nil {
		return nil, err
	}
	r := &reply{}
	if err := json.Unmarshal(out, r); err != nil {
		return nil, fmt.Errorf("herdr %s answered something that is not JSON", strings.Join(args[:min(2, len(args))], " "))
	}
	if r.Error != nil {
		switch r.Error.Code {
		case codeNotFound:
			return nil, ErrGone
		case codeBlocked:
			return nil, ErrBlocked
		}
		return nil, fmt.Errorf("herdr %s: %s (%s)", strings.Join(args[:min(2, len(args))], " "), r.Error.Message, r.Error.Code)
	}
	return r, nil
}

// The workspace is found by its label, and made in the repository when there is none, so a
// server mapped before its workspace existed still gets a session.
func Workspace(run Runner, label, cwd string) (string, error) {
	r, err := call(run, "workspace", "list")
	if err != nil {
		return "", err
	}
	for _, w := range r.Result.Workspaces {
		if w.Label == label {
			return w.WorkspaceID, nil
		}
	}

	made, err := call(run, "workspace", "create", "--label", label, "--cwd", cwd, "--no-focus")
	if err != nil {
		return "", err
	}
	if made.Result.Workspace.WorkspaceID == "" {
		return "", fmt.Errorf("herdr made the %s workspace but did not name it", label)
	}
	return made.Result.Workspace.WorkspaceID, nil
}

// The labels of a workspace's tabs, so a new one can be told apart from them.
func Tabs(run Runner, workspace string) ([]string, error) {
	r, err := call(run, "tab", "list", "--workspace", workspace)
	if err != nil {
		return nil, err
	}
	labels := make([]string, 0, len(r.Result.Tabs))
	for _, t := range r.Result.Tabs {
		labels = append(labels, t.Label)
	}
	return labels, nil
}

// Never focused: Tim may be in the middle of something in that workspace.
func Tab(run Runner, workspace, label, cwd string, env ...string) (string, error) {
	args := []string{"tab", "create", "--workspace", workspace, "--label", label, "--cwd", cwd, "--no-focus"}
	for _, kv := range env {
		args = append(args, "--env", kv)
	}
	r, err := call(run, args...)
	if err != nil {
		return "", err
	}
	if r.Result.RootPane.PaneID == "" {
		return "", fmt.Errorf("herdr made the %s tab but named no pane in it", label)
	}
	return r.Result.RootPane.PaneID, nil
}

// No model or effort: the session runs on Tim's own defaults.
func Start(run Runner, agent, pane string) error {
	_, err := call(run, "agent", "start", agent, "--kind", "claude", "--pane", pane, "--timeout", "20000")
	return err
}

// Cleaned here as well as in the fence, so no part of a prompt, whoever wrote it, can end
// the paste.
func Prompt(run Runner, agent, text string) error {
	_, err := call(run, "agent", "prompt", agent, fence.Clean(text))
	return err
}

// Interrupt is an esc: it ends the turn an agent is in, or cancels the question it waits on.
func Interrupt(run Runner, agent string) error {
	_, err := call(run, "agent", "send-keys", agent, "esc")
	return err
}

// Answer cancels the question an agent is waiting on and prompts it in the question's place.
// The esc goes first because herdr refuses a prompt to an agent still on its question, and is
// read back rather than assumed, since herdr goes on calling the agent blocked for a moment
// after the cancel has landed.
func Answer(run Runner, agent, text string, nap func(time.Duration)) error {
	if err := Interrupt(run, agent); err != nil {
		return err
	}
	status, err := Status(run, agent)
	for left := escWindow; err == nil && status == Blocked && left > 0; left -= escEvery {
		nap(escEvery)
		status, err = Status(run, agent)
	}
	switch {
	case err != nil:
		return err
	case status == Blocked:
		return ErrStillBlocked
	}
	return Prompt(run, agent, text)
}

// CloseTab closes the tab an agent runs in, and only while it is still the tab mochi opened and
// nobody is using it: the label mochi gave it, the one pane, not in front of Tim, and an agent
// that is neither working nor waiting on a question.
func CloseTab(run Runner, agent, label string) error {
	r, err := call(run, "agent", "get", agent)
	if err != nil {
		return err
	}
	tab := r.Result.Agent.TabID
	if tab == "" {
		return fmt.Errorf("herdr named no tab for %s", agent)
	}
	switch status := r.Result.Agent.Status; status {
	case "working", Blocked:
		return fmt.Errorf("%s is %s, so its tab is left open", agent, status)
	}
	t, err := call(run, "tab", "get", tab)
	if err != nil {
		return err
	}
	switch {
	case t.Result.Tab.Label != label:
		return fmt.Errorf("%s runs in a tab labelled %q rather than %q, which mochi did not open", agent, t.Result.Tab.Label, label)
	case t.Result.Tab.PaneCount != 1:
		return fmt.Errorf("%s's tab has %d panes, so something else is in it", agent, t.Result.Tab.PaneCount)
	case t.Result.Tab.Focused:
		return fmt.Errorf("%s's tab is the one in front, so Tim may be at it", agent)
	}
	_, err = call(run, "tab", "close", tab)
	return err
}

// ErrGone for an agent herdr has never heard of, which is a session Tim closed.
func Status(run Runner, agent string) (string, error) {
	r, err := call(run, "agent", "get", agent)
	if err != nil {
		return "", err
	}
	if r.Result.Agent.Status == "" {
		return "", fmt.Errorf("herdr said nothing about what %s is doing", agent)
	}
	return r.Result.Agent.Status, nil
}
