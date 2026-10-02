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
	"strings"
	"time"
)

const usage = `usage: hachiko [--dry-run | --test-alert]
       hachiko notify <incident-id> <message-file>
       hachiko oncall <name> <brief-file>

  (no option)    check free space and what is burning CPU, and alert when it matters
  --dry-run      report what a check sees, change nothing, alert nothing
  --test-alert   send a short message to the webhook, to prove it works
  notify         send a message about an incident to the channel Tim watches,
                 which is how the on-call session reports its findings
  oncall         open a Claude Code session in herdr to work an incident, and
                 print the label of the tab it is waiting in
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
	cfg := configFromEnv()

	if len(args) > 0 {
		switch args[0] {
		case "notify":
			if len(args) != 3 {
				return badUsage("notify takes an incident id and a file")
			}
			return notify(cfg, args[1], args[2])
		case "oncall":
			if len(args) != 3 {
				return badUsage("oncall takes a name and a file")
			}
			return oncall(cfg, args[1], args[2])
		}
	}

	dry := false
	for _, arg := range args {
		switch arg {
		case "--dry-run":
			dry = true
		case "--test-alert":
			return testAlert(cfg)
		// Left out of the usage: this is how the one step that holds the webhook URL
		// is re-entered under `op run`, and nothing else should call it.
		case "--send":
			return sendMode(os.Stdin)
		case "-h", "--help":
			fmt.Print(usage)
			return nil
		default:
			return badUsage("unknown option %s", arg)
		}
	}

	deps := realDeps(cfg)
	return sweeper{cfg: cfg, deps: deps, store: Store{dir: cfg.StateDir}, dry: dry}.run()
}

// The on-call session's own way to reach the channel, and the only one it has: it is
// never handed the URL. The message arrives as a file so that it is not in the
// arguments of a process the whole machine can read either.
func notify(cfg Config, incident, messageFile string) error {
	message, err := os.ReadFile(messageFile)
	if err != nil {
		return fmt.Errorf("cannot read the message at %s", messageFile)
	}
	if len(strings.TrimSpace(string(message))) == 0 {
		return fmt.Errorf("%s is empty, so there is nothing to send", messageFile)
	}

	if err := sendThroughOP(cfg, string(message)); err != nil {
		return err
	}

	store := Store{dir: cfg.StateDir}
	now := clockFromEnv()

	// The send is what the deadline was waiting for, so the incident stops being
	// pending even if the state cannot be written: a duplicate line in the log is
	// cheaper than a second message saying the agent went quiet.
	if err := store.Update(5*time.Minute, now, func(state *State) {
		delete(state.Pending, incident)
	}); err != nil {
		fmt.Fprintf(os.Stdout, "%s hachiko: reported on %s, but the state was not updated: %v\n",
			now().Format("2006-01-02T15:04:05-0700"), incident, err)
		return nil
	}

	fmt.Fprintf(os.Stdout, "%s hachiko: the on-call session reported on %s\n",
		now().Format("2006-01-02T15:04:05-0700"), incident)
	return nil
}

func oncall(cfg Config, name, briefFile string) error {
	brief, err := os.ReadFile(briefFile)
	if err != nil {
		return fmt.Errorf("cannot read the brief at %s", briefFile)
	}

	label, err := openOncall(cfg, herdrCLI, name, string(brief))
	if err != nil {
		return err
	}

	fmt.Println(label)
	return nil
}

func testAlert(cfg Config) error {
	free, err := freeKB(cfg.Home)
	if err != nil {
		return err
	}

	message := fmt.Sprintf("hachiko on %s: test alert, %s GB free. Nothing is wrong.", cfg.Host, gbStr(free))
	if err := sendThroughOP(cfg, message); err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "%s hachiko: sent a test alert\n", time.Now().Format("2006-01-02T15:04:05-0700"))
	return nil
}
