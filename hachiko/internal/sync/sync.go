// Package sync is `hachiko sync`: it commits what changes in the repositories
// ~/.config/hachiko/sync lists and pushes them, so the work of an agent editing files
// publishes itself. It runs from the io.github.timche.hachiko-sync LaunchAgent.
//
// It exists for files written by something other than a person at an editor — most often an
// agent writing documentation during a session — and read somewhere else, most often on
// GitHub from another device. The delay between the write and the read is the thing worth
// shortening. Not every repository wants the same treatment: a half-finished paragraph
// published early is harmless in a notes repository, and a half-written install script in a
// configuration repository is a broken machine, which is what push_delay separates.
//
// The predecessor worth naming is a timer running a shell script every minute, and the
// reason to replace it is not latency. What a timer cannot do well is fail: a push that
// cannot land is retried on the next tick with no backoff, and reporting the failure means a
// second unit wired to the first one's failure. So the retry ladder and the message are both
// inside this process.
//
// Polling `git status` rather than watching the tree, which is the one decision that did not
// carry over from the daemon this replaces. A recursive watcher cannot exclude `.git`, so
// every pass's own writes to the index arrive as events and have to be filtered out by path,
// and on Linux a tracked file being read arrives as an event too — `git status` opens every
// one of them, which kept an idle repository running a no-op pass every debounce for ever.
// A `git status` a second is two processes a second for two repositories and asks git itself
// what changed, which is the only answer that was ever wanted. It also means the loop needs
// no timer arithmetic: a deadline is a comparison on the next tick.
//
// No lock, unlike the sweep. launchd runs one of these at a time, git's own index.lock is
// what keeps two gits out of one repository, and a lock this daemon held for the life of the
// process would make `hachiko sync --once` a no-op rather than the explicit "sync now".
package sync

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/logs"
	"github.com/timche/mac-mini/hachiko/internal/self"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// A second, which is as often as a person writing a file would want it noticed and cheap
// enough to do for ever: the debounce is what decides when a burst of writes becomes a
// commit, and this is only how often the question is asked.
const poll = time.Second

// How often the heartbeat the five-minute check reads is written, and how often this process
// looks at the binary behind it. Both are well inside the ten minutes the check gives the
// heartbeat, so a sync that is alive is never read as one that has stopped.
const keepInterval = 30 * time.Second

func Run(cfg config.Config, once, dryFlag bool) error {
	syncCfg, err := config.LoadSync(cfg.SyncConfig, cfg.Home)
	if err != nil {
		return err
	}

	d := &daemon{
		cfg:   cfg,
		sync:  syncCfg,
		deps:  realDeps(cfg),
		store: &Store{Dir: cfg.SyncStateDir},
		// The flag or the config: the flag is a session asking, and the config is how the
		// agent runs until the one line in it says otherwise.
		dry: dryFlag || syncCfg.DryRun(),
		// The heartbeat says the agent is alive, which it is in the config's dry run too; a
		// session's --dry-run beside it is not the agent.
		beat: !dryFlag,
	}

	if err := d.prepare(); err != nil {
		return err
	}

	if once {
		return d.once()
	}
	return d.run()
}

// What every repository is asked about once, before any pass: where it really is, that it is
// a repository at all, and which branch a limited one is synced on.
//
// One that cannot answer is left out and the daemon starts on the rest, because these are
// separate repositories: an `origin/HEAD` nobody set on one of them is no reason for the
// others to stop being published, and a daemon that exited over it would take every one of
// them down and retry the same refusal every ten seconds for as long as the Mac is up. It is
// a failure of its own, said and posted like any other, and nothing starts only when nothing
// could.
func (d *daemon) prepare() error {
	var started []config.SyncRepo

	for _, repo := range d.sync.Repos {
		ready, err := d.ready(repo)
		if err == nil {
			started = append(started, ready)
			continue
		}

		d.say("%s is not being synced: %v", ready.Path, err)
		if !d.dry {
			d.report(ready.Path, &Failure{
				Kind:    failedSetup,
				Subject: ready.Path,
				Remote:  repo.Remote,
				Detail:  err.Error(),
			})
		}
	}

	if len(started) == 0 {
		return fmt.Errorf("not one repository %s names could be synced", d.cfg.SyncConfig)
	}
	d.sync.Repos = started
	return nil
}

