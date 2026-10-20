package self

import (
	"os"
	"path/filepath"
	"testing"
)

func binaryAt(t *testing.T) (string, func() (ID, bool)) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mochi")
	if err := os.WriteFile(path, []byte("the build that is running"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, func() (ID, bool) {
		info, err := os.Stat(path)
		if err != nil {
			return ID{}, false
		}
		return idOf(info), true
	}
}

// What the wrapper does: build elsewhere and rename into place.
func movedInto(t *testing.T, path, content string) {
	t.Helper()
	fresh := path + ".fresh"
	if err := os.WriteFile(fresh, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(fresh, path); err != nil {
		t.Fatal(err)
	}
}

func TestOnlyABuildMovedIntoPlaceIsAReplacement(t *testing.T) {
	path, look := binaryAt(t)
	w := Looking(look)
	if w.Replaced() {
		t.Fatal("an untouched binary reads as replaced")
	}

	movedInto(t, path, "the build that is running")
	if !w.Replaced() {
		t.Error("a same-sized build moved into place was not noticed")
	}
}

func TestABinaryThatCannotBeStattedIsNotReplaced(t *testing.T) {
	path, look := binaryAt(t)
	w := Looking(look)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if w.Replaced() {
		t.Error("a binary that has gone reads as replaced")
	}
	if Looking(func() (ID, bool) { return ID{}, false }).Replaced() {
		t.Error("a watch with no baseline reads as replaced")
	}
}
