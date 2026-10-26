package update

import (
	"fmt"
	"strings"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// The labels the summary goes under. Here rather than in wording because they are this
// command's own words.
const (
	labelMoved  = "Moved"
	labelAlso   = "Also"
	labelFailed = "Failed"
	labelStep   = "Step"
	labelSaid   = "What it said"
	labelSince  = "Failing since"
)

// Failure is one step the update could not finish. The step is its identity — one that
// fails every night is one incident for as long as it does — and the action is what Tim can
// do about it, which only the step itself knows.
type Failure struct {
	Step   string
	Detail string
	Action string
}

// What the five-minute check is handed about it. The action is filled in here rather than
// where the message is written, so that the one step with nothing specific to suggest still
// says what there is to say about it.
func (f Failure) handed(since int64) Handed {
	action := f.Action
	switch {
	case action != "":
	case f.Step == stepSecurity:
		// Nothing asks for this run a second time, it being one somebody or something
		// out of band started. What does come round is the night's own run, which moves
		// the same version once the release-age hold this one was bypassing has passed.
		action = "**Needs you:** nothing asks for this upgrade again, and tomorrow night's run " +
			"moves the version once the release-age hold has passed. " +
			"The step is " + wording.CodeSpan(f.Step) + " in `hachiko update --security`."
	default:
		action = "**Needs you:** nothing here tries it again before tomorrow night. " +
			"The step is " + wording.CodeSpan(f.Step) + " in `hachiko update`."
	}
	return Handed{Step: f.Step, Phrase: phraseFor(f.Step), Detail: f.Detail, Since: since, Action: action}
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
	case stepSecurity:
		return "upgrade what a published advisory named"
	default:
		return "read what Apple has waiting"
	}
}

// The summary to the channel Tim reads for this, and what is failing left for the five-minute
// check to raise in the forum every other alert goes to. Two channels because they are two
// things: what moved overnight is news nobody answers, and a step that will fail again
// tomorrow night is an incident with an on-call session on it.
//
// Nothing about a failed step is posted from here, which is the whole point of the hand-off:
// one incident is one post, and everything an incident is talked about in — the post and its
// thread, the session, Tim's reply out of Discord, the raw details if nobody reports, the
// reminders — is the check's. The line saying a step works again is the exception, because
// this is the only thing that ever knows it does.
func (u *updater) report(state *State, now time.Time) {
	u.summary(now)

	going := map[string]Failure{}
	for _, f := range u.failures {
		going[f.Step] = f
	}

	// One night's failures are one incident, so a step that works again closes the post only
	// when it is the last one under it: an outcome archives the thread and retags it Resolved,
	// and a sibling still failing would be left under a post that says it is over.
	raised := u.hand.Raised()
	open := map[string]int{}
	for step, was := range raised {
		if _, failing := state.Failing[step]; failing {
			open[was.Incident]++
		}
	}
	for _, step := range statedir.SortedKeys(state.Failing) {
		// Only a step this run attempted. A security upgrade runs two of them, and its silence
		// about `brew update` is not evidence that `brew update` works again — a run that read
		// it that way would close the night's incident on the strength of never having tried.
		if _, still := going[step]; !still && u.answerable(step) {
			was := raised[step]
			open[was.Incident]--
			u.sayCleared(state, step, now, was, open[was.Incident] == 0)
		}
	}

	var fresh []string
	for _, step := range statedir.SortedKeys(going) {
		if _, known := state.Failing[step]; known {
			continue
		}
		if state.Failing == nil {
			state.Failing = map[string]Failing{}
		}
		state.Failing[step] = Failing{Since: now.Unix()}
		fresh = append(fresh, step)
	}
	if len(fresh) > 0 {
		u.say("left %s for the five-minute check to raise as an incident: %s",
			wording.CountOf(int64(len(fresh)), "step", "steps"), strings.Join(fresh, ", "))
	}

	// Whole, and whether or not anything changed tonight: what it says is which steps are
	// failing, so one still in it is one the check has already raised and one gone from it is
	// one nobody is waiting on any more.
	was := map[string]Handed{}
	for _, step := range u.hand.Failing() {
		was[step.Step] = step
	}

	handed := make([]Handed, 0, len(state.Failing))
	for _, step := range statedir.SortedKeys(state.Failing) {
		// A step this run did not attempt keeps what the run that did attempt it said, because
		// the file is rewritten whole and a security upgrade would otherwise blank the night's
		// own account of why `brew update` failed.
		if !u.answerable(step) {
			if kept, known := was[step]; known {
				handed = append(handed, kept)
				continue
			}
		}

		// A step that works again but whose message did not send stays on the hand-off, so the
		// check goes on waiting on its incident until Tim has heard that it is over. There is
		// nothing a command said about it tonight, because tonight it worked.
		f, still := going[step]
		if !still {
			f = Failure{Step: step}
		}
		handed = append(handed, f.handed(state.Failing[step].Since))
	}
	if err := u.hand.SetFailing(handed); err != nil {
		u.say("what is failing could not be left for the five-minute check, so nothing is raised about it until the next update: %v", err)
	}
}

