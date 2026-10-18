package update

import (
	"fmt"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// The labels the summary goes under. Here rather than in wording because they are this
// command's own words.
const (
	labelMoved = "Moved"
	labelAlso  = "Also"
	labelStep  = "Step"
	labelSaid  = "What it said"
	labelSince = "Failing since"
)

// Failure is one step the update could not finish. The step is its identity — one that
// fails every night is one message for as long as it does — and the action is what Tim can
// do about it, which only the step itself knows.
type Failure struct {
	Step   string
	Detail string
	Action string
}

// What the log says about it, which is one line and carries no markdown.
func (f Failure) line() string {
	return fmt.Sprintf("could not %s: %s", phraseFor(f.Step), oneLine(f.Detail))
}

// One verb phrase per step, which both messages about it are built from: the one saying it
// failed and the one saying it works again.
func phraseFor(step string) string {
	switch step {
	case stepMiseSelf:
		return "update mise itself"
	case stepMiseTools:
		return "upgrade the tools mise manages"
	case stepLockfiles:
		return "publish the lockfiles mise rewrote"
	case stepPortless:
		return "put portless's proxy daemon back"
	case stepBrewUpdate:
		return "fetch Homebrew's formulae"
	case stepBrewUp:
		return "upgrade Homebrew's packages"
	case stepBrewClean:
		return "clear what Homebrew replaced"
	case stepTailscale:
		return "restart tailscaled after its upgrade"
	case stepHerdr:
		return "update herdr"
	case stepOhMyZsh:
		return "bring oh-my-zsh forward"
	default:
		return "read what Apple has waiting"
	}
}

// The summary to the channel Tim reads for this, and one incident per failed step in the
// forum every other alert goes to. Two channels because they are two things: what moved
// overnight is news nobody answers, and a step that will fail again tomorrow night is an
// incident.
func (u *updater) report(state *State, now time.Time) {
	u.summary(now)

	going := map[string]Failure{}
	for _, f := range u.failures {
		going[f.Step] = f
	}

	for _, step := range statedir.SortedKeys(going) {
		u.sayFailure(state, going[step], now)
	}
	for _, step := range statedir.SortedKeys(state.Posted) {
		if _, still := going[step]; !still {
			u.sayCleared(state, step, now)
		}
	}
}

// Nothing moved and nothing to say about it is a night that goes unmentioned: this runs
// every night for ever, and a message saying so would be the one nobody reads the night it
// matters. The log still has the whole of it.
func (u *updater) summary(now time.Time) {
	if len(u.moved) == 0 && len(u.notes) == 0 {
		u.say("nothing moved")
		return
	}

	var bullets []string
	for _, move := range u.moved {
		u.say("%s moved from %s to %s", move.What, move.From, move.To)
		bullets = append(bullets, movedBullet(move))
	}
	for _, note := range u.notes {
		u.say("%s", note)
	}

	if u.cfg.Discord.UpdatesID == "" {
		u.say("no updates channel is configured, so the summary stays in this log")
		return
	}

	message := wording.Lead(wording.MarkerInfo, summarySentence(len(u.moved), len(u.failures))).
		Bullets(labelMoved, bullets).
		Bullets(labelAlso, u.notes).
		About("", u.cfg.Host).
		String()

	if _, err := u.deps.Send(discord.Outgoing{Text: message, Channel: u.cfg.Discord.UpdatesID}); err != nil {
		u.say("the summary did not send, so this night is in the log alone: %v", err)
	}
}

// A name and two versions, all three of them a tool's own spelling rather than hachiko's.
func movedBullet(move Move) string {
	short := func(s string) string { return wording.Safe(s, wording.NameLimit) }
	return wording.CodeSpan(short(move.What)) + " " +
		wording.PlainWords(short(move.From)) + " → " + wording.PlainWords(short(move.To))
}

func summarySentence(moved, failed int) string {
	sentence := "Nightly update: " + wording.CountOf(int64(moved), "version", "versions") + " moved"
	if moved == 0 {
		sentence = "Nightly update: nothing moved"
	}
	if failed > 0 {
		sentence += ", " + wording.CountOf(int64(failed), "step", "steps") + " failed"
	}
	return sentence
}

// One message per failing step while it keeps failing, and a line in the same thread when
// it stops. Not one a night: a cleanup that meets a root-owned keg every night is a
// notification Tim turns off, and then the next one is one he never sees.
func (u *updater) sayFailure(state *State, f Failure, now time.Time) {
	was, known := state.Posted[f.Step]

	// A send that failed is a failure nobody has heard about, so the next night says it
	// again rather than treating the record as a message that went out.
	if known && was.Sent {
		return
	}

	rec := Posted{Step: f.Step, Since: now.Unix(), Detail: f.Detail}
	if known {
		rec.Since, rec.Thread = was.Since, was.Thread
	}

	out := discord.Outgoing{
		Text:   f.message(u.cfg.Host, time.Unix(rec.Since, 0), now),
		Thread: rec.Thread,
	}
	if rec.Thread == "" {
		out.OpenThread = f.Step
	}

	thread, err := u.deps.Send(out)
	if err != nil {
		u.say("the message about this did not send and is left to the next update: %v", err)
	} else {
		rec.Sent = true
		if thread != "" {
			rec.Thread = thread
		}
	}

	if state.Posted == nil {
		state.Posted = map[string]Posted{}
	}
	state.Posted[f.Step] = rec
}

func (u *updater) sayCleared(state *State, step string, now time.Time) {
	rec := state.Posted[step]

	// Nobody was ever told, so there is nothing to tell them is over.
	if !rec.Sent {
		delete(state.Posted, step)
		return
	}

	message := wording.Lead(wording.MarkerRecovered, "The nightly update can "+phraseFor(step)+" again").
		Field(labelStep, wording.CodeSpan(step)).
		Field(labelSince, wording.DurationPhrase(now.Sub(time.Unix(rec.Since, 0)))).
		About("", u.cfg.Host).
		String()

	if _, err := u.deps.Send(discord.Outgoing{Text: message, Thread: rec.Thread}); err != nil {
		u.say("the message saying this is over did not send and is left to the next update: %v", err)
		return
	}
	delete(state.Posted, step)
}

func (f Failure) message(host string, since, now time.Time) string {
	action := f.Action
	if action == "" {
		action = "**Needs you:** nothing here tries it again before tomorrow night. " +
			"The step is " + wording.CodeSpan(f.Step) + " in `hachiko update`."
	}

	return wording.Lead(wording.MarkerDegraded, "The nightly update could not "+phraseFor(f.Step)).
		Field(labelStep, wording.CodeSpan(f.Step)).
		Output(labelSaid, f.Detail).
		Field(labelSince, wording.DurationPhrase(now.Sub(since))).
		Can(action).
		About("", host).
		String()
}
