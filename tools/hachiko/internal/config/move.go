package config

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// The variables that name each state directory, for a test, a fixture or a second hachiko
// that must not read the live one. Each is also what turns its own folder's one-time move
// off, so a run told where to keep its state neither reads nor empties what the agents are
// using.
const (
	StateDirVar       = "HACHIKO_STATE_DIR"
	GCStateDirVar     = "HACHIKO_GC_STATE_DIR"
	UpdateStateDirVar = "HACHIKO_UPDATE_STATE_DIR"
	SyncStateDirVar   = "HACHIKO_SYNC_STATE_DIR"
)

// The four folders hachiko recorded itself in beside its binary, and everything in each that
// is hachiko's own record rather than a build's. The build output the watch's folder also
// held — the binary, its hash, the build log and a worktree's folder of both — is the
// wrapper's and stays where it is, which is what leaves ~/Library/Caches/hachiko holding a
// cache and nothing else. Each list leaves out the locks and the temporary files a whole
// write makes: a lock says nothing, and taking one over would swap the file under a process
// holding it.
type moveSpec struct {
	Var   string
	Old   string
	New   string
	Names []string
}

var moves = []moveSpec{
	{
		Var: StateDirVar, Old: "hachiko", New: "watch",
		Names: []string{
			"state.json", "host", "update-raised.json",
			"reported", "approvals", "listen",
		},
	},
	{Var: GCStateDirVar, Old: "hachiko-gc", New: "gc", Names: []string{"state.json", "last-run"}},
	{
		Var: UpdateStateDirVar, Old: "hachiko-update", New: "update",
		Names: []string{"state.json", "last-run", "failing.json"},
	},
	{Var: SyncStateDirVar, Old: "hachiko-sync", New: "sync", Names: []string{"state.json", "heartbeat"}},
}

// The cache the four used to be under, worked out the way they were: ~/Library/Caches on a
// Mac, and the directory the rest of the world uses on anything else.
func oldRoot(home string) string {
	if _, err := os.Stat(filepath.Join(home, "Library", "Caches")); err == nil {
		return filepath.Join(home, "Library", "Caches")
	}
	return filepath.Join(home, ".cache")
}

// MoveState brings what hachiko records about itself over from the build cache it used to
// keep it in, on the way into any command. Six of hachiko's processes run at once — the
// five-minute watch, the sweep, sync, the listener, the nightly update and an on-call
// session's own commands — and any of them can be the one that starts first, so the move
// happens under a lock of its own and a start that finds nothing left to move takes no lock
// at all. Nothing already in the new folder is written over, that being what hachiko has
// been using, and a failure leaves it reading the new folders regardless: one line said
// about it is the whole of the handling, since a watch that stops watching over a file it
// could not rename is worse than a watch with a gap in its record.
func MoveState() error {
	home := homeDir()
	root := stateRoot(home)

	left := pending(home)
	if len(left) == 0 {
		return nil
	}

	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(root, "move.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	var errs []error
	for _, m := range left {
		from, to := filepath.Join(oldRoot(home), m.Old), filepath.Join(root, m.New)
		if err := os.MkdirAll(to, 0o755); err != nil {
			errs = append(errs, err)
			continue
		}
		for _, name := range m.Names {
			errs = append(errs, move(filepath.Join(from, name), filepath.Join(to, name)))
		}
	}
	return errors.Join(errs...)
}

// Which of the four still have something of hachiko's in the old folder, read before the
// lock is taken: on all but the one run that does the move, this is four stats per folder
// and nothing else.
func pending(home string) []moveSpec {
	var left []moveSpec
	for _, m := range moves {
		if os.Getenv(m.Var) != "" {
			continue
		}
		from := filepath.Join(oldRoot(home), m.Old)
		to := filepath.Join(stateRoot(home), m.New)
		if from == to {
			continue
		}
		for _, name := range m.Names {
			if _, err := os.Lstat(filepath.Join(from, name)); err == nil {
				left = append(left, m)
				break
			}
		}
	}
	return left
}

// One file or folder over, by rename, both sides being in HOME and so on one volume. A
// folder the new side also has is merged entry by entry, since what the listener has seen
// and what has been reported since hachiko first read the new folder are in it.
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
