// Package update is how a Mac that Claude Code runs on its own keeps itself up to date:
// once a night it moves mise, the tools mise manages, Homebrew's formulae and casks, herdr
// and oh-my-zsh forward, puts the proxy daemon back on the node a `mise up` just replaced,
// restarts tailscaled when its formula moved, and says what is waiting from Apple. Nobody
// is at this Mac to run any of it, and a tool that is a year behind is the kind of thing
// nobody notices until it breaks.
//
// It is the one thing here that moves a version. `install.sh` deliberately does not — it
// applies the Brewfile with `--no-upgrade` and installs mise's tools `--locked`, so a
// re-run after a pull installs what is missing and moves nothing.
//
// The five-minute check is what starts it, through Nightly: there is no second LaunchAgent,
// because an agent that fires once a day on a Mac that may be asleep at four in the morning
// is an agent that skips the day. So the schedule is read from a stamp rather than from
// launchd — past the hour, and the last run that finished was before that hour came round —
// and a Mac that was off at four updates on its next check instead.
//
// What Nightly starts is a process of its own rather than the rest of this sweep. An
// upgrade takes tens of minutes, the check it would be running inside has five, and
// shibuya reports this Mac offline a quarter of an hour after the last check-in — so an
// update held inline would page Tim about a Mac that is busy updating. The lock here is
// what keeps the next check from starting a second one.
package update

import (
	"errors"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/logs"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
)

// How long an update's lock is held before the next one takes it over. Two hours is far
// longer than a night's upgrading has ever taken and short enough that a run killed halfway
// does not cost a week of updates.
const lockStale = 2 * time.Hour

// Nightly is the five-minute check's half of it: start the update when it is owing, and
// otherwise do nothing at all. A dry run says whether it would.
func Nightly(cfg config.Config, dry bool) error {
	return nightly(cfg, realDeps(cfg), dry)
}

func nightly(cfg config.Config, deps Deps, dry bool) error {
	now := deps.Now()
	last := LastRun(cfg.UpdateStamp())

	if !Due(now, last, cfg.UpdateHour) {
		return nil
	}

	say := logs.Logger{Out: deps.Log, Now: deps.Now, Name: logName}.Say

	if dry {
		say("the nightly update is owing, so this check would start one")
		return nil
	}

	if err := deps.Spawn("update"); err != nil {
		say("the nightly update is owing and could not be started, so the next check tries again: %v", err)
		return err
	}

	say("started the nightly update")
	return nil
}

// Due says whether an update is owing: it is past the hour updates run at, and the last one
// that finished did so before that hour came round today. So one run a day, counted from
// when it finished rather than from when it was meant to start, and a Mac that was off or
// asleep at the hour catches up on its next check.
func Due(now, last time.Time, hour int) bool {
	at := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	if now.Before(at) {
		return false
	}
	return last.Before(at)
}

const logName = "hachiko update"

// Run is the update itself: every step in order, each one reported. A dry run takes the
// readings and says what it would move, and changes nothing — no version, no commit, no
// state, no lock, and nothing sent.
func Run(cfg config.Config, dry bool) error {
	return (&updater{
		cfg:   cfg,
		deps:  realDeps(cfg),
		store: Store{Dir: cfg.UpdateStateDir},
		hand:  Handoff{Dir: cfg.UpdateStateDir, WatchDir: cfg.StateDir},
		dry:   dry,
	}).run()
}

type updater struct {
	cfg   config.Config
	deps  Deps
	store Store
	hand  Handoff
	dry   bool

	// The things one published advisory named, when this is the out-of-cycle run rather than
	// the night's: empty on a night's run, and what makes the difference between a run that is
	// answerable for every step and one that is answerable for two. See security.go.
	targets []string

	// What moved and what could not be done, the second of which the next five-minute check
	// raises as an incident.
	moved    []Move
	failures []Failure

	// And what is waiting on Tim, read from what this Mac is rather than from what tonight
	// did: an update nobody has installed yet is still waiting tomorrow, so it is in every
	// summary until it is dealt with rather than in the one on the night it appeared.
	waiting []string
}

// Move is one thing that is now at a different version from the one it was at.
type Move struct {
	What string
	From string
	To   string

	// What else that version moving took with it, said after the versions rather than on a
	// line of its own: a daemon put back on the path a new version is at is news about that
	// version, and a reader who has to join the two up has been given a puzzle instead.
	Qualifier string
}

func (u *updater) say(format string, args ...any) {
	logs.Logger{Out: u.deps.Log, Now: u.deps.Now, Name: logName}.Say(format, args...)
}

func (u *updater) run() error {
	now := u.deps.Now()

	if !u.dry {
		lock, takenFrom, err := statedir.LockDir(u.cfg.UpdateLock, lockStale, now, "an earlier update")

		switch {
		case errors.Is(err, statedir.ErrLockHeld):
			u.say("another update is running, so this one does nothing")
			return nil
		case err != nil:
			u.say("the lock %s could not be taken, so this update runs without one: %v", u.cfg.UpdateLock, err)
		}

		if lock != nil {
			defer lock.Release()
		}
		if takenFrom != "" {
			u.say("taking over a lock left behind by %s", takenFrom)
		}
	}

	state, err := u.store.Load()
	if err != nil {
		u.say("%v", err)
	}

	// The order is the order the dependencies run in. mise first, because the tools it
	// manages are what portless's daemon is pointed at; the lockfiles straight after the
	// upgrade that rewrote them, so a step failing later does not keep them off main;
	// Homebrew's packages next, with the tailscaled restart behind it, since whether it is
	// needed is what the upgrade found; and Apple's last, which is the one nothing here can
	// install.
	u.miseSelf()
	tools := u.miseTools()
	u.lockfiles(tools)
	u.portless(tools)
	u.tailscale(u.brew())
	u.herdrServer(u.herdr())
	u.ohMyZsh()
	u.macOS()

	if u.dry {
		u.sayWaiting()
		u.say("dry run over")
		return nil
	}

	u.report(state, now)

	if err := u.store.Save(state); err != nil {
		u.say("the state could not be written, so what has been reported is forgotten: %v", err)
	}

	// Last, and whether or not every step worked: what it says is that an update ran to the
	// end, which is what keeps the five-minute check from starting another one every five
	// minutes for the rest of the day.
	if err := u.store.Stamp(now); err != nil {
		u.say("the last-run stamp could not be written, so the next check starts another update: %v", err)
	}
	return nil
}

func (u *updater) move(what, from, to string) {
	if from == "" || to == "" || from == to {
		return
	}
	u.moved = append(u.moved, Move{What: what, From: from, To: to})
}

// On the first of these that moved, in the order they are given: the proxy daemon goes back
// because node or portless moved, and the version to read it beside is portless's own where
// both did.
func (u *updater) qualify(qualifier string, candidates ...string) {
	for _, what := range candidates {
		for i := range u.moved {
			if u.moved[i].What == what {
				u.moved[i].Qualifier = qualifier
				return
			}
		}
	}
}

func (u *updater) waits(item string) { u.waiting = append(u.waiting, item) }

// A dry run says what is waiting rather than sending it, the readings being the whole of what
// one is for.
func (u *updater) sayWaiting() {
	for _, item := range u.waiting {
		u.say("%s", item)
	}
}

func (u *updater) failed(f Failure) {
	u.say("%s", f.line())
	u.failures = append(u.failures, f)
}
