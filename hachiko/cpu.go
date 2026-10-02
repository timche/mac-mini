package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// Hot is a process that has held its share of a core for the whole window.
type Hot struct {
	Process  Process
	Share    float64
	HotSince time.Time
}

func (h Hot) HotFor(now time.Time) time.Duration { return now.Sub(h.HotSince) }

// Utilisation is the CPU time a process took over the wall clock between two
// samples, so a half-busy core reads 50 whatever ps thinks of the process's whole
// life. A sample that comes in low starts the window again, and because the window
// is a span of time rather than a count of runs, an interval the agent missed costs
// nothing.
func cpuHot(prev CPUSample, procs []Process, now time.Time, share float64, window time.Duration) (CPUSample, []Hot) {
	sample := CPUSample{At: now.Unix(), Procs: make(map[string]ProcSample, len(procs))}
	var hot []Hot

	prevAt := time.Unix(prev.At, 0)
	span := now.Sub(prevAt).Seconds()
	hadSample := prev.At > 0 && span > 0

	for _, p := range procs {
		key := p.Key()
		cpu := p.CPU.Seconds()
		next := ProcSample{CPU: cpu}

		if was, ok := prev.Procs[key]; hadSample && ok {
			if (cpu-was.CPU)/span*100 >= share {
				next.HotSince, next.CPUAtHotSince = was.HotSince, was.CPUAtHotSince
				if next.HotSince == 0 {
					next.HotSince, next.CPUAtHotSince = prev.At, was.CPU
				}
			}
		}

		sample.Procs[key] = next

		if next.HotSince == 0 {
			continue
		}
		since := time.Unix(next.HotSince, 0)
		if held := now.Sub(since); held >= window {
			hot = append(hot, Hot{
				Process:  p,
				Share:    (cpu - next.CPUAtHotSince) / held.Seconds() * 100,
				HotSince: since,
			})
		}
	}

	return sample, hot
}

// The repository a directory belongs to, which is what says whether a process left
// running is somebody's work or nobody's. The two worktree roots this machine makes
// are named differently, and a plain checkout sits directly under ~/projects.
func repoOf(cfg Config, dir string) string {
	if rest, ok := under(cfg.HerdrRoot, dir); ok {
		parts := strings.Split(rest, "/")
		if len(parts) >= 2 {
			return fmt.Sprintf("the %s worktree %s", parts[0], parts[1])
		}
	}

	if before, after, found := strings.Cut(dir, "/.claude/worktrees/"); found {
		name, _, _ := strings.Cut(after, "/")
		if name != "" {
			return fmt.Sprintf("a worktree of %s, %s", filepath.Base(before), name)
		}
	}

	if rest, ok := under(cfg.ProjectsRoot, dir); ok {
		repo, _, _ := strings.Cut(rest, "/")
		if repo != "" {
			return fmt.Sprintf("the %s checkout", repo)
		}
	}

	return ""
}

func under(root, dir string) (string, bool) {
	if root == "" {
		return "", false
	}
	prefix := strings.TrimSuffix(root, "/") + "/"
	if !strings.HasPrefix(dir, prefix) {
		return "", false
	}
	return strings.TrimPrefix(dir, prefix), true
}
