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
		EventName    string                     `json:"hookEventName"`
		Decision     string                     `json:"permissionDecision"`
		Reason       string                     `json:"permissionDecisionReason"`
		UpdatedInput map[string]json.RawMessage `json:"updatedInput,omitempty"`
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
		return write(out, answer)
	}
	return nil
}

// What a hook that may change the command answers with: a refusal and its reason, a
// command that takes the place of the one the event carried, or neither, which lets the
// command run as it was written.
type Answer struct {
	Deny    bool
	Reason  string
	Command string
}

// The event as a hook that rewrites reads it: the whole tool input, since what it answers
// with is that input back with one field changed, and Claude Code runs what it is handed.
type input struct {
	ToolInput map[string]json.RawMessage `json:"tool_input"`
}

// PreToolUseRewrite hands the command the event carries to read and writes what it
// answers: a deny, or an allow carrying the tool input with the new command in it.
func PreToolUseRewrite(in io.Reader, out io.Writer, read func(command string) Answer) (err error) {
	defer func() {
		if recover() != nil {
			err = nil
		}
	}()

	body, err := io.ReadAll(in)
	if err != nil {
		return nil
	}
	var ev input
	if json.Unmarshal(body, &ev) != nil {
		return nil
	}
	var command string
	if json.Unmarshal(ev.ToolInput["command"], &command) != nil || command == "" {
		return nil
	}

	answer := read(command)
	decided := decision{}
	decided.Output.EventName = "PreToolUse"
	decided.Output.Reason = answer.Reason
	switch {
	case answer.Deny:
		decided.Output.Decision = "deny"
	case answer.Command != "":
		decided.Output.Decision = "allow"
		replaced, err := json.Marshal(answer.Command)
		if err != nil {
			return nil
		}
		updated := make(map[string]json.RawMessage, len(ev.ToolInput))
		for key, value := range ev.ToolInput {
			updated[key] = value
		}
		updated["command"] = replaced
		decided.Output.UpdatedInput = updated
	default:
		return nil
	}
	return write(out, decided)
}

// A reason is read by a person as often as by a model, and `kill <pid>` in it is not HTML.
func write(out io.Writer, answer decision) error {
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(answer)
}
