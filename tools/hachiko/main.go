// hachiko is the account's caretaker for this Mac: the things that have to be looked
// after on a machine with no screen and nobody at it, each of them a command of its own
// and a package of its own behind that.
//
// The watch is the one a bare `hachiko` runs: every five minutes it checks free space,
// what is burning CPU, what a finished session left running and how much of the account's
// Claude usage limits is left, and alerts when it matters. Most of the rest are what grew out of
// that alert — the on-call session hachiko opens in herdr to work an incident, the two
// commands that session runs for itself, and the listener that takes Tim's reply to it out
// of Discord. `hachiko gc` is the other thing a Mac with nobody at it needs: every ten
// minutes it sweeps what a removed worktree and a finished session left behind, and once an
// hour it removes the worktree of a branch whose pull request has merged. `hachiko
// sync` is the third, and the one that never finishes: it polls the repositories it is given
// and commits and pushes what gets written in them, so a session's work publishes itself.
//
// `hachiko update` is the fourth, and the one thing on this Mac that moves a version: once a
// night the five-minute check starts it, and it brings mise, the tools mise manages,
// Homebrew's packages and herdr forward and deals with what each of those leaves behind.
//
// `hachiko status` is the one command here nothing on a timer runs: it is for a person or a
// session in a terminal, and it says in one screen what the others are doing. It reads and
// changes nothing at all.
//
// Nothing in this file does any of it: it parses the command and hands it to the package
// that owns it, which is what keeps a new one to one case and one import.
package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/gc"
	"github.com/timche/mac-mini/hachiko/internal/listen"
	"github.com/timche/mac-mini/hachiko/internal/oncall"
	"github.com/timche/mac-mini/hachiko/internal/session"
	"github.com/timche/mac-mini/hachiko/internal/status"
	"github.com/timche/mac-mini/hachiko/internal/sync"
	"github.com/timche/mac-mini/hachiko/internal/update"
	"github.com/timche/mac-mini/hachiko/internal/watch"
)

