// Package hook is the PreToolUse event in and the decision out.
//
// Everything about how it answers follows from where it runs: a hook on the Bash tool of
// every session on this Mac. A deny is a JSON decision on stdout and a status of 0; an
// allow is nothing at all, so a session's own shell output is never joined by a line from
// here. An event it cannot read is an allow too, and so is a panic, because the one thing
// this must never do is refuse a command for a reason that is its own bug: it exits 2 for
// nothing, 2 being what tells Claude Code to block.
package hook

import (
	"encoding/json"
	"io"
)

type event struct {
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

type decision struct {
	Output struct {
		EventName string `json:"hookEventName"`
		Decision  string `json:"permissionDecision"`
		Reason    string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

// A check of the command a session is about to run, and the reason it gives for refusing
// it.
type Check struct {
	Denies func(command string) bool
	Reason string
}

// PreToolUse runs each check over the command the event carries, in order, and writes the
// decision of the first that refuses.
func PreToolUse(in io.Reader, out io.Writer, checks []Check) (err error) {
	defer func() {
		if recover() != nil {
			err = nil
		}
	}()

	body, err := io.ReadAll(in)
	if err != nil {
		return nil
	}
	var ev event
	if json.Unmarshal(body, &ev) != nil || ev.ToolInput.Command == "" {
		return nil
	}

	for _, check := range checks {
		if !check.Denies(ev.ToolInput.Command) {
			continue
		}
		answer := decision{}
		answer.Output.EventName = "PreToolUse"
		answer.Output.Decision = "deny"
		answer.Output.Reason = check.Reason
		// A reason is read by a person as often as by a model, and `kill <pid>` in it is
		// not HTML.
		encoder := json.NewEncoder(out)
		encoder.SetEscapeHTML(false)
		return encoder.Encode(answer)
	}
	return nil
}
