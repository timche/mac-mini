package listen

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"reflect"
	"testing"

	"github.com/timche/mac-mini/mochi/internal/self"
)

func TestANewBinaryReplacesThisProcessAfterDiscordCloses(t *testing.T) {
	t.Setenv("MOCHI_DISCORD_BOT_TOKEN", "carried-over")
	var order []string
	var gotPath string
	var gotArgv, gotEnv []string
	r := reexec{
		executable: func() (string, error) { return "/cache/mochi", nil },
		exec: func(path string, argv, env []string) error {
			order = append(order, "exec")
			gotPath, gotArgv, gotEnv = path, argv, env
			return errors.New("exec format error")
		},
	}
	closed := func() error { order = append(order, "close"); return nil }

	err := r.run(closed, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if !reflect.DeepEqual(order, []string{"close", "exec"}) {
		t.Errorf("order = %v", order)
	}
	if gotPath != "/cache/mochi" || !reflect.DeepEqual(gotArgv, os.Args) {
		t.Errorf("exec %s %v", gotPath, gotArgv)
	}
	carried := false
	for _, kv := range gotEnv {
		carried = carried || kv == "MOCHI_DISCORD_BOT_TOKEN=carried-over"
	}
	if !carried {
		t.Error("the token was not carried into the new binary, which would need a second op run")
	}
	if !errors.Is(err, self.ErrReplaced) {
		t.Errorf("a failed exec returned %v rather than exiting for KeepAlive", err)
	}
}

func TestABinaryThatCannotBeFoundExitsForKeepAlive(t *testing.T) {
	execed := false
	r := reexec{
		executable: func() (string, error) { return "", errors.New("gone") },
		exec:       func(string, []string, []string) error { execed = true; return nil },
	}
	if err := r.run(func() error { return nil }, slog.New(slog.NewTextHandler(io.Discard, nil))); !errors.Is(err, self.ErrReplaced) || execed {
		t.Errorf("err = %v, execed = %v", err, execed)
	}
}
