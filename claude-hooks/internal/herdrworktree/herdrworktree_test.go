package herdrworktree

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const session = "s1"

// A case is an event, what herdr is going to answer, and the state file the session
// already has; what it wants is the herdr commands that came out of it, in order, and the
// state file left behind. WT stands for the worktree in the fixture and MAIN for the
// checkout that owns it, both temporary directories and so different every run.
type testCase struct {
	about string

	// The payload. event is the hook event name, PostToolUse unless the case says
	// otherwise; response and cwd are the tool_response and the cwd it carries, and
	// agent an agent_id, which is how a subagent's own worktree looks.
	tool     string
	event    string
	agent    string
	response string
	cwd      string

	// outside leaves HERDR_ENV unset, which is every terminal herdr did not start.
	outside bool

	// branch moves the worktree onto another branch before the event, as a session
	// that renamed its own branch has.
	branch string

	// before is the state the session is carrying, its worktree filled in with the
	// fixture's; replies answers a herdr command differently from the canned answer,
	// and omit leaves one unanswered, which is how herdr refuses.
	before  *state
	replies map[string]string
	omit    []string

	want  []string
	after *state
}

// What herdr 0.9.5 answers, trimmed to the fields that are read. Keyed by the first two
// words of the command, which is how the fake picks an answer.
var canned = map[string]string{
	"worktree open": `{"id":"cli:worktree:open","result":{"already_open":false,` +
		`"root_pane":{"pane_id":"wB:p1"},"tab":{"tab_id":"wB:t1"},` +
		`"workspace":{"workspace_id":"wB"}}}`,
	"tab close": `{"id":"cli:tab:close","result":{"type":"ok"}}`,
	"pane get": `{"id":"cli:pane:get","result":{"type":"pane_info","pane":{"pane_id":"w9:p1",` +
		`"tab_id":"w9:t1","workspace_id":"w9","focused":true}}}`,
	"workspace list": `{"id":"cli:workspace:list","result":{"type":"workspace_list","workspaces":[` +
		`{"workspace_id":"w9","label":"widget","focused":true,"worktree":{"checkout_path":"MAIN"}},` +
		`{"workspace_id":"wB","label":"worktree-feature","focused":false,"worktree":{"checkout_path":"WT"}}]}}`,
	"pane move": `{"id":"cli:pane:move","result":{"move_result":{"pane":{"pane_id":"wB:p2"},` +
		`"previous_workspace_id":"w9"}}}`,
	"workspace rename": `{"id":"cli:workspace:rename","result":{"type":"ok"}}`,
}

// A pane in the worktree's workspace, which is where the exit cases start from.
const inWorktree = `{"id":"cli:pane:get","result":{"type":"pane_info","pane":{"pane_id":"wB:p2",` +
	`"tab_id":"wB:t2","workspace_id":"wB","focused":true}}}`

