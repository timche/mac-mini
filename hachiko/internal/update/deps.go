package update

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
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
	ctx, cancel := context.WithTimeout(context.Background(), cmd.Timeout)
	defer cancel()

	exe := exec.CommandContext(ctx, cmd.Name, cmd.Args...)
	exe.Dir = cmd.Dir

	var stdout, both bytes.Buffer
	exe.Stdout = io.MultiWriter(&stdout, &both)
	exe.Stderr = &both

	err := exe.Run()
	text := strings.TrimSpace(both.String())
	if err != nil && text == "" {
		text = err.Error()
	}
	return Output{OK: err == nil, Stdout: stdout.String(), Text: text}
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
