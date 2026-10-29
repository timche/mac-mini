// Package herdrworktree gives a worktree Claude Code made for itself the place in herdr
// that a worktree herdr made has: a workspace of its own in the sidebar, with the session's
// tab in it, and the tab back where it came from when Claude leaves.
//
// It watches rather than takes part. Claude Code's WorktreeCreate hook replaces the
// creation it is a hook on, so wiring this there would put the making of the worktree —
// the base ref, the branch, the cleanup ExitWorktree does — on this side of the line. So
// EnterWorktree and ExitWorktree create and remove exactly as they do without this, and
// what runs here is a PostToolUse and a SessionEnd observer that answers nothing.
//
// Everything it does to herdr is through the pane the session is already in. `worktree
// open` registers a worktree that exists rather than making one, and takes a shell tab of
// its own that nobody asked for, which is closed again. `pane move` carries the pane, and
// so the terminal and this very process, into the new workspace; the pane id the session
// was started with keeps resolving to the pane afterwards, which is the only id a process
// inside it has. A workspace whose last tab leaves is closed by herdr, so leaving a
// worktree is a move home and no removal: the sidebar entry goes by itself.
//
// Nothing here is worth a session's tool call, so every way this can fail — outside herdr,
// in a subagent, a herdr that will not answer, a payload that is not the shape it was — is
// a return and a line on stderr.
package herdrworktree

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The whole run, shared by every call it makes, since what is waiting on it is a session
// between one tool call and the next.
const budget = 5 * time.Second

// Files a session that was killed never came back to delete.
const keepState = 14 * 24 * time.Hour

type event struct {
	SessionID    string          `json:"session_id"`
	EventName    string          `json:"hook_event_name"`
	ToolName     string          `json:"tool_name"`
	AgentID      string          `json:"agent_id"`
	Cwd          string          `json:"cwd"`
	ToolInput    json.RawMessage `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response"`
}

type observer struct {
	ctx     context.Context
	pane    string
	session string
}

