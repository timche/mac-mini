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
	ErrGone    = errors.New("the agent is gone")
	ErrBlocked = errors.New("the agent is waiting on a question in herdr")
)

type reply struct {
	Result struct {
		Agent struct {
			Status string `json:"agent_status"`
		} `json:"agent"`
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
