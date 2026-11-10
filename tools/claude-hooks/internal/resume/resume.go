// Package resume keeps a Claude Code session with the herdr-core terminal it was typed
// into, so that a terminal which comes back as a shell comes back on the same session.
//
// Every session on this Mac is a terminal of one core, almost always `claude` typed into
// the terminal's login zsh. A core that stops parks its kept terminals and the next core
// start replaces each lost program with a fresh login shell in its last folder — the
// program itself is never re-run, so after a reboot every session is still on disk with
// nothing saying which terminal it belonged to. The core will do this itself once OSC 7501
// carries a resume reference (herdrdev/herdr-core#15); until then the session id is a
// label on the terminal, set by a hook, and the shell that comes back reads it.
//
// So this is two halves of one fact. Track writes the label as a session starts and takes
// it off when that session is deliberately ended; Recall answers the shell with what the
// label says, if anything. Both are silent and both fail into doing nothing: a hook that
// printed would put its words in the session's context, and a shell startup that failed
// loudly would be every terminal Tim opens.
package resume

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

// The two labels, under a prefix of this tool's own so that nothing of herdr.* is touched:
// the session to come back on, and the folder to come back to, which is the session's cwd
// rather than the terminal's — a session is resumed where it was working.
const (
	SessionLabel = "claude.session"
	FolderLabel  = "claude.cwd"
)

// A SessionStart hook has the session waiting on it and a shell has Tim waiting on it, so
// a core that will not answer costs seconds rather than the hook's whole budget.
const wait = 3 * time.Second

// A terminal as herdr-core reports it. argv is the program the terminal was created to
// run, which outlives whatever is in it now; pid is that program's; foreground is what is
// running in it at this moment.
type Terminal struct {
	Argv       []string          `json:"argv"`
	PID        int               `json:"pid"`
	Labels     map[string]string `json:"labels"`
	Foreground struct {
		PID int `json:"pid"`
	} `json:"foreground"`
}

// The core, as this needs it: the one terminal this process is in, read and labelled.
type Core interface {
	Get() (Terminal, error)
	Label(labels ...string) error
}

// Open is the core this process's terminal belongs to, or nothing at all when there is no
// terminal — which is every shell and every Claude outside one, and the reason this costs
// them not even a process.
func Open() Core {
	id := os.Getenv("HC_TERMINAL_ID")
	if id == "" {
		return nil
	}
	return core{terminal: id}
}

// SessionsDir is where Claude Code keeps its transcripts, a folder per project.
func SessionsDir() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "projects")
	}
	return filepath.Join(os.Getenv("HOME"), ".claude", "projects")
}

type event struct {
	Name    string `json:"hook_event_name"`
	Session string `json:"session_id"`
	CWD     string `json:"cwd"`
	Reason  string `json:"reason"`
}

// Track reads a SessionStart or SessionEnd event and labels the terminal with the session
// it is now on, or takes the label off, as decide says. claude is CLAUDE_PID, the pid of
// the Claude Code that ran the hook.
func Track(in io.Reader, c Core, claude int) {
	defer func() { recover() }()

	if c == nil {
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
	terminal, err := c.Get()
	if err != nil {
		return
	}
	if labels := decide(ev, terminal, claude); labels != nil {
		c.Label(labels...)
	}
}

// decide is the labels to set for one event, or nil for an event that changes nothing.
//
// Nothing is recorded for a terminal whose own program is not a shell: hachiko creates a
// terminal with `claude` itself as the program, and replaces that program after a core
// restart — a shell is what says the session was typed in and so is a shell's to type
// again.
func decide(ev event, terminal Terminal, claude int) []string {
	if !isShell(terminal.Argv) {
		return nil
	}

	switch ev.Name {
	case "SessionStart":
		if ev.Session == "" || ev.CWD == "" {
			return nil
		}
		// The one Claude the terminal is sitting at, and not a `claude -p` a Bash tool
		// started or anything else nested inside it: Claude Code sets CLAUDE_PID to its
		// own process, so a nested one's is not the terminal's foreground.
		if claude == 0 || claude != terminal.Foreground.PID {
			return nil
		}
		// Every source relabels, `clear` and `compact` included, each of which leaves the
		// terminal on a session with a new id.
		return []string{SessionLabel + "=" + ev.Session, FolderLabel + "=" + ev.CWD}

	case "SessionEnd":
		// Only an exit Tim asked for. `clear` and `resume` are each followed by a
		// SessionStart that relabels, and `other` is everything else — a SIGTERM from a
		// reboot or a core stop arrives as `other`, which is the one case this whole
		// arrangement exists for, so an end that is not named here leaves the label alone.
		if ev.Reason != "prompt_input_exit" && ev.Reason != "logout" {
			return nil
		}
		// The label says whether this is the session the terminal is on, which at a
		// session's end is a surer test than a foreground being torn down.
		if ev.Session == "" || terminal.Labels[SessionLabel] != ev.Session {
			return nil
		}
		return []string{SessionLabel + "=", FolderLabel + "="}
	}
	return nil
}

// Recall writes the session the terminal is on and the folder it was working in, one per
// line, for the shell a parked terminal came back as; nothing at all when there is nothing
// to come back to. shell is the pid that shell gave for itself.
//
// Two plain lines rather than anything a shell would evaluate, because what is printed
// comes out of a label and a label is written by whoever can reach the core.
func Recall(out io.Writer, c Core, shell int, sessions string) {
	defer func() { recover() }()

	if c == nil || shell == 0 {
		return
	}
	terminal, err := c.Get()
	if err != nil {
		return
	}
	// Only the shell the core itself put there. A zsh started inside the terminal, or one
	// of Claude's own, would otherwise take over the session that terminal is already on.
	if shell != terminal.PID {
		return
	}
	session, folder := terminal.Labels[SessionLabel], terminal.Labels[FolderLabel]
	if !isSession(session) || folder == "" {
		return
	}
	if !transcript(sessions, session) {
		return
	}
	fmt.Fprintf(out, "%s\n%s\n", session, folder)
}

// A session id as Claude Code writes one, checked rather than trusted: it goes into a glob
// below and comes from a label anything able to reach the core could have written.
func isSession(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '-':
		default:
			return false
		}
	}
	return true
}

// Claude Code keeps a transcript under a folder named for the project it was in, so which
// project that was is one glob rather than a slug this would have to spell the same way
// Claude Code does. A session whose transcript has been cleaned away is one `--resume`
// would open an empty window on.
func transcript(sessions, session string) bool {
	found, err := filepath.Glob(filepath.Join(sessions, "*", session+".jsonl"))
	return err == nil && len(found) > 0
}

// The program a terminal was created to run, read as a shell or not. A login shell is
// conventionally argv[0] with a `-` in front, which is not a path.
func isShell(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	switch filepath.Base(strings.TrimPrefix(argv[0], "-")) {
	case "zsh", "bash", "sh", "fish":
		return true
	}
	return false
}

type core struct {
	terminal string
}

func (c core) Get() (Terminal, error) {
	var terminal Terminal
	out, err := c.run("get", c.terminal, "--json")
	if err != nil {
		return terminal, err
	}
	if err := json.Unmarshal(out, &terminal); err != nil {
		return Terminal{}, err
	}
	return terminal, nil
}

func (c core) Label(labels ...string) error {
	_, err := c.run(append([]string{"label", c.terminal}, labels...)...)
	return err
}

// hc by name: it is on PATH wherever a terminal's own environment reaches, and the
// variables that say which core and which terminal are already in that environment.
func (c core) run(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	return exec.CommandContext(ctx, "hc", args...).Output()
}
