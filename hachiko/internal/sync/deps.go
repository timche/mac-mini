package sync

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/self"
)

// Deps is everything a sync learns about the machine or does to it. Each one is a function
// so that every decision below is driven by a test with no remote to reach, no minutes to
// wait and no Discord to post to.
type Deps struct {
	Now   func() time.Time
	Log   io.Writer
	Sleep func(d time.Duration)

	// One git command in one repository: the arguments that follow `git -C <dir>`.
	Git func(dir string, args ...string) Output

	// Reaches the channel Tim watches, and answers with the thread it opened. The only
	// thing here that ever sees the webhook or the bot token, and it is the watch's own
	// sender: a failure here goes out the same way an alert does.
	Send func(out discord.Outgoing) (string, error)

	// What the binary this process is running looks like on disk. The wrapper moves a fresh
	// build into place in one step, so the file behind the same path is a different one —
	// which is the whole of how a daemon that never exits picks up an edit.
	Self func() (self.ID, bool)
}

// A git that hangs is a daemon that stops syncing every repository, not just this one, so
// every call is bounded. The push and the fetch get the long one because they are the two
// that go over the network; the rest are local and answer in milliseconds.
const (
	localTimeout   = 30 * time.Second
	networkTimeout = 5 * time.Minute
)

func realDeps(cfg config.Config) Deps {
	return Deps{
		Now:   config.ClockFromEnv(),
		Log:   os.Stdout,
		Sleep: time.Sleep,
		Git:   runGit,
		Send:  func(out discord.Outgoing) (string, error) { return discord.SendThroughOP(cfg, out) },
		Self:  self.Stat,
	}
}

func runGit(dir string, args ...string) Output {
	limit := localTimeout
	switch args[0] {
	case "fetch", "push", "pull":
		limit = networkTimeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)

	// A git that asks for a password has nobody to ask: under launchd there is no terminal,
	// and without this it is a push that waits for ever rather than one that fails and is
	// retried.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err := cmd.Run()
	text := strings.TrimSpace(strings.TrimSpace(stderr.String()) + "\n" + strings.TrimSpace(stdout.String()))
	if err != nil && text == "" {
		text = err.Error()
	}
	return Output{OK: err == nil, Stdout: stdout.String(), Text: strings.TrimSpace(text)}
}

func failed(out Output) error { return &gitError{out.Text} }

type gitError struct{ text string }

func (e *gitError) Error() string {
	if e.text == "" {
		return "git failed and said nothing"
	}
	return e.text
}