const usage = `usage: hachiko [--dry-run | --test-alert]
       hachiko status
       hachiko gc [--dry-run]
       hachiko sync [--once | --dry-run]
       hachiko update [--dry-run] [--security <thing>…]
       hachiko notify [--outcome] [--attempt] <incident-id> <message-file>
       hachiko oncall <name> <brief-file>
       hachiko approval-request <incident-id> <action-file>
       hachiko listen

  (no option)        check free space, what is burning CPU, what a finished session
                     left running and what is left of the account's Claude usage
                     limits, and alert when it matters
  --dry-run          report what a check sees, change nothing, alert nothing
  --test-alert       send a short message to the channel, to prove it works
  status             print one screen of how this Mac's own machinery is doing: the
                     agents, free space, what the watch has open, every repository
                     sync keeps upstream, and the logs it caps. Reads and nothing else
  gc                 sweep what a removed worktree and a finished session left behind:
                     their compose projects, volumes, processes and scratch folders,
                     once an hour the worktree of a branch whose pull request has
                     merged and nothing is using, and once a day docker's build cache
                     and the images nothing refers to; --dry-run says what a sweep
                     would do and changes nothing
  sync               commit and push the repositories ~/.hachiko/sync lists, as
                     they change, until stopped; --once is one pass over every one of
                     them with the push delay ignored, and --dry-run says what the
                     daemon would commit and push and changes nothing
  update             move this Mac's own tooling forward: mise and the tools it
                     manages, Homebrew's packages, herdr, and what each of
                     those left to do afterwards; --dry-run says what would
                     move and changes nothing. The five-minute check
                     starts one a night, so this is the explicit "update now".
                     --security takes the rest of the line as the things one
                     published advisory named and moves only those, past the
                     release-age hold
  notify             send a message about an incident to the channel Tim watches,
                     which is how the on-call session reports its findings;
                     --outcome marks the one that says the incident is resolved,
                     and --attempt the one it sends before its single fix, which
                     is what records that the attempt happened
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
		case "update":
			// `--security` takes the rest of the line, so a target is never mistaken for an
			// option and an option is never mistaken for a target.
			dry, targets, only := false, []string(nil), false
			for i := 1; i < len(args); i++ {
				switch {
				case args[i] == "--security":
					only, targets = true, args[i+1:]
					i = len(args)
				case args[i] == "--dry-run":
					dry = true
				default:
					return badUsage("update takes --dry-run and --security and nothing else")
				}
			}
			if only {
				if len(targets) == 0 {
					return badUsage("update --security takes at least one thing to upgrade")
				}
				return update.RunSecurity(cfg, targets, dry)
			}
			return update.Run(cfg, dry)
		case "notify":
			// Constant words and nothing of the incident's, so they are among the few things
			// here that may be arguments: ps showing one says only that a session said it had
			// finished, or that it was about to try something.
			rest, outcome, attempt := args[1:], false, false
			for len(rest) > 0 && strings.HasPrefix(rest[0], "--") {
				switch rest[0] {
				case "--outcome":
					outcome = true
				case "--attempt":
					attempt = true
				default:
					return badUsage("notify takes --outcome and --attempt and no other option")
				}
				rest = rest[1:]
			}
			if len(rest) != 2 {
				return badUsage("notify takes an incident id and a file, after an optional --outcome or --attempt")
			}
			return session.Notify(cfg, rest[0], rest[1], outcome, attempt)
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
		// Left out of the usage: these three are how the steps that hold the bot token are
		// re-entered under `op run`, and nothing else should call them.
		case "--send":
			return sendFromStdin(args[i+1:])
		case "--listen-mode":
			return listen.WithToken(cfg)
		case "--prune-posts":
			return pruneFromArgs(args[i+1:])
		case "-h", "--help":
			fmt.Print(usage)
			return nil
		default:
			return badUsage("unknown option %s", args[i])
		}
	}

	// The watch, and then the nightly update when it is owing. One agent rather than two,
	// because an agent that fires once a day skips the day on a Mac that was asleep at the
	// hour — and what the check starts is a process of its own, since an upgrade takes tens
	// of minutes and the check has five. A watch that failed still gets its update started:
	// the two are about different things.
	if !dry {
		if err := cfg.RememberHost(); err != nil {
			fmt.Fprintf(os.Stderr, "hachiko: the tailnet's name for this Mac could not be kept, so a check while tailscale is down falls back to the hostname: %v\n", err)
		}
	}

	err := watch.Run(cfg, dry)
	if nightly := update.Nightly(cfg, dry); err == nil {
		err = nightly
	}
	return err
}

// The flags the other `op run` child takes: the forum and how long a resolved post stays in
// it, neither of them a secret and neither of them anything an incident chose.
func pruneFromArgs(args []string) error {
	channel, keep := "", time.Duration(0)

	for i := 0; i+1 < len(args); i += 2 {
		switch args[i] {
		case "--channel":
			channel = args[i+1]
		case "--keep":
			seconds, err := strconv.ParseInt(args[i+1], 10, 64)
			if err != nil || seconds <= 0 {
				return badUsage("--keep takes a number of seconds")
			}
			keep = time.Duration(seconds) * time.Second
		default:
			return badUsage("unknown option %s", args[i])
		}
	}

	if keep == 0 {
		return badUsage("--prune-posts takes --keep")
	}
	return discord.PruneMode(channel, keep, time.Now())
}

// The flags the `op run` child is handed: a channel and a thread, which are ids in a URL
// rather than secrets, and whether this is the message that ends the incident. The thread
// it opened is the one thing it prints.
func sendFromStdin(args []string) error {
	out, channel := discord.Outgoing{}, ""

	for i := 0; i < len(args); i++ {
		// The one flag of the three that is a word on its own rather than a name and a value.
		if args[i] == "--outcome" {
			out.Outcome = true
			continue
		}
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