var cases = []testCase{
	{
		about:    "entering a worktree opens it, takes away the tab herdr added and moves the pane in",
		tool:     "EnterWorktree",
		response: `{"worktreePath":"WT","worktreeBranch":"worktree-feature","message":"Created worktree"}`,
		want: []string{
			"worktree open --cwd MAIN --path WT --label worktree-feature --no-focus",
			"pane get w9:p1",
			"workspace list",
			"pane move w9:p1 --workspace wB --new-tab --focus",
			"tab close wB:t1",
		},
		after: &state{Pane: "w9:p1", Source: "w9", Workspace: "wB", Label: "worktree-feature"},
	},
	{
		about:    "a pane nobody is typing in is moved without being focused",
		tool:     "EnterWorktree",
		response: `{"worktreePath":"WT"}`,
		replies: map[string]string{
			"pane get": `{"id":"cli:pane:get","result":{"pane":{"pane_id":"w9:p1",` +
				`"workspace_id":"w9","focused":false}}}`,
		},
		want: []string{
			"worktree open --cwd MAIN --path WT --label worktree-feature --no-focus",
			"pane get w9:p1",
			"workspace list",
			"pane move w9:p1 --workspace wB --new-tab --no-focus",
			"tab close wB:t1",
		},
		after: &state{Pane: "w9:p1", Source: "w9", Workspace: "wB", Label: "worktree-feature"},
	},
	{
		// Otherwise a session working in a workspace nobody is looking at pulls the
		// sidebar over to its worktree.
		about:    "a focused pane in a workspace nobody is looking at is moved without being focused",
		tool:     "EnterWorktree",
		response: `{"worktreePath":"WT"}`,
		replies: map[string]string{
			"workspace list": `{"id":"cli:workspace:list","result":{"workspaces":[` +
				`{"workspace_id":"w9","focused":false},{"workspace_id":"wC","focused":true}]}}`,
		},
		want: []string{
			"worktree open --cwd MAIN --path WT --label worktree-feature --no-focus",
			"pane get w9:p1",
			"workspace list",
			"pane move w9:p1 --workspace wB --new-tab --no-focus",
			"tab close wB:t1",
		},
		after: &state{Pane: "w9:p1", Source: "w9", Workspace: "wB", Label: "worktree-feature"},
	},
	{
		// A workspace herdr already had has no tab of its own to take away.
		about:    "a worktree herdr already has keeps its tabs",
		tool:     "EnterWorktree",
		response: `{"worktreePath":"WT"}`,
		replies: map[string]string{
			"worktree open": `{"id":"cli:worktree:open","result":{"already_open":true,` +
				`"tab":{"tab_id":"wB:t1"},"workspace":{"workspace_id":"wB"}}}`,
		},
		want: []string{
			"worktree open --cwd MAIN --path WT --label worktree-feature --no-focus",
			"pane get w9:p1",
			"workspace list",
			"pane move w9:p1 --workspace wB --new-tab --focus",
		},
		after: &state{Pane: "w9:p1", Source: "w9", Workspace: "wB", Label: "worktree-feature"},
	},
	{
		// Every session that starts in a herdr worktree and enters it again.
		about:    "a pane already in the worktree's workspace is left where it is",
		tool:     "EnterWorktree",
		response: `{"worktreePath":"WT"}`,
		replies: map[string]string{
			"worktree open": `{"id":"cli:worktree:open","result":{"already_open":true,` +
				`"workspace":{"workspace_id":"wB"}}}`,
			"pane get": inWorktree,
		},
		want: []string{
			"worktree open --cwd MAIN --path WT --label worktree-feature --no-focus",
			"pane get w9:p1",
			"workspace list",
		},
	},
	{
		// The payload is read for what it says happened and falls back to where the
		// session ended up, so a tool_response of another shape still finds the
		// worktree.
		about:    "a response that is not an object falls back to the session's directory",
		tool:     "EnterWorktree",
		response: `"Created worktree at WT on branch worktree-feature."`,
		cwd:      "WT",
		want: []string{
			"worktree open --cwd MAIN --path WT --label worktree-feature --no-focus",
			"pane get w9:p1",
			"workspace list",
			"pane move w9:p1 --workspace wB --new-tab --focus",
			"tab close wB:t1",
		},
		after: &state{Pane: "w9:p1", Source: "w9", Workspace: "wB", Label: "worktree-feature"},
	},
	{
		// A directory that is not a linked worktree is not one to register: the main
		// checkout is where a session that never entered a worktree is.
		about:    "a session in the checkout itself is not in a worktree",
		tool:     "EnterWorktree",
		response: `"Created worktree"`,
		cwd:      "MAIN",
	},
	{
		about:    "the branch the pane moved on is the label, slashes and all",
		tool:     "EnterWorktree",
		response: `{"worktreePath":"WT"}`,
		branch:   "feature/x",
		want: []string{
			"worktree open --cwd MAIN --path WT --label feature-x --no-focus",
			"pane get w9:p1",
			"workspace list",
			"pane move w9:p1 --workspace wB --new-tab --focus",
			"tab close wB:t1",
		},
		after: &state{Pane: "w9:p1", Source: "w9", Workspace: "wB", Label: "feature-x"},
	},
	{
		// Home is the workspace the session started in, not the worktree it is
		// leaving, however many worktrees it moved through.
		about:    "entering a second worktree keeps the workspace the session started in",
		tool:     "EnterWorktree",
		response: `{"worktreePath":"WT"}`,
		before:   &state{Pane: "w9:p1", Source: "w9", Workspace: "wB", Label: "worktree-old"},
		replies: map[string]string{
			"worktree open": `{"id":"cli:worktree:open","result":{"already_open":false,` +
				`"tab":{"tab_id":"wC:t1"},"workspace":{"workspace_id":"wC"}}}`,
			"pane get": inWorktree,
		},
		want: []string{
			"worktree open --cwd MAIN --path WT --label worktree-feature --no-focus",
			"pane get w9:p1",
			"workspace list",
			"pane move w9:p1 --workspace wC --new-tab --no-focus",
			"tab close wC:t1",
		},
		after: &state{Pane: "w9:p1", Source: "w9", Workspace: "wC", Label: "worktree-feature"},
	},
	{
		// The workspace the worktree had goes with the pane: herdr closes a workspace
		// whose last tab left.
		about:   "leaving the worktree moves the pane home",
		tool:    "ExitWorktree",
		before:  &state{Pane: "w9:p1", Source: "w9", Workspace: "wB", Label: "worktree-feature"},
		replies: map[string]string{"pane get": inWorktree},
		want: []string{
			"workspace list",
			"pane get w9:p1",
			"pane move w9:p1 --workspace w9 --new-tab --no-focus",
		},
	},
	{
		// Whether Claude is keeping or removing the worktree is an answer to a prompt
		// no hook sees, and the tab belongs where it came from either way.
		about:  "a session that ends in a worktree sends the tab home",
		event:  "SessionEnd",
		before: &state{Pane: "w9:p1", Source: "w9", Workspace: "wB", Label: "worktree-feature"},
		replies: map[string]string{
			"pane get": inWorktree,
			"workspace list": `{"id":"cli:workspace:list","result":{"workspaces":[` +
				`{"workspace_id":"w9","focused":false},{"workspace_id":"wB","focused":true}]}}`,
		},
		want: []string{
			"workspace list",
			"pane get w9:p1",
			"pane move w9:p1 --workspace w9 --new-tab --focus",
		},
	},
	{
		// Nowhere to go home to, and a workspace of its own would strand the tab
		// somewhere belonging to no checkout.
		about:   "a pane whose workspace has closed stays where it is",
		tool:    "ExitWorktree",
		before:  &state{Pane: "w9:p1", Source: "wZ", Workspace: "wB", Label: "worktree-feature"},
		replies: map[string]string{"pane get": inWorktree},
		want:    []string{"workspace list"},
	},
	{
		about:  "a session that never entered a worktree has nothing to leave",
		tool:   "ExitWorktree",
		before: nil,
	},
	{
		about:  "a branch renamed under the session renames the sidebar entry",
		tool:   "Bash",
		branch: "feature/x",
		before: &state{Pane: "w9:p1", Source: "w9", Workspace: "wB", Label: "worktree-feature"},
		want:   []string{"workspace rename wB feature-x"},
		after:  &state{Pane: "w9:p1", Source: "w9", Workspace: "wB", Label: "feature-x"},
	},
	{
		// It runs after every Bash call in a worktree session, so the branch it
		// already knows about costs herdr nothing.
		about:  "a branch that has not moved is not renamed",
		tool:   "Bash",
		before: &state{Pane: "w9:p1", Source: "w9", Workspace: "wB", Label: "worktree-feature"},
		after:  &state{Pane: "w9:p1", Source: "w9", Workspace: "wB", Label: "worktree-feature"},
	},
	{
		about: "a Bash call outside a worktree asks herdr nothing",
		tool:  "Bash",
	},
	{
		about:    "nothing happens in a terminal herdr did not start",
		tool:     "EnterWorktree",
		response: `{"worktreePath":"WT"}`,
		outside:  true,
	},
	{
		// A subagent works in the session's pane and has a directory of its own, so a
		// worktree it entered is not the tab's to follow.
		about:    "a subagent's worktree is not the session tab's to follow",
		tool:     "EnterWorktree",
		agent:    "agent_1",
		response: `{"worktreePath":"WT"}`,
	},
	{
		// A hook is not worth a session's tool call, so a herdr that refuses is the
		// end of it and nothing is recorded as moved.
		about:    "a herdr that refuses leaves the pane and the state alone",
		tool:     "EnterWorktree",
		response: `{"worktreePath":"WT"}`,
		omit:     []string{"worktree open"},
		want:     []string{"worktree open --cwd MAIN --path WT --label worktree-feature --no-focus"},
	},
	{
		// The worktree exists once the call has run, and nowhere else: the same tool
		// name on another event is a call that was asked about or that failed.
		about:    "the same tool on another event is not a worktree that was entered",
		tool:     "EnterWorktree",
		event:    "PreToolUse",
		response: `{"worktreePath":"WT"}`,
	},
}

