package update

import (
	"fmt"
	"strings"

	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// The lockfiles a `mise up` rewrites that belong to this checkout, as pathspecs inside it.
// `~/.config/mise/mise.lock` and `~/.config/mise/locks` are symlinks into it, so upgrading
// the global tools writes the checkout's own files — and a lockfile that stayed on this Mac
// would be a rebuilt Mac installing whatever `latest` meant on the day instead of what runs
// here.
//
// Every git command below ends in these paths. Nothing else in the tree is added, committed
// or pushed: sessions work in this checkout, and what one of them has staged or left dirty
// is not this step's to publish.
var lockfiles = []string{
	"mise.lock",
	"home/.config/mise/mise.lock",
	"home/.config/mise/locks",
}

// The commits are this Mac's own: it is the one machine that resolves these versions, and
// the repository's rule is that the thing which made a change commits it.
func (u *updater) lockfiles(tools []string) {
	git := func(args ...string) Output {
		return u.deps.Run(Command{
			Name:    "git",
			Args:    append([]string{"-C", u.cfg.MachineDir}, args...),
			Timeout: gitTimeout,
		})
	}

	// Without the optional lock, because a session's git may be working in this tree and a
	// read that refreshes the index takes index.lock to do it.
	status := git(u.limit("--no-optional-locks", "status", "--porcelain=v1")...)
	if !status.OK {
		u.failed(Failure{Step: stepLockfiles, Detail: status.Text})
		return
	}
	if strings.TrimSpace(status.Stdout) == "" {
		return
	}

	if u.dry {
		u.say("the lockfiles moved and would be committed and pushed")
		return
	}

	// A commit in the middle of somebody's rebase or merge would land inside it, and a
	// partial commit is what git refuses during a merge outright. Said rather than reported:
	// the lockfiles are still on this Mac and the next night tries again.
	branch := strings.TrimSpace(git("rev-parse", "--abbrev-ref", "HEAD").Stdout)
	if why := busy(git, branch); why != "" {
		u.say("the lockfiles moved and %s, so they are left uncommitted", why)
		return
	}

	// Only on the branch the repository lands on, which is what every session here commits
	// to. A lockfile committed onto somebody's branch would go up with their pull request.
	main := defaultBranch(git)
	if main == "" || branch != main {
		u.say("the lockfiles moved and %s is checked out rather than %s, so they are left uncommitted",
			branch, main)
		return
	}

	if out := git(u.limit("add", "-A")...); !out.OK {
		u.failed(Failure{Step: stepLockfiles, Detail: out.Text})
		return
	}

	// `-- <paths>` is git's own `--only`: it commits the working-tree state of those paths
	// and leaves whatever a session has staged elsewhere staged and uncommitted.
	subject := lockSubject(tools)
	if out := git(u.limit("commit", "-m", subject)...); !out.OK {
		u.failed(Failure{Step: stepLockfiles, Detail: out.Text})
		return
	}

	if out := git("push", "origin", branch); !out.OK {
		u.failed(Failure{
			Step:   stepLockfiles,
			Detail: out.Text,
			Action: "**Needs you:** the commit is here and nothing else will push it — " +
				wording.CodeSpan("git -C ~/.mac-mini push") + " once whatever refused it is dealt with.",
		})
		return
	}
	// Said and not reported: a lockfile following the version it locks onto main is this step
	// having worked, which is not news the summary is read for.
	u.say("the lockfiles mise rewrote are committed and on %s", branch)
}

// Every command that reads or writes the tree, limited to the lockfiles and nothing else.
func (u *updater) limit(args ...string) []string {
	return append(append(args, "--"), lockfiles...)
}

func busy(git func(args ...string) Output, branch string) string {
	if branch == "HEAD" || branch == "" {
		return "HEAD is detached, so a rebase or a bisect is under way"
	}
	for _, ref := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD"} {
		if git("rev-parse", "-q", "--verify", ref).OK {
			return ref + " is there, so a merge, a cherry-pick or a revert is under way"
		}
	}
	return ""
}

// The branch the remote lands on, as this clone's own `refs/remotes/origin/HEAD` has it.
// Read locally, so it costs no network and does not change when somebody checks something
// out here.
func defaultBranch(git func(args ...string) Output) string {
	out := git("symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if !out.OK {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(out.Stdout), "origin/")
}

// What the commit is called. The tools rather than their versions, which are in the diff:
// a subject is read in a list of them, and one line of it is all there is room for.
func lockSubject(names []string) string {
	if subject := "mise up moves " + strings.Join(names, ", "); len(names) > 0 && len(subject) <= 72 {
		return subject
	}
	if len(names) > 0 {
		return fmt.Sprintf("mise up moves %d of the tools it locks", len(names))
	}
	return "mise up rewrites the lockfiles"
}
