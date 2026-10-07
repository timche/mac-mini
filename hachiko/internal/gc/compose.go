package gc

import (
	"sort"
	"strings"

	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// Asked once per sweep: `docker info` is a subprocess and a daemon that is down takes the
// whole of its timeout to say so, which would otherwise be paid twice.
func (s *sweeper) docker() bool {
	if s.dockerKnown {
		return s.dockerUp
	}
	s.dockerUp, s.dockerKnown = s.deps.Docker(), true
	return s.dockerUp
}

// Compose projects whose worktree is gone, and the record of which projects ran in one at
// all. config_files is the second opinion on the worktree being gone — a project keeping any
// of them is one whose folder is still there in some form.
//
// docker absent or its daemon down is not a fault and says nothing: a Mac with OrbStack
// stopped has no containers to sweep, and a line about it every ten minutes would bury the
// lines that matter.
func (s *sweeper) compose() {
	if !s.docker() {
		return
	}

	found, err := s.deps.Containers()
	if err != nil {
		s.say("docker would not say what it is running, so no compose project was swept: %v", err)
		return
	}

	for _, p := range byProject(found) {
		worktree, inRoot := worktreeOf(s.cfg.HerdrRoot, p.WorkingDir)
		if !inRoot {
			continue
		}

		if p.WorkingDir == "" || s.deps.IsDir(p.WorkingDir) || s.deps.IsDir(worktree) {
			continue
		}
		if s.anyConfigLeft(p.ConfigFiles) {
			continue
		}

		if s.dry {
			s.say("would remove compose project %s, whose worktree %s is gone",
				safe(p.Project), safe(worktree))
			continue
		}

		s.say("removing compose project %s, whose worktree %s is gone", safe(p.Project), safe(worktree))

		out, _ := s.deps.Down(p.Project)
		s.indent(out)
	}
}

func (s *sweeper) anyConfigLeft(files []string) bool {
	for _, file := range files {
		if file != "" && s.deps.Exists(file) {
			return true
		}
	}
	return false
}

// One decision per project rather than per container: a project with three containers has
// three identical sets of labels, and `down` is about the project.
func byProject(found []Container) []Container {
	merged := map[string]*Container{}

	for _, c := range found {
		if c.Project == "" {
			continue
		}

		into, ok := merged[c.Project]
		if !ok {
			copy := c
			merged[c.Project] = &copy
			continue
		}
		if into.WorkingDir == "" {
			into.WorkingDir = c.WorkingDir
		}
		for _, file := range c.ConfigFiles {
			if !contains(into.ConfigFiles, file) {
				into.ConfigFiles = append(into.ConfigFiles, file)
			}
		}
	}

	out := make([]Container, 0, len(merged))
	for _, project := range sortedKeys(merged) {
		out = append(out, *merged[project])
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// What docker or git printed, under the line that says what was being done — the whole of
// it, because the log is where somebody looks once a message has said something failed.
func (s *sweeper) indent(out []byte) {
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			s.say("  %s", safe(line))
		}
	}
}

// Everything in a line of this log was chosen by whatever a session was running: a path, a
// project name, a line of docker's output. A newline in one is how a line of data becomes a
// line that reads as the sweep's own.
//
// Long enough for any path macOS will make, since the log is where somebody looks for the
// one it was about.
const logLimit = 1024

func safe(s string) string { return wording.Safe(s, logLimit) }
