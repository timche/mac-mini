package hook

import (
	"encoding/json"
	"strings"
	"testing"
)

var checks = []Check{
	{Denies: func(command string) bool { return strings.Contains(command, "first") }, Reason: "the first"},
	{Denies: func(command string) bool { return strings.Contains(command, "second") }, Reason: "the second"},
}

func run(t *testing.T, in string) string {
	t.Helper()
	out := &strings.Builder{}
	if err := PreToolUse(strings.NewReader(in), out, checks); err != nil {
		t.Fatalf("the event could not be answered: %v", err)
	}
	return out.String()
}

func eventJSON(command string) string {
	body, err := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": command},
	})
	if err != nil {
		panic(err)
	}
	return string(body)
}

func TestARefusalIsADenyDecisionWithTheCheckSReason(t *testing.T) {
	var answer struct {
		Output struct {
			EventName string `json:"hookEventName"`
			Decision  string `json:"permissionDecision"`
			Reason    string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(run(t, eventJSON("the second one"))), &answer); err != nil {
		t.Fatalf("the decision is not JSON: %v", err)
	}
	if answer.Output.EventName != "PreToolUse" {
		t.Errorf("the decision names %q and not the event", answer.Output.EventName)
	}
	if answer.Output.Decision != "deny" {
		t.Errorf("the decision is %q and not a deny", answer.Output.Decision)
	}
	if answer.Output.Reason != "the second" {
		t.Errorf("the reason is %q and not the refusing check's", answer.Output.Reason)
	}
}

// The order the checks are given in, so a command that is both is refused for the first
// reason rather than for a mix of the two.
func TestTheFirstCheckToRefuseGivesTheReason(t *testing.T) {
	out := run(t, eventJSON("the first and the second"))
	if !strings.Contains(out, `"the first"`) {
		t.Errorf("the decision is %s and not the first check's", out)
	}
}

// A session's own shell output is never joined by a line from here.
func TestAnAllowedCommandAndAnUnreadableEventSayNothing(t *testing.T) {
	for _, in := range []string{
		eventJSON("ls -la"),
		eventJSON(""),
		`{"tool_input": {}}`,
		`{"tool_input": "a string"}`,
		`{}`,
		"not json",
		"",
	} {
		if out := run(t, in); out != "" {
			t.Errorf("%q was answered with %q rather than with nothing", in, out)
		}
	}
}