// Nothing moved and nothing to say about it is a night that goes unmentioned: this runs
// every night for ever, and a message saying so would be the one nobody reads the night it
// matters. The log still has the whole of it.
//
// A security upgrade is the exception and always says something: it ran because a published
// advisory named a version on this Mac, so an upgrade that moved nothing is the news rather
// than the absence of it.
func (u *updater) summary(now time.Time) {
	if len(u.moved) == 0 && len(u.notes) == 0 && len(u.targets) == 0 {
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

	message := wording.Lead(wording.MarkerInfo, u.sentence()).
		Bullets(labelMoved, bullets).
		Bullets(labelAlso, u.notes).
		Bullets(labelFailed, u.failedBullets()).
		About("", u.cfg.Host).
		String()

	if _, err := u.deps.Send(discord.Outgoing{Text: message, Channel: u.cfg.Discord.UpdatesID}); err != nil {
		u.say("the summary did not send, so this night is in the log alone: %v", err)
	}
}

// The step and what it could not do, so the summary says what failed without anybody having
// to find the incident it was raised as, which is posted once and not again for as long as it
// goes on failing.
func (u *updater) failedBullets() []string {
	var bullets []string
	for _, f := range u.failures {
		bullets = append(bullets, wording.CodeSpan(f.Step)+": could not "+phraseFor(f.Step))
	}
	return bullets
}

// A name and two versions, all three of them a tool's own spelling rather than hachiko's.
func movedBullet(move Move) string {
	short := func(s string) string { return wording.Safe(s, wording.NameLimit) }
	return wording.CodeSpan(short(move.What)) + " " +
		wording.PlainWords(short(move.From)) + " → " + wording.PlainWords(short(move.To))
}

// Which run this was, how much moved and how much did not. The two read differently because
// they are different news: a night's run is a report on keeping the Mac current, and a
// security upgrade is a report on one fault.
func (u *updater) sentence() string {
	what, failed := "Nightly update", "step"
	if len(u.targets) > 0 {
		what, failed = "Security update", "target"
	}

	sentence := what + ": " + wording.CountOf(int64(len(u.moved)), "version", "versions") + " moved"
	if len(u.moved) == 0 {
		sentence = what + ": nothing moved"
	}
	if len(u.failures) > 0 {
		sentence += ", " + wording.CountOf(int64(len(u.failures)), failed, failed+"s") + " failed"
	}
	return sentence
}

// The one message about a failed step this still sends: the line saying it works again,
// into the thread of the post the check opened about it, and marked as that incident's
// outcome — which is what retags the post Resolved and closes it, and so what the forum
// sweep deletes it by three days later. Without that flag a step that came back weeks ago
// is still an open post.
func (u *updater) sayCleared(state *State, step string, now time.Time, raised Raised, last bool) {
	// Nobody was ever told: no check has raised it yet, which is a step that failed and came
	// back inside five minutes. There is nothing to tell them is over.
	if raised.Incident == "" {
		delete(state.Failing, step)
		return
	}

	message := wording.Lead(wording.MarkerRecovered, whoRan(step)+" can "+phraseFor(step)+" again").
		Field(labelStep, wording.CodeSpan(step)).
		Field(labelSince, wording.DurationPhrase(now.Sub(time.Unix(state.Failing[step].Since, 0)))).
		About(raised.Incident, u.cfg.Host).
		String()

	if _, err := u.deps.Send(discord.Outgoing{Text: message, Thread: raised.Thread, Outcome: last}); err != nil {
		u.say("the message saying this is over did not send and is left to the next update: %v", err)
		return
	}
	u.say("the nightly update can %s again", phraseFor(step))
	delete(state.Failing, step)
}

// Which run a step belongs to, for the two sentences that name it. Every step but one is a
// night's; the out-of-cycle run has a step of its own, and saying the nightly update could not
// do it would be a message about a run that never happened.
func whoRan(step string) string {
	if step == stepSecurity {
		return "A security upgrade"
	}
	return "The nightly update"
}

// What a post about these steps opens with, which is also its title in the forum: one step by
// what it could not do, and several by how many there were, since four phrases do not fit in
// a title and each of them is named in the detail under it.
func Lead(failed []Handed) string {
	if len(failed) == 1 {
		return whoRan(failed[0].Step) + " could not " + failed[0].Phrase
	}
	return "The nightly update failed " + wording.CountOf(int64(len(failed)), "step", "steps")
}

// What goes under it: one group per step, each saying which step, what the command said, how
// long it has been failing and the one thing Tim can do about it. Written here rather than
// where the post is sent because the words for a step are the update's own — the check knows
// a step by its name and nothing else.
func Details(failed []Handed, now time.Time) string {
	var groups []string

	for _, h := range failed {
		step := wording.CodeSpan(wording.Safe(h.Step, wording.NameLimit))
		// Only where there are several, since with one the lead has already said it.
		if len(failed) > 1 {
			step += " — could not " + h.Phrase
		}

		group := []string{"**" + labelStep + ":** " + step}
		if said := wording.CodeBlock(h.Detail, wording.OutputLimit); said != "" {
			group = append(group, "**"+labelSaid+":**\n"+said)
		}
		group = append(group, "**"+labelSince+":** "+
			wording.DurationPhrase(now.Sub(time.Unix(h.Since, 0))))
		if h.Action != "" {
			group = append(group, h.Action)
		}

		groups = append(groups, strings.Join(group, "\n\n"))
	}
	return strings.Join(groups, "\n\n")
}
