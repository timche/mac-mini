package watch

import (
	"path/filepath"
	"sort"
	"strings"

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
