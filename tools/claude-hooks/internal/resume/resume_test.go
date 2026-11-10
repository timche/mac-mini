package resume

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	shellPID  = 4001
	claudePID = 4002
	session   = "11808bfe-c763-4a23-8187-aca24282226d"
	folder    = "/Users/x/projects/acme/app"
)

// The terminal every case starts from: a kept login zsh with Claude running in it, which
// is what every Claude Code session on this Mac is.
func typedIn() Terminal {
	terminal := Terminal{
		Argv:   []string{"/bin/zsh", "-l"},
		PID:    shellPID,
		Labels: map[string]string{"herdr.space": "sp-mac-mini"},
	}
	terminal.Foreground.PID = claudePID
	return terminal
}

// A core that answers with one terminal and records what it was asked to label it with.
type fake struct {
	terminal Terminal
	err      error
	labelled [][]string
}

func (f *fake) Get() (Terminal, error) { return f.terminal, f.err }

func (f *fake) Label(labels ...string) error {
	f.labelled = append(f.labelled, labels)
	return nil
}

func start(source string) string {
	return encode(map[string]any{
		"hook_event_name": "SessionStart",
		"session_id":      session,
		"cwd":             folder,
		"source":          source,
		"transcript_path": "/Users/x/.claude/projects/-Users-x-projects-acme-app/" + session + ".jsonl",
	})
}

func end(reason string) string {
	return encode(map[string]any{
		"hook_event_name": "SessionEnd",
		"session_id":      session,
		"cwd":             folder,
		"reason":          reason,
	})
}

func encode(fields map[string]any) string {
	body, err := json.Marshal(fields)
	if err != nil {
		panic(err)
	}
	return string(body)
}

func track(t *testing.T, body string, terminal Terminal, claude int) [][]string {
	t.Helper()

	core := &fake{terminal: terminal}
	Track(strings.NewReader(body), core, claude)
	return core.labelled
}

func only(t *testing.T, labelled [][]string) []string {
	t.Helper()

	if len(labelled) != 1 {
		t.Fatalf("labelled the terminal %d times, wanted once: %v", len(labelled), labelled)
	}
	return labelled[0]
}

// Every way a session can start leaves the terminal pointing at the session it is on now:
// a /clear and a compaction each hand the terminal a new id, and a resume one it already
// had.
func TestEverySourceRecords(t *testing.T) {
	for _, source := range []string{"startup", "resume", "clear", "compact", "fork", "whatever-is-added-next"} {
		labels := only(t, track(t, start(source), typedIn(), claudePID))
		want := []string{SessionLabel + "=" + session, FolderLabel + "=" + folder}
		if strings.Join(labels, " ") != strings.Join(want, " ") {
			t.Errorf("source %q labelled %v, wanted %v", source, labels, want)
		}
	}
}

// The folder is the session's own rather than the terminal's, so a session resumes where
// it was working.
func TestRecordsTheSessionsFolder(t *testing.T) {
	body := encode(map[string]any{
		"hook_event_name": "SessionStart",
		"session_id":      session,
		"cwd":             folder + "/.claude/worktrees/agent-7",
		"source":          "startup",
	})
	labels := only(t, track(t, body, typedIn(), claudePID))
	if labels[1] != FolderLabel+"="+folder+"/.claude/worktrees/agent-7" {
		t.Errorf("recorded %q", labels[1])
	}
}

// hachiko creates a terminal with claude as the program itself, for on-call and for mochi,
// and replaces that program after a core restart. Nothing here may resume one.
func TestATerminalWhoseProgramIsClaudeIsNotRecorded(t *testing.T) {
	terminal := typedIn()
	terminal.Argv = []string{"claude", "--dangerously-skip-permissions", "on-call"}
	if labelled := track(t, start("startup"), terminal, claudePID); labelled != nil {
		t.Errorf("labelled a terminal running claude itself: %v", labelled)
	}
}

func TestEveryShellIsRecorded(t *testing.T) {
	for _, argv := range [][]string{
		{"/bin/zsh", "-l"},
		{"-zsh"},
		{"/opt/homebrew/bin/bash"},
		{"/bin/sh"},
		{"/opt/homebrew/bin/fish"},
	} {
		terminal := typedIn()
		terminal.Argv = argv
		if labelled := track(t, start("startup"), terminal, claudePID); labelled == nil {
			t.Errorf("argv %v was not read as a shell", argv)
		}
	}
}

