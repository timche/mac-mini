package sync

import (
	"fmt"
	"strings"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
)

// The six things one pass can come to, distinct because each one is a different thing to do
// next: nothing was needed, another machine's commits came down, this machine's went up, the
// push is waiting out its delay, a person has to look at it, and the remote would not answer.
type Outcome int

const (
	Nothing Outcome = iota
	Pulled
	Pushed
	Held
	NeedsHuman
	Unreachable
)

func (o Outcome) isFailure() bool { return o == NeedsHuman || o == Unreachable }

// Result is what the loop reads off a pass: the outcome, and the one failure to report when
// there was one.
type Result struct {
	Outcome Outcome
	Failure *Failure
	Subject string

	// Why the pass left the tree alone, for the caller to say. Said by the caller rather than
	// here because the reason outlasts the pass: a tree nobody may commit in stays dirty, so
	// the next tick a second later finds the same one, and the loop says it once.
	Left string

	// Whether what stopped it was git's own index.lock, which is the one reason the caller
	// has to time: a lock is a race until it has lasted long enough to be a fault.
	Locked bool
}

// Whether this pass may hold the push back. `hachiko sync --once` is the explicit "sync
// now", so it never does.
type pushMode int

const (
	pushWhenDue pushMode = iota
	pushNow
)

type passer struct {
	host string
	repo config.SyncRepo

	// The one branch this repository is synced on, or "" for one synced on whatever branch it
	// is on. Only a limited repository has one; see sync.go.
	onBranch string

	// Which model writes the commit subject, or "" for the file list. Off in a dry run
	// whatever the config says, since a dry run reaches nothing.
	subjectModel string

	// Whether the index has already been locked for longer than a race could last, which is
	// the caller's to measure: a pass knows only what this one git call said.
	lockedLong bool

	git   gitRepo
	retry config.SyncRetry
	deps  Deps
	say   func(format string, args ...any)
}

// How much longer the oldest unpushed commit has to wait before push_delay is up, and zero
// when nothing is waiting or the wait is over.
func (p passer) pushWait() time.Duration {
	if p.repo.PushDelay == 0 {
		return 0
	}
	at := p.oldestMineAt()
	if at.IsZero() {
		return 0
	}

	left := p.repo.PushDelay - p.deps.Now().Sub(at)
	if left <= 0 {
		return 0
	}
	return left
}

// The unpushed commits this pass is for: every one of them where the whole repository is
// synced, and sync's own where it is limited to paths. The difference is a branch a session
// commits to as well — pushing what it has committed but not pushed would publish work it is
// still shaping, and a commit it then amends or squashes is one it can no longer push without
// forcing. So nothing of a session's starts a push; what it does do is go up underneath
// sync's when there is one, since a push publishes the branch rather than a commit.
func (p passer) mine() []string {
	if p.repo.Limited() {
		return p.git.ownUnpushed()
	}
	return p.git.unpushed()
}

func (p passer) oldestMineAt() time.Time {
	if p.repo.Limited() {
		return p.git.oldestOwnUnpushedAt()
	}
	return p.git.oldestUnpushedAt()
}

// Whether this pass has anything to push: a commit of sync's own, or — only where the whole
// repository is synced — a branch that has never been published at all. Publishing a branch
// nobody published is not something a limited repository does, there being nothing of sync's
// on it to publish.
func (p passer) somethingToPush(setUpstream bool) bool {
	if len(p.mine()) > 0 {
		return true
	}
	return setUpstream && !p.repo.Limited()
}

func (p passer) run(mode pushMode) Result {
	result := p.try(mode)

	if result.Outcome.isFailure() && result.Failure != nil {
		p.say("%s", result.Failure.line())
	}
	return result
}

