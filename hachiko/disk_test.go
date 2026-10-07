package main

import (
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/config"
)

func TestTruncatableOnlyUnderTheRootsWhereAFileIsALog(t *testing.T) {
	cfg := config.Config{
		TmpRoot:     "/private/tmp",
		ScratchRoot: "/Users/x/.cache/claude-tmp",
		LogsRoot:    "/Users/x/Library/Logs",
	}

	cases := []struct {
		path string
		want bool
	}{
		{"/private/tmp/devbackend.log", true},
		{"/private/tmp/nested/worker.out", true},
		{"/private/tmp/worker.err", true},
		{"/private/tmp/worker.output", true},
		{"/private/tmp/worker.log.1", true},
		{"/Users/x/.cache/claude-tmp/session/run.log", true},
		{"/Users/x/Library/Logs/app.log", true},
		// A name that does not say it is a log is somebody's data, whatever it is
		// doing to the disk.
		{"/private/tmp/worker.bin", false},
		{"/private/tmp/database", false},
		{"/private/tmp/logs", false},
		// Outside the three roots, even with the right name.
		{"/Users/x/Downloads/worker.log", false},
		{"/Users/x/projects/app/debug.log", false},
		// A sibling whose name only starts the same way.
		{"/private/tmp-evil/worker.log", false},
		{"/private/tmp", false},
	}

	for _, c := range cases {
		if got := truncatable(cfg, c.path); got != c.want {
			t.Errorf("truncatable(%s) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestGrowthReportsNothingWithoutAPreviousSample(t *testing.T) {
	files := []FileSize{{Path: "/tmp/a.log", KB: 10 * config.GiB}}

	sizes, growing := growth(nil, files, 2*config.GiB, false)

	equal(t, len(growing), 0, "files growing on the first sample")
	equal(t, sizes["/tmp/a.log"], 10*config.GiB, "the size recorded on the first sample")
}

// A file the previous sample has no line for was under the floor then, so the whole of
// it arrived in one interval.
func TestAFileThatWasUnderTheFloorCountsItsWholeSizeAsGrowth(t *testing.T) {
	prev := map[string]int64{"/tmp/old.log": 4 * config.GiB}
	files := []FileSize{
		{Path: "/tmp/old.log", KB: 5 * config.GiB},
		{Path: "/tmp/new.log", KB: 9 * config.GiB},
	}

	_, growing := growth(prev, files, 2*config.GiB, true)

	equal(t, len(growing), 1, "files growing")
	equal(t, growing[0].Path, "/tmp/new.log", "the growing file")
	equal(t, growing[0].GrewKB, 9*config.GiB, "the growth of a file that was under the floor")
}

// Fastest first, because the fastest is the only one a truncate ever touches.
func TestGrowingFilesComeFastestFirst(t *testing.T) {
	prev := map[string]int64{"/tmp/a.log": 0, "/tmp/b.log": 0, "/tmp/c.log": 0}
	files := []FileSize{
		{Path: "/tmp/a.log", KB: 3 * config.GiB},
		{Path: "/tmp/b.log", KB: 9 * config.GiB},
		{Path: "/tmp/c.log", KB: 6 * config.GiB},
	}

	_, growing := growth(prev, files, 2*config.GiB, true)

	equal(t, len(growing), 3, "files growing")
	equal(t, growing[0].Path, "/tmp/b.log", "the fastest growing file")
	equal(t, growing[2].Path, "/tmp/a.log", "the slowest growing file")
}

func TestAFileThatShrankIsNotGrowing(t *testing.T) {
	prev := map[string]int64{"/tmp/a.log": 20 * config.GiB}
	files := []FileSize{{Path: "/tmp/a.log", KB: 1 * config.GiB}}

	_, growing := growth(prev, files, 2*config.GiB, true)
	equal(t, len(growing), 0, "files growing after a truncate")
}
