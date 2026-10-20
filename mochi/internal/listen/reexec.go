package listen

import (
	"log/slog"
	"os"
	"syscall"

	"github.com/timche/mac-mini/mochi/internal/self"
)

// A new binary is run in this same process rather than by exiting: the environment already
// holds the token and the arguments the re-entry under `op run` was given, so the new build
// needs no second `op run`, and nothing waits out launchd's throttle. Only when the exec
// fails does the listener exit for KeepAlive, as before.
type reexec struct {
	executable func() (string, error)
	exec       func(path string, argv, env []string) error
}

var inPlace = reexec{executable: os.Executable, exec: syscall.Exec}

// Discord's session is closed first, so the new process's connection is the only one.
func (r reexec) run(close func() error, log *slog.Logger) error {
	if err := close(); err != nil {
		log.Warn("the Discord connection did not close cleanly before the new binary", "error", err)
	}
	path, err := r.executable()
	if err == nil {
		log.Info("a new mochi binary is in place, so this process becomes it")
		err = r.exec(path, os.Args, os.Environ())
	}
	log.Error("the new binary could not be run in place, so this one exits for launchd to start it", "error", err)
	return self.ErrReplaced
}