func (p passer) try(mode pushMode) Result {
	branch, err := p.git.branch()
	if err != nil {
		return p.unreachable("the branch could not be read: " + err.Error())
	}

	// A commit in the middle of somebody's rebase or merge would land in it, and a push of a
	// detached HEAD names no branch at all. A limited repository's commit is a partial one,
	// which git refuses during a merge outright.
	if why := p.git.busy(branch); why != "" {
		return Result{Outcome: Nothing, Left: why}
	}

	// A commit on a branch somebody checked out here would carry files that are nobody's
	// work in progress onto it, and the push would publish that branch; with pulling off,
	// nothing would ever bring them back to the branch they were meant for.
	if p.onBranch != "" && branch != p.onBranch {
		return Result{Outcome: Nothing, Left: fmt.Sprintf(
			"%s is checked out, and this repository is synced on %s alone", branch, p.onBranch)}
	}

	setUpstream := !p.git.hasUpstream()
	dirty := p.git.status() != ""

	// With pulling on, an idle repository is still worth a pass: the remote may have moved
	// even though nothing here did.
	if !p.repo.Pull && !dirty && !p.somethingToPush(setUpstream) {
		return Result{Outcome: Nothing}
	}

	// Committing before the retry loop is what makes the rebase below safe: it runs over a
	// checkpoint rather than over half-written work.
	//
	// Only on a dirty tree, because `git add -A` is fatal on a pathspec that matches nothing
	// at all — and a status that is empty is a repository with nothing staged either, since
	// the index is half of what the status reads.
	subject := ""
	if dirty {
		if out := p.git.addAll(); !out.OK {
			if isLocked(out.Text) {
				return p.locked(out.Text)
			}
			return p.failure(NeedsHuman, failedCommit, "git add -A failed: "+out.Text)
		}
		if staged := p.git.stagedFiles(); len(staged) > 0 {
			subject = p.subject(staged)
			if out := p.git.commit(subject); !out.OK {
				if isLocked(out.Text) {
					return p.locked(out.Text)
				}
				return p.failure(NeedsHuman, failedCommit, "the commit failed: "+out.Text)
			}
		}
	}

	waiting := time.Duration(0)
	if mode == pushWhenDue {
		waiting = p.pushWait()
	}
	if waiting > 0 {
		return p.hold(branch, subject, waiting)
	}

	return p.pushLoop(branch, subject, setUpstream)
}

// Holding the push is no reason to fall behind the remote: what waits is publishing this
// machine's work, not taking another's down.
func (p passer) hold(branch, subject string, left time.Duration) Result {
	if p.repo.Pull {
		switch rebased := p.fetchAndRebase(branch); rebased.kind {
		case rebaseConflict:
			return p.failure(NeedsHuman, failedConflict, rebased.text)
		case rebaseFetchFailed:
			p.say("the remote could not be fetched while the push waits: %s", rebased.text)
		default:
			if rebased.commits > 0 {
				p.say("%s came down from %s", commits(rebased.commits), p.repo.Remote)
			}
		}
	}

	if subject != "" {
		p.say("committed `%s`, pushing in %s", subject, left.Round(time.Second))
	}
	return Result{Outcome: Held, Subject: subject}
}

func (p passer) pushLoop(branch, subject string, setUpstream bool) Result {
	delay, pulled, last := p.retry.Base, 0, ""

	for attempt := 1; attempt <= p.retry.Attempts; attempt++ {
		// An unpublished branch has no upstream to fetch against and nothing upstream to
		// rebase onto.
		if p.repo.Pull && !setUpstream {
			rebased := p.fetchAndRebase(branch)

			switch rebased.kind {
			case rebaseConflict:
				return p.failure(NeedsHuman, failedConflict, rebased.text)

			case rebaseFetchFailed:
				// An offline machine with nothing of its own to send is not a failure at all:
				// retrying the ladder and posting about it every minute would make ordinary
				// idleness look broken. The same rule as a docker that is down.
				if !p.somethingToPush(setUpstream) {
					p.say("the remote could not be fetched, and there is nothing to push: %s", rebased.text)
					return Result{Outcome: Nothing}
				}
				last = rebased.text
				p.backOff(attempt, &delay)
				continue

			default:
				pulled += rebased.commits
			}
		}

		if !p.somethingToPush(setUpstream) {
			if pulled > 0 {
				p.say("%s came down from %s", commits(pulled), p.repo.Remote)
				return Result{Outcome: Pulled}
			}
			// No log line: with the fetch interval on, this is the common case and it happens
			// every minute.
			return Result{Outcome: Nothing}
		}

		out := p.git.push(p.repo.Remote, branch, setUpstream)

		// Kept even with the fetch above: something can land between the two.
		if !out.OK && isRejection(out.Text) {
			if !p.repo.Pull {
				return p.failure(NeedsHuman, failedPush, out.Text)
			}

			pull := p.git.pullRebase(p.repo.Remote, branch)
			if !pull.OK {
				p.abort()
				return p.failure(NeedsHuman, failedConflict, pull.Text)
			}
			setUpstream = false

			// The push has to follow the rebase here rather than on the next attempt, which on
			// the last one would never come.
			out = p.git.push(p.repo.Remote, branch, false)
			if !out.OK && isRejection(out.Text) {
				// Rejected again on a freshly rebased branch: something else is writing to it,
				// and retrying would only lose whichever race.
				return p.failure(NeedsHuman, failedPush, out.Text)
			}
		}

		if out.OK {
			if subject != "" {
				p.say("pushed `%s`", subject)
			} else {
				p.say("pushed work that was already committed")
			}
			return Result{Outcome: Pushed, Subject: subject}
		}
		last = out.Text
		p.backOff(attempt, &delay)
	}

	return p.failure(Unreachable, failedPush, last)
}

