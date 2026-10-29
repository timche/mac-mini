package gc

import (
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// The worktree of a branch whose pull request has merged. Every other pass here reacts to a
// worktree somebody removed; this one removes it, because the rule that a session takes its
// own worktree and branch away once its change has merged is a rule a session can forget,
// and what a forgotten worktree holds is not the folder but what a build put in it — a
// node_modules, a target, a dist, each of them gigabytes that nothing will ever read again.
// Removing it is what hands the folder to the four passes above, which take the compose
// project, its volumes and whatever is still running in it.
//
// Merged as GitHub reports it, not as git can work out: a squash merge leaves a branch whose
// commits are nowhere in the base's history, which is the normal shape of a landed change
// here, so `git branch --merged` and every ancestry test against the base answer no for
// exactly the worktrees this is for. That makes it the one pass that asks the network, which
// is why it runs on a clock of its own rather than six times an hour.
//
// A pull request that was closed without merging, and a branch with no pull request at all,
// are left alone: unmerged work is the one thing in a worktree that exists nowhere else.
func (s *sweeper) mergedWorktrees(state *State, now time.Time) {
	last := time.Unix(state.Merged, 0)
	next := last.Add(s.cfg.GCMergedEvery)
	due := state.Merged == 0 || !now.Before(next)

	if !due {
		s.keepOpenWorktrees(state)
		if s.dry {
			s.say("pull requests were read %s ago, so no merged worktree would go for another %s",
				wording.DurationPhrase(now.Sub(last)), wording.DurationPhrase(next.Sub(now)))
		}
		return
	}

	// git says what a worktree is and gh says whether it has landed; without either there is
	// no question to ask. Neither is a fault and neither is worth a line, for the reason a
	// stopped docker daemon is not: a machine without them has no merged worktree to find.
	if !s.deps.Git() || !s.deps.GH() {
		s.keepOpenWorktrees(state)
		if s.dry {
			s.say("there is no git and gh to read pull requests with, so no merged worktree would go")
		}
		return
	}

	if !s.dry {
		// Written down before the work rather than left to the save at the end of the sweep,
		// as the daily prune's is and for the same reason: this pass is a network call per
		// worktree, and a sweep killed or timed out partway is exactly the one that never
		// reaches that save. A GitHub that refuses every call would otherwise be a round of
		// them every ten minutes.
		state.Merged = now.Unix()
		if err := s.store.Save(state); err != nil {
			s.say("the time of this pull request read could not be written, so the next sweep may read them again: %v", err)
		}
	}

	pass := &mergedPass{
		held:   map[string]map[string]bool{},
		failed: map[string]bool{},
	}
	pass.open, _ = s.openDirs()
	pass.claimed, _ = s.sessions()

	for _, dir := range s.worktreeFolders() {
		s.mergedWorktree(dir, pass)
	}
}

// What one run of the pass knows about the machine. Each of the three is read once: an lsof
// over every process, the session records, and herdr asked about each repository as a
// worktree of it turns up.
type mergedPass struct {
	open    []CWD
	claimed []string

	held   map[string]map[string]bool
	failed map[string]bool
}

// Which of the repository's worktrees herdr has open, asked once per repository and false
// where it would not say. A sweep that cannot tell what Tim has in his sidebar has no way to
// tell a forgotten worktree from one somebody is sitting in, so none of that repository's go
// — and the refusal is remembered, so a herdr that is down is one line rather than one per
// worktree.
func (s *sweeper) herdrHolds(repo string, pass *mergedPass) (map[string]bool, bool) {
	if pass.failed[repo] {
		return nil, false
	}
	if held, asked := pass.held[repo]; asked {
		return held, true
	}

	held, err := s.deps.HerdrWorktrees(repo)
	if err != nil {
		s.say("herdr would not say which of %s's worktrees it has open, so none of them went: %v",
			safe(repo), err)
		pass.failed[repo] = true
		return nil, false
	}

	pass.held[repo] = held
	return held, true
}

func (s *sweeper) keepOpenWorktrees(state *State) {
	s.keepOpen(state, failedWorktree, nil)
}

// One worktree. The order is the cost of the questions: what git says about the folder is
// free, GitHub is a round trip, and everything after it is free again — so nothing is asked
// of GitHub about a directory that is not a worktree on a branch, and nothing at all is done
// to a worktree whose branch has not landed.
func (s *sweeper) mergedWorktree(dir string, pass *mergedPass) {
	if !s.deps.IsDir(dir) {
		return
	}

	repo, branch, ok := s.worktreeBranch(dir)
	if !ok {
		return
	}

	held, ok := s.herdrHolds(repo, pass)
	if !ok {
		return
	}

	pr, err := s.deps.MergedPR(dir, branch)
	if err != nil {
		// Said on a dry run alone. A gh that is not logged in, or a repository with no
		// GitHub remote, fails the same way for every worktree in it on every sweep, and a
		// line each would be the sweep's whole log.
		if s.dry {
			s.say("GitHub would not say whether %s has merged, so %s would stay: %v",
				safe(branch), safe(dir), err)
		}
		return
	}
	if pr == nil {
		return
	}

	// Each of these is somebody still having the worktree, or work that exists only in it.
	// Said on a dry run, because a worktree that has merged and is still here is the one
	// thing this pass has an opinion about and `--dry-run` is where that opinion is read;
	// not on a live sweep, where it is the ordinary state of a worktree in use and would be
	// a line an hour for as long as it lasts.
	if reason := s.keeping(dir, pr, held, pass); reason != "" {
		if s.dry {
			s.say("%s has merged as %s but %s would stay: %s",
				safe(branch), safe(pr.URL), safe(dir), reason)
		}
		return
	}

	if s.dry {
		s.say("would remove the worktree %s and its branch %s, merged as %s",
			safe(dir), safe(branch), safe(pr.URL))
		return
	}

	s.say("removing the worktree %s and its branch %s, merged as %s",
		safe(dir), safe(branch), safe(pr.URL))

	out, err := s.deps.GitIn(repo, "worktree", "remove", dir)
	if err != nil {
		s.failed(Failure{
			Kind: failedWorktree, Subject: dir,
			Label: wording.PlainWords(short(pr.URL)), Detail: lastLine(out, err),
		})
		return
	}

	// -D rather than -d, for the very reason GitHub had to be asked at all: a squash merge
	// leaves no commit of this branch in the base's history, so git's own merged test refuses
	// to delete the branch of a change that has landed. What authorises it is the merged pull
	// request above, and nothing here ever touches the branch on the remote.
	//
	// A branch left behind is a line and no more. It costs a file of forty bytes, where the
	// folder this has just removed was the gigabytes, and a message about it would clear on
	// the next sweep anyway, the worktree it was keyed to being gone.
	if out, err := s.deps.GitIn(repo, "branch", "-D", branch); err != nil {
		s.say("the branch %s was left behind: %s", safe(branch), lastLine(out, err))
	}
}

// Why a worktree whose pull request has merged is being kept, and the empty string for one
// that is nobody's any more.
func (s *sweeper) keeping(dir string, pr *PullRequest, held map[string]bool, pass *mergedPass) string {
	if held[dir] {
		return "herdr has a workspace open on it"
	}
	for _, cwd := range pass.claimed {
		if inside(dir, cwd) {
			return "a Claude Code session is running in it"
		}
	}
	for _, c := range pass.open {
		if inside(dir, c.Dir) {
			return "process " + strconv.Itoa(c.PID) + " is sitting in it"
		}
	}

	// Uncommitted and untracked both, which is also what `git worktree remove` refuses on:
	// asking first is what makes a refusal a reason rather than a failure. Files git is
	// ignoring are not among them, node_modules being the whole point of removing the folder.
	out, err := s.deps.GitIn(dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return "git would not say whether it is clean: " + safe(lastLine(out, err))
	}
	if strings.TrimSpace(string(out)) != "" {
		return "it has changes that were never committed"
	}

	// Anything committed in the worktree after the merge is work the pull request does not
	// carry, so the folder is the only place it exists. HEAD being the merged head itself
	// passes this too, --is-ancestor counting a commit as its own ancestor; a head GitHub
	// named that this repository has never fetched fails it, which is the safe way round.
	if _, err := s.deps.GitIn(dir, "merge-base", "--is-ancestor", "HEAD", pr.Head); err != nil {
		return "it has commits that " + safe(pr.URL) + " does not"
	}
	return ""
}

// The repository a worktree belongs to and the branch it is on, and false for anything that
// is not a linked worktree on a branch. The common git directory is the owning repository's
// wherever it is asked from, and a git directory of its own is what makes the folder a
// linked worktree rather than the checkout itself, which is nobody's to remove. A detached
// HEAD has no branch, so no pull request to have merged.
func (s *sweeper) worktreeBranch(dir string) (repo, branch string, ok bool) {
	out, err := s.deps.GitIn(dir, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir")
	if err != nil {
		return "", "", false
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 || lines[0] == "" || lines[0] == lines[1] {
		return "", "", false
	}

	out, err = s.deps.GitIn(dir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", "", false
	}
	if branch = strings.TrimSpace(string(out)); branch == "" {
		return "", "", false
	}
	return filepath.Dir(lines[1]), branch, true
}

// Every folder in the two roots this machine makes worktrees in, whether or not it is one:
// what a folder really is is git's to say, and this only has to find the candidates.
//
// herdr's root is a folder per repository, named for the repository alone, with a folder per
// branch inside it. Claude Code's are inside the repository they branched from, so the
// repositories have to be walked to find them — this very repository among them, which is
// in HOME rather than under the projects root and is where the agent worktrees pile up.
func (s *sweeper) worktreeFolders() []string {
	var found []string

	for _, repo := range s.names(s.cfg.HerdrRoot) {
		dir := filepath.Join(s.cfg.HerdrRoot, repo)
		for _, branch := range s.names(dir) {
			found = append(found, filepath.Join(dir, branch))
		}
	}

	repos := s.repositories()
	if s.cfg.MachineDir != "" && s.deps.IsDir(filepath.Join(s.cfg.MachineDir, ".git")) {
		repos = append(repos, s.cfg.MachineDir)
	}

	for _, repo := range repos {
		nested := filepath.Join(repo, ".claude", "worktrees")
		for _, name := range s.names(nested) {
			found = append(found, filepath.Join(nested, name))
		}
	}
	return found
}

func (s *sweeper) names(dir string) []string {
	if !s.deps.IsDir(dir) {
		return nil
	}

	names, err := s.deps.ReadDir(dir)
	if err != nil {
		s.say("%s could not be read, so the worktrees in it were left alone: %v", safe(dir), err)
		return nil
	}
	return names
}

// Whether a path is the directory itself or anything below it. The trailing separator
// matters for the reason it does in the roots: without it `worktree-old` reads as being
// inside `worktree`.
func inside(dir, path string) bool {
	if dir == "" || path == "" {
		return false
	}
	trimmed := strings.TrimSuffix(dir, "/")
	return path == trimmed || strings.HasPrefix(path, trimmed+"/")
}
