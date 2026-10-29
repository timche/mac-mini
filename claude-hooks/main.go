// claude-hooks is the binary every Claude Code hook on this Mac runs as a subcommand of,
// so that one build, one cache and one wrapper serve all of them.
//
// guard reads a command every session is about to run and refuses the two kinds of
// command that reach out of the session that ran them: a kill that picks its target by
// name, pattern or port, which stops another session's copy of the same dev server, and a
// command that takes the Mac's one foreground without the lock that serialises it, which
// types and clicks into another session's app.
//
// strip-attribution takes Claude's attribution out of what a gh command sends GitHub,
// which git's own hooks never see, and refuses a commit or a push that skips those hooks.
//
// project-docs prints the shared docs folder and the project's own at session start, so
// finding them is a mechanism rather than a rule a session has to remember.
//
// herdr-worktree watches for the worktree Claude Code makes for itself and gives it the
// place in herdr a worktree herdr made has: a workspace in the sidebar with this session's
// tab in it, and the tab back where it came from when Claude leaves.
//
// It is a compiled binary rather than a script because a hook that cannot run is a hook
// that is silently off: a Python that a mise or a brew upgrade moved takes the hook with
// it, and nothing in a session says so. This depends on a kernel and nothing else.
//
// Nothing in this file decides anything: it picks the subcommand and hands the event to
// the packages that own its checks.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/timche/mac-mini/claude-hooks/internal/attribution"
	"github.com/timche/mac-mini/claude-hooks/internal/focus"
	"github.com/timche/mac-mini/claude-hooks/internal/fresh"
	"github.com/timche/mac-mini/claude-hooks/internal/herdrworktree"
	"github.com/timche/mac-mini/claude-hooks/internal/hook"
	"github.com/timche/mac-mini/claude-hooks/internal/killbypid"
	"github.com/timche/mac-mini/claude-hooks/internal/projectdocs"
)

// The folder this was built from and the wrapper that built it, written in by the wrapper
// at build time. Both are empty in anyone else's build, which turns the handoff below off
// rather than letting it guess where a checkout is.
var (
	source  string
	wrapper string
)

const usage = `usage: claude-hooks <hook>

  guard               read a PreToolUse hook event on stdin and print a deny decision when
                      the command it carries kills by name, pattern or port, or takes the
                      Mac's one foreground outside focus-lock; print nothing otherwise

  herdr-worktree      read a PostToolUse or SessionEnd hook event on stdin and keep this
                      session's herdr pane with the worktree Claude Code is working in: a
                      workspace of the worktree's own while it is in one, the workspace it
                      came from once it is not, and the branch's name on the sidebar entry;
                      print nothing

  project-docs        print the shared docs folder and the one this project's docs are in,
                      each as a path and the shape of what is in it, for a SessionStart
                      hook to inject into the session

  strip-attribution   read a PreToolUse hook event on stdin and print the command it
                      carries with Claude's attribution gone from it, having taken the
                      same out of the body files its gh commands name; print a deny
                      decision when it skips git's hooks, and nothing otherwise
`

// 1 and never 2: 2 is how a PreToolUse hook blocks the command, so a hook that was called
// wrong reports itself without costing the session its command.
func main() {
	if len(os.Args) != 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	var run func() error
	switch os.Args[1] {
	case "guard":
		run = guard
	case "herdr-worktree":
		run = herdrWorktree
	case "project-docs":
		run = projectDocsHook
	case "strip-attribution":
		run = stripAttribution
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	handoff()

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "claude-hooks: %v\n", err)
		os.Exit(1)
	}
}

func guard() error {
	checks := []hook.Check{
		{Denies: killbypid.Denies, Reason: killbypid.Reason},
		{Denies: focus.Denies, Reason: focus.Reason},
	}
	return hook.PreToolUse(os.Stdin, os.Stdout, checks)
}

// A SessionStart hook speaks by printing and has nothing to answer, so this never fails:
// what it could not read is a line the listing does not have.
func projectDocsHook() error {
	projectdocs.Print(os.Stdout)
	return nil
}

// An observer of a tool call that has already happened, which answers nothing and whose
// every failure is a line on stderr, so this never fails either.
func herdrWorktree() error {
	herdrworktree.Observe(os.Stdin)
	return nil
}

func stripAttribution() error {
	return hook.PreToolUseRewrite(os.Stdin, os.Stdout, attribution.Decide)
}

// Keeping an edit live, from the side of the arrangement that cannot build: settings.json
// runs this binary rather than the wrapper, whose shell, find, shasum and mise cost
// several times what the check does, before every Bash call in every session. So when the
// sources have moved the wrapper is handed this process — argv, environment and an
// untouched stdin, so the event arrives there unread and the session waits on one hook
// either way.
//
// Every way this can fail is a return, and then the command is checked by this binary,
// sources older than it or not. A hook that exits 2 blocks the command it was asked
// about, and nothing but a check refusing may do that.
func handoff() {
	defer func() { recover() }()

	if wrapper == "" {
		return
	}
	self, err := os.Executable()
	if err != nil {
		return
	}
	stale := fresh.Stale(fresh.Params{
		Source:  source,
		Binary:  self,
		Marker:  filepath.Join(filepath.Dir(self), "attempted"),
		Wrapped: os.Getenv(fresh.LoopGuard) != "",
	})
	if !stale {
		return
	}

	// argv[0] is the wrapper's path and not this binary's, because resolving $0 is how
	// the wrapper finds the checkout it builds from. The loop guard is set here as well
	// as in the wrapper, since an exec that came back would be an exec before every
	// command in every session.
	argv := append([]string{wrapper}, os.Args[1:]...)
	syscall.Exec(wrapper, argv, append(os.Environ(), fresh.LoopGuard+"=1"))
}
