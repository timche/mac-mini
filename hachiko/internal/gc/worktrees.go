package gc

import (
	"path/filepath"
	"strings"
)

// The worktree entries git keeps for folders that are gone. Its own prune rather than a
// rule of ours: it drops metadata only, and only for a worktree whose folder git cannot
// find, so a session still working keeps its entry. Nothing outside the projects root,
// where this machine's repositories are, and a .git directory rather than a file, which is
// what a worktree itself has.
func (s *sweeper) worktreeEntries() {
	root := s.cfg.GCProjectsRoot
	if !s.deps.IsDir(root) || !s.deps.Git() {
		return
	}

	names, err := s.deps.ReadDir(root)
	if err != nil {
		s.say("%s could not be read, so no worktree entry was pruned: %v", safe(root), err)
		return
	}

	verb := "pruning"
	if s.dry {
		verb = "would prune"
	}

	for _, name := range names {
		repo := filepath.Join(root, name)
		if !s.deps.IsDir(filepath.Join(repo, ".git")) {
			continue
		}

		// git reports what it pruned on stderr, hence the whole of its output and the filter
		// on the lines that name an entry.
		out, err := s.deps.Prune(repo, s.dry)
		if err != nil {
			s.say("%s's worktree entries could not be pruned: %v", safe(repo), err)
		}

		for _, line := range strings.Split(string(out), "\n") {
			rest, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "Removing ")
			if !ok {
				continue
			}

			entry, _, _ := strings.Cut(rest, ":")
			_, reason, found := strings.Cut(rest, ": ")
			if !found {
				reason = rest
			}

			s.say("%s %s's worktree entry %s, whose folder is gone (%s)",
				verb, safe(repo), safe(entry), safe(reason))
		}
	}
}
