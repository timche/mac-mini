package listen

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/timche/mac-mini/mochi/internal/op"
	"github.com/timche/mac-mini/mochi/internal/self"
)

// A new binary is run in this same process rather than by exiting: the environment already
// holds the token and the arguments the re-entry under `op run` was given, so the new build
// needs no second `op run`, and nothing waits out launchd's throttle. Only when the exec
// fails does the listener exit for KeepAlive, as before.
type reexec struct {
	executable func() (string, error)
	exec       func(path string, argv, env []string) error

	// Asks the new binary whether it can start, before anything is given up for it; nil asks
	// nothing.
	preflight func(path string) error
}

var inPlace = reexec{executable: os.Executable, exec: syscall.Exec, preflight: checkConfig}

// errKept is a new binary that failed its preflight: this process keeps running, on the
// connection it has.
var errKept = errors.New("the new mochi binary failed its preflight, so this one keeps running")

// Discord's session is closed first, so the new process's connection is the only one.
func (r reexec) run(close func() error, log *slog.Logger) error {
	path, err := r.executable()
	if err == nil && r.preflight != nil {
		if perr := r.preflight(path); perr != nil {
			log.Error("the new mochi binary could not load its config, so this one keeps running until a build that can", "error", perr)
			return errKept
		}
	}
	if cerr := close(); cerr != nil {
		log.Warn("the Discord connection did not close cleanly before the new binary", "error", cerr)
	}
	if err == nil {
		log.Info("a new mochi binary is in place, so this process becomes it")
		err = r.exec(path, os.Args, os.Environ())
	}
	log.Error("the new binary could not be run in place, so this one exits for launchd to start it", "error", err)
	return self.ErrReplaced
}

// checkConfig runs the new binary's `version --check-config`, which reads the config the way
// a start does, without the token: a config the new build cannot read is a listener that
// would exit on start and leave nothing listening.
func checkConfig(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "version", "--check-config")
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, op.TokenVar+"=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