// One repository's startup, and the branch a limited one is to be synced on. The path is
// canonicalised before anything else looks at it, so the path in a message and the path in
// the state are the one git is being run in — a home reached through a symlink is
// `/var/folders/x` in the config and `/private/var/folders/x` to git.
func (d *daemon) ready(repo config.SyncRepo) (config.SyncRepo, error) {
	real, err := filepath.EvalSymlinks(repo.Path)
	if err != nil {
		return repo, fmt.Errorf("its path could not be resolved: %w", err)
	}
	repo.Path = real

	if !d.git(repo).isWorkTree() {
		return repo, fmt.Errorf("%s is not a git work tree", real)
	}
	if !repo.Limited() {
		return repo, nil
	}

	// A limited repository is one whose other branches are somebody's work in progress, and
	// with pulling off there is nothing that would ever bring a commit on one of them back to
	// the branch it was meant for. Read here and not per pass, because a repository that is
	// not synced is something to fix, where one that decides this a thousand times a day
	// decides it differently as soon as somebody checks something out.
	branch, err := d.git(repo).defaultBranch(repo.Remote)
	if err != nil {
		return repo, fmt.Errorf("it is limited to paths and the branch to sync it on could not "+
			"be read from %s/HEAD, which `git remote set-head %s --auto` records: %w",
			repo.Remote, repo.Remote, err)
	}

	if d.onBranch == nil {
		d.onBranch = map[string]string{}
	}
	d.onBranch[real] = branch
	return repo, nil
}

type daemon struct {
	cfg   config.Config
	sync  config.Sync
	deps  Deps
	store *Store
	dry   bool
	beat  bool

	// The branch each limited repository is synced on, by canonical path, as its remote's
	// HEAD had it at startup. Nothing for a repository that syncs whole, which goes on being
	// synced on whatever branch it is on.
	onBranch map[string]string

	// The last tick of each repository's loop, which is what the heartbeat is written from:
	// a loop wedged on something no timeout caught stops advancing its own, and the
	// heartbeat is the oldest of them.
	beats []atomic.Int64
}

func (d *daemon) say(format string, args ...any) {
	logs.Logger{Out: d.deps.Log, Now: d.deps.Now, Name: "hachiko sync"}.Say(format, args...)
}

// A line that names the repository it is about, since one log carries every repository's.
func (d *daemon) about(repo string) func(string, ...any) {
	return func(format string, args ...any) {
		d.say("%s: %s", filepath.Base(repo), fmt.Sprintf(format, args...))
	}
}

func (d *daemon) git(repo config.SyncRepo) gitRepo {
	dir := repo.Path
	return gitRepo{
		dir:   dir,
		paths: repo.Paths,
		run:   func(args ...string) Output { return d.deps.Git(dir, args...) },
	}
}

// One pass over every repository, with the delay ignored: this is the explicit "sync now",
// for a backstop or a one-off, and it exits non-zero when anything ended in failure.
func (d *daemon) once() error {
	bad := 0

	for _, repo := range d.sync.Repos {
		if d.dry {
			d.loopFor(repo, 0).dryReport(d.deps.Now())
			continue
		}

		// Through the same record as a pass of the daemon's, so a `--once` run posts the
		// message the daemon would have posted and a conflict it meets pauses the repository
		// rather than leaving the daemon to rebase over it again.
		//
		// One pass has no clock to time a lock against, so one it meets is a line saying to
		// try again rather than a message: a session's git holding the index for a moment is
		// not news, and the daemon is what notices one that stays.
		result := d.passer(repo).run(pushNow)
		if result.Left != "" {
			d.about(repo.Path)("left alone: %s", result.Left)
		}
		d.record(repo, d.git(repo), result)

		if result.Outcome.isFailure() {
			bad++
		}
	}

	if bad > 0 {
		return fmt.Errorf("%d of %d repositories did not sync", bad, len(d.sync.Repos))
	}
	return nil
}

