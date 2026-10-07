package harness

import (
	"fmt"
	"strings"
	"testing"
)

// Herdr is the fake herdr CLI, answering in the shapes the real one does, so a regression
// is caught without a run that reaches the server and leaves a tab in somebody's
// workspace. A refusal is answered the way the real runner hands one back: the JSON object
// herdr prints on stderr, with no error of its own, so the code in it is what decides.
type Herdr struct {
	Calls         []string
	HasAgent      bool
	HasWorkspace  bool
	Unreachable   bool
	BlockedPrompt bool
	PromptFails   bool

	// An agent herdr has never heard of, a send-keys it refuses outright, and a pane that
	// took the esc and stayed on its question anyway — which is the one failure a prompt
	// sent afterwards would be refused for.
	AgentGone  bool
	EscRefused bool
	EscIgnored bool

	// herdr goes on calling the agent `blocked` for a moment after the esc, because
	// send-keys answers for the keys arriving and the agent's own status follows. EscLag is
	// how many status reads that takes here; the real herdr was measured taking one.
	EscLag  int
	escSent bool
}

// Blocked for as long as the question is up, and working once an esc has taken it away,
// which is the transition every cancelled question turns on.
func (h *Herdr) status() string {
	if h.BlockedPrompt {
		return "blocked"
	}
	return "working"
}

func (h *Herdr) Run(args ...string) ([]byte, error) {
	line := strings.Join(args, " ")
	h.Calls = append(h.Calls, line)

	if h.Unreachable {
		return nil, fmt.Errorf("herdr %s failed: no server is listening", line)
	}

	switch args[0] + " " + args[1] {
	case "agent get":
		if h.AgentGone {
			return []byte(`{"error":{"code":"agent_not_found","message":"agent target oncall-disk not found"},"id":"cli:agent:get"}`), nil
		}
		// Read before the lag is counted down, so EscLag of one is one read that still says
		// blocked rather than none.
		status := h.status()
		if h.escSent && !h.EscIgnored && h.EscLag > 0 {
			h.EscLag--
			if h.EscLag == 0 {
				h.BlockedPrompt = false
			}
		}
		return []byte(fmt.Sprintf(`{"result":{"agent":{"agent":"claude","agent_status":%q,"name":"oncall-disk","pane_id":"w3:pB","tab_id":"w3:t9"},"type":"agent_info"}}`,
			status)), nil

	case "agent send-keys":
		if h.EscRefused {
			return []byte(`{"error":{"code":"pane_not_found","message":"pane w3:pB not found"},"id":"cli:agent:send-keys"}`), nil
		}
		h.escSent = true
		if !h.EscIgnored && h.EscLag == 0 {
			h.BlockedPrompt = false
		}
		return []byte(`{"result":{"type":"keys_sent"}}`), nil
	}

	switch args[0] + " " + args[1] {
	case "agent list":
		if h.HasAgent {
			return []byte(`{"result":{"agents":[{"agent":"claude","agent_status":"blocked","name":"oncall-disk","pane_id":"w3:p9","tab_id":"w3:t9","workspace_id":"w3"}],"type":"agent_list"}}`), nil
		}
		return []byte(`{"result":{"agents":[{"agent":"claude","agent_status":"idle","pane_id":"w2:p1","tab_id":"w2:t1","workspace_id":"w2"}],"type":"agent_list"}}`), nil

	case "workspace list":
		if h.HasWorkspace {
			return []byte(`{"result":{"type":"workspace_list","workspaces":[{"label":"meru","number":1,"workspace_id":"w2"},{"label":".mac-mini","number":2,"workspace_id":"w3"}]}}`), nil
		}
		return []byte(`{"result":{"type":"workspace_list","workspaces":[{"label":"meru","number":1,"workspace_id":"w2"}]}}`), nil

	case "workspace create":
		return []byte(`{"result":{"root_pane":{"pane_id":"w3:p1"},"tab":{"tab_id":"w3:t1"},"type":"workspace_created","workspace":{"label":".mac-mini","workspace_id":"w3"}}}`), nil

	case "tab create":
		return []byte(`{"result":{"root_pane":{"pane_id":"w3:p9"},"tab":{"label":"disk-0000","tab_id":"w3:t9"},"type":"tab_created"}}`), nil

	case "tab get":
		return []byte(`{"result":{"tab":{"label":"disk-1200","tab_id":"w3:t9"},"type":"tab"}}`), nil

	case "agent start":
		return []byte(`{"result":{"agent":{"name":"oncall-disk"},"type":"agent_started"}}`), nil

	case "agent prompt":
		switch {
		case h.BlockedPrompt:
			// What herdr answers when the agent is already waiting on a question, which is
			// exactly where the standing orders leave an on-call session.
			return []byte(`{"error":{"code":"agent_blocked","message":"agent oncall-disk is blocked"},"id":"cli:agent:prompt"}`), nil
		case h.PromptFails:
			return []byte(`{"error":{"code":"agent_prompt_stalled","message":"agent oncall-disk did not reach a working state"},"id":"cli:agent:prompt"}`), nil
		}
		return []byte(`{"result":{"type":"agent_prompted"}}`), nil
	}

	return nil, fmt.Errorf("the fake herdr was asked something it does not answer: %s", line)
}

func (h *Herdr) Said(substring string) bool {
	for _, call := range h.Calls {
		if strings.Contains(call, substring) {
			return true
		}
	}
	return false
}

func (h *Herdr) Count(substring string) int {
	n := 0
	for _, call := range h.Calls {
		n += strings.Count(call, substring)
	}
	return n
}

func (h *Herdr) Prompt() string {
	for i := len(h.Calls) - 1; i >= 0; i-- {
		if strings.HasPrefix(h.Calls[i], "agent prompt ") {
			return h.Calls[i]
		}
	}
	return ""
}

func AssertOrder(t *testing.T, herdr *Herdr, want ...string) {
	t.Helper()

	at := -1
	for _, call := range want {
		found := -1
		for i := at + 1; i < len(herdr.Calls); i++ {
			if strings.Contains(herdr.Calls[i], call) {
				found = i
				break
			}
		}
		if found < 0 {
			t.Fatalf("%q did not come after the call before it: %v", call, herdr.Calls)
		}
		at = found
	}
}
