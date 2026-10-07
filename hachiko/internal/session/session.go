// Package session is the two commands an on-call session runs for itself. Both take
// their text as a file rather than an argument, for the same reason: what a session
// writes about an incident names paths and command lines chosen by whatever filled the
// disk, and an argument is readable by every process on the Mac.
package session

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/logs"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// The on-call session's own way to reach the channel, and the only one it has: it is
// never handed the URL. The message arrives as a file so that it is not in the
// arguments of a process the whole machine can read either.
func Notify(cfg config.Config, incident, messageFile string, outcome bool) error {
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
	if err := store.MarkReported(incident, FallbackOption(string(message)), outcome); err != nil {
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

func FallbackOption(message string) string {
	matches := fallbackLine.FindAllStringSubmatch(message, -1)
	if len(matches) == 0 {
		return ""
	}
	return wording.Safe(matches[len(matches)-1][1], wording.FallbackLimit)
}

// The session's own way to say which single action it is asking to be allowed. The action
// arrives as a file for the same reason a report does: it names paths and commands chosen
// by whatever filled the disk, and an argument is readable by every process on the Mac.
func ApprovalRequest(cfg config.Config, incident, actionFile string) error {
	action, err := os.ReadFile(actionFile)
	if err != nil {
		return fmt.Errorf("cannot read the action at %s", actionFile)
	}

	store := statedir.Store{Dir: cfg.StateDir}
	if err := store.RequestApproval(incident, wording.Clip(strings.TrimSpace(string(action)), wording.ReplyLimit)); err != nil {
		return err
	}

	logs.Logger{Out: os.Stdout, Now: config.ClockFromEnv()}.Say(
		"%s is waiting for a code from Tim before it does what it registered", incident)
	return nil
}
