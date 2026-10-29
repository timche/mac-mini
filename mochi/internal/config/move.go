package config

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// StateDirVar names the folder mochi records itself in, for a test or a second listener that
// must not read the live one.
const StateDirVar = "MOCHI_STATE_DIR"

// Everything mochi writes about itself, which is what MoveState carries over and what a
// folder of mochi's own holds: the build output beside the old ones — the binary, its hash,
// the build log, a worktree's folder — is the wrapper's and is never touched. The marks'
// lock is left out: a lock file says nothing, and taking it over would swap the file under a
// process holding it.
var stateNames = []string{
	"state.json", "marks.json", "waits.json", "pending.json", "prs.json",
	"heartbeat", "identify", "op-attempt", "events", "sessions",
}

func oldStateDir(home string) string {
	return filepath.Join(home, "Library", "Caches", "mochi")
}

// MoveState brings mochi's own files over from the build cache they used to share, on the way
// into any command, which is the only moment no part of mochi is reading them: the listener
// and a session's `mochi post` or `mochi session-event` start at any time, so the move happens
// under a lock of its own and a start that finds nothing left to move takes no lock at all.
// Nothing already in the new folder is written over — that is what mochi has been using — and
// a failure leaves mochi reading the new folder regardless.
func MoveState() error {
	if os.Getenv(StateDirVar) != "" {
		return nil
	}
	home := homeDir()
	old, now := oldStateDir(home), Dir(home)
	if old == now || !anyLeft(old, now) {
		return nil
	}
	if err := os.MkdirAll(now, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(now, "move.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	var errs []error
	for _, name := range stateNames {
		errs = append(errs, move(filepath.Join(old, name), filepath.Join(now, name)))
	}
	return errors.Join(errs...)
}

func anyLeft(old, now string) bool {
	for _, name := range stateNames {
		if _, err := os.Lstat(filepath.Join(old, name)); err == nil {
			return true
		}
	}
	return false
}

// One file or folder over, by rename since both are in HOME and so on one volume. A folder
// the new side also has is merged entry by entry, since the events written and the
// attachments downloaded since mochi first read the new folder are in it.
func move(from, to string) error {
	f, err := os.Lstat(from)
	if err != nil {
		return nil
	}
	t, err := os.Lstat(to)
	if err != nil {
		return os.Rename(from, to)
	}
	if !f.IsDir() || !t.IsDir() {
		return nil
	}
	entries, err := os.ReadDir(from)
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		errs = append(errs, move(filepath.Join(from, e.Name()), filepath.Join(to, e.Name())))
	}
	// Goes only when the merge took all of it.
	os.Remove(from)
	return errors.Join(errs...)
}
