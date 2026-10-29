// guard is the hook that reads a command every session on this Mac is about to run and
// refuses the two kinds of command that reach out of the session that ran them: a kill
// that picks its target by name, pattern or port, which stops another session's copy of
// the same dev server, and a command that takes the Mac's one foreground without the lock
// that serialises it, which types and clicks into another session's app.
//
// It is a compiled binary rather than a script because a hook that cannot run is a hook
// that is silently off: a Python that a mise or a brew upgrade moved takes the guard with
// it, and nothing in a session says so. This depends on a kernel and nothing else.
//
// Nothing in this file decides anything: it parses the command and hands the event to the
// packages that own the two checks.
package main

import (
	"fmt"
	"os"

	"github.com/timche/mac-mini/guard/internal/focus"
	"github.com/timche/mac-mini/guard/internal/hook"
	"github.com/timche/mac-mini/guard/internal/killbypid"
)

const usage = `usage: guard pre-tool-use

  pre-tool-use     read a PreToolUse hook event on stdin and print a deny decision when
                   the command it carries kills by name, pattern or port, or takes the
                   Mac's one foreground outside focus-lock; print nothing otherwise
`

// 1 and never 2: 2 is how a PreToolUse hook blocks the command, so a guard that was
// called wrong reports itself without costing the session its command.
func main() {
	if len(os.Args) != 2 || os.Args[1] != "pre-tool-use" {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	checks := []hook.Check{
		{Denies: killbypid.Denies, Reason: killbypid.Reason},
		{Denies: focus.Denies, Reason: focus.Reason},
	}
	if err := hook.PreToolUse(os.Stdin, os.Stdout, checks); err != nil {
		fmt.Fprintf(os.Stderr, "guard: %v\n", err)
		os.Exit(1)
	}
}