// Returns as soon as any one repository stops being synced, or as soon as the binary behind
// this process has been replaced. Joining the loops in order would hide a dead repository
// behind a live one, and a daemon syncing half of what it was asked to is worse than a dead
// one: this way the process exits non-zero and launchd starts the next.
func (d *daemon) run() error {
	if d.dry {
		d.say("a dry run: it fetches and reads, and commits, rebases, pushes, posts and " +
			"records nothing")
	}

	d.beats = make([]atomic.Int64, len(d.sync.Repos))
	stopped := make(chan error, len(d.sync.Repos)+1)

	for i, repo := range d.sync.Repos {
		d.beats[i].Store(d.deps.Now().Unix())

		go func(i int, repo config.SyncRepo) {
			stopped <- d.loopFor(repo, i).run()
		}(i, repo)

		d.say("syncing %s to %s, pushing once the oldest unpushed commit is %s old",
			repo.Path, repo.Remote, repo.PushDelay)
	}

	go func() { stopped <- d.keep() }()

	return <-stopped
}

// The heartbeat, and the one thing that makes a daemon with no interval of its own notice an
// edit: the wrapper builds a new binary and moves it into place, and nothing would otherwise
// run it until the Mac restarted. Exiting non-zero is what starts it, because the agent
// restarts on a failure alone — a daemon that exited cleanly would stay exited.
func (d *daemon) keep() error {
	binary := self.Looking(d.deps.Self)

	for {
		d.deps.Sleep(keepInterval)

		if d.beat {
			if err := d.store.Beat(d.oldestBeat()); err != nil {
				d.say("the heartbeat could not be written, so the watch will say sync has stopped: %v", err)
			}
		}

		if binary.Replaced() {
			return self.ErrReplaced
		}
	}
}

func (d *daemon) oldestBeat() time.Time {
	oldest := int64(0)
	for i := range d.beats {
		if beat := d.beats[i].Load(); oldest == 0 || beat < oldest {
			oldest = beat
		}
	}
	return time.Unix(oldest, 0)
}

func (d *daemon) passer(repo config.SyncRepo) passer {
	return passer{
		host:         d.cfg.Host,
		repo:         repo,
		onBranch:     d.onBranch[repo.Path],
		subjectModel: d.subjectModel(),
		git:          d.git(repo),
		retry:        d.sync.Retry,
		deps:         d.deps,
		say:          d.about(repo.Path),
	}
}

// Nothing is asked of a model in a dry run, which commits nothing and so has no subject to
// write — and starting a process is not something a run that changes nothing does.
func (d *daemon) subjectModel() string {
	if d.dry {
		return ""
	}
	return d.sync.SubjectModel
}

func (d *daemon) loopFor(repo config.SyncRepo, index int) *loop {
	l := &loop{d: d, repo: repo, index: index, git: d.git(repo), say: d.about(repo.Path)}
	l.p = d.passer(repo)
	return l
}

// loop is one repository being polled. Everything it remembers between ticks is here, and
// nothing in it survives a restart but the pause, which is in the state file.
type loop struct {
	d     *daemon
	repo  config.SyncRepo
	index int
	git   gitRepo
	p     passer
	say   func(string, ...any)

	// The status the last tick read and when it last changed, which together are the
	// debounce: a burst of writes restarts the wait, so it lands as one commit.
	lastStatus string
	changedAt  time.Time

	outstanding bool
	paused      bool

	// The reason the last pass left the tree alone, so the next one that leaves it alone for
	// the same reason says nothing.
	leftFor string

	// When the index was first found locked, and the zero time whenever a pass found it free.
	// A lock held across the grace is one somebody has to remove rather than a git that is
	// still working.
	lockedSince time.Time

	lastSync time.Time
	fetchDue time.Time
	pushDue  time.Time

	// A dry run commits nothing, so the tree stays dirty and every tick would report the
	// same line for ever. The status the last report was about is what stops that.
	saidFor string
	saidAny bool
}

