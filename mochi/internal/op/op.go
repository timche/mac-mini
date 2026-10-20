// Package op is how every mochi command that needs the bot token gets it: the binary runs
// itself again under `op run`, so the token is in one process's environment and in no
// argument, file or log.
package op

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// The flag the re-entered process is started with. Not in the usage: nothing else should
// call it.
const WithToken = "--with-token"

// Replacing this process rather than starting a child, so a long-running listener is one
// process in every listing and the one launchd tracks.
func Reenter(envFile string, args []string) error {
	if _, err := os.Stat(envFile); err != nil {
		return fmt.Errorf("%s is missing, so there is no bot token to use", envFile)
	}
	op, err := exec.LookPath("op")
	if err != nil {
		return errors.New("no op on PATH, so the bot token cannot be resolved")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	argv := append([]string{"op", "run", "--env-file", envFile, "--", self, WithToken}, args...)
	return syscall.Exec(op, argv, os.Environ())
}
