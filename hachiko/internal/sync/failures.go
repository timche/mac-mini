package sync

import (
	"fmt"
	"strings"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// The three things a sync can fail at. Everything else it does is a line in a log nobody
// reads — this is what Tim hears about, because each one of them means nothing written in
// that repository is reaching the origin and nothing will until he looks.
const (
	failedPush     = "push"
	failedConflict = "rebase-conflict"
	failedCommit   = "commit"
)

// The labels these messages go under. Here rather than in wording because they are sync's
// own words and reach two messages each: the one that says something failed and the one
// that says it has stopped failing.
const (
	labelRepo        = "Repository"
	labelRemote      = "Remote"
	labelSaid        = "What it said"
	labelUnpushed    = "Not on the remote"
	labelUncommitted = "Uncommitted"
	labelSince       = "Failing since"
)

// How much of git's own output a message carries. Discord takes 2,000 characters and a
// rebase that conflicted in forty files would be the whole of it.
const detailLimit = 600

// Failure is one thing a pass could not do. Kind and Subject together are its identity: a
// repository whose push will not land is one message for as long as it will not, however
// many passes that takes.
type Failure struct {
	Kind    string
	Subject string

	// What else the message names, none of it part of the identity — the same failure
	// phrased differently by a new git is still the same failure.
	Remote string
	Detail string

	// The two listings a message quotes, so Tim can see what is stuck without attaching to
	// the Mac. Filled in when the message is built rather than when the failure is made.
	Unpushed    []string
	Uncommitted []string
}

func (f Failure) key() string { return f.Kind + " " + f.Subject }

// The log's version, which is one line and carries no markdown.
func (f Failure) line() string {
	switch f.Kind {
	case failedConflict:
		return fmt.Sprintf("the rebase in %s conflicted, so pulling there is paused until it moves: %s",
			short(f.Subject), oneLine(short(f.Detail)))
	case failedCommit:
		return fmt.Sprintf("nothing in %s could be committed: %s", short(f.Subject), oneLine(short(f.Detail)))
	default:
		return fmt.Sprintf("%s could not be pushed to %s: %s",
			short(f.Subject), short(f.Remote), oneLine(short(f.Detail)))
	}
}

// One message per failure while it persists, and one line in the same thread when it stops.
// Not one per pass: a push that will not land is a pass a minute, and sixty notifications an
// hour is a notification Tim turns off — and then the next one is one he never sees.
func (d *daemon) report(repo string, failure *Failure) {
	now := d.deps.Now()

	err := d.store.Change(func(state *State) {
		if failure != nil {
			d.sayFailure(state, *failure, now)
		}

		for _, key := range statedir.SortedKeys(state.Posted) {
			rec := state.Posted[key]
			if rec.Subject != repo {
				continue
			}
			if failure != nil && key == failure.key() {
				continue
			}
			d.sayCleared(state, key, now)
		}
	})
	if err != nil {
		d.say("the record of what has been reported could not be kept: %v", err)
	}
}

func (d *daemon) sayFailure(state *State, f Failure, now time.Time) {
	was, known := state.Posted[f.key()]

	// A send that failed is a failure nobody has heard about, so the next pass says it again
	// rather than treating the record as a message that went out.
	if known && was.Sent {
		return
	}

	rec := Posted{Kind: f.Kind, Subject: f.Subject, Since: now.Unix()}
	if known {
		rec.Since, rec.Thread = was.Since, was.Thread
	}

	out := discord.Outgoing{
		Text:   f.message(d.cfg.Host, time.Unix(rec.Since, 0), now),
		Thread: rec.Thread,
	}
	if rec.Thread == "" {
		out.OpenThread = f.key()
	}

	thread, err := d.deps.Send(out)
	if err != nil {
		d.say("the message about this did not send and is left to the next pass: %v", err)
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

func (d *daemon) sayCleared(state *State, key string, now time.Time) {
	rec := state.Posted[key]

	// Nobody was ever told, so there is nothing to tell them is over.
	if !rec.Sent {
		delete(state.Posted, key)
		return
	}

	message := wording.Lead(wording.MarkerRecovered, clearSentence(rec.Kind)).
		Field(labelRepo, wording.CodeSpan(short(rec.Subject))).
		Field(labelSince, wording.DurationPhrase(now.Sub(time.Unix(rec.Since, 0)))).
		About("", d.cfg.Host).
		String()

	if _, err := d.deps.Send(discord.Outgoing{Text: message, Thread: rec.Thread}); err != nil {
		d.say("the message saying this is over did not send and is left to the next pass: %v", err)
		return
	}
	delete(state.Posted, key)
}

func (f Failure) message(host string, since, now time.Time) string {
	return wording.Lead(wording.MarkerDegraded, failSentence(f.Kind)).
		Field(labelRepo, wording.CodeSpan(short(f.Subject))).
		Field(labelRemote, wording.CodeSpan(short(f.Remote))).
		Field(labelSaid, wording.CodeSpan(quote(f.Detail))).
		Field(labelUnpushed, listed(f.Unpushed)).
		Field(labelUncommitted, listed(f.Uncommitted)).
		Field(labelSince, wording.DurationPhrase(now.Sub(since))).
		Can(f.action()).
		About("", host).
		String()
}

// What Tim can do about each of them, and nothing he cannot: a path was chosen by whatever
// wrote in it, so none of these puts one in a command he is told to paste — the repository is
// named in its own field above and the command is the one he runs once he is in it.
func (f Failure) action() string {
	switch f.Kind {
	case failedConflict:
		return "**Needs you:** the rebase is already aborted, so the repository above is an " +
			"ordinary working tree with work of its own that the remote has moved under. " +
			"Rebase it by hand, or reset to whichever side is right. Pulling there is paused " +
			"until its HEAD or its upstream moves, and sync picks it up by itself within the " +
			"minute after that — there is nothing here to close."
	case failedCommit:
		return "**Needs you:** nothing in that repository is being committed, so nothing " +
			"written there is safe. " + wording.CodeSpan("git status") + " in it says why; a " +
			"lock file left by a killed git and a signing key the agent has lost are the two " +
			"usual answers. Sync tries again every minute."
	default:
		return "**Needs you:** nothing written there is reaching the origin. " +
			wording.CodeSpan("git push") + " in the repository above says what the remote " +
			"refuses. Sync retries on its own and says so here when it lands."
	}
}

func failSentence(kind string) string {
	switch kind {
	case failedConflict:
		return "A repository's rebase conflicted, so it is not being pulled"
	case failedCommit:
		return "A repository's changes could not be committed"
	default:
		return "A repository could not be pushed"
	}
}

func clearSentence(kind string) string {
	switch kind {
	case failedConflict:
		return "The repository that could not rebase is syncing again"
	case failedCommit:
		return "The repository's changes are being committed again"
	default:
		return "The repository that could not be pushed is pushing again"
	}
}

// A listing of commits or of changed files, one bullet each and capped: a message is a
// glance at what is stuck, and the whole of it is in the repository.
const listedLines = 10

func listed(lines []string) string {
	if len(lines) == 0 {
		return ""
	}

	shown := lines
	rest := ""
	if len(shown) > listedLines {
		shown, rest = shown[:listedLines], fmt.Sprintf(" and %d more", len(lines)-listedLines)
	}

	var items []string
	for _, line := range shown {
		items = append(items, wording.CodeSpan(short(line)))
	}
	return strings.Join(items, ", ") + rest
}

// git's own output on one line and cut: it goes in an inline code span, and a newline ends
// one whatever the fence is.
func quote(text string) string { return wording.Safe(oneLine(text), detailLimit) }

func short(s string) string { return wording.Safe(s, wording.PathLimit) }