func (l *loop) run() error {
	l.start()

	for {
		l.d.deps.Sleep(poll)

		now := l.d.deps.Now()
		l.d.beats[l.index].Store(now.Unix())
		l.tick(now)
	}
}

func (l *loop) start() {
	now := l.d.deps.Now()

	// A repository paused before the restart is one no pass may run on: the conflict is
	// still there and rebasing over it again would be the message the pause exists to stop.
	// Work written in it is still committed, by the tick that finds the tree dirty.
	if at, paused := l.d.store.PausedAt(l.repo.Path); paused {
		l.paused, l.lastSync, l.fetchDue = true, now, now.Add(l.repo.FetchInterval)
		l.say("pulling is still paused on the rebase that conflicted %s ago, and resumes when HEAD or %s moves",
			wording.DurationPhrase(now.Sub(time.Unix(at.Since, 0))), l.repo.Remote)
		return
	}

	// A catch-up pass before the first tick: a commit that fell due while the Mac was off
	// goes out now rather than waiting for the next thing anybody writes.
	l.pass(now)
}

func (l *loop) tick(now time.Time) {
	status := l.git.status()
	if status != l.lastStatus {
		l.lastStatus, l.changedAt = status, now
	}

	settled := status != "" && !now.Before(l.changedAt.Add(l.repo.Debounce))
	if l.d.dry && settled && status == l.saidFor {
		settled = false
	}

	fetchTick := l.repo.Pull && l.repo.FetchInterval > 0 &&
		!l.fetchDue.IsZero() && !now.Before(l.fetchDue)

	// Both the fetch and the recheck stop while a repository is paused: re-running a rebase
	// that can only conflict again is a message a minute about one that already has. A
	// settled change still syncs, since that is new work rather than the same two sides.
	if l.paused {
		if !fetchTick {
			if settled {
				l.pass(now)
			}
			return
		}
		if !l.rearm(now) {
			l.fetchDue = now.Add(l.repo.FetchInterval)
			return
		}
	}

	recheckTick := l.outstanding && !now.Before(l.lastSync.Add(l.repo.Recheck))
	pushTick := !l.pushDue.IsZero() && !now.Before(l.pushDue)

	if settled || fetchTick || recheckTick || pushTick {
		l.pass(now)
	}
}

func (l *loop) pass(now time.Time) {
	if l.d.dry {
		l.dryReport(now)
	} else {
		// A lock is a race until it has lasted longer than one could, and the loop is what
		// holds the clock: the pass itself sees one git call's output and nothing before it.
		l.p.lockedLong = !l.lockedSince.IsZero() && !now.Before(l.lockedSince.Add(lockedGrace))

		result := l.p.run(pushWhenDue)
		l.outstanding = result.Outcome.isFailure()

		switch {
		case !result.Locked:
			l.lockedSince = time.Time{}
		case l.lockedSince.IsZero():
			l.lockedSince = now
		}

		l.d.record(l.repo, l.git, result)
		l.paused = result.Failure != nil && result.Failure.Kind == failedConflict

		// Once per reason, not once a second: a tree left alone stays dirty, so every tick
		// from here on is another pass that leaves it alone for the same reason.
		if result.Left != "" && result.Left != l.leftFor {
			l.say("left alone: %s", result.Left)
		}
		l.leftFor = result.Left
	}

	l.lastSync = now
	l.fetchDue = now.Add(l.repo.FetchInterval)

	// A deadline left in the past is a deadline woken on every tick for ever, so it is
	// recomputed whether or not this pass was the one it was for.
	l.pushDue = time.Time{}
	if left := l.p.pushWait(); left > 0 {
		l.pushDue = now.Add(left)
	}
	l.saidFor = l.lastStatus
}

