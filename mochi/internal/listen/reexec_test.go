package listen

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

// A new build that cannot read the config would exit on start and leave nothing listening:
// this process keeps its connection and goes on.
func TestABuildThatFailsItsPreflightIsNotExeced(t *testing.T) {
	var order []string
	r := reexec{
		executable: func() (string, error) { return "/cache/mochi", nil },
		exec:       func(string, []string, []string) error { order = append(order, "exec"); return nil },
		preflight:  func(path string) error { order = append(order, "preflight "+path); return errors.New("no config.json") },
	}
	err := r.run(func() error { order = append(order, "close"); return nil }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !errors.Is(err, errKept) || !reflect.DeepEqual(order, []string{"preflight /cache/mochi"}) {
		t.Errorf("err = %v, order = %v", err, order)
	}
}

// The preflight is the new binary's own `version --check-config`, run without the token.
func TestThePreflightRunsTheNewBinaryWithoutTheToken(t *testing.T) {
	t.Setenv("MOCHI_DISCORD_BOT_TOKEN", "secret")
	dir := t.TempDir()
	bin := filepath.Join(dir, "mochi")
	os.WriteFile(bin, []byte("#!/bin/sh\n[ \"$*\" = \"version --check-config\" ] && [ -z \"$MOCHI_DISCORD_BOT_TOKEN\" ] || { echo \"argv $* token ${MOCHI_DISCORD_BOT_TOKEN:-none}\"; exit 1; }\n[ -e \"$(dirname \"$0\")/broken\" ] && { echo \"config.json: no such file\"; exit 1; }\nexit 0\n"), 0o755)

	if err := checkConfig(bin); err != nil {
		t.Errorf("a binary that starts failed its preflight: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "broken"), nil, 0o600)
	if err := checkConfig(bin); err == nil || !strings.Contains(err.Error(), "config.json: no such file") {
		t.Errorf("err = %v", err)
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
