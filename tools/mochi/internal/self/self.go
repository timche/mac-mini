// Package self asks whether the wrapper has moved a new build into place over the binary
// this process is running. The listener never finishes a run and has no interval, so without
// this an edit to mochi would not run until the Mac restarted. Copied from hachiko's.
package self

import (
	"errors"
	"os"
	"syscall"
)

var ErrReplaced = errors.New("the mochi binary behind this process has been replaced, so this one exits for the new one")

type ID struct {
	Dev, Inode uint64
	ModUnix    int64
	Size       int64
}

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

// The wrapper installs a build with a rename, which is a new inode at the same path; the
// size and time cover an inode a build has just freed being reused.
func idOf(info os.FileInfo) ID {
	id := ID{ModUnix: info.ModTime().Unix(), Size: info.Size()}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		id.Dev, id.Inode = uint64(st.Dev), st.Ino
	}
	return id
}

type Watch struct {
	look  func() (ID, bool)
	was   ID
	known bool
}

func Looking(look func() (ID, bool)) *Watch {
	w := &Watch{look: look}
	w.was, w.known = look()
	return w
}

// False whenever either reading failed, rather than a listener that restarts every half
// minute for ever over a binary nothing can stat.
func (w *Watch) Replaced() bool {
	now, ok := w.look()
	return w.known && ok && now != w.was
}

// Skip takes the binary there now as the one already seen, so a build that was refused is not
// reported again and only a newer one is.
func (w *Watch) Skip() {
	if now, ok := w.look(); ok {
		w.was, w.known = now, true
	}
}
