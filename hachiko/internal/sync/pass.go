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
	at := p.git.oldestUnpushedAt()
	if at.IsZero() {
		return 0
	}

	left := p.repo.PushDelay - p.deps.Now().Sub(at)
	if left <= 0 {
		return 0
	}
	return left
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

	// Without an upstream the branch has never been published, so there is always something
	// to push even when `@{upstream}..HEAD` cannot be asked.
	unpushed := setUpstream || len(p.git.unpushed()) > 0

	// With pulling on, an idle repository is still worth a pass: the remote may have moved
	// even though nothing here did.
	if !p.repo.Pull && !dirty && !unpushed {
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
			return p.failure(NeedsHuman, failedCommit, "git add -A failed: "+out.Text)
		}
		if staged := p.git.stagedFiles(); len(staged) > 0 {
			subject = p.subject(staged)
			if out := p.git.commit(subject); !out.OK {
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
				if len(p.git.unpushed()) == 0 {
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

		if !setUpstream && len(p.git.unpushed()) == 0 {
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
