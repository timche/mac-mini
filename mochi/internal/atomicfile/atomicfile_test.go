package atomicfile

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestWritePutsTheWholeFileInPlaceAt0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := Write(path, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("two")); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if string(body) != "two" || info.Mode().Perm() != 0o600 {
		t.Errorf("body %q, mode %v", body, info.Mode().Perm())
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("left %v", entries)
	}
}

// A fixed temporary name let two writers truncate each other's half-written file.
func TestConcurrentWritersNeverShareATemporaryFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marks.json")
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := byte('a' + i)
			if err := Write(path, []byte{c, c, c}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	body, _ := os.ReadFile(path)
	if len(body) != 3 || body[0] != body[1] || body[1] != body[2] {
		t.Errorf("a torn file: %q", body)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("left %v", entries)
	}
}

func TestAFailedFillLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	err := WriteFrom(filepath.Join(dir, "download"), func(w io.Writer) error {
		w.Write([]byte("half"))
		return errors.New("cut short")
	})
	if err == nil {
		t.Fatal("no error")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left %v", entries)
	}
}