func (p passer) backOff(attempt int, delay *time.Duration) {
	if attempt >= p.retry.Attempts {
		return
	}
	p.deps.Sleep(*delay)

	if *delay *= 2; *delay > p.retry.Max {
		*delay = p.retry.Max
	}
}

type rebaseKind int

const (
	rebaseDone rebaseKind = iota
	rebaseFetchFailed
	rebaseConflict
)

type rebased struct {
	kind    rebaseKind
	commits int
	text    string
}

func (p passer) fetchAndRebase(branch string) rebased {
	if out := p.git.fetch(p.repo.Remote); !out.OK {
		return rebased{kind: rebaseFetchFailed, text: out.Text}
	}

	behind := len(p.git.behind())
	if behind == 0 {
		return rebased{}
	}

	if pull := p.git.pullRebase(p.repo.Remote, branch); !pull.OK {
		p.abort()
		return rebased{kind: rebaseConflict, text: pull.Text}
	}
	return rebased{commits: behind}
}

// Leave no half-finished rebase behind: whoever fixes this by hand should find an ordinary
// working tree.
func (p passer) abort() {
	if out := p.git.rebaseAbort(); !out.OK {
		p.say("the rebase could not be aborted, so the tree is left mid-rebase: %s", out.Text)
	}
}

// git's `index.lock`, which is one git refusing to work in a tree another one is already
// writing to. In a repository a session shares with sync that is the ordinary case rather
// than a fault: a session runs git a few times a second, each lock is held for a fraction of
// one, and the next pass a second later finds it gone. So a locked index is a pass that does
// nothing and tries again, and only a lock that has outlasted the grace below is reported —
// which is the shape of a real one, left behind by a git somebody killed.
func (p passer) locked(text string) Result {
	if p.lockedLong {
		return p.failure(NeedsHuman, failedCommit,
			"git's index has stayed locked by something else in the tree: "+text)
	}
	return Result{
		Outcome: Nothing,
		Locked:  true,
		Left:    "git's index is locked by something else in the tree, so the next pass tries again",
	}
}

// How long a lock has to last before it is somebody's to remove rather than a race to wait
// out. A minute is far longer than any git a session runs holds the index, and far shorter
// than it would take for a tree that is not being committed to matter.
const lockedGrace = time.Minute

func isLocked(text string) bool {
	return strings.Contains(strings.ToLower(text), "index.lock")
}

func (p passer) failure(outcome Outcome, kind, text string) Result {
	return Result{
		Outcome: outcome,
		Failure: &Failure{Kind: kind, Subject: p.git.dir, Remote: p.repo.Remote, Detail: text},
	}
}

func (p passer) unreachable(text string) Result {
	return p.failure(Unreachable, failedPush, text)
}

// Deliberately not a bare `rejected`: `! [remote rejected]` is a hook or a protected branch
// refusing the push, which no amount of rebasing fixes.
func isRejection(text string) bool {
	text = strings.ToLower(text)

	for _, marker := range []string{"[rejected]", "fetch first", "non-fast-forward"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func commits(n int) string {
	if n == 1 {
		return "1 commit"
	}
	return fmt.Sprintf("%d commits", n)
}
