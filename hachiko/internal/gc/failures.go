package gc

import (
	"fmt"
	"regexp"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// The six things a sweep can fail at. Routine cleanup is a log and nothing else — this is
// what Tim hears about, because every one of them is work left undone that the next sweep
// will fail at in exactly the same way.
const (
	failedDown        = "compose-down"
	failedVolume      = "volume-rm"
	failedKill        = "kill"
	failedRemove      = "remove"
	failedPrune       = "git-prune"
	failedDockerPrune = "prune"
)

// The labels these messages go under. Here rather than in wording because they are gc's own
// words and reach two messages each: the one that says something failed and the one that
// says it has stopped failing.
const (
	labelProject  = "Compose project"
	labelVolume   = "Volume"
	labelWorktree = "Worktree"
	labelFolder   = "Folder"
	labelRepo     = "Repository"
	labelProcess  = "Process"
	labelCommand  = "Command"
	labelPrune    = "Prune"
	labelSaid     = "What it said"
	labelSince    = "Failing since"
)

// Failure is one thing a sweep could not do. Kind and Subject together are its identity: a
// compose project that will not go down is one message for as long as it will not, however
// many sweeps that takes.
type Failure struct {
	Kind    string
	Subject string

	// What else the message names, none of it part of the identity — the same failure
	// phrased differently by a new docker version is still the same failure.
	Project  string
	Worktree string
	Label    string
	Detail   string
}

func (f Failure) key() string { return f.Kind + " " + f.Subject }

func (s *sweeper) failed(f Failure) {
	s.say("%s", f.line())
	s.failures = append(s.failures, f)
}

// The log's version, which is one line and carries no markdown.
func (f Failure) line() string {
	switch f.Kind {
	case failedDown:
		return fmt.Sprintf("compose project %s would not go down: %s", safe(f.Subject), f.Detail)
	case failedVolume:
		return fmt.Sprintf("volume %s would not go: %s", safe(f.Subject), f.Detail)
	case failedKill:
		return fmt.Sprintf("%s is still there after SIGKILL", safe(f.Subject))
	case failedDockerPrune:
		return fmt.Sprintf("docker would not prune its %s: %s", safe(f.Subject), f.Detail)
	case failedRemove:
		return fmt.Sprintf("%s could not be removed: %s", safe(f.Subject), f.Detail)
	default:
		return fmt.Sprintf("%s's worktree entries could not be pruned: %s", safe(f.Subject), f.Detail)
	}
}

// One message per failure while it persists, and one line in the same thread when it stops.
// Not one message per sweep: six an hour about a volume docker will not remove is a
// notification Tim turns off, and then the next one is one he never sees.
func (s *sweeper) report(state *State, now time.Time) {
	going := make(map[string]Failure, len(s.failures))
	for _, f := range s.failures {
		going[f.key()] = f
	}

	for _, key := range sortedKeys(going) {
		s.sayFailure(state, going[key], now)
	}
	for _, key := range sortedKeys(state.Posted) {
		if _, still := going[key]; !still {
			s.sayCleared(state, key, now)
		}
	}
}

func (s *sweeper) sayFailure(state *State, f Failure, now time.Time) {
	was, known := state.Posted[f.key()]

	// A send that failed is a failure nobody has heard about, so the next sweep says it
	// again rather than treating the record as a message that went out.
	if known && was.Sent {
		return
	}

	rec := Posted{Kind: f.Kind, Subject: f.Subject, Since: now.Unix(), Detail: f.Detail}
	if known {
		rec.Since, rec.Thread = was.Since, was.Thread
	}

	out := discord.Outgoing{
		Text:   f.message(s.cfg.Host, time.Unix(rec.Since, 0), now),
		Thread: rec.Thread,
	}
	if rec.Thread == "" {
		out.OpenThread = f.key()
	}

	thread, err := s.deps.Send(out)
	if err != nil {
		s.say("the message about this did not send and is left to the next sweep: %v", err)
	} else {
		rec.Sent = true
		if thread != "" {
			rec.Thread = thread
		}
	}

	if state.Posted == nil {
		state.Posted = map[string]Posted{}
	}
	state.Posted[f.key()] = rec
}

func (s *sweeper) sayCleared(state *State, key string, now time.Time) {
	rec := state.Posted[key]

	// Nobody was ever told, so there is nothing to tell them is over.
	if !rec.Sent {
		delete(state.Posted, key)
		return
	}

	message := wording.Lead(wording.MarkerRecovered, clearSentence(rec.Kind)).
		Field(subjectLabel(rec.Kind), subjectValue(rec.Kind, rec.Subject)).
		Field(labelSince, wording.DurationPhrase(now.Sub(time.Unix(rec.Since, 0)))).
		About("", s.cfg.Host).
		String()

	if _, err := s.deps.Send(discord.Outgoing{Text: message, Thread: rec.Thread}); err != nil {
		s.say("the message saying this is over did not send and is left to the next sweep: %v", err)
		return
	}
	delete(state.Posted, key)
}

func (f Failure) message(host string, since, now time.Time) string {
	m := wording.Lead(wording.MarkerDegraded, failSentence(f.Kind))

	switch f.Kind {
	case failedDown:
		m.Field(labelProject, wording.CodeSpan(short(f.Subject)))
	case failedVolume:
		m.Field(labelVolume, wording.CodeSpan(short(f.Subject))).
			Field(labelProject, wording.CodeSpan(short(f.Project)))
	case failedKill:
		m.Field(labelProcess, f.Label).Field(labelCommand, wording.CodeSpan(short(f.Detail)))
	case failedRemove:
		m.Field(labelFolder, wording.CodeSpan(short(f.Subject)))
	case failedPrune:
		m.Field(labelRepo, wording.CodeSpan(short(f.Subject)))
	case failedDockerPrune:
		m.Field(labelPrune, subjectValue(f.Kind, f.Subject))
	}

	if f.Worktree != "" {
		m.Field(labelWorktree, wording.CodeSpan(short(f.Worktree))+" — already gone")
	}
	if f.Kind != failedKill && f.Detail != "" {
		m.Field(labelSaid, wording.CodeSpan(short(f.Detail)))
	}

	return m.Field(labelSince, wording.DurationPhrase(now.Sub(since))).
		Can(f.action()).
		About("", host).
		String()
}

// A compose project and a volume are both names docker will only take out of a narrow set,
// so they can go in a command Tim is told to run. A path cannot: it was chosen by whatever
// a session was running, and a semicolon in one is a second command in a line he pasted.
func (f Failure) action() string {
	switch {
	case f.Kind == failedDown && name.MatchString(f.Subject):
		return "**You can run:** " +
			wording.CodeSpan("docker compose -p "+f.Subject+" down -v --remove-orphans") +
			" — the sweep tries again every ten minutes until it works."
	case f.Kind == failedVolume && name.MatchString(f.Subject):
		return "**You can run:** " + wording.CodeSpan("docker volume rm "+f.Subject) +
			" — the sweep tries again every ten minutes until it works."
	case f.Kind == failedKill:
		return "**Needs you:** a process that survives SIGKILL is waiting on something the " +
			"kernel will not interrupt, usually a mount that has gone away. Nothing but a " +
			"restart clears one."
	case f.Kind == failedPrune:
		return "**Needs you:** run " + wording.CodeSpan("git worktree prune -v") +
			" in the repository named above and see what it says."
	case f.Kind == failedDockerPrune:
		return "**You can run:** " + wording.CodeSpan(f.Label) +
			" — the sweep tries it again at its next daily prune, and nothing but disk is at stake."
	default:
		return "**Needs you:** the folder above is a finished session's scratch and nothing " +
			"is using it. The sweep tries again every ten minutes."
	}
}

// What docker takes as a project or a volume name. Checked rather than assumed, because the
// daemon can be one another machine shares and these names arrive from it.
var name = regexp.MustCompile(`\A[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}\z`)

func failSentence(kind string) string {
	switch kind {
	case failedDown:
		return "A removed worktree's containers would not go"
	case failedVolume:
		return "A removed worktree's volume would not go"
	case failedKill:
		return "A process in a removed worktree survived SIGKILL"
	case failedRemove:
		return "A finished session's scratch folder would not go"
	case failedDockerPrune:
		return "Docker would not prune what nothing is using"
	default:
		return "A repository's worktree entries would not prune"
	}
}

func clearSentence(kind string) string {
	switch kind {
	case failedDown:
		return "A removed worktree's containers are gone now"
	case failedVolume:
		return "A removed worktree's volume is gone now"
	case failedKill:
		return "The process in a removed worktree is gone now"
	case failedRemove:
		return "The scratch folder that would not go is gone now"
	case failedDockerPrune:
		return "Docker prunes what nothing is using again"
	default:
		return "A repository's worktree entries prune cleanly again"
	}
}

// A subject that is a name docker or git chose reads as code, because it is one. The prune's
// is two words of this file's own, and code-spanning prose is how a message stops reading
// like a message.
func subjectValue(kind, subject string) string {
	if kind == failedDockerPrune {
		return wording.PlainWords(short(subject))
	}
	return wording.CodeSpan(short(subject))
}

func subjectLabel(kind string) string {
	switch kind {
	case failedDown:
		return labelProject
	case failedVolume:
		return labelVolume
	case failedKill:
		return labelProcess
	case failedRemove:
		return labelFolder
	case failedDockerPrune:
		return labelPrune
	default:
		return labelRepo
	}
}
