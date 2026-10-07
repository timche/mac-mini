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
func (s *sweeper) compose(state *State) {
	if !s.docker() {
		return
	}

	found, err := s.deps.Containers()
	if err != nil {
		s.say("docker would not say what it is running, so no compose project was swept: %v", err)
		return
	}

	now := s.deps.Now()

	// A project is named `<repo>-<branch>`, so a branch first run in a worktree and later
	// checked out in the main checkout is the same project with the same volumes. Seen
	// running anywhere outside a worktree root, it is somebody's checkout again: its record
	// goes, and nothing of it is taken down.
	outside := map[string]bool{}
	for _, c := range found {
		if _, inRoot := worktreeOf(s.cfg.HerdrRoot, c.WorkingDir); c.Project != "" && c.WorkingDir != "" && !inRoot {
			outside[c.Project] = true
		}
	}

	for _, p := range byProject(found) {
		if outside[p.Project] {
			delete(state.Projects, p.Project)
			continue
		}

		worktree, inRoot := worktreeOf(s.cfg.HerdrRoot, p.WorkingDir)
		if !inRoot {
			continue
		}

		// Written down whether or not the worktree is still there, because this is the only
		// moment a project and a folder are ever seen together: a volume carries the project
		// and nothing else, and by the time one is worth removing there is no container left
		// to ask.
		state.see(p.Project, worktree, now)

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

		out, err := s.deps.Down(p.Project)
		s.indent(out)
		if err != nil {
			s.failed(Failure{
				Kind: failedDown, Subject: p.Project, Worktree: worktree,
				Detail: lastLine(out, err),
			})
		}
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

// Named volumes of a worktree that is gone. A volume says which compose project made it and
// nothing about where that project was, so the record above is what authorises this: a
// project never seen running inside a worktree root is never touched, however its volumes
// are named.
//
// Anonymous volumes are left alone whatever project they carry. One is a mount a Dockerfile
// or a compose file asked for without naming, so what is in it was never anybody's to find
// again — but it is also the one kind a `down -v` already takes, so the ones left here are
// the ones something else is keeping.
func (s *sweeper) volumes(state *State) {
	if !s.docker() {
		return
	}

	all, err := s.deps.Volumes()
	if err != nil {
		s.say("docker would not list its volumes, so none was swept: %v", err)
		return
	}

	// How many volumes each project still has, which decides whether its record has
	// anything left to do.
	left := map[string]int{}
	for _, v := range all {
		if v.Project != "" {
			left[v.Project]++
		}
	}

	for _, v := range all {
		if v.Anonymous || v.InUse || v.Project == "" {
			continue
		}

		seen, ok := state.Projects[v.Project]
		if !ok || s.deps.IsDir(seen.Worktree) {
			continue
		}

		if s.dry {
			s.say("would remove volume %s of compose project %s, whose worktree %s is gone",
				safe(v.Name), safe(v.Project), safe(seen.Worktree))
			continue
		}

		s.say("removing volume %s of compose project %s, whose worktree %s is gone",
			safe(v.Name), safe(v.Project), safe(seen.Worktree))

		out, err := s.deps.RemoveVolume(v.Name)
		if err != nil {
			s.failed(Failure{
				Kind: failedVolume, Subject: v.Name, Project: v.Project,
				Worktree: seen.Worktree, Detail: lastLine(out, err),
			})
			continue
		}
		left[v.Project]--
	}

	// A record for a worktree that is gone and a project with nothing left of it is a record
	// that can never authorise anything again. One whose removal failed keeps its own, which
	// is what makes the next sweep try it.
	for _, project := range sortedKeys(state.Projects) {
		if left[project] == 0 && !s.deps.IsDir(state.Projects[project].Worktree) {
			delete(state.Projects, project)
		}
	}
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

// The one line of a command's output a message quotes. The last rather than the first:
// docker and git both print what they were doing before they print what went wrong.
func lastLine(out []byte, err error) string {
	for _, line := range reversed(strings.Split(strings.TrimRight(string(out), "\n"), "\n")) {
		if text := strings.TrimSpace(line); text != "" {
			return text
		}
	}
	return err.Error()
}

func reversed(lines []string) []string {
	out := make([]string, 0, len(lines))
	for i := len(lines) - 1; i >= 0; i-- {
		out = append(out, lines[i])
	}
	return out
}

// Everything in a line of this log was chosen by whatever a session was running: a path, a
// project name, a line of docker's output. A newline in one is how a line of data becomes a
// line that reads as the sweep's own.
//
// Long enough for any path macOS will make, since the log is where somebody looks for the
// one it was about; a message is `short` instead, because Discord counts the whole of it.
const logLimit = 1024

func safe(s string) string  { return wording.Safe(s, logLimit) }
func short(s string) string { return wording.Safe(s, wording.PathLimit) }
