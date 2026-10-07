// hachiko watches a Mac with no screen for the two failures nobody is there to
// notice: a process logging into a file nothing bounds, and a process burning a core
// for an hour after whatever wanted it has gone. Every five minutes, from the
// io.github.timche.hachiko LaunchAgent.
//
// Nothing here kills anything, and the one thing it changes is a truncate. An alert
// is a one-line message the moment something fires, an on-call session in herdr that
// investigates and reports the analysis itself, and hachiko's own raw details if
// that session never reports — the session is the better alert and the worse
// guarantee, so hachiko never depends on it for the first word.
package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/oncall"
	"github.com/timche/mac-mini/hachiko/internal/session"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

const usage = `usage: hachiko [--dry-run | --test-alert]
       hachiko notify [--outcome] <incident-id> <message-file>
       hachiko oncall <name> <brief-file>
       hachiko approval-request <incident-id> <action-file>
       hachiko listen

  (no option)        check free space and what is burning CPU, and alert when it matters
  --dry-run          report what a check sees, change nothing, alert nothing
  --test-alert       send a short message to the channel, to prove it works
  notify             send a message about an incident to the channel Tim watches,
                     which is how the on-call session reports its findings;
                     --outcome marks the one that says the incident is resolved
  oncall             open a Claude Code session in herdr to work an incident, and
                     print the label of the tab it is waiting in
  approval-request   register the one action an on-call session is asking Tim to
                     approve with a code, so a code alone approves nothing else
  listen             watch the incident threads in Discord for a reply from Tim and
                     hand it to the on-call session; does nothing unless configured
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		var usageErr usageError
		if errors.As(err, &usageErr) {
			fmt.Fprint(os.Stderr, usage)
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "hachiko: %v\n", err)
		os.Exit(1)
	}
}

type usageError struct{ error }

func badUsage(format string, args ...any) error {
	return usageError{fmt.Errorf(format, args...)}
}

func run(args []string) error {
	cfg := config.FromEnv()

	if len(args) > 0 {
		switch args[0] {
		case "notify":
			// A constant word and nothing of the incident's, so it is one of the few things
			// here that may be an argument: ps showing it says only that a session said it had
			// finished.
			rest, outcome := args[1:], false
			if len(rest) > 0 && rest[0] == "--outcome" {
				rest, outcome = rest[1:], true
			}
			if len(rest) != 2 {
				return badUsage("notify takes an incident id and a file, after an optional --outcome")
			}
			return session.Notify(cfg, rest[0], rest[1], outcome)
		case "oncall":
			if len(args) != 3 {
				return badUsage("oncall takes a name and a file")
			}
			return oncall.Run(cfg, args[1], args[2])
		case "approval-request":
			if len(args) != 3 {
				return badUsage("approval-request takes an incident id and a file")
			}
			return session.ApprovalRequest(cfg, args[1], args[2])
		case "listen":
			if len(args) != 1 {
				return badUsage("listen takes no arguments")
			}
			return listen(cfg)
		}
	}

	dry := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run":
			dry = true
		case "--test-alert":
			return testAlert(cfg)
		// Left out of the usage: these two are how the steps that hold the webhook URL and
		// the bot token are re-entered under `op run`, and nothing else should call them.
		case "--send":
			return sendFromStdin(args[i+1:])
		case "--listen-mode":
			return listenWithToken(cfg)
		case "-h", "--help":
			fmt.Print(usage)
			return nil
		default:
			return badUsage("unknown option %s", args[i])
		}
	}

	deps := realDeps(cfg)
	return sweeper{cfg: cfg, deps: deps, store: statedir.Store{Dir: cfg.StateDir}, dry: dry}.run()
}

// The flags the `op run` child is handed: a channel and a thread, which are ids in a URL
// rather than secrets. The thread it opened is the one thing it prints.
func sendFromStdin(args []string) error {
	out, channel := discord.Outgoing{}, ""

	for i := 0; i < len(args); i++ {
		if i+1 >= len(args) {
			return badUsage("%s takes a value", args[i])
		}
		switch args[i] {
		case "--channel":
			channel = args[i+1]
		case "--thread":
			out.Thread = args[i+1]
		case "--open-thread":
			out.OpenThread = args[i+1]
		default:
			return badUsage("unknown option %s", args[i])
		}
		i++
	}

	thread, err := discord.SendMode(os.Stdin, out, channel)
	if err != nil {
		return err
	}
	if thread != "" {
		fmt.Println(thread)
	}
	return nil
}

func testAlert(cfg config.Config) error {
	free, err := freeKB(cfg.Home)
	if err != nil {
		return err
	}

	message := wording.Lead(wording.MarkerInfo, "Test alert from hachiko — nothing is wrong").
		Field(wording.LabelFreeSpace, wording.GBUnit(free)).
		About("", cfg.Host).
		String()

	if _, err := discord.SendThroughOP(cfg, discord.Outgoing{Text: message}); err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "%s hachiko: sent a test alert\n", time.Now().Format("2006-01-02T15:04:05-0700"))
	return nil
}
