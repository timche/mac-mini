// Package atomicfile puts a file in place whole or not at all, for the state the listener,
// the hooks and `mochi post` all write: a reader never sees half of one, and two writers
// never share a temporary file.
package atomicfile

import (
	"io"
	"os"
	"path/filepath"
)

// Suffix ends every temporary file this package makes, which is how what a killed writer
// left behind is told from the files themselves.
const Suffix = ".tmp"

// Write puts body at path, 0600, through a temporary file of its own beside it.
func Write(path string, body []byte) error {
	return WriteFrom(path, func(w io.Writer) error {
		_, err := w.Write(body)
		return err
	})
}

// WriteFrom is Write for a body that streams, and an error from fill leaves nothing behind.
// The temporary name is unique, since a hook and the listener can write the same file at
// once, and the file is synced before the rename so a crash just after it cannot leave the
// name pointing at an empty file.
func WriteFrom(path string, fill func(io.Writer) error) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*"+Suffix)
	if err != nil {
		return err
	}
	name := tmp.Name()
	err = fill(tmp)
	if err == nil {
		err = tmp.Chmod(0o600)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		os.Remove(name)
	}
	return err
}