// Observe reads the hook event and moves the session's pane to match the worktree it is
// in. A pane is the one thing it needs and cannot be told: HERDR_PANE_ID is set by the
// herdr that started this terminal, so no pane means no herdr, or a terminal herdr does
// not own.
func Observe(in io.Reader) {
	defer func() {
		if problem := recover(); problem != nil {
			warn("%v", problem)
		}
	}()

	pane := os.Getenv("HERDR_PANE_ID")
	if os.Getenv("HERDR_ENV") != "1" || pane == "" {
		return
	}

	body, err := io.ReadAll(in)
	if err != nil {
		return
	}
	var ev event
	if json.Unmarshal(body, &ev) != nil {
		return
	}
	// A subagent runs in the session's pane and has a working directory of its own, so a
	// worktree it entered is not the tab's to follow. The session id names the state
	// file, so one that is a path is one this does not write.
	if ev.AgentID != "" || ev.SessionID == "" || strings.ContainsAny(ev.SessionID, `/\`) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	o := &observer{ctx: ctx, pane: pane, session: ev.SessionID}

	// A tool that has run, and the end of a session that may still be in a worktree.
	// Every other event, a PreToolUse or the failure of the very call this follows,
	// says nothing about where the session is.
	switch {
	case ev.EventName == "SessionEnd":
		o.leave()
	case ev.EventName != "PostToolUse":
		return
	case ev.ToolName == "EnterWorktree":
		o.enter(ev)
	case ev.ToolName == "ExitWorktree":
		o.leave()
	case ev.ToolName == "Bash":
		o.relabel()
	}
}

func (o *observer) enter(ev event) {
	worktree, checkout := o.worktreeOf(ev)
	if worktree == "" {
		return
	}
	label := o.labelFor(worktree)

	opened, err := o.herdr("worktree", "open",
		"--cwd", checkout, "--path", worktree, "--label", label, "--no-focus")
	if err != nil {
		warn("%v", err)
		return
	}
	workspace := opened.text("workspace", "workspace_id")
	if workspace == "" {
		return
	}
	from, focused, err := o.focus()
	if err != nil {
		warn("%v", err)
		return
	}
	// herdr made this worktree and the pane is in its workspace already, which is every
	// session that starts in one.
	if from == workspace {
		return
	}

	// The workspace to go home to is the one the session started in, not the one it is
	// in: a second EnterWorktree moves the pane on, and home is still home.
	source := from
	if previous, ok := load(o.session); ok && previous.Source != "" {
		source = previous.Source
	}

	if _, err := o.herdr("pane", "move", o.pane,
		"--workspace", workspace, "--new-tab", focusFlag(focused)); err != nil {
		warn("%v", err)
		return
	}
	// Only once the pane is in: herdr closes a workspace whose last tab goes, so closing
	// the shell tab it opened first closes the workspace the pane was about to move to.
	if !opened.flag("already_open") {
		if tab := opened.text("tab", "tab_id"); tab != "" {
			if _, err := o.herdr("tab", "close", tab); err != nil {
				warn("%v", err)
			}
		}
	}
	save(o.session, state{
		Pane:      o.pane,
		Source:    source,
		Workspace: workspace,
		Worktree:  worktree,
		Label:     label,
	})
}

// Home at the end of the worktree and at the end of the session both, since a session that
// exits while still in one leaves the tab wherever the worktree was: whether Claude is
// keeping or removing the worktree is the user's answer to a prompt this never sees, and
// the pane belongs in the workspace it came from either way.
func (o *observer) leave() {
	st, ok := load(o.session)
	if !ok || st.Source == "" {
		return
	}
	// A state file written for another pane is not this one's to act on.
	if st.Pane != "" && st.Pane != o.pane {
		forget(o.session)
		return
	}

	open, err := o.workspaces()
	if err != nil {
		warn("%v", err)
		return
	}
	// There is nowhere to go home to. A --new-workspace would strand the tab in a
	// workspace belonging to no checkout, where leaving it is one herdr closes with the
	// worktree when the pane finally goes.
	if _, ok := open[st.Source]; !ok {
		forget(o.session)
		return
	}

	workspace, focused, err := o.place()
	if err != nil {
		warn("%v", err)
		return
	}
	if workspace == st.Source {
		forget(o.session)
		return
	}

	if _, err := o.herdr("pane", "move", o.pane,
		"--workspace", st.Source, "--new-tab", focusFlag(focused && open[workspace])); err != nil {
		// Kept, so the SessionEnd that follows a failed ExitWorktree tries again.
		warn("%v", err)
		return
	}
	forget(o.session)
}

// A branch the session moved to or renamed under itself, which the sidebar would otherwise
// still name by the branch the worktree was entered on. It runs after every Bash call, so
// the state file and one git are all it costs a session that is not in a worktree.
func (o *observer) relabel() {
	st, ok := load(o.session)
	if !ok || st.Worktree == "" || st.Workspace == "" {
		return
	}
	if st.Pane != "" && st.Pane != o.pane {
		return
	}
	label := o.labelFor(st.Worktree)
	if label == "" || label == st.Label {
		return
	}
	if _, err := o.herdr("workspace", "rename", st.Workspace, label); err != nil {
		warn("%v", err)
		return
	}
	st.Label = label
	save(o.session, st)
}

// The worktree the event is about and the checkout that owns it. EnterWorktree answers
// with the path it created, which is the one thing here that says what happened rather
// than where the session ended up; a payload without it falls back to the path it was
// asked for and then to the session's own directory, and each candidate has to be a linked
// worktree to be the one.
func (o *observer) worktreeOf(ev event) (worktree, checkout string) {
	for _, candidate := range []string{
		fields(ev.ToolResponse).text("worktreePath"),
		fields(ev.ToolInput).text("path"),
		ev.Cwd,
	} {
		if candidate == "" {
			continue
		}
		owner, linked, err := o.repo(candidate)
		if err != nil || !linked {
			continue
		}
		return candidate, owner
	}
	return "", ""
}

// The checkout a directory belongs to, and whether the directory is a linked worktree of
// it rather than the checkout itself. `worktree open` refuses a --cwd that is a linked
// worktree, and the common git directory is the main one's wherever it is asked from.
func (o *observer) repo(dir string) (checkout string, linked bool, err error) {
	out, err := o.git(dir, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir")
	if err != nil {
		return "", false, err
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 2 || lines[0] == "" || lines[1] == "" {
		return "", false, fmt.Errorf("git in %s did not say where its git directories are", dir)
	}
	return filepath.Dir(lines[1]), lines[0] != lines[1], nil
}

// herdr names a worktree's workspace for its branch with the slashes turned into dashes,
// so a workspace this registers is named the way the sidebar names every other one. A
// detached worktree has the directory's name to go on.
func (o *observer) labelFor(worktree string) string {
	branch, err := o.git(worktree, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || branch == "" || branch == "HEAD" {
		return filepath.Base(worktree)
	}
	return strings.ReplaceAll(branch, "/", "-")
}

// Where the pane is and whether it is the pane the eye is on, which is its own focus and
// its workspace's together: a focused pane in a workspace nobody is looking at must not
// pull the sidebar over to the worktree.
func (o *observer) focus() (workspace string, focused bool, err error) {
	workspace, own, err := o.place()
	if err != nil {
		return "", false, err
	}
	open, err := o.workspaces()
	if err != nil {
		return "", false, err
	}
	return workspace, own && open[workspace], nil
}

func (o *observer) place() (workspace string, focused bool, err error) {
	got, err := o.herdr("pane", "get", o.pane)
	if err != nil {
		return "", false, err
	}
	workspace = got.text("pane", "workspace_id")
	if workspace == "" {
		return "", false, fmt.Errorf("herdr says nothing about what workspace %s is in", o.pane)
	}
	return workspace, got.flag("pane", "focused"), nil
}

// Every workspace herdr has, and which of them is the focused one.
func (o *observer) workspaces() (map[string]bool, error) {
	listed, err := o.herdr("workspace", "list")
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID      string `json:"workspace_id"`
		Focused bool   `json:"focused"`
	}
	if json.Unmarshal(listed["workspaces"], &rows) != nil {
		return nil, fmt.Errorf("herdr workspace list is not a list of workspaces")
	}
	open := make(map[string]bool, len(rows))
	for _, row := range rows {
		open[row.ID] = row.Focused
	}
	return open, nil
}

// HERDR_BIN_PATH is the herdr that started this terminal, which is the one holding the
// socket the pane ids belong to; the PATH is for a terminal that was handed no path.
func (o *observer) herdr(args ...string) (reply, error) {
	bin := os.Getenv("HERDR_BIN_PATH")
	if info, err := os.Stat(bin); err != nil || info.IsDir() {
		bin = "herdr"
	}

	out, err := exec.CommandContext(o.ctx, bin, args...).Output()
	called := strings.Join(args, " ")

	// A refusal is JSON on stdout and a status of 1 together, and what it says is the
	// only account of why.
	var answer struct {
		Result reply `json:"result"`
		Error  struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(out, &answer) == nil && answer.Error.Code != "" {
		return nil, fmt.Errorf("herdr %s: %s", called, answer.Error.Message)
	}
	if err != nil {
		return nil, fmt.Errorf("herdr %s: %w", called, err)
	}
	if answer.Result == nil {
		return nil, fmt.Errorf("herdr %s answered nothing", called)
	}
	return answer.Result, nil
}

func (o *observer) git(dir string, args ...string) (string, error) {
	out, err := exec.CommandContext(o.ctx, "git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}

func focusFlag(focused bool) string {
	if focused {
		return "--focus"
	}
	return "--no-focus"
}

// What herdr answered, read by name rather than into a type of its own: this reads five
// fields of five commands, and a struct per command would be five more things to keep in
// step with a herdr that adds a field.
type reply map[string]json.RawMessage

func fields(raw json.RawMessage) reply {
	var r reply
	if len(raw) == 0 || json.Unmarshal(raw, &r) != nil {
		return nil
	}
	return r
}

func (r reply) at(keys ...string) json.RawMessage {
	node := r
	for i, key := range keys {
		raw, ok := node[key]
		if !ok {
			return nil
		}
		if i == len(keys)-1 {
			return raw
		}
		if node = fields(raw); node == nil {
			return nil
		}
	}
	return nil
}

func (r reply) text(keys ...string) string {
	var value string
	if json.Unmarshal(r.at(keys...), &value) != nil {
		return ""
	}
	return value
}

func (r reply) flag(keys ...string) bool {
	var value bool
	return json.Unmarshal(r.at(keys...), &value) == nil && value
}

// What the pane was carrying when it moved, which nothing in a later event says: the
// workspace to go home to, and what the sidebar entry is called.
type state struct {
	Pane      string `json:"pane"`
	Source    string `json:"source_workspace"`
	Workspace string `json:"workspace"`
	Worktree  string `json:"worktree"`
	Label     string `json:"label"`
}

// Beside the binary's own cache, so a session trying a change out with
// CLAUDE_HOOKS_CACHE_DIR keeps its state out of the one every other session is using.
func statePath(session string) string {
	cache := os.Getenv("CLAUDE_HOOKS_CACHE_DIR")
	if cache == "" {
		cache = filepath.Join(os.Getenv("HOME"), "Library", "Caches", "claude-hooks")
	}
	return filepath.Join(cache, "herdr-worktree", session+".json")
}

func load(session string) (state, bool) {
	body, err := os.ReadFile(statePath(session))
	if err != nil {
		return state{}, false
	}
	var st state
	if json.Unmarshal(body, &st) != nil {
		return state{}, false
	}
	return st, st.Workspace != ""
}

func save(session string, st state) {
	path := statePath(session)
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	sweep(filepath.Dir(path))
	body, err := json.Marshal(st)
	if err != nil {
		return
	}
	os.WriteFile(path, body, 0o600)
}

func forget(session string) {
	os.Remove(statePath(session))
}

// A session that was killed never reaches SessionEnd, so its file is the one thing here
// that nothing deletes.
func sweep(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err == nil && time.Since(info.ModTime()) > keepState {
			os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}

func warn(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "claude-hooks herdr-worktree: "+format+"\n", args...)
}
