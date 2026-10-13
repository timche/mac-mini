package watch

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
)

// Growing is a file that gained more than the growth threshold between two samples.
type Growing struct {
	Path   string
	KB     int64
	GrewKB int64
}

// A file the previous sample has no line for was under the floor then, so the whole
// of it arrived in one interval — which is exactly the shape of the failure this
// watches for.
func growth(prev map[string]int64, files []FileSize, growthKB int64, hadSample bool) (map[string]int64, []Growing) {
	sizes := make(map[string]int64, len(files))
	var growing []Growing

	for _, f := range files {
		sizes[f.Path] = f.KB

		if !hadSample {
			continue
		}
		if grew := f.KB - prev[f.Path]; grew >= growthKB {
			growing = append(growing, Growing{Path: f.Path, KB: f.KB, GrewKB: grew})
		}
	}

	// Fastest first, because the fastest is the only one a truncate ever touches.
	sort.Slice(growing, func(i, j int) bool {
		if growing[i].GrewKB != growing[j].GrewKB {
			return growing[i].GrewKB > growing[j].GrewKB
		}
		return growing[i].Path < growing[j].Path
	})

	return sizes, growing
}

// Capped because a rate of a kilobyte an interval projects centuries, and a century
// overflows a Duration into a negative one — which reads here as a disk about to fill.
const longestFill = 100000 * time.Hour

// How long the free space would last if every growing file kept the rate it was just
// measured at. Combined, because they are filling one volume between them, and against
// the whole of the free space rather than the critical mark: this decides whether growth
// is worth waking somebody for, where the mark is what a projection in an open incident
// is measured to.
func fillTime(growing []Growing, freeKB int64, span time.Duration) (time.Duration, bool) {
	seconds := span.Seconds()
	if len(growing) == 0 || seconds <= 0 {
		return 0, false
	}

	var grewKB int64
	for _, g := range growing {
		grewKB += g.GrewKB
	}
	if grewKB <= 0 {
		return 0, false
	}
	if freeKB <= 0 {
		return 0, true
	}

	if until := float64(freeKB) / (float64(grewKB) / seconds); until < longestFill.Seconds() {
		return time.Duration(until) * time.Second, true
	}
	return longestFill, true
}

// Only under the roots where a file is a log and nothing else, and only when its
// name says so too. Everything else is somebody's data, whatever it is doing to the
// disk.
func truncatable(cfg config.Config, path string) bool {
	under := false
	for _, root := range []string{cfg.TmpRoot, cfg.ScratchRoot, cfg.LogsRoot} {
		if root != "" && strings.HasPrefix(path, strings.TrimSuffix(root, "/")+"/") {
			under = true
			break
		}
	}
	if !under {
		return false
	}

	name := filepath.Base(path)
	for _, suffix := range []string{".log", ".out", ".err", ".output"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return strings.Contains(name, ".log.")
}