func TestObserve(t *testing.T) {
	for _, c := range cases {
		t.Run(c.about, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("CLAUDE_HOOKS_CACHE_DIR", filepath.Join(home, "cache"))

			main, worktree := fixture(t, c)
			replace := strings.NewReplacer("MAIN", main, "WT", worktree)

			log := filepath.Join(t.TempDir(), "argv")
			t.Setenv("HERDR_FAKE_LOG", log)
			t.Setenv("HERDR_FAKE_REPLIES", answers(t, c, replace))
			t.Setenv("HERDR_BIN_PATH", fake(t))
			t.Setenv("HERDR_ENV", "1")
			if c.outside {
				t.Setenv("HERDR_ENV", "")
			}
			t.Setenv("HERDR_PANE_ID", "w9:p1")

			if c.before != nil {
				before := *c.before
				before.Worktree = worktree
				save(session, before)
			}

			Observe(strings.NewReader(payload(t, c, replace)))

			var want []string
			for _, command := range c.want {
				want = append(want, replace.Replace(command))
			}
			if got := called(t, log); !same(got, want) {
				t.Errorf("herdr was asked\n  %s\nand not\n  %s",
					strings.Join(got, "\n  "), strings.Join(want, "\n  "))
			}

			got, kept := load(session)
			switch {
			case c.after == nil && kept:
				t.Errorf("the state file says %+v and should be gone", got)
			case c.after != nil && !kept:
				t.Errorf("the state file is gone and should say %+v", *c.after)
			case c.after != nil:
				after := *c.after
				after.Worktree = worktree
				if got != after {
					t.Errorf("the state file says\n  %+v\nand not\n  %+v", got, after)
				}
			}
		})
	}
}

