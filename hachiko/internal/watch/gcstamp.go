package watch

import (
	"fmt"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

const labelLastSweep = "Last sweep"

// Whether `hachiko gc` is still sweeping. It writes a stamp at the end of every run it
// finishes, and the age of that stamp is the whole of what the watch knows about it — which
// is the right amount: the sweep reports its own failures, and what nothing can report
// about itself is not running at all.
//
// Not an incident and no on-call session. Every incident the watch raises is a question
// about the machine that an agent can read its way to an answer to; this one has a single
// answer already written down, which is to load the agent again, and it is Tim's. A session
// opened for it would spend a Claude login working out what the message below already says.
//
// Nothing is said at all on a Mac with no gc LaunchAgent installed, which is the only way to
// tell a Mac that was never meant to be sweeping from one that has stopped: a fresh Mac has
// no stamp because nothing has ever swept, and alerting on that would fire on every machine
// this repository has provisioned, every five minutes, until install.sh reached the agent.
// Where the plist is there and no stamp is, the clock runs from the plist instead — so an
// agent that was installed and has never run is caught rather than excused, and a re-render
// of it gives the first sweep an hour to happen in.
func (s sweeper) gcStopped(state *statedir.State, now time.Time) {
	since, installed := s.deps.GCLastRun()
	if !installed {
		return
	}

	age := now.Sub(since)
	stale := age > s.cfg.GCStaleAfter
	was := state.GCStale != 0

	switch {
	case stale == was:
		return

	case s.dry && stale:
		s.say("would report that the worktree sweep has not run for %s", hmStr(age))
		return
	case s.dry:
		s.say("would report that the worktree sweep is running again")
		return

	case stale:
		s.say("the worktree sweep has not run for %s, so nothing is cleaning up after a removed worktree", hmStr(age))

		message := wording.Lead(wording.MarkerDegraded,
			fmt.Sprintf("The worktree sweep has not run for %s", wording.DurationPhrase(age))).
			Field(labelLastSweep, wording.TimePhrase(since, now)).
			Field(wording.LabelWhy, "Nothing is cleaning up after a removed worktree: its compose "+
				"project, the volumes that project made, the processes left in its folder and its "+
				"scratch space all stay.").
			Can(s.loadTheSweep()).
			About("", s.cfg.Host).
			String()

		thread, err := s.deps.Send(discord.Outgoing{Text: message, OpenThread: s.cfg.GCLabel()})
		if err != nil {
			s.say("the message about the worktree sweep did not send and is left to the next check: %v", err)
			return
		}
		state.GCStale, state.GCThread = now.Unix(), thread

	default:
		s.say("the worktree sweep is running again")

		message := wording.Lead(wording.MarkerRecovered, "The worktree sweep is running again").
			Field(labelLastSweep, wording.TimePhrase(since, now)).
			About("", s.cfg.Host).
			String()

		if _, err := s.deps.Send(discord.Outgoing{Text: message, Thread: state.GCThread}); err != nil {
			s.say("the message saying the worktree sweep is back did not send and is left to the next check: %v", err)
			return
		}
		state.GCStale, state.GCThread = 0, ""
	}
}

// The one command that starts it again, with no password behind it: the agent is this
// account's own. Spelled from the plist's own name, so it cannot name an agent other than
// the one whose absence decided whether to look at all.
func (s sweeper) loadTheSweep() string {
	return "**You can run:** " +
		wording.CodeSpan(fmt.Sprintf("launchctl kickstart -k gui/%d/%s", s.deps.Getuid(), s.cfg.GCLabel())) +
		" — it sweeps at once, and says why it could not in " +
		wording.CodeSpan("~/Library/Logs/hachiko-gc.log") + "."
}
