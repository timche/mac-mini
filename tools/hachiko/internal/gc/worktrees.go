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

	for _, name := range names {
		dir := filepath.Join(root, name)
		if s.deps.IsDir(filepath.Join(dir, ".git")) {
			s.pruneEntries(dir)
			continue
		}

		// A checkout sits at <owner>/<repo>, so a directory that is not a repository itself
		// is the owner's folder and the repositories are one level in. The docs clone, which
		// is directly under the root, is the first branch above.
		owned, err := s.deps.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, sub := range owned {
			repo := filepath.Join(dir, sub)
			if s.deps.IsDir(filepath.Join(repo, ".git")) {
				s.pruneEntries(repo)
			}
		}
	}
}

func (s *sweeper) pruneEntries(repo string) {
	verb := "pruning"
	if s.dry {
		verb = "would prune"
	}

	// git reports what it pruned on stderr, hence the whole of its output and the filter on
	// the lines that name an entry.
	out, err := s.deps.Prune(repo, s.dry)

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

	if err != nil {
		s.failed(Failure{Kind: failedPrune, Subject: repo, Detail: lastLine(out, err)})
	}
}