// A `claude -p` a Bash tool started, a `claude` in a nested shell, anything but the one
// Claude the terminal is sitting at: Claude Code sets CLAUDE_PID to its own process, so a
// nested one does not match the foreground the core reports.
func TestOnlyTheTerminalsForegroundClaudeIsRecorded(t *testing.T) {
	if labelled := track(t, start("startup"), typedIn(), claudePID+77); labelled != nil {
		t.Errorf("labelled for a nested Claude: %v", labelled)
	}
	if labelled := track(t, start("startup"), typedIn(), 0); labelled != nil {
		t.Errorf("labelled with no CLAUDE_PID to check: %v", labelled)
	}
}

// An exit Tim asked for takes the label off. Everything else leaves it: a reboot and a
// core stop arrive as `other`, which is the case this whole arrangement exists for, and
// `clear` and `resume` are each followed by a SessionStart that relabels.
func TestOnlyADeliberateExitForgets(t *testing.T) {
	forgets := map[string]bool{
		"prompt_input_exit": true,
		"logout":            true,
		"clear":             false,
		"resume":            false,
		"other":             false,
		"":                  false,
	}
	for reason, wanted := range forgets {
		terminal := typedIn()
		terminal.Labels[SessionLabel] = session
		terminal.Labels[FolderLabel] = folder

		labelled := track(t, end(reason), terminal, claudePID)
		if !wanted {
			if labelled != nil {
				t.Errorf("reason %q relabelled the terminal: %v", reason, labelled)
			}
			continue
		}
		labels := only(t, labelled)
		want := []string{SessionLabel + "=", FolderLabel + "="}
		if strings.Join(labels, " ") != strings.Join(want, " ") {
			t.Errorf("reason %q labelled %v, wanted %v", reason, labels, want)
		}
	}
}

// A session ending is only the terminal's own session ending when the label says so.
func TestAnExitForgetsNothingButItsOwnSession(t *testing.T) {
	terminal := typedIn()
	terminal.Labels[SessionLabel] = "another-session"
	terminal.Labels[FolderLabel] = folder

	if labelled := track(t, end("prompt_input_exit"), terminal, claudePID); labelled != nil {
		t.Errorf("took another session's label off: %v", labelled)
	}
	if labelled := track(t, end("prompt_input_exit"), typedIn(), claudePID); labelled != nil {
		t.Errorf("labelled a terminal that had no session on it: %v", labelled)
	}
}

// Everything a hook cannot read is nothing at all: it has a session waiting on it and
// stdout of its own would land in that session's context.
func TestAnEventItCannotReadChangesNothing(t *testing.T) {
	for _, body := range []string{
		"",
		"not json",
		`{"hook_event_name":"SessionStart","source":"startup"}`,
		`{"hook_event_name":"SessionStart","session_id":"x","source":"startup"}`,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash"}`,
	} {
		if labelled := track(t, body, typedIn(), claudePID); labelled != nil {
			t.Errorf("%q labelled the terminal: %v", body, labelled)
		}
	}
}

func TestNoTerminalAndNoCoreChangeNothing(t *testing.T) {
	Track(strings.NewReader(start("startup")), nil, claudePID)

	core := &fake{terminal: typedIn(), err: os.ErrNotExist}
	Track(strings.NewReader(start("startup")), core, claudePID)
	if core.labelled != nil {
		t.Errorf("labelled a terminal the core would not report: %v", core.labelled)
	}
}