// A checkout with a worktree where Claude Code puts one, on the branch it names it for.
// The paths come back resolved, since git answers with a resolved one and the commands are
// compared as strings.
func fixture(t *testing.T, c testCase) (main, worktree string) {
	t.Helper()

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	main = filepath.Join(root, "widget")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, main, "init", "-q", "-b", "main")

	// A worktree needs a commit to branch from, and an identity to make one with that
	// the account's own configuration is not there to give: HOME is the fixture.
	git(t, main, "-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false",
		"commit", "-q", "--allow-empty", "-m", "first")

	worktree = filepath.Join(main, ".claude", "worktrees", "feature")
	git(t, main, "worktree", "add", "-q", worktree, "-b", "worktree-feature")
	if c.branch != "" {
		git(t, worktree, "branch", "-m", c.branch)
	}
	return main, worktree
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

// A herdr that writes down what it was asked and answers from a file, so what a case pins
// is the commands and not the socket.
func fake(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "herdr")
	script := `#!/bin/sh
printf '%s\n' "$*" >>"$HERDR_FAKE_LOG"
if [ -f "$HERDR_FAKE_REPLIES/$1 $2" ]; then
  cat "$HERDR_FAKE_REPLIES/$1 $2"
  exit 0
fi
printf '{"id":"cli","error":{"code":"refused","message":"no answer for %s"}}\n' "$1 $2"
exit 1
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func answers(t *testing.T, c testCase, replace *strings.Replacer) string {
	t.Helper()

	dir := t.TempDir()
	for command, reply := range canned {
		if given, ok := c.replies[command]; ok {
			reply = given
		}
		if omitted(c.omit, command) {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, command), []byte(replace.Replace(reply)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func omitted(omit []string, command string) bool {
	for _, name := range omit {
		if name == command {
			return true
		}
	}
	return false
}

func payload(t *testing.T, c testCase, replace *strings.Replacer) string {
	t.Helper()

	event := c.event
	if event == "" {
		event = "PostToolUse"
	}
	body := map[string]any{"session_id": session, "hook_event_name": event}
	if c.tool != "" {
		body["tool_name"] = c.tool
	}
	if c.agent != "" {
		body["agent_id"] = c.agent
	}
	if c.response != "" {
		body["tool_response"] = json.RawMessage(replace.Replace(c.response))
	}
	if c.cwd != "" {
		body["cwd"] = replace.Replace(c.cwd)
	}
	out, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func called(t *testing.T, log string) []string {
	t.Helper()

	body, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(body), "\n"), "\n")
}

func same(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
