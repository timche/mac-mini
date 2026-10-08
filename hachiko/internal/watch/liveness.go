package watch

import (
	"fmt"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

const (
	labelLastSweep = "Last sweep"
	labelLastBeat  = "Last heartbeat"
	labelLastCheck = "Last check"

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
// Agent is one of them as anything but this check reads one: the stamp it writes, how old
// that may get, the agent's own name and label, and the log that says why it stopped.
// Exported because `hachiko status` prints the same facts about the same agents, and two
// tables would be two answers about one agent — a threshold moved here and not there is a
// screen that says an agent is fine while the watch is alerting about it.
type Agent struct {
	// How the state remembers this agent, and what the log and the messages call it.
	Key     string
	Subject string
	Title   string

	// What the stamp's age is the age of, one of these finishing a run and the other two
	// never doing so.
	LastLabel string

	// The one command that starts it again, with no password behind it — the agents are this
	// account's own — and the log that says why it could not. The label is spelled from the
	// plist's own name, so a message cannot name an agent other than the one whose absence
	// decided whether to look at all.
	Label   string
	LogFile string

	// How old the stamp may get before nothing is running, and how to read it. The second
	// value is whether the agent is there to be running at all, since a Mac this repository
	// has not finished provisioning is not a Mac whose agents have stopped.
	Stale time.Duration
	Last  func() (time.Time, bool)

	// What no stamp at all means, which is not the same thing for all three: for the listener
	// it is answering from Discord being off, which ships off and is nothing to report, and
	// for the other two an agent install.sh has not reached.
	Unstamped string
}

// What the watch reads about each of them, which is the stamp and nothing else: each agent
// reports its own failures, and what nothing can report about itself is not running.
func Agents(cfg config.Config) []Agent {
	return []Agent{{
		Key:       statedir.AgentGC,
		Subject:   "the worktree sweep",
		Title:     "The worktree sweep",
		LastLabel: labelLastSweep,
		Label:     cfg.GCLabel(),
		LogFile:   logsRoot + "hachiko-gc.log",
		Stale:     cfg.GCStaleAfter,
		Last:      func() (time.Time, bool) { return gcLastRun(cfg) },
		Unstamped: "no sweep agent is installed",
	}, {
		Key:       statedir.AgentSync,
		Subject:   "sync",
		Title:     "Auto-sync",
		LastLabel: labelLastBeat,
		Label:     cfg.SyncLabel(),
		LogFile:   logsRoot + "hachiko-sync.log",
		Stale:     cfg.SyncStaleAfter,
		Last:      func() (time.Time, bool) { return syncLastBeat(cfg) },
		Unstamped: "no sync agent is installed",
	}, {
		Key:       statedir.AgentListen,
		Subject:   "the Discord listener",
		Title:     "The Discord listener",
		LastLabel: labelLastBeat,
		Label:     cfg.ListenLabel(),
		LogFile:   logsRoot + "hachiko-listen.log",
		Stale:     cfg.ListenStaleAfter,
		Last:      func() (time.Time, bool) { return listenLastBeat(cfg) },
		Unstamped: "off, nothing configured",
	}}
}

// The watch's own agent, which is the one entry Agents does not have: a watch cannot notice
// itself not running, which is the whole reason there is a Worker off this Mac that can. So it
// carries no threshold and nothing here ever checks it — but a person reading one screen wants
// to know whether the agent is loaded, and the end of every check is a state file written,
// which is the nearest thing it has to a stamp of its own.
func WatchAgent(cfg config.Config) Agent {
	return Agent{
		Key:       "watch",
		Subject:   "the watch",
		Title:     "The watch",
		LastLabel: labelLastCheck,
		Label:     cfg.WatchLabel(),
		LogFile:   logsRoot + "hachiko.log",
		Last:      func() (time.Time, bool) { return watchLastRun(cfg) },
		Unstamped: "no check has finished yet",
	}
}

type liveness struct {
	Agent

	// What is going unmade while the agent is not running, and what that costs.
	meanwhile string
	why       string

	// The lead for the one case that is neither stopped nor running: the agent is not there
	// to be running any more. Said only where an alert was open, since an agent that was
	// never installed is not news.
	gone     string
	saidGone string

	// What the one command that starts it again does once it has.
	starts string
}

// The table above with this check's own words on it, and its own readings: every one of them
// is injected, so a sweep's whole decision about an agent is driven by a test without a stamp
// on disk or an hour to wait.
func (s sweeper) agents() []liveness {
	words := map[string]liveness{
		statedir.AgentGC: {
			meanwhile: "so nothing is cleaning up after a removed worktree",
			why: "Nothing is cleaning up after a removed worktree: its compose " +
				"project, the volumes that project made, the processes left in its folder and its " +
				"scratch space all stay.",
			gone:     "The worktree sweep is no longer installed, so nothing further is said about it",
			saidGone: "the worktree sweep is no longer installed, so nothing further is said about it",
			starts:   "it sweeps at once",
		},
		statedir.AgentSync: {
			meanwhile: "so nothing is committing or pushing what gets written",
			why: "Nothing written in the repositories it watches is being " +
				"committed or pushed, so nothing of this session's work is reaching GitHub " +
				"and nothing another machine wrote is arriving.",
			gone:     "Auto-sync is no longer installed, so nothing further is said about it",
			saidGone: "sync is no longer installed, so nothing further is said about it",
			starts:   "it starts syncing at once",
		},
		statedir.AgentListen: {
			meanwhile: "so a reply in an incident thread is reaching nobody",
			why: "A reply in an incident thread is not reaching the on-call session, so " +
				"answering from Discord does nothing and attaching to herdr is the only way to " +
				"answer at all.",
			gone:     "Answering from Discord is switched off, so nothing further is said about the listener",
			saidGone: "answering from Discord is switched off, so nothing further is said about the listener",
			starts:   "it starts listening at once",
		},
	}

	readings := map[string]func() (time.Time, bool){
		statedir.AgentGC:     s.deps.GCLastRun,
		statedir.AgentSync:   s.deps.SyncLastBeat,
		statedir.AgentListen: s.deps.ListenLastBeat,
	}

	out := make([]liveness, 0, len(words))
	for _, agent := range Agents(s.cfg) {
		agent.Last = readings[agent.Key]

		one := words[agent.Key]
		one.Agent = agent
		out = append(out, one)
	}
	return out
}

// Which of them are running, since what the watch says about each repository sync is meant
// to be keeping upstream is only worth saying with sync itself alive.
func (s sweeper) liveness(state *statedir.State, now time.Time) map[string]bool {
	alive := map[string]bool{}
	for _, agent := range s.agents() {
		alive[agent.Key] = s.stillRunning(state, now, agent)
	}
	return alive
}

func (s sweeper) stillRunning(state *statedir.State, now time.Time, a liveness) bool {
	since, installed := a.Last()
	if !installed {
		s.agentGone(state, a)
		return false
	}

	age := now.Sub(since)
	stale := age > a.Stale

	switch was := state.Agent(a.Key).Stale != 0; {
	case stale == was:
	case s.dry && stale:
		s.say("would report that %s has not run for %s", a.Subject, hmStr(age))
	case s.dry:
		s.say("would report that %s is running again", a.Subject)
	case stale:
		s.sayAgentStopped(state, now, a, since, age)
	default:
		s.sayAgentBack(state, now, a, since)
	}

	return !stale
}

func (s sweeper) sayAgentStopped(state *statedir.State, now time.Time, a liveness, since time.Time, age time.Duration) {
	s.say("%s has not run for %s, %s", a.Subject, hmStr(age), a.meanwhile)

	message := wording.Lead(wording.MarkerDegraded,
		fmt.Sprintf("%s has not run for %s", a.Title, wording.DurationPhrase(age))).
		Field(a.LastLabel, wording.TimePhrase(since, now)).
		Field(wording.LabelWhy, a.why).
		Can("**You can run:** "+
			wording.CodeSpan(fmt.Sprintf("launchctl kickstart -k gui/%d/%s", s.deps.Getuid(), a.Label))+
			" — "+a.starts+", and says why it could not in "+
			wording.CodeSpan(a.LogFile)+".").
		About("", s.cfg.Host).
		String()

	thread, err := s.deps.Send(discord.Outgoing{Text: message, OpenThread: a.Label})
	if err != nil {
		s.say("the message about %s did not send and is left to the next check: %v", a.Subject, err)
		return
	}
	state.SetAgent(a.Key, statedir.AgentLiveness{Stale: now.Unix(), Thread: thread})
}

func (s sweeper) sayAgentBack(state *statedir.State, now time.Time, a liveness, since time.Time) {
	s.say("%s is running again", a.Subject)

	message := wording.Lead(wording.MarkerRecovered, a.Title+" is running again").
		Field(a.LastLabel, wording.TimePhrase(since, now)).
		About("", s.cfg.Host).
		String()

	if _, err := s.deps.Send(discord.Outgoing{Text: message, Thread: state.Agent(a.Key).Thread}); err != nil {
		s.say("the message saying %s is back did not send and is left to the next check: %v", a.Subject, err)
		return
	}
	state.ForgetAgent(a.Key)
}

// An agent that has gone while the watch had an alert open on it. Nothing at all is said
// about one that was never there, but silence under a ⚠️ is the one thing this watch never
// leaves: the agent being gone answers "it has stopped" as much as its coming back does.
func (s sweeper) agentGone(state *statedir.State, a liveness) {
	if state.Agent(a.Key).Stale == 0 {
		return
	}
	if s.dry {
		s.say("would report that %s", a.saidGone)
		return
	}
	s.say("%s", a.saidGone)

	message := wording.Lead(wording.MarkerInfo, a.gone).About("", s.cfg.Host).String()

	if _, err := s.deps.Send(discord.Outgoing{Text: message, Thread: state.Agent(a.Key).Thread}); err != nil {
		s.say("the message saying %s has gone did not send and is left to the next check: %v", a.Subject, err)
		return
	}
	state.ForgetAgent(a.Key)
}
