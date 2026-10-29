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
	if !s.deps.Git() {
		return
	}
	for _, repo := range s.repositories() {
		s.pruneEntries(repo)
	}
}

// Every repository under the projects root, where this machine's checkouts are. A checkout
// sits at <owner>/<repo>, so a directory that is not a repository itself is the owner's
// folder and the repositories are one level in; the docs clone, directly under the root, is
// the other shape. A .git directory rather than a file, which is what a worktree itself has.
func (s *sweeper) repositories() []string {
	root := s.cfg.GCProjectsRoot
	if !s.deps.IsDir(root) {
		return nil
	}

	names, err := s.deps.ReadDir(root)
	if err != nil {
		s.say("%s could not be read, so the repositories under it were left alone: %v", safe(root), err)
		return nil
	}

	var repos []string
	for _, name := range names {
		dir := filepath.Join(root, name)
		if s.deps.IsDir(filepath.Join(dir, ".git")) {
			repos = append(repos, dir)
			continue
		}

		owned, err := s.deps.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, sub := range owned {
			repo := filepath.Join(dir, sub)
			if s.deps.IsDir(filepath.Join(repo, ".git")) {
				repos = append(repos, repo)
			}
		}
	}
	return repos
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
