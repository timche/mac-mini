package harness

import (
	"fmt"
	"strings"
	"testing"
)

// HC is the fake hc CLI, answering in the shapes the real one does, so a regression is
// caught without a run that reaches the core and leaves a terminal in Tim's app. A refusal
// is answered the way the real runner hands one back: the JSON object hc prints on stderr,
// with no error of its own, so the kind in it is what decides.
type HC struct {
	Calls       []string
	HasSession  bool
	Unreachable bool

	// A send hc refuses because the agent is waiting on a question, and one it refuses for
	// anything else.
	BlockedSend bool
	SendFails   bool

	// A session there is nothing left of, an interrupt hc refuses outright, and an agent
	// that took the esc and stayed on its question anyway — which is the one failure a send
	// afterwards would be refused for.
	SessionGone bool
	EscRefused  bool
	EscIgnored  bool

	// A live terminal with nothing reporting a status in it, which is what `--keep` leaves
	// behind when the program it was started with died: hc puts this machine's shell there.
	ShellOnly bool

	// A first message hc had to type and could not, which is what a template run answers
	// with; a task in the argv is never one of those.
	FirstMessageLost bool

	// An agent goes on reporting `blocked` for a moment after the esc, because the key
	// arriving is not the agent acting on it. EscLag is how many status reads that takes
	// here; the real one was measured taking one.
	EscLag  int
	escSent bool
}

// Blocked for as long as the question is up, and working once an esc has taken it away,
// which is the transition every cancelled question turns on.
func (h *HC) status() string {
	if h.BlockedSend {
		return "blocked"
	}
	return "working"
}

func (h *HC) Run(args ...string) ([]byte, error) {
	line := strings.Join(args, " ")
	h.Calls = append(h.Calls, line)

	if h.Unreachable {
		return nil, fmt.Errorf("hc %s failed: no core is listening", line)
	}

	switch args[0] {
	case "get":
		if h.SessionGone || !h.HasSession {
			return []byte(`{"error":{"hint":"see ` + "`hc ls --all`" + `","kind":"not_found","message":"no terminal \"oncall-disk\""}}`), nil
		}
		if h.ShellOnly {
			return []byte(`{"address":"t7","argv":["/bin/zsh","-l"],"cwd":"/Users/x/.mac-mini","labels":{"herdr.space":"sp-mac-mini"},"name":"oncall-disk","reported":true,"state":"live","status":null,"turn":3}`), nil
		}
		// Read before the lag is counted down, so EscLag of one is one read that still says
		// blocked rather than none.
		status := h.status()
		if h.escSent && !h.EscIgnored && h.EscLag > 0 {
			h.EscLag--
			if h.EscLag == 0 {
				h.BlockedSend = false
			}
		}
		return []byte(fmt.Sprintf(`{"address":"t7","cwd":"/Users/x/.mac-mini","labels":{"herdr.space":"sp-mac-mini","herdr.tab":"tb-oncall-disk-1200"},"name":"oncall-disk","state":"live","status":{"app":"claude-code","state":%q},"turn":3}`,
			status)), nil

	case "interrupt":
		if h.EscRefused {
			return []byte(`{"error":{"hint":"hc read t7","kind":"finished","message":"t7 has finished"}}`), nil
		}
		h.escSent = true
		if !h.EscIgnored && h.EscLag == 0 {
			h.BlockedSend = false
		}
		return []byte(`{"key":"esc","outcome":"stopped"}`), nil

	case "run":
		// What hc answers a background run with: the terminal, and nothing about a first
		// message unless it had one of its own to type.
		if h.FirstMessageLost {
			return []byte(`{"address":"t7","agent":"claude","cwd":"/Users/x/.mac-mini","first_message":{"reason":"it never became ready","sent":false},"name":"oncall-disk","state":"live","turn":0}`), nil
		}
		h.HasSession = true
		return []byte(`{"address":"t7","agent":"claude","cwd":"/Users/x/.mac-mini","name":"oncall-disk","state":"live","turn":0}`), nil

	case "send":
		switch {
		case h.BlockedSend:
			// What hc answers when the agent is waiting on a question, which is exactly
			// where the standing orders leave an on-call session.
			return []byte(`{"error":{"hint":"typing would answer its dialog; read its screen first (hc read t7), or --force","kind":"blocked","message":"t7 is blocked (waiting)"}}`), nil
		case h.SendFails:
			return []byte(`{"error":{"hint":"hc read t7","kind":"finished","message":"t7 has finished"}}`), nil
		}
		return []byte(`{"started":true,"status":"idle","turn":4}`), nil
	}

	return nil, fmt.Errorf("the fake hc was asked something it does not answer: %s", line)
}

func (h *HC) Said(substring string) bool {
	for _, call := range h.Calls {
		if strings.Contains(call, substring) {
			return true
		}
	}
	return false
}

// Whether hc was asked to do this at all, by the command the call starts with: a prompt's
// own text quotes words like "send" and "run", so a substring is no test of that.
func (h *HC) Ran(command string) bool {
	for _, call := range h.Calls {
		if call == command || strings.HasPrefix(call, command+" ") {
			return true
		}
	}
	return false
}

func (h *HC) Count(substring string) int {
	n := 0
	for _, call := range h.Calls {
		n += strings.Count(call, substring)
	}
	return n
}

// The last call that carried a prompt, which is a send for a session that was already open
// and the run itself for one this call started: hc takes the first task as the program's
// own argument rather than typing it.
func (h *HC) Prompt() string {
	for i := len(h.Calls) - 1; i >= 0; i-- {
		if strings.HasPrefix(h.Calls[i], "send ") || strings.HasPrefix(h.Calls[i], "run ") {
			return h.Calls[i]
		}
	}
	return ""
}

func AssertOrder(t *testing.T, hc *HC, want ...string) {
	t.Helper()

	at := -1
	for _, call := range want {
		found := -1
		for i := at + 1; i < len(hc.Calls); i++ {
			if strings.Contains(hc.Calls[i], call) {
				found = i
				break
			}
		}
		if found < 0 {
			t.Fatalf("%q did not come after the call before it: %v", call, hc.Calls)
		}
		at = found
	}
}