// Whether a paused repository may be pulled again. A fetch that does not rebase, and then
// the two shas against the ones recorded at the conflict: either of them moving means
// somebody has been here, and nothing else does.
func (l *loop) rearm(now time.Time) bool {
	at, paused := l.d.store.PausedAt(l.repo.Path)
	if !paused {
		l.paused = false
		return true
	}

	if out := l.git.fetch(l.repo.Remote); !out.OK {
		l.say("paused, and the remote could not be fetched to see whether it has moved: %s",
			oneLine(short(out.Text)))
		return false
	}

	head, upstream := l.git.head(), l.git.upstreamHead()
	if head == at.Head && upstream == at.Upstream {
		return false
	}

	l.say("HEAD or %s has moved since the rebase conflicted, so pulling resumes", l.repo.Remote)
	l.paused = false

	if err := l.d.store.Change(func(state *State) { delete(state.Paused, l.repo.Path) }); err != nil {
		l.say("the pause could not be cleared from the state: %v", err)
	}
	return true
}

// A pass's failure, the two listings its message quotes, and the pause a conflict leaves
// behind, all written down together: the message and the state have to agree about what is
// stuck, and a daemon that restarts may not rebase over the conflict that stopped it.
func (d *daemon) record(repo config.SyncRepo, git gitRepo, result Result) {
	if result.Failure != nil {
		result.Failure.Unpushed = git.unpushed()
		result.Failure.Uncommitted = statusPaths(git.status())

		if result.Failure.Kind == failedConflict {
			at := Paused{
				Since:    d.deps.Now().Unix(),
				Head:     git.head(),
				Upstream: git.upstreamHead(),
			}
			err := d.store.Change(func(state *State) {
				if state.Paused == nil {
					state.Paused = map[string]Paused{}
				}
				state.Paused[repo.Path] = at
			})
			if err != nil {
				d.say("the pause on %s could not be written down, so a restart would rebase over the conflict again: %v",
					repo.Path, err)
			}
		}
	}

	d.report(repo.Path, result.Failure)
}

// What a dry run says it would do. It fetches, because that is how it can say how far behind
// the remote a repository is, and it does nothing else at all: no add, no commit, no rebase,
// no push, no state and no message.
func (l *loop) dryReport(now time.Time) {
	behind := 0
	if l.repo.Pull {
		if out := l.git.fetch(l.repo.Remote); !out.OK {
			l.say("the remote could not be fetched: %s", oneLine(short(out.Text)))
		} else {
			behind = len(l.git.behind())
		}
	}

	paths := statusPaths(l.git.status())
	unpushed := len(l.git.unpushed())
	left := l.p.pushWait()

	switch {
	case len(paths) > 0 && l.d.sync.SubjectModel != "":
		l.say("would commit `%s`, or the subject %s writes in its place",
			commitSubject(paths), l.d.sync.SubjectModel)
	case len(paths) > 0:
		l.say("would commit `%s`", commitSubject(paths))
	case !l.saidAny:
		l.say("nothing is uncommitted")
	}

	switch {
	case left > 0:
		l.say("would hold %s for another %s", commits(unpushed), left.Round(time.Second))
	case unpushed > 0:
		l.say("would push %s to %s", commits(unpushed), l.repo.Remote)
	}
	if behind > 0 {
		l.say("would rebase onto %s from %s", commits(behind), l.repo.Remote)
	}

	l.saidAny = true
	_ = now
}

// The paths in `git status --porcelain=v1 -z`, which is what a dry run reports in place of
// the index it may not touch. A rename or a copy is two records, the new path and then the
// old one, and only the new one is a path that changed.
func statusPaths(status string) []string {
	records := strings.Split(status, "\x00")
	var paths []string

	for i := 0; i < len(records); i++ {
		record := records[i]
		if len(record) < 4 {
			continue
		}
		paths = append(paths, record[3:])

		if record[0] == 'R' || record[0] == 'C' {
			i++
		}
	}
	return paths
}
