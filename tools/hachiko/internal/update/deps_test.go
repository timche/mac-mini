package update

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// A stand-in `hc` whose `run --bg ... -- argv` starts argv in the background and returns at
// once, the way the real one hands the program to a terminal. It records its arguments.
func fakeTerminal(t *testing.T, tool string) string {
	t.Helper()
	bin := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("hc", `echo "$@" >"$(dirname "$0")/hc.args"
while [ "$1" != "--" ]; do shift; done; shift
"$@" </dev/null >/dev/null 2>&1 &
`)
	write("tool", tool)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))

	old := terminalPoll
	terminalPoll = 10 * time.Millisecond
	t.Cleanup(func() { terminalPoll = old })
	return bin
}

func TestACommandInATerminalReportsWhatItSaidAndHowItEnded(t *testing.T) {
	bin := fakeTerminal(t, "sleep 0.2; echo moved; echo warned >&2\n")

	out := run(Command{Name: "tool", Args: []string{"update"}, Timeout: 5 * time.Second, InTerminal: true})

	harness.Equal(t, out.OK, true, "ok")
	harness.Wants(t, out.Text, "moved")
	harness.Wants(t, out.Text, "warned")

	args, _ := os.ReadFile(filepath.Join(bin, "hc.args"))
	harness.Wants(t, string(args), "run --bg --no-parent --no-notify --rm -- /bin/sh -c")
	harness.Wants(t, string(args), filepath.Join(bin, "tool")+" update")
}

func TestACommandThatFailsInATerminalFails(t *testing.T) {
	fakeTerminal(t, "echo 'isn'\\''t writable by you'; exit 3\n")

	out := run(Command{Name: "tool", Timeout: 5 * time.Second, InTerminal: true})

	harness.Equal(t, out.OK, false, "ok")
	harness.Wants(t, out.Text, "isn't writable by you")
}

func TestACommandThatNeverFinishesInATerminalTimesOut(t *testing.T) {
	fakeTerminal(t, "sleep 5\n")

	out := run(Command{Name: "tool", Timeout: 100 * time.Millisecond, InTerminal: true})

	harness.Equal(t, out.OK, false, "ok")
	harness.Wants(t, out.Text, "had not finished in its hc terminal")
}

func TestATerminalThatCannotStartFails(t *testing.T) {
	bin := fakeTerminal(t, "exit 0\n")
	if err := os.WriteFile(filepath.Join(bin, "hc"), []byte("#!/bin/sh\necho 'no core'; exit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	out := run(Command{Name: "tool", Timeout: 5 * time.Second, InTerminal: true})

	harness.Equal(t, out.OK, false, "ok")
	harness.Wants(t, out.Text, "no core")
}
