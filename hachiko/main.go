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
	"regexp"
	"strings"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/logs"
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
			return notify(cfg, rest[0], rest[1], outcome)
		case "oncall":
			if len(args) != 3 {
				return badUsage("oncall takes a name and a file")
			}
			return oncall(cfg, args[1], args[2])
		case "approval-request":
			if len(args) != 3 {
				return badUsage("approval-request takes an incident id and a file")
			}
			return approvalRequest(cfg, args[1], args[2])
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

// The on-call session's own way to reach the channel, and the only one it has: it is
// never handed the URL. The message arrives as a file so that it is not in the
// arguments of a process the whole machine can read either.
func notify(cfg config.Config, incident, messageFile string, outcome bool) error {
	message, err := os.ReadFile(messageFile)
	if err != nil {
		return fmt.Errorf("cannot read the message at %s", messageFile)
	}
	if len(strings.TrimSpace(string(message))) == 0 {
		return fmt.Errorf("%s is empty, so there is nothing to send", messageFile)
	}

	store := statedir.Store{Dir: cfg.StateDir}
	log := logs.Logger{Out: os.Stdout, Now: config.ClockFromEnv()}

	// Into the incident's own thread when there is one, so the analysis is under the alert
	// it is about and Tim's reply to it is somewhere the listener is already watching. Read
	// without the lock: a sweep holds that lock across a herdr call and an `op run`, and
	// this is one field of a map the sweep alone writes.
	out := discord.Outgoing{Text: string(message)}
	if state, err := store.Load(); err == nil {
		out.Thread = state.Threads[incident]
	}

	if _, err := discord.SendThroughOP(cfg, out); err != nil {
		return err
	}

	// A marker rather than an edit to the state, and so no lock: a sweep holds that lock
	// across a herdr call and an `op run`, and an on-call session told to report in
	// under five minutes has none of them to spend waiting. The next sweep is what
	// clears the incident, and it looks here first.
	if err := store.MarkReported(incident, fallbackOption(string(message)), outcome); err != nil {
		log.Say("the report on %s was sent, but it was not recorded, so the raw details may follow it: %v",
			incident, err)
		return nil
	}

	if outcome {
		log.Say("the on-call session reported the outcome of %s", incident)
		return nil
	}

	log.Say("the on-call session reported on %s", incident)
	return nil
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

// What the session says it would do if nobody answers, read out of the report rather
// than taken as a flag of its own. The line is one Tim reads too, so stating it to him
// and stating it to hachiko are the same act — and nothing the agent writes about an
// incident goes in a command line, where the whole machine reads it and a path chosen
// by whatever filled the disk would be an argument.
// A line of its own, which is what the orders ask for: only what a model puts in front of
// one is allowed before it — a dash, a bullet, a quote marker, the emphasis it reaches for —
// and nothing else. Anything looser matched a log line the session had quoted into the
// middle of a sentence, and the option hachiko then held out to Tim was a string chosen by
// whatever filled the disk.
//
// The last one wins, because a session that quotes an earlier message of its own, or a log
// line on a line of its own, has the line it means last.
var fallbackLine = regexp.MustCompile(`(?mi)^[-*>+\t ]*(?:\*\*)?[ \t]*if no answer:\**[ \t]*([^\n]+?)[ \t*]*$`)

func fallbackOption(message string) string {
	matches := fallbackLine.FindAllStringSubmatch(message, -1)
	if len(matches) == 0 {
		return ""
	}
	return wording.Safe(matches[len(matches)-1][1], wording.FallbackLimit)
}

func oncall(cfg config.Config, name, briefFile string) error {
	brief, err := os.ReadFile(briefFile)
	if err != nil {
		return fmt.Errorf("cannot read the brief at %s", briefFile)
	}

	// The label goes to stdout and nothing else does, because the caller reads it to
	// name the tab in the message it is about to send.
	session, err := openOncall(cfg, herdrCLI, name, string(brief))
	if err != nil {
		return err
	}

	fmt.Println(session.Tab)
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
