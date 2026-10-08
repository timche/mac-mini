package watch

import (
	"fmt"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

const (
	labelLastSweep = "Last sweep"
	labelLastBeat  = "Last heartbeat"

	// As a message spells it rather than as this process resolved it: `~` is shorter than
	// the account name and the same on every Mac here. Each agent's own log file is written
	// out below rather than derived from its label, because a message that named the label
	// instead told Tim to tail a file that does not exist.
	logsRoot = "~/Library/Logs/"
)

// liveness is one of the agents that are meant to be running on a Mac with nobody at it: the
// worktree sweep on its ten-minute interval, and sync and the Discord listener, which never
// finish a run at all. Each of them writes a file as it goes, and the age of that file is
// the whole of what the watch knows about it — which is the right amount, because each
// reports its own failures and what nothing can report about itself is not running.
//
// None of the three is an incident and none opens an on-call session. Every incident the
// watch raises is a question about the machine that an agent can read its way to an answer
// to; each of these has one answer already written down, which is to load the agent again,
// and it is Tim's. A session opened for it would spend a Claude login working out what the
// message below already says.
//
// Nothing is said at all where the agent is not there to be running, which is the only way
// to tell a Mac that was never meant to run one from a Mac where it has stopped: a fresh Mac
// has no stamp because nothing has ever run, and alerting on that would fire on every
// machine this repository provisions, every five minutes, until install.sh reached the
// agent.
type liveness struct {
	// How the state remembers this agent, and what the log and the messages call it.
	key     string
	subject string
	title   string

	// What the message says the file's age is the age of, what is going unmade while the
	// agent is not running, and what that costs.
	lastLabel string
	meanwhile string
	why       string

	// The lead for the one case that is neither stopped nor running: the agent is not there
	// to be running any more. Said only where an alert was open, since an agent that was
	// never installed is not news.
	gone     string
	saidGone string

	// The one command that starts it again, with no password behind it — the agents are this
	// account's own — and the log that says why it could not. The label is spelled from the
	// plist's own name, so a message cannot name an agent other than the one whose absence
	// decided whether to look at all.
	label   string
	starts  string
	logFile string

	last  func() (time.Time, bool)
	stale time.Duration
}

func (s sweeper) agents() []liveness {
	return []liveness{{
		key:       statedir.AgentGC,
		subject:   "the worktree sweep",
		title:     "The worktree sweep",
		lastLabel: labelLastSweep,
		meanwhile: "so nothing is cleaning up after a removed worktree",
		why: "Nothing is cleaning up after a removed worktree: its compose " +
			"project, the volumes that project made, the processes left in its folder and its " +
			"scratch space all stay.",
		gone:     "The worktree sweep is no longer installed, so nothing further is said about it",
		saidGone: "the worktree sweep is no longer installed, so nothing further is said about it",
		label:    s.cfg.GCLabel(),
		starts:   "it sweeps at once",
		logFile:  logsRoot + "hachiko-gc.log",
		last:     s.deps.GCLastRun,
		stale:    s.cfg.GCStaleAfter,
	}, {
		key:       statedir.AgentSync,
		subject:   "sync",
		title:     "Auto-sync",
		lastLabel: labelLastBeat,
		meanwhile: "so nothing is committing or pushing what gets written",
		why: "Nothing written in the repositories it watches is being " +
			"committed or pushed, so nothing of this session's work is reaching GitHub " +
			"and nothing another machine wrote is arriving.",
		gone:     "Auto-sync is no longer installed, so nothing further is said about it",
		saidGone: "sync is no longer installed, so nothing further is said about it",
		label:    s.cfg.SyncLabel(),
		starts:   "it starts syncing at once",
		logFile:  logsRoot + "hachiko-sync.log",
		last:     s.deps.SyncLastBeat,
		stale:    s.cfg.SyncStaleAfter,
	}, {
		key:       statedir.AgentListen,
		subject:   "the Discord listener",
		title:     "The Discord listener",
		lastLabel: labelLastBeat,
		meanwhile: "so a reply in an incident thread is reaching nobody",
		why: "A reply in an incident thread is not reaching the on-call session, so " +
			"answering from Discord does nothing and attaching to herdr is the only way to " +
			"answer at all.",
		gone:     "Answering from Discord is switched off, so nothing further is said about the listener",
		saidGone: "answering from Discord is switched off, so nothing further is said about the listener",
		label:    s.cfg.ListenLabel(),
		starts:   "it starts listening at once",
		logFile:  logsRoot + "hachiko-listen.log",
		last:     s.deps.ListenLastBeat,
		stale:    s.cfg.ListenStaleAfter,
	}}
}

// Which of them are running, since what the watch says about each repository sync is meant
// to be keeping upstream is only worth saying with sync itself alive.
func (s sweeper) liveness(state *statedir.State, now time.Time) map[string]bool {
	alive := map[string]bool{}
	for _, agent := range s.agents() {
		alive[agent.key] = s.stillRunning(state, now, agent)
	}
	return alive
}

func (s sweeper) stillRunning(state *statedir.State, now time.Time, a liveness) bool {
	since, installed := a.last()
	if !installed {
		s.agentGone(state, a)
		return false
	}

	age := now.Sub(since)
	stale := age > a.stale

	switch was := state.Agent(a.key).Stale != 0; {
	case stale == was:
	case s.dry && stale:
		s.say("would report that %s has not run for %s", a.subject, hmStr(age))
	case s.dry:
		s.say("would report that %s is running again", a.subject)
	case stale:
		s.sayAgentStopped(state, now, a, since, age)
	default:
		s.sayAgentBack(state, now, a, since)
	}

	return !stale
}

func (s sweeper) sayAgentStopped(state *statedir.State, now time.Time, a liveness, since time.Time, age time.Duration) {
	s.say("%s has not run for %s, %s", a.subject, hmStr(age), a.meanwhile)

	message := wording.Lead(wording.MarkerDegraded,
		fmt.Sprintf("%s has not run for %s", a.title, wording.DurationPhrase(age))).
		Field(a.lastLabel, wording.TimePhrase(since, now)).
		Field(wording.LabelWhy, a.why).
		Can("**You can run:** "+
			wording.CodeSpan(fmt.Sprintf("launchctl kickstart -k gui/%d/%s", s.deps.Getuid(), a.label))+
			" — "+a.starts+", and says why it could not in "+
			wording.CodeSpan(a.logFile)+".").
		About("", s.cfg.Host).
		String()

	thread, err := s.deps.Send(discord.Outgoing{Text: message, OpenThread: a.label})
	if err != nil {
		s.say("the message about %s did not send and is left to the next check: %v", a.subject, err)
		return
	}
	state.SetAgent(a.key, statedir.AgentLiveness{Stale: now.Unix(), Thread: thread})
}

func (s sweeper) sayAgentBack(state *statedir.State, now time.Time, a liveness, since time.Time) {
	s.say("%s is running again", a.subject)

	message := wording.Lead(wording.MarkerRecovered, a.title+" is running again").
		Field(a.lastLabel, wording.TimePhrase(since, now)).
		About("", s.cfg.Host).
		String()

	if _, err := s.deps.Send(discord.Outgoing{Text: message, Thread: state.Agent(a.key).Thread}); err != nil {
		s.say("the message saying %s is back did not send and is left to the next check: %v", a.subject, err)
		return
	}
	state.ForgetAgent(a.key)
}

// An agent that has gone while the watch had an alert open on it. Nothing at all is said
// about one that was never there, but silence under a ⚠️ is the one thing this watch never
// leaves: the agent being gone answers "it has stopped" as much as its coming back does.
func (s sweeper) agentGone(state *statedir.State, a liveness) {
	if state.Agent(a.key).Stale == 0 {
		return
	}
	if s.dry {
		s.say("would report that %s", a.saidGone)
		return
	}
	s.say("%s", a.saidGone)

	message := wording.Lead(wording.MarkerInfo, a.gone).About("", s.cfg.Host).String()

	if _, err := s.deps.Send(discord.Outgoing{Text: message, Thread: state.Agent(a.key).Thread}); err != nil {
		s.say("the message saying %s has gone did not send and is left to the next check: %v", a.subject, err)
		return
	}
	state.ForgetAgent(a.key)
}
