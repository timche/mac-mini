package sync

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
)

// Shelling out to git rather than reaching for a library, which is the decision this was
// ported from and the reason stands: the semantics are identical to what a person would
// type, and git is on every machine this runs on.
type gitRepo struct {
	run func(args ...string) Output
	dir string

	// The repository's own paths, or none for the whole of it. Held here rather than passed
	// to each call, so a command that reads or writes the tree cannot be added without the
	// limit that goes with it.
	paths []string
}

// Every command that reads or writes the working tree ends in `-- <paths>`, which for
// `commit` is git's own `--only`: it commits those paths' working-tree state and leaves
// whatever a session has staged elsewhere staged and uncommitted. `--only` is also what git
// refuses during a merge, which is one more reason the pass leaves a busy tree alone.
func (g gitRepo) limit(args ...string) []string {
	return append(args, config.Pathspec(g.paths)...)
}

// Output is one git command's result. Text is both streams together, because what git has
// to say about a failure is the half a message quotes and it writes most of it on stderr.
type Output struct {
	OK     bool
	Stdout string
	Text   string
}

func (g gitRepo) lines(args ...string) []string {
	out := g.run(args...)
	if !out.OK {
		return nil
	}
	return fields(out.Stdout, "\n")
}

// `-z` throughout, so a path arrives as the bytes it is rather than in git's own quoted
// spelling: a quoted path in a commit subject reads as backslashes, and a quoted one in a
// comparison is a tree that looks changed when git changes its mind about quoting.
func (g gitRepo) nulLines(args ...string) []string {
	out := g.run(args...)
	if !out.OK {
		return nil
	}
	return fields(out.Stdout, "\x00")
}

func fields(text, sep string) []string {
	var kept []string
	for _, field := range strings.Split(text, sep) {
		if field = strings.TrimRight(field, "\r"); field != "" {
			kept = append(kept, field)
		}
	}
	return kept
}

func (g gitRepo) isWorkTree() bool {
	return strings.TrimSpace(g.run("rev-parse", "--is-inside-work-tree").Stdout) == "true"
}

func (g gitRepo) branch() (string, error) {
	out := g.run("rev-parse", "--abbrev-ref", "HEAD")
	if !out.OK {
		return "", failed(out)
	}
	return strings.TrimSpace(out.Stdout), nil
}

// Why the tree is somebody's to finish by hand, or "" when it is not. A rebase and a bisect
// both detach HEAD; a merge, a cherry-pick and a revert stop on the branch and leave a
// pseudo-ref behind instead.
func (g gitRepo) busy(branch string) string {
	if branch == "HEAD" {
		return "HEAD is detached, so a rebase or a bisect is under way"
	}
	for _, ref := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD"} {
		if g.run("rev-parse", "-q", "--verify", ref).OK {
			return ref + " is there, so a merge, a cherry-pick or a revert is under way"
		}
	}
	return ""
}

func (g gitRepo) hasUpstream() bool {
	return g.run("rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}").OK
}

// What the poll reads, and the whole of what says a tree has changed: the porcelain format
// is the one git promises not to move, and `v1` says which of them even when a later git
// changes the default. Without the optional lock, because a read every second that refreshes
// the index would also hold index.lock against whatever else is running git in the tree.
func (g gitRepo) status() string {
	out := g.run(g.limit("--no-optional-locks", "status", "--porcelain=v1", "-z")...)
	if !out.OK {
		return ""
	}
	return out.Stdout
}

func (g gitRepo) unpushed() []string { return g.lines("log", "--oneline", "@{upstream}..HEAD") }
func (g gitRepo) behind() []string   { return g.lines("log", "--oneline", "HEAD..@{upstream}") }

// What the commit is about to carry, and so what its subject names. Limited like the rest,
// which is what keeps a change a session staged outside the paths out of the subject as well
// as out of the commit.
func (g gitRepo) stagedFiles() []string {
	return g.nulLines(g.limit("diff", "--cached", "--name-only", "-z")...)
}

// When the oldest commit that is not on the remote was made, and the zero time when there
// is nothing unpushed or no upstream to compare against. Read from git on every pass rather
// than remembered, so a restart does not reset the wait and a commit that was already due
// when the daemon came up goes out at once.
func (g gitRepo) oldestUnpushedAt() time.Time {
	stamps := g.lines("log", "--format=%ct", "@{upstream}..HEAD")
	if len(stamps) == 0 {
		return time.Time{}
	}

	seconds, err := strconv.ParseInt(strings.TrimSpace(stamps[len(stamps)-1]), 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(seconds, 0)
}

// The two shas the pause is re-armed against: a paused repository resumes by itself the
// moment either moves, because either one means somebody has been here.
func (g gitRepo) head() string {
	return strings.TrimSpace(g.run("rev-parse", "HEAD").Stdout)
}

func (g gitRepo) upstreamHead() string {
	return strings.TrimSpace(g.run("rev-parse", "@{upstream}").Stdout)
}

func (g gitRepo) addAll() Output             { return g.run(g.limit("add", "-A")...) }
func (g gitRepo) fetch(remote string) Output { return g.run("fetch", remote) }

func (g gitRepo) commit(subject string) Output {
	return g.run(g.limit("commit", "-m", subject)...)
}

// The one branch a limited repository is synced on, as this clone's own
// `refs/remotes/<remote>/HEAD` has it. Local and read once at startup: no network, and an
// answer that does not change when somebody checks something out here.
func (g gitRepo) defaultBranch(remote string) (string, error) {
	out := g.run("symbolic-ref", "--short", "refs/remotes/"+remote+"/HEAD")
	if !out.OK {
		return "", failed(out)
	}

	name := strings.TrimSpace(out.Stdout)
	short := strings.TrimPrefix(name, remote+"/")
	if short == name || short == "" {
		return "", &gitError{text: fmt.Sprintf("%s/HEAD is %q, which names no branch of %s",
			remote, name, remote)}
	}
	return short, nil
}

func (g gitRepo) push(remote, branch string, setUpstream bool) Output {
	args := []string{"push"}
	if setUpstream {
		args = append(args, "--set-upstream")
	}
	return g.run(append(args, remote, branch)...)
}

func (g gitRepo) pullRebase(remote, branch string) Output {
	return g.run("pull", "--rebase", "--autostash", remote, branch)
}

func (g gitRepo) rebaseAbort() Output { return g.run("rebase", "--abort") }
