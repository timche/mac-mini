// Package self is the one thing hachiko asks about the file behind its own process:
// whether the wrapper has moved a new build into place over it. Two commands never finish
// a run — `hachiko sync` and `hachiko listen` — and neither has an interval of its own, so
// without this nothing would run a binary built this morning until the Mac restarted.
//
// Not part of `internal/process`, which is what hachiko learns about everything else
// running on this Mac and the one place it runs a subprocess: this needs no ps, no lsof and
// no child, and the listener has no use for any of those.
package self

import (
	"errors"
	"os"
	"syscall"
)

// What both agents exit with. Non-zero, because the sync agent is restarted on a failure
// alone and a daemon that exited cleanly would stay exited.
var ErrReplaced = errors.New("the hachiko binary behind this process has been replaced, so this one exits for the new one")

// Enough of a stat to tell one file from another at the same path, and nothing that moves
// on its own: a rebuild is a new inode, and a touch is a new modification time.
type ID struct {
	Dev, Inode uint64
	ModUnix    int64
	Size       int64
}

// The binary rather than the wrapper: the wrapper's job is to decide which binary runs, and
// this one is already running.
func Stat() (ID, bool) {
	path, err := os.Executable()
	if err != nil {
		return ID{}, false
	}
	info, err := os.Stat(path)
	if err != nil {
		return ID{}, false
	}
	return idOf(info), true
}

// The device and inode first, because the wrapper installs a build with a rename and a
// rename is a new inode at the same path. The size and the time are there for the case the
// inode is reused, which an inode a build has just freed readily is.
func idOf(info os.FileInfo) ID {
	id := ID{ModUnix: info.ModTime().Unix(), Size: info.Size()}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		id.Dev, id.Inode = uint64(st.Dev), st.Ino
	}
	return id
}

// Watch is the binary as it was when the agent started, and the one question asked of it
// afterwards.
type Watch struct {
	look  func() (ID, bool)
	was   ID
	known bool
}

// The baseline is taken here rather than on the first question, so an agent that starts one
// of these and only asks half an hour later is still comparing against the binary it is
// actually running.
func Looking(look func() (ID, bool)) *Watch {
	w := &Watch{look: look}
	w.was, w.known = look()
	return w
}

// False whenever either reading failed: a binary nothing can stat is a question that says
// nothing, rather than an agent that restarts every half minute for ever.
func (w *Watch) Replaced() bool {
	now, ok := w.look()
	return w.known && ok && now != w.was
}
