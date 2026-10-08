// hachiko is the account's caretaker for this Mac: the things that have to be looked
// after on a machine with no screen and nobody at it, each of them a command of its own
// and a package of its own behind that.
//
// The watch is the one a bare `hachiko` runs: every five minutes it checks free space and
// what is burning CPU, and alerts when it matters. Most of the rest are what grew out of
// that alert — the on-call session hachiko opens in herdr to work an incident, the two
// commands that session runs for itself, and the listener that takes Tim's reply to it out
// of Discord. `hachiko gc` is the other thing a Mac with nobody at it needs: every ten
// minutes it sweeps what a removed worktree and a finished session left behind. `hachiko
// sync` is the third, and the one that never finishes: it polls the repositories it is given
// and commits and pushes what gets written in them, so a session's work publishes itself.
//
// `hachiko status` is the one command here nothing on a timer runs: it is for a person or a
// session in a terminal, and it says in one screen what the four of them are doing. It reads
// and changes nothing at all.
//
// Nothing in this file does any of it: it parses the command and hands it to the package
// that owns it, which is what keeps a new one to one case and one import.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/gc"
	"github.com/timche/mac-mini/hachiko/internal/listen"
	"github.com/timche/mac-mini/hachiko/internal/oncall"
	"github.com/timche/mac-mini/hachiko/internal/session"
	"github.com/timche/mac-mini/hachiko/internal/status"
	"github.com/timche/mac-mini/hachiko/internal/sync"
	"github.com/timche/mac-mini/hachiko/internal/watch"
)

const usage = `usage: hachiko [--dry-run | --test-alert]
       hachiko status
       hachiko gc [--dry-run]
       hachiko sync [--once | --dry-run]
       hachiko notify [--outcome] <incident-id> <message-file>
       hachiko oncall <name> <brief-file>
       hachiko approval-request <incident-id> <action-file>
       hachiko listen

  (no option)        check free space and what is burning CPU, and alert when it matters
  --dry-run          report what a check sees, change nothing, alert nothing
  --test-alert       send a short message to the channel, to prove it works
  status             print one screen of how this Mac's own machinery is doing: the
                     agents, free space, what the watch has open, every repository
                     sync keeps upstream, and the logs it caps. Reads and nothing else
  gc                 sweep what a removed worktree and a finished session left behind:
                     their compose projects, volumes, processes and scratch folders,
                     and once a day docker's build cache and the images nothing refers
                     to; --dry-run says what a sweep would do and changes nothing
  sync               commit and push the repositories ~/.config/hachiko/sync lists, as
                     they change, until stopped; --once is one pass over every one of
                     them with the push delay ignored, and --dry-run says what the
                     daemon would commit and push and changes nothing
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
		case "status":
			if len(args) != 1 {
				return badUsage("status takes no arguments")
			}
			return status.Run(cfg)
		case "gc":
			dry := false
			for _, arg := range args[1:] {
				if arg != "--dry-run" {
					return badUsage("gc takes --dry-run and nothing else")
				}
				dry = true
			}
			return gc.Run(cfg, dry)
		case "sync":
			once, dry := false, false
			for _, arg := range args[1:] {
				switch arg {
				case "--once":
					once = true
				case "--dry-run":
					dry = true
				default:
					return badUsage("sync takes --once or --dry-run and nothing else")
				}
			}
			if once && dry {
				return badUsage("sync takes --once or --dry-run, not both")
			}
			return sync.Run(cfg, once, dry)
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
			return listen.Run(cfg)
		}
	}

	dry := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run":
			dry = true
		case "--test-alert":
			return watch.TestAlert(cfg)
		// Left out of the usage: these two are how the steps that hold the webhook URL and
		// the bot token are re-entered under `op run`, and nothing else should call them.
		case "--send":
			return sendFromStdin(args[i+1:])
		case "--listen-mode":
			return listen.WithToken(cfg)
		case "-h", "--help":
			fmt.Print(usage)
			return nil
		default:
			return badUsage("unknown option %s", args[i])
		}
	}

	return watch.Run(cfg, dry)
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
