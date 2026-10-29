package self

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// A stand-in for the wrapper's build cache: a file at a fixed path, and a reading of it
// taken the way the real one is taken of os.Executable().
func binaryAt(t *testing.T) (string, func() (ID, bool)) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "hachiko")
	harness.WriteFile(t, path, "the build that is running")

	return path, func() (ID, bool) {
		info, err := os.Stat(path)
		if err != nil {
			return ID{}, false
		}
		return idOf(info), true
	}
}

// What the wrapper actually does: it builds somewhere else and moves the result into place
// in one step, so the file behind the same path is a different inode.
func movedInto(t *testing.T, path, content string) {
	t.Helper()

	fresh := path + ".fresh"
	harness.WriteFile(t, fresh, content)
	if err := os.Rename(fresh, path); err != nil {
		t.Fatal(err)
	}
}

func TestABinaryNothingHasTouchedIsNotReplaced(t *testing.T) {
	_, look := binaryAt(t)

	w := Looking(look)
	harness.Equal(t, w.Replaced(), false, "whether an untouched binary reads as replaced")
	harness.Equal(t, w.Replaced(), false, "whether it reads as replaced on a second look")
}

func TestABuildMovedIntoPlaceIsReplaced(t *testing.T) {
	path, look := binaryAt(t)

	w := Looking(look)
	movedInto(t, path, "a fresh build")

	harness.Equal(t, w.Replaced(), true, "whether a build moved into place reads as replaced")
}

// A build of exactly the same length is the case the modification time and the size alone
// would miss, and the one a rename always answers: the inode is new.
func TestABuildOfTheSameSizeIsStillReplaced(t *testing.T) {
	path, look := binaryAt(t)

	w := Looking(look)
	movedInto(t, path, "the build that is running")

	harness.Equal(t, w.Replaced(), true, "whether a same-sized build reads as replaced")
}

// An agent that restarted every half minute because it cannot stat its own binary would be
// an agent that never does its job on exactly the Mac worth watching.
func TestABinaryThatCannotBeStattedIsNotReplaced(t *testing.T) {
	path, look := binaryAt(t)

	w := Looking(look)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	harness.Equal(t, w.Replaced(), false, "whether a binary that has gone reads as replaced")

	// And nor is one that was never readable in the first place, however much the file
	// behind it moves afterwards.
	blind := Looking(func() (ID, bool) { return ID{}, false })
	harness.Equal(t, blind.Replaced(), false, "whether a watch with no baseline reads as replaced")
}

// The one reading taken of the real process, which is the path the agents use.
func TestTheRunningBinaryCanBeStatted(t *testing.T) {
	id, ok := Stat()

	harness.Equal(t, ok, true, "whether the running binary could be statted")
	if id.Inode == 0 {
		t.Error("the running binary has no inode")
	}
}
