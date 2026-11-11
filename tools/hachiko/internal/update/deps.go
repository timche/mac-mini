package update

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/discord"
)

// Command is one thing the update runs. Dir because every mise call has to name the
// directory it is about — mise decides which tools are active from it, and a LaunchAgent
// starts in / — and a timeout because this runs with nobody watching: an upgrade waiting on
// a prompt or a dead mirror would hold the lock until the Mac restarts.
type Command struct {
	Name    string
	Args    []string
	Dir     string
	Timeout time.Duration

	// The whole environment the command gets, when it is not to inherit this process's.
	Env []string

	// Run it in a new hc terminal rather than as a child of this process. macOS's App
	// Management refuses a change to an app bundle unless the responsible process holds that
	// grant, and for anything launchd starts here that is hachiko, which is ad-hoc signed: a
	// grant to it is pinned to one build's cdhash and lapses at the next rebuild. A terminal
	// answers to its holder, Herdr Core.app, whose Developer ID keeps its grants across
	// releases. The terminal takes the core's environment, so Env does not reach it.
	InTerminal bool
}

func (c Command) line() string { return strings.TrimSpace(c.Name + " " + strings.Join(c.Args, " ")) }

// Output is what came of it. Stdout alone is what anything is parsed out of, since mise and
// brew both write their progress and their warnings to stderr; Text is both together, which
// is what a message about a failure quotes.
type Output struct {
	OK     bool
	Stdout string
	Text   string
}

// Deps is everything the update does to this Mac or learns about it. One function each, so
// the whole of the decision-making — the schedule, which step runs, what moved, what is
// reported — is driven in tests by a table of command lines and answers rather than by
// upgrading anything.
type Deps struct {
	Now   func() time.Time
	Log   io.Writer
	Sleep func(time.Duration)

	Run func(cmd Command) Output

	// Reaches Discord, and the only thing that ever sees the bot token.
	Send func(out discord.Outgoing) (string, error)

	// Starts the update as a process of its own, which is the whole of how the five-minute
	// check runs it: the sweep has to be back inside its interval, and an upgrade takes
	// tens of minutes.
	Spawn func(args ...string) error
}

func realDeps(cfg config.Config) Deps {
	return Deps{
		Now:   config.ClockFromEnv(),
		Log:   os.Stdout,
		Sleep: time.Sleep,
		Run:   run,
		Send:  func(out discord.Outgoing) (string, error) { return discord.SendThroughOP(cfg, out) },
		Spawn: spawn,
	}
}

func run(cmd Command) Output {
	if cmd.InTerminal {
		return runInTerminal(cmd)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cmd.Timeout)
	defer cancel()

	exe := exec.CommandContext(ctx, cmd.Name, cmd.Args...)
	exe.Dir = cmd.Dir
	exe.Env = cmd.Env

	var stdout bytes.Buffer
	var both lockedBuffer
	exe.Stdout = io.MultiWriter(&stdout, &both)
	exe.Stderr = &both

	err := exe.Run()
	text := strings.TrimSpace(both.String())
	if err != nil && text == "" {
		text = err.Error()
	}
	return Output{OK: err == nil, Stdout: stdout.String(), Text: text}
}

var terminalPoll = 2 * time.Second

// `hc run --bg` with the result left in files, rather than `hc run` waiting, because the command
// this is for, `hc update`, restarts the core: the terminal survives that, and a client waiting
// on it need not. The shell writes the exit code last and by a rename, so a code file that
// exists means the output beside it is whole.
func runInTerminal(cmd Command) Output {
	hc, err := exec.LookPath("hc")
	if err != nil {
		return Output{Text: err.Error()}
	}
	// Resolved here, since the terminal's PATH is the core's and launchd started the core.
	name, err := exec.LookPath(cmd.Name)
	if err != nil {
		return Output{Text: err.Error()}
	}

	dir, err := os.MkdirTemp("", "hachiko-terminal-")
	if err != nil {
		return Output{Text: err.Error()}
	}
	defer os.RemoveAll(dir)
	outFile, codeFile := filepath.Join(dir, "out"), filepath.Join(dir, "code")

	args := []string{"run", "--bg", "--no-parent", "--no-notify", "--rm"}
	if cmd.Dir != "" {
		args = append(args, "--cwd", cmd.Dir)
	}
	args = append(args, "--", "/bin/sh", "-c",
		`out=$1 code=$2; shift 2; "$@" >"$out" 2>&1; echo $? >"$code.tmp" && mv "$code.tmp" "$code"`,
		"sh", outFile, codeFile, name)
	args = append(args, cmd.Args...)

	if started := run(Command{Name: hc, Args: args, Timeout: time.Minute}); !started.OK {
		return started
	}

	deadline := time.Now().Add(cmd.Timeout)
	for {
		if code, err := os.ReadFile(codeFile); err == nil {
			out, _ := os.ReadFile(outFile)
			text := strings.TrimSpace(string(out))
			status := strings.TrimSpace(string(code))
			if text == "" && status != "0" {
				text = "exit " + status
			}
			return Output{OK: status == "0", Stdout: string(out), Text: text}
		}
		if time.Now().After(deadline) {
			out, _ := os.ReadFile(outFile)
			return Output{Text: strings.TrimSpace(cmd.line() + " had not finished in its hc terminal after " +
				cmd.Timeout.String() + "\n" + string(out))}
		}
		time.Sleep(terminalPoll)
	}
}

// exec copies stdout and stderr on a goroutine each, and both reach this one: a plain
// bytes.Buffer written from two at once came back empty, which is how "Already up to date"
// went missing from git's output.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// The binary rather than the wrapper, which would rebuild and test the whole module before
// it ran anything, and in a session of its own so that launchd reaping the five-minute check
// does not take the upgrade with it. Its output goes where the check's does, the log the
// plist opened with `>>`.
func spawn(args ...string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}

	cmd := exec.Command(self, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