// What the shell a parked terminal came back as is told, over a transcript folder laid out
// the way Claude Code lays one out.
func recall(t *testing.T, terminal Terminal, shell int, transcripts ...string) string {
	t.Helper()

	sessions := filepath.Join(t.TempDir(), "projects")
	for _, id := range transcripts {
		dir := filepath.Join(sessions, "-Users-x-projects-acme-app")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var out strings.Builder
	Recall(&out, &fake{terminal: terminal}, shell, sessions)
	return out.String()
}

func onIt() Terminal {
	terminal := typedIn()
	terminal.Labels[SessionLabel] = session
	terminal.Labels[FolderLabel] = folder
	// A parked terminal comes back as a shell with nothing in it yet.
	terminal.Foreground.PID = shellPID
	return terminal
}

func TestTheShellIsToldTheSessionAndTheFolder(t *testing.T) {
	got := recall(t, onIt(), shellPID, session)
	if got != session+"\n"+folder+"\n" {
		t.Errorf("told the shell %q", got)
	}
}

// A new terminal has no label, and a terminal whose session was deliberately ended no
// longer has one.
func TestNoLabelIsNothingToResume(t *testing.T) {
	if got := recall(t, typedIn(), shellPID, session); got != "" {
		t.Errorf("told the shell %q", got)
	}
}

// A zsh started inside the terminal, and Claude's own shells, are not the program the core
// put there, and must not take over the session the terminal is already on.
func TestOnlyTheTerminalsOwnShellResumes(t *testing.T) {
	if got := recall(t, onIt(), shellPID+9, session); got != "" {
		t.Errorf("a nested shell was told %q", got)
	}
	if got := recall(t, onIt(), 0, session); got != "" {
		t.Errorf("a shell with no pid to check was told %q", got)
	}
}

// A session whose transcript has been cleaned away is one `--resume` would open an empty
// window on, so the shell is told nothing and left at its prompt.
func TestASessionWithNoTranscriptIsNotResumed(t *testing.T) {
	if got := recall(t, onIt(), shellPID); got != "" {
		t.Errorf("told the shell %q", got)
	}
	if got := recall(t, onIt(), shellPID, "a-different-session"); got != "" {
		t.Errorf("told the shell %q", got)
	}
}

// What is printed comes out of a label, and a label is written by whoever can reach the
// core, so an id that is not one Claude Code wrote never reaches the glob or the shell.
func TestASessionIdThatIsNotOneIsRefused(t *testing.T) {
	for _, id := range []string{"../../etc/passwd", "a b", "a;rm -rf /", "*"} {
		terminal := onIt()
		terminal.Labels[SessionLabel] = id
		if got := recall(t, terminal, shellPID, session); got != "" {
			t.Errorf("label %q was told to the shell as %q", id, got)
		}
	}
}

func TestAFolderlessLabelIsNothingToResume(t *testing.T) {
	terminal := onIt()
	delete(terminal.Labels, FolderLabel)
	if got := recall(t, terminal, shellPID, session); got != "" {
		t.Errorf("told the shell %q", got)
	}
}

func TestNoTerminalIsNothingToResume(t *testing.T) {
	var out strings.Builder
	Recall(&out, nil, shellPID, t.TempDir())
	if out.String() != "" {
		t.Errorf("told the shell %q", out.String())
	}
}

// The terminal as the core reports it, read off a real `hc get --json`, so the fields this
// decides on are the fields herdr-core answers with.
func TestTheCoresAnswerIsReadAsItComes(t *testing.T) {
	const answer = `{"address":"t37","argv":["/bin/zsh","-l"],"cwd":"/Users/x/.mac-mini",
	  "foreground":{"argv":["claude"],"cwd":"/Users/x/.mac-mini","pid":22850},
	  "keep":true,"labels":{"herdr.space":"sp-mac-mini","claude.session":"` + session + `",
	  "claude.cwd":"` + folder + `"},"pid":22047,"state":"live"}`

	var terminal Terminal
	if err := json.Unmarshal([]byte(answer), &terminal); err != nil {
		t.Fatal(err)
	}
	if !isShell(terminal.Argv) || terminal.PID != 22047 || terminal.Foreground.PID != 22850 {
		t.Fatalf("read the terminal as %+v", terminal)
	}
	labels := only(t, track(t, start("startup"), terminal, 22850))
	if labels[0] != SessionLabel+"="+session {
		t.Errorf("labelled %v", labels)
	}
}

func TestTheSessionsFolderFollowsTheConfigDirectory(t *testing.T) {
	t.Setenv("HOME", "/Users/x")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if got := SessionsDir(); got != "/Users/x/.claude/projects" {
		t.Errorf("sessions are in %q", got)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "/Users/x/elsewhere")
	if got := SessionsDir(); got != "/Users/x/elsewhere/projects" {
		t.Errorf("sessions are in %q", got)
	}
}

// The commands the core is actually sent, over a stand-in hc that records its arguments:
// one terminal read and one label call, the terminal named every time rather than left to
// the one hc would pick for itself.
func TestTheCommandsTheCoreIsSent(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	stand := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >>%s
case $1 in
  get) printf '%%s' '{"argv":["/bin/zsh","-l"],"pid":%d,"foreground":{"pid":%d},"labels":{}}' ;;
esac
`, calls, shellPID, claudePID)
	if err := os.WriteFile(filepath.Join(dir, "hc"), []byte(stand), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("HC_TERMINAL_ID", "t37")

	Track(strings.NewReader(start("startup")), Open(), claudePID)

	sent, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	want := "get t37 --json\nlabel t37 " + SessionLabel + "=" + session + " " + FolderLabel + "=" + folder + "\n"
	if string(sent) != want {
		t.Errorf("sent the core %q, wanted %q", sent, want)
	}
}

func TestNoTerminalInTheEnvironmentIsNoCoreAtAll(t *testing.T) {
	t.Setenv("HC_TERMINAL_ID", "")
	if Open() != nil {
		t.Error("asked for a core outside a terminal of one")
	}
	t.Setenv("HC_TERMINAL_ID", "t37")
	if Open() == nil {
		t.Error("no core inside a terminal")
	}
}
