package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/oncall"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// The other way an incident goes unresolved. An on-call session investigates, reports,
// and then waits on its question — and if Tim is asleep or in the middle of something,
// nothing ever comes back to it: the question goes stale, the options in it are about a
// disk that has since moved, and the writer is still writing. So the wait is a timeline
// rather than an open end. A reminder at an hour, a warning a quarter of an hour before
// the deadline, and at three hours the session is told to decide inside the limits its
// standing orders gave it. Three hours is Tim's number: long enough that he is asleep
// rather than busy, short enough that a disk losing 90 GB an hour has not taken it.
const (
	stepRemind   = "remind"
	stepWarn     = "warn"
	stepEarly    = "early"
	stepHandover = "handover"

	// The one line saying the decision reached nobody. A step of its own because it is sent
	// once per incident however long the handover goes on failing, and nothing about the
	// failure stops repeating by itself.
	stepStuck = "stuck"

	// The news that what fired the incident has stopped by itself. Once per incident for the
	// same reason: a quiet Mac is every check after the first one.
	stepCleared = "cleared"
)

// How many esc-and-prompt attempts in a row may fail before nothing of the agent's is
// cancelled again until it is seen off its question. Three, because the failure this answers
// is herdr taking the keys and the agent not acting on them, which the poll in interruptWith
// already waits out — a fourth try in the same state is a question taken away for nothing.
//
// The cap holds only while the agent sits on a question that will not go, which is the one
// state nothing can be delivered in at all: once there is no question, every step goes as a
// prompt and the count is spent. A session stuck there is in the channel line the failed
// handover sent, which is Tim's to look at.
const escAttempts = 3

// How long a briefed session has to get as far as a question before the wait on it is
// dropped. Much longer than the report deadline, which is about its first message and has a
// message of its own when it passes: the question comes after the reading, and a session
// that asked at twelve minutes had its whole timeline thrown away when this was ten.
const questionBound = time.Hour

// What this check measured, which every reminder and every handover carries instead of
// the numbers the question was asked with: an answer to a three-hour-old question is
// about a machine that has moved on.
type nowReading struct {
	free     int64
	prevFree int64
	level    int64
	span     time.Duration
	grewKB   int64
	sizes    map[string]int64
	writers  []string

	// The detail sections as a message shows them, already labelled and bulleted: what was
	// emptied, what is growing fast, what is busy, and what stopping it would take.
	sections string

	// Whether the span is one the check had to invent rather than one it measured, which is
	// what a clock that moved looks like from here.
	spanClamped bool

	// Per kind, what this check found is no longer firing, in hachiko's own words and with
	// its own numbers: no file growing fast and room back on the volume, or nothing over the
	// CPU share. Empty for a kind whose trigger is still going, and empty on a check whose
	// reading of that half was missing rather than clear.
	cleared map[string]string

	// The directories the walk did not open, which the wait reads against the files its
	// question was asked about: one of them under a skipped directory is a file nothing
	// looked at rather than a file that stopped growing.
	stalled []statedir.Stall
}

func (r nowReading) snapshot(now time.Time) statedir.Asked {
	sizes := make(map[string]int64, len(r.sizes))
	for path, kb := range r.sizes {
		sizes[path] = kb
	}
	return statedir.Asked{
		At:      now.Unix(),
		FreeKB:  r.free,
		Level:   r.level,
		Sizes:   sizes,
		Writers: append([]string(nil), r.writers...),
	}
}

// What the machine looks like this minute, which every message about an unanswered
// question carries in place of the numbers the question was asked with.
func (r nowReading) numbers() string {
	now := "**" + wording.LabelNow + ":** " + wording.GBUnit(r.free) + " free"
	if r.level != 0 {
		now += fmt.Sprintf(", under the %d GB mark", r.level)
	}
	return joinBlocks(now, r.sections)
}

// One `herdr agent get` per kind and no more, because there is one on-call agent per
// kind: what it is doing is the whole of how hachiko knows whether anybody answered.
func (s sweeper) chaseAnswers(state *statedir.State, now time.Time, reading nowReading) {
	for _, kind := range statedir.SortedKeys(state.Waiting) {
		w := state.Waiting[kind]

		status, err := s.deps.AgentStatus(kind)
		if err != nil {
			if next, keep := s.unreachable(state, now, kind, w, reading, err); keep {
				state.Waiting[kind] = next
			} else {
				delete(state.Waiting, kind)
			}
			continue
		}

		// Any answer at all ends the run, so the next outage is measured from itself rather
		// than from one hours ago that came back.
		if w.Unreachable != 0 {
			w.Unreachable = 0
			state.Waiting[kind] = w
		}

		// The message the session marked as the outcome, and only that one: it sends others
		// while it waits — the same question posted into the Discord thread, an update when
		// the situation moves — and reading any of them as the outcome ended the wait on the
		// strength of a message about the question still being open.
		//
		// Not while the incident is still pending either: that marker is the first report,
		// which chaseLateReports has yet to consume.
		if _, pending := state.Pending[w.Incident]; !pending && s.store.ReportedOutcome(w.Incident) {
			s.say("the %s on-call agent reported the outcome of %s", kind, w.Incident)
			s.store.ClearReported(w.Incident)
			delete(state.Waiting, kind)
			continue
		}

		switch {
		case status == oncall.StatusGone:
			s.noAgent(state, now, kind, w, reading)

		case status == oncall.StatusBlocked:
			// It is on a question again, so nothing is owing: the prompt hachiko owed was to
			// replace the question it took away, and this is not that one. Whatever step
			// wanted that prompt is unrecorded and comes round again.
			w.Owed, w.OwedSteps = "", nil
			w.Settled, w.Busy = 0, 0
			state.Waiting[kind] = s.escalate(state, now, kind, w, reading, agentOnQuestion)

		case w.Owed != "":
			state.Waiting[kind] = s.retryOwed(now, kind, w, reading)

		case w.Since == 0:
			// Briefed and not yet on a question. The clock starts whenever the first blocked
			// sighting comes, however late: a session that reported inside its ten minutes and
			// then read for another two before asking is a session that asked, and dropping the
			// wait at the report deadline threw away the whole timeline for it. So the only
			// thing that drops it here is an hour of a live agent never asking anything, by
			// which point it is not going to.
			if now.Sub(time.Unix(w.Opened, 0)) >= questionBound {
				s.say("the %s on-call agent never asked anything about %s in %s, so the wait on it is dropped",
					kind, w.Incident, hmStr(questionBound))
				delete(state.Waiting, kind)
			}

		case status == oncall.StatusWorking:
			// Something is in flight: the answer Tim gave it, or what hachiko handed it in
			// place of the question. The clock defers to that, since a reminder or a handover
			// on top of it is hachiko talking over the work it asked for — but only for the
			// minutes a session is given to report in, and escalate is what holds that grace,
			// because a disk about to fill does not wait for a tool call either.
			w.Settled, w.Escs = 0, 0
			if w.Busy == 0 {
				w.Busy = now.Unix()
			}
			state.Waiting[kind] = s.escalate(state, now, kind, w, reading, agentBusy)

		default:
			if next, keep := s.settled(state, now, kind, w, reading); keep {
				state.Waiting[kind] = next
			} else {
				delete(state.Waiting, kind)
			}
		}
	}
}

// Not blocked, not working, and nothing has reported anything: the question is gone and the
// agent has nothing in flight. That is what Tim answering looks like from here — and it is
// equally what hachiko's own esc looks like, and what a session that gave up on its turn
// looks like. herdr cannot tell them apart and neither can this, so ending the wait here was
// ending it on the weakest evidence there is, which is how a whole night's incident went by
// with nothing acting and nothing said.
//
// So the timeline runs on instead, and what ends the wait is something that actually says
// what happened: the outcome, a fresh question, a closed session, or the deadline followed
// by silence.
func (s sweeper) settled(state *statedir.State, now time.Time, kind string, w statedir.Waiting, reading nowReading) (statedir.Waiting, bool) {
	// The agent has no question up, so there is nothing an esc could take away and the cap on
	// cancelling one is spent: every step from here goes as a prompt. The cap holds only while
	// it sits on a question that will not go, which is the state nothing can be delivered in.
	w.Busy, w.Escs = 0, 0
	if w.Settled == 0 {
		w.Settled = now.Unix()
	}

	// An unblock hachiko did not cause may well be Tim answering in herdr, with the agent
	// between turns for a moment. He gets the minutes a session is given to report in before
	// the timeline resumes over the top of it — and no more than that, because an answer that
	// was really an answer ends in an outcome, and this one has not. Nothing of the kind is
	// extended to an unblock hachiko caused itself: that is the reading the whole of this
	// exists to stop being trusted.
	if w.Nudged == 0 && now.Sub(time.Unix(w.Settled, 0)) < s.cfg.OncallDeadline {
		return w, true
	}

	// The decision has been handed over and the agent has gone quiet without reporting. It
	// has had the minutes it was given to report its findings in to say what it did, so the
	// wait stops being counted rather than being counted for ever.
	if statedir.Contains(w.Steps, stepHandover) && now.Sub(time.Unix(w.Settled, 0)) >= s.cfg.OncallDeadline {
		if !s.sayNoOutcome(state, kind, w, reading,
			"the agent was handed the decision and went quiet without reporting an outcome") {
			return w, true
		}

		s.say("nothing reported the outcome of %s and the %s on-call agent has nothing left in flight, so the wait on it ends after %s",
			w.Incident, kind, hmStr(now.Sub(time.Unix(w.Since, 0))))
		return w, false
	}

	return s.escalate(state, now, kind, w, reading, agentQuiet), true
}

// How a check found the agent. It decides two things: what may be sent to it — an esc only
// to a question, a prompt to either of the other two — and how much of the clock runs, since
// an agent with something in flight is given a grace before the timeline resumes over the
// top of it.
type agentState int

const (
	agentOnQuestion agentState = iota
	agentBusy
	agentQuiet
)

// The clock, and the one message per step on it. The order is deliberate: a handover
// that is due makes a reminder noise, and a question that is about to be cancelled and
// asked again is not one to remind him about either.
func (s sweeper) escalate(state *statedir.State, now time.Time, kind string, w statedir.Waiting, reading nowReading, found agentState) statedir.Waiting {
	blocked := found == agentOnQuestion
	if blocked {
		if w.Since == 0 {
			w.Since = now.Unix()
			s.say("the %s on-call agent is waiting for an answer on %s", kind, w.Incident)
		}
		// A question with no numbers behind it yet: the first one, or one asked again under a
		// brief this agent has not been measured against. Everything a stale question is
		// found out by is measured from here.
		if w.Asked.At == 0 {
			w.Asked = reading.snapshot(now)
		}
		// There is a question up, so it is not one hachiko took away.
		w.Nudged = 0
	}
	// A session that named no fallback option in its first report may have named one in a
	// message since, and the marker keeps the earliest it was given.
	if w.Default == "" {
		w.Default = s.store.ReportedFallback(w.Incident)
	}

	waited := now.Sub(time.Unix(w.Since, 0))
	handed := statedir.Contains(w.Steps, stepHandover)

	// Read on every check whatever the agent is doing, and before the grace below, because a
	// disk that will be full before he wakes does not wait for a tool call to finish — and
	// because the rule it answers is two checks in a row, which skipping a check breaks.
	w, worse := s.worsening(w, reading, waited, now)

	// The grace the clock gives work in flight. Everything below is either a message to Tim
	// about a question the agent has already asked or a prompt on top of what it is doing,
	// and neither belongs over the first minutes of a turn. The early handover above is the
	// exception, since what it is for is the case where waiting costs more than interrupting.
	if found == agentBusy && worse == "" && now.Sub(time.Unix(w.Busy, 0)) < s.cfg.OncallDeadline {
		return w
	}

	// An esc that keeps failing is an esc every five minutes, each one taking away whatever
	// the session has asked since. After a few in a row that did not land, nothing of the
	// agent's is cancelled again until its state has moved — herdr refuses a prompt to an
	// agent on a question, so there is nothing else to send it meanwhile. Messages to Tim are
	// not esc and go on either way.
	escSpent := blocked && w.Escs >= escAttempts

	// Before anything on the clock, and the one change that outlives a handover already
	// made: what fired the incident has stopped by itself. The authority to kill a process
	// is no use against one that has already stopped, a reminder about options is the wrong
	// message, and a session that was handed the decision and asked again is still the only
	// thing that can verify this and close — which is why this one is not gated on the
	// handover not having happened, as every other change is.
	w, clear := s.clearing(w, reading, now, kind)
	if clear.happened() && !escSpent {
		return s.refresh(now, kind, w, reading, clear, blocked)
	}

	changed := materialChange(w, reading)

	// An early handover is a step of its own and not the deadline's. Recorded as the
	// deadline's, a session that was handed the decision early and judged that waiting was
	// safe had its three hours quietly cancelled: it re-asked, and the deadline that was the
	// whole point of the clock never came.
	if worse != "" && !handed && !statedir.Contains(w.Steps, stepEarly) && !escSpent {
		return s.handOver(state, now, kind, w, reading, worse, blocked)
	}
	if waited >= s.cfg.HandoverAfter && !handed && !escSpent {
		return s.handOver(state, now, kind, w, reading, "", blocked)
	}
	if handed {
		return w
	}

	if changed.happened() && !escSpent {
		return s.refresh(now, kind, w, reading, changed, blocked)
	}

	switch {
	case waited >= s.cfg.WarnAfter && !statedir.Contains(w.Steps, stepWarn):
		return s.warn(state, w, reading, waited)
	case waited >= s.cfg.RemindAfter && !statedir.Contains(w.Steps, stepRemind):
		return s.remind(state, w, reading, waited)
	}
	return w
}

func (s sweeper) remind(state *statedir.State, w statedir.Waiting, reading nowReading, waited time.Duration) statedir.Waiting {
	message := wording.Lead(wording.MarkerDegraded, fmt.Sprintf("Still no answer on %s after %s",
		wording.IncidentWords(kindOf(w.Incident)), wording.DurationPhrase(waited))).
		Block(reading.numbers()).
		Can(wording.AnswerAction(s.cfg, w.Tab)).
		About(w.Incident, s.cfg.Host).
		String()

	return s.step(state, w, stepRemind, message,
		fmt.Sprintf("reminded about %s after %s", w.Incident, hmStr(waited)))
}

// A quarter of an hour, which is long enough to answer from a phone and short enough
// that it is not a second reminder.
//
// The `If no answer` line is a line of its own and stays one, because it is the line
// fallbackOption reads a session's own option out of: anything that folded it into a
// sentence would hand Tim an option chosen by whatever filled the disk.
func (s sweeper) warn(state *statedir.State, w statedir.Waiting, reading nowReading, waited time.Duration) statedir.Waiting {
	fallback := w.Default
	if fallback == "" {
		fallback = "the agent named no fallback option, so it will decide when it re-checks"
	}

	message := wording.Lead(wording.MarkerDegraded, fmt.Sprintf("No answer on %s — the agent decides in %s",
		wording.IncidentWords(kindOf(w.Incident)), wording.DurationPhrase(s.cfg.HandoverAfter-s.cfg.WarnAfter))).
		Field(wording.LabelWaiting, wording.DurationPhrase(waited)).
		Field(wording.LabelFallback, fallback).
		Block(reading.numbers()).
		Can(wording.AnswerAction(s.cfg, w.Tab)).
		About(w.Incident, s.cfg.Host).
		String()

	return s.step(state, w, stepWarn, message,
		fmt.Sprintf("warned that %s is %s from the handover", w.Incident, hmStr(s.cfg.HandoverAfter-waited)))
}

// A step is done only once the message has actually left the machine, and the steps
// before it are marked with it: a check that comes back after an outage has no reason to
// send an hour's reminder about a question that is already past its deadline.
func (s sweeper) step(state *statedir.State, w statedir.Waiting, step, message, said string) statedir.Waiting {
	if err := s.send(state, w.Incident, message); err != nil {
		s.say("the %s step on %s did not send and is left to the next check: %v", step, w.Incident, err)
		return w
	}

	s.say("%s", said)
	switch step {
	case stepRemind:
		w.Steps = mergeSorted(w.Steps, []string{stepRemind})
	case stepWarn:
		w.Steps = mergeSorted(w.Steps, []string{stepRemind, stepWarn})
	case stepStuck:
		w.Steps = mergeSorted(w.Steps, []string{stepStuck})
	}
	return w
}

// Nothing of hachiko's own goes to the channel at a handover that worked, the session's own
// message being what says what it did. One that reaches nobody is the opposite case: without
// this line the last word in the channel is the warning that the agent would decide in a
// quarter of an hour, and nothing after it. Once per incident, however long it goes on
// failing, since nothing about the failure stops repeating by itself.
func (s sweeper) sayHandoverStuck(state *statedir.State, kind string, w statedir.Waiting, reading nowReading, why string) statedir.Waiting {
	if statedir.Contains(w.Steps, stepStuck) {
		return w
	}

	message := wording.Lead(wording.MarkerDown, fmt.Sprintf("The decision on %s reached no agent", wording.IncidentWords(kind))).
		Field(wording.LabelWhy, why).
		Block(reading.numbers()).
		Can("Nothing has acted on it, and hachiko tries again every five minutes. "+
			wording.AnswerAction(s.cfg, w.Tab)).
		About(w.Incident, s.cfg.Host).
		String()

	return s.step(state, w, stepStuck, message,
		fmt.Sprintf("said in the channel that the decision on %s has reached nobody", w.Incident))
}

// And a wait that ends with nothing to show says so, for the same reason: the alternative is
// a channel whose last word was a warning about a decision a quarter of an hour away.
func (s sweeper) sayNoOutcome(state *statedir.State, kind string, w statedir.Waiting, reading nowReading, why string) bool {
	message := wording.Lead(wording.MarkerDegraded, fmt.Sprintf("No outcome reported on %s", wording.IncidentWords(kind))).
		Field(wording.LabelWhy, why).
		Block(reading.numbers()).
		Can("hachiko has stopped waiting and does not know whether anything was done. Check herdr: "+
			wording.HerdrWhere(s.cfg, w.Tab)+", or leave it to the next check to raise it again.").
		About(w.Incident, s.cfg.Host).
		String()

	if err := s.send(state, w.Incident, message); err != nil {
		s.say("nothing reported the outcome of %s and the message saying so did not send either: %v", w.Incident, err)
		return false
	}
	return true
}

// The handover itself: the question goes, and the session is told to decide. Nothing of
// hachiko's own goes to the channel here — the session's own message is what says what
// it did and why, and two messages about one decision would be one too many.
func (s sweeper) handOver(state *statedir.State, now time.Time, kind string, w statedir.Waiting, reading nowReading, worse string, blocked bool) statedir.Waiting {
	waited := now.Sub(time.Unix(w.Since, 0))

	promptLead := fmt.Sprintf(`Tim has not answered for %s, so the autonomy in your standing orders is handed over to you now. Re-check the situation from scratch first — the numbers below are this minute's, not the ones you asked about — then pick and carry out the least destructive option that resolves it, inside the limits those orders give you. Spawn the oncall-partner agent with your proposed action first and act only if it agrees. Verify it worked, send one message with what you did, why, which limit allowed it and what the partner said, write the incident note, and stop.`,
		wording.DurationPhrase(waited))

	if worse != "" {
		promptLead = fmt.Sprintf(`The incident is getting worse rapidly: %s. Tim has not answered for %s, and the three-hour handover would come too late, so the autonomy in your standing orders is handed over to you now, early.

Re-check the situation from scratch — the numbers below are this minute's — and judge it yourself. If you agree that waiting costs more than acting, pick and carry out the least destructive option that resolves it, inside the limits those orders give you, with the oncall-partner agent's agreement first. If you judge instead that it is about to stop by itself or that acting costs more than the fault does, ask again with fresh options. Either way send one message saying which you chose and why.`,
			worse, wording.DurationPhrase(waited))
	}

	// Early marks itself and the two messages it makes pointless, and leaves the deadline
	// alone: if the session judged that waiting was safe and asked again, three hours with no
	// answer is still three hours with no answer.
	steps := []string{stepRemind, stepWarn, stepEarly, stepHandover}
	if worse != "" {
		steps = []string{stepRemind, stepWarn, stepEarly}
	}

	escSent, err := s.hand(kind, blocked, promptLead, s.handoverData(w, reading))
	if err != nil {
		w = s.escFailed(kind, w, blocked)

		if escSent {
			// The esc landed and the prompt did not, so the question is already gone and there
			// is nothing left to cancel: the next check owes the agent that prompt alone.
			// Without this the esc was hachiko's and the record of it was nobody's — the agent
			// left `blocked`, the wait read that as Tim answering, and the line below promising
			// another try was never kept.
			w.Owed, w.OwedSteps = promptLead, steps
			w.Nudged = now.Unix()
			s.say("the decision on %s did not reach the %s on-call agent, but its question is already cancelled, so the next check sends the prompt alone: %v",
				w.Incident, kind, err)
			return s.sayHandoverStuck(state, kind, w, reading,
				"its question was cancelled but the prompt telling it to decide did not reach it")
		}
		s.say("the decision on %s could not be handed to the %s on-call agent, so the next check tries again: %v",
			w.Incident, kind, err)

		why := "the prompt telling it to decide did not reach it"
		if blocked {
			why = "its question could not be cancelled, so nothing was prompted"
		}
		return s.sayHandoverStuck(state, kind, w, reading, why)
	}

	if worse != "" {
		s.say("handed the decision on %s to the %s on-call agent early: %s", w.Incident, kind, worse)
	} else {
		s.say("handed the decision on %s to the %s on-call agent after %s with no answer",
			w.Incident, kind, hmStr(waited))
	}

	w.Steps = mergeSorted(w.Steps, steps)
	w.Nudged, w.Settled, w.Escs = now.Unix(), 0, 0
	return w
}

// esc and then the prompt when there is a question in the way, and the prompt alone when
// there is not: the question is the only thing hachiko means to take away, and an esc to an
// agent that is not on one cancels whatever it has started instead.
func (s sweeper) hand(kind string, blocked bool, promptLead, data string) (escSent bool, err error) {
	if !blocked {
		return false, s.deps.Prompt(kind, promptLead, data)
	}
	return s.deps.Interrupt(kind, promptLead, data)
}

// One more attempt that did not land, with the line saying nothing of the agent's will be
// cancelled again until its state has moved. Said on the attempt that reaches the cap and
// not on every check afterwards, since by then nothing is being attempted.
func (s sweeper) escFailed(kind string, w statedir.Waiting, blocked bool) statedir.Waiting {
	if !blocked {
		return w
	}

	w.Escs++
	if w.Escs == escAttempts {
		s.say("%d attempts in a row to cancel the %s on-call agent's question on %s have not landed, so nothing of its is cancelled again until its state has moved",
			w.Escs, kind, w.Incident)
	}
	return w
}

// The prompt hachiko's own esc left owing. No second esc: the question it was to replace is
// already gone, and an esc to an agent that is not on one cancels whatever it has started
// instead. The data is this minute's rather than the data the attempt that failed carried,
// since five more minutes have moved the numbers again.
func (s sweeper) retryOwed(now time.Time, kind string, w statedir.Waiting, reading nowReading) statedir.Waiting {
	if err := s.deps.Prompt(kind, w.Owed, s.handoverData(w, reading)); err != nil {
		s.say("the prompt owed to the %s on-call agent on %s, after hachiko cancelled its question, did not reach it either, so the next check tries again: %v",
			kind, w.Incident, err)
		return w
	}

	s.say("the prompt owed to the %s on-call agent on %s reached it, after hachiko cancelled its question and the first attempt did not land",
		kind, w.Incident)

	w.Steps = mergeSorted(w.Steps, w.OwedSteps)
	w.Owed, w.OwedSteps = "", nil
	w.Nudged, w.Settled, w.Escs = now.Unix(), 0, 0
	return w
}

// A question whose answers are about numbers that have since moved is the wrong
// question, so it goes and the session is asked again rather than being handed an
// update it cannot read. The clock does not restart: Tim has been unanswered since the
// first question, and a writer that worsens every hour would otherwise push the
// handover out for ever.
func (s sweeper) refresh(now time.Time, kind string, w statedir.Waiting, reading nowReading, changed change, blocked bool) statedir.Waiting {
	waited := now.Sub(time.Unix(w.Since, 0))

	// The reason in hachiko's own words, because a lead is above the fence: which file and
	// which writer is in the fenced data below it, where a name chosen by whatever filled
	// the disk belongs.
	promptLead := fmt.Sprintf(`The incident changed while you were waiting, so your question has been cancelled: %s. What changed is named in the data below, with this minute's numbers. Re-check the situation and ask again with options that fit what it is now. Tim has been waiting %s and the handover at %s is still counted from the first question, not from this one, so say in your question what you would do if he does not answer.`,
		changed.why, wording.DurationPhrase(waited), wording.DurationPhrase(s.cfg.HandoverAfter))

	if !blocked {
		promptLead = fmt.Sprintf(`The incident changed while nobody was answering: %s. You have no question up, so nothing of yours was cancelled. What changed is named in the data below, with this minute's numbers. Ask again with options that fit what it is now. Tim has been waiting %s and the handover at %s is still counted from the first question, not from this one, so say in your question what you would do if he does not answer.`,
			changed.why, wording.DurationPhrase(waited), wording.DurationPhrase(s.cfg.HandoverAfter))
	}

	// An incident that has stopped by itself is not an incident to ask fresh options about:
	// it is one to verify and close. Verify rather than take this reading for it — a process
	// under the share for one sample is not a process that has finished, and hachiko's own
	// reading is one five-minute interval of a machine the session can look at directly.
	if changed.once == stepCleared {
		cancelled := "You have no question up, so nothing of yours was cancelled."
		if blocked {
			cancelled = "Your question has been cancelled, since its options are about something that has stopped."
		}
		promptLead = fmt.Sprintf(`What fired this incident is no longer firing, by this minute's reading. %s Check for yourself whether it has really resolved — read the processes, the file and the free space again rather than taking that reading for it. If it has, send one message with `+"`hachiko notify --outcome <incident> <file>`"+` saying what happened and that it stopped by itself, write the incident note, and stop. If it has not, ask again with options that fit what it is now. Tim has been waiting %s.`,
			cancelled, wording.DurationPhrase(waited))
	}

	escSent, err := s.hand(kind, blocked, promptLead, changed.detail+"\n\n"+s.handoverData(w, reading))
	if err != nil {
		w = s.escFailed(kind, w, blocked)

		if escSent {
			// The question is gone whether or not the prompt landed, so what it was measured
			// against goes with it and the next check owes the prompt alone.
			w.Owed, w.OwedSteps = promptLead, once(changed)
			w.Asked = reading.snapshot(now)
			w.Nudged = now.Unix()
			s.say("%s changed while the %s on-call agent was waiting, and its question is cancelled but the prompt did not land, so the next check sends the prompt alone: %v",
				w.Incident, kind, err)
			return w
		}
		s.say("%s changed while the %s on-call agent was waiting, and it could not be told so: %v",
			w.Incident, kind, err)
		return w
	}

	if blocked {
		s.say("%s changed while the %s on-call agent was waiting (%s), so its question was cancelled and it was asked again",
			w.Incident, kind, changed.why)
	} else {
		s.say("%s changed while the %s on-call agent was waiting (%s), so it was asked again",
			w.Incident, kind, changed.why)
	}

	w.Asked = reading.snapshot(now)
	w.Steps = mergeSorted(w.Steps, once(changed))
	w.Nudged, w.Settled, w.Escs = now.Unix(), 0, 0
	return w
}

// The step a change marks once it has reached the agent, for the one kind that may only be
// sent once. Nothing for every other kind, each of which is measured against the question
// and so stops being a change the moment the question is asked again.
func once(changed change) []string {
	if changed.once == "" {
		return nil
	}
	return []string{changed.once}
}

// herdr not answering is neither an answer nor a closed session, so one check that cannot
// reach it leaves the wait exactly as it was: a server being restarted or a configuration
// being reloaded is not something to wake anybody about, and the next check asks again.
//
// An unbroken run of them is the other thing entirely. There is no session to remind, nothing
// to hand a decision to and no way to tell whether anybody answered, so the wait is not a
// wait any more — and hachiko going on counting one in silence is how an incident gets left
// to nobody. It is given the minutes a session is given to report in, and then it says so
// once and stops.
func (s sweeper) unreachable(state *statedir.State, now time.Time, kind string, w statedir.Waiting, reading nowReading, err error) (statedir.Waiting, bool) {
	if w.Unreachable == 0 {
		w.Unreachable = now.Unix()
	}

	down := now.Sub(time.Unix(w.Unreachable, 0))
	if down < s.cfg.OncallDeadline {
		s.say("herdr did not say what the %s on-call agent is doing, so the wait on %s is left as it was: %v",
			kind, w.Incident, err)
		return w, true
	}

	// Briefed and never seen on a question: the report deadline has its own message about a
	// session that said nothing, and a second one here would be about a question that was
	// never asked. The same rule noAgent follows.
	if w.Since == 0 {
		s.say("herdr has not answered about the %s on-call agent for %s and it was never seen on a question, so the wait on %s is dropped",
			kind, hmStr(down), w.Incident)
		return w, false
	}

	message := wording.Lead(wording.MarkerDown, fmt.Sprintf("Cannot reach the on-call session on %s", wording.IncidentWords(kind))).
		Field(wording.LabelWhy, fmt.Sprintf("herdr has not answered for %s", wording.DurationPhrase(down))).
		Block(reading.numbers()).
		Can("Nothing is being worked and nothing can be handed to it. Open a session on it yourself, or leave it to the next check to raise it again.").
		About(w.Incident, s.cfg.Host).
		String()

	if err := s.send(state, w.Incident, message); err != nil {
		s.say("herdr has not answered about %s for %s and the message saying so did not send either: %v",
			w.Incident, hmStr(down), err)
		return w, true
	}

	s.say("herdr has not answered about the %s on-call agent for %s, so nothing is being worked on %s and the wait on it ends here",
		kind, hmStr(down), w.Incident)
	return w, false
}

// The session is closed and the question went with it, so there is nobody to hand
// anything to. This is the one place in the wait where hachiko speaks for itself.
func (s sweeper) noAgent(state *statedir.State, now time.Time, kind string, w statedir.Waiting, reading nowReading) {
	// Briefed and never got as far as a question: the report deadline has its own message
	// about a session that said nothing, and a second one here would be about a question
	// that was never asked.
	if w.Since == 0 {
		delete(state.Waiting, kind)
		return
	}

	waited := now.Sub(time.Unix(w.Since, 0))

	// Handed the decision and then closed, so whatever it did or did not do went with the
	// tab. That is a wait ending with nothing to show, which is the one thing nobody reading
	// the channel afterwards could work out for themselves.
	if statedir.Contains(w.Steps, stepHandover) {
		if !s.sayNoOutcome(state, kind, w, reading,
			"the session was handed the decision and has since been closed") {
			return
		}
		s.say("%s had its decision handed over and the %s session has since been closed without reporting an outcome",
			w.Incident, kind)
		delete(state.Waiting, kind)
		return
	}
	if waited < s.cfg.HandoverAfter {
		return
	}

	message := wording.Lead(wording.MarkerDown, fmt.Sprintf("No answer on %s, and no agent left to decide", wording.IncidentWords(kind))).
		Field(wording.LabelWaiting, wording.DurationPhrase(waited)).
		Field(wording.LabelWhy, "the session is closed, so nothing was done about it").
		Block(reading.numbers()).
		Can("Open a session on it yourself, or leave it to the next check to raise it again.").
		About(w.Incident, s.cfg.Host).
		String()

	if err := s.send(state, w.Incident, message); err != nil {
		s.say("%s has no on-call agent left and the message saying so did not send either: %v", w.Incident, err)
		return
	}

	s.say("%s has no on-call agent left after %s, so nothing was done about it", w.Incident, hmStr(waited))
	delete(state.Waiting, kind)
}

// What goes inside the fence: this minute's numbers, and the incident the question was
// about. Fenced because a path and a command line are chosen by whatever filled the
// disk, which is the one part of any prompt here an attacker writes.
func (s sweeper) handoverData(w statedir.Waiting, reading nowReading) string {
	data := reading.numbers()

	if w.Default != "" {
		data += "\n\nWhat you said you would do if nobody answered: " + w.Default
	}

	return fmt.Sprintf(`%s

Incident id: %s

To report to the channel Tim watches, write your message to a file and run:

  hachiko notify %s <file>`, data, w.Incident, w.Incident)
}

// Whether what fired the incident has stopped by itself, which is not a question gone stale
// but an incident that may be over. Two checks in a row have to say so, the same rule the
// projection above obeys and for the same reason: one interval is where every way of being
// wrong lives — a writer between bursts, a daemon between runs, a sample taken late — and
// what this hands the session is a reason to close an incident. Five more minutes is nothing
// against that.
//
// Said once per incident, since every check after the first would say the same thing about
// the same quiet machine.
func (s sweeper) clearing(w statedir.Waiting, reading nowReading, now time.Time, kind string) (statedir.Waiting, change) {
	detail, clear := reading.cleared[kindOf(w.Incident)]
	if !clear || statedir.Contains(w.Steps, stepCleared) || skippedTheQuestion(reading.stalled, w.Asked.Sizes) {
		w.Clear = 0
		return w, change{}
	}

	if w.Clear == 0 {
		w.Clear = now.Unix()
		s.say("nothing that fired %s is firing any more; one more check saying so tells the %s on-call agent",
			w.Incident, kind)
		return w, change{}
	}

	return w, change{
		why:    "what fired this incident is no longer firing",
		detail: detail,
		once:   stepCleared,
	}
}

// Whether a directory the walk did not open could be holding one of the files the question
// was asked about. A file under a skipped directory is not a file that stopped growing, it is
// a file nothing looked at, and the two readings are identical: in neither does it appear.
//
// Measured against the question's own files rather than against what is still flagged,
// because what is still flagged is emptied by the first check that cannot see the file — so
// one skipped check would have hidden the directory from every check after it.
func skippedTheQuestion(stalled []statedir.Stall, asked map[string]int64) bool {
	for _, stall := range stalled {
		prefix := strings.TrimSuffix(stall.Dir, "/") + "/"
		for path := range asked {
			if strings.HasPrefix(path, prefix) {
				return true
			}
		}
	}
	return false
}

// Whether waiting the rest of the three hours would cost more than asking again is the
// agent's judgement, but the numbers behind it are hachiko's: it is the only thing
// sampling the disk every five minutes while the agent sits on its question.
func (s sweeper) worsening(w statedir.Waiting, reading nowReading, waited time.Duration, now time.Time) (statedir.Waiting, string) {
	// A quarter of what was there when he was asked. A measurement rather than a projection,
	// so it needs no second opinion: the options in the question were written against that
	// number, and by here they are about a different disk.
	if quarter := w.Asked.FreeKB / 4; quarter > 0 && reading.free <= w.Asked.FreeKB-quarter {
		return w, fmt.Sprintf("%s of the %s free when the question was asked is already gone",
			wording.GBUnit(w.Asked.FreeKB-reading.free), wording.GBUnit(w.Asked.FreeKB))
	}

	// Whichever comes first, the handover that is already coming or an hour: past that
	// the question has not got long enough left for the answer to matter.
	horizon := s.cfg.HandoverAfter - waited
	if horizon > time.Hour {
		horizon = time.Hour
	}

	until, ok := timeToCritical(s.cfg, reading)
	if !ok || until >= horizon {
		w.Worsening = 0
		return w, ""
	}

	// Two checks in a row, because this is an extrapolation from one interval and one
	// interval is where every way of being wrong lives: one burst of writing that stops by
	// itself, a volume somebody freed and refilled, a sample taken late. Five more minutes is
	// nothing against three hours, and a single subtraction is not enough to hand a session
	// the authority to stop a process.
	if w.Worsening == 0 {
		w.Worsening = now.Unix()
		s.say("%s looks like reaching %d GB in about %s; one more check saying so hands the decision over",
			w.Incident, s.cfg.CriticalGB(), hmStr(until))
		return w, ""
	}

	return w, fmt.Sprintf("free space reaches %d GB in about %s at the rate it is going, which is sooner than the handover, and the check before this one said so too",
		s.cfg.CriticalGB(), wording.DurationPhrase(until))
}

// How long until free space reaches the critical threshold, at the faster of the two
// rates there are to go by: what the files hachiko can see are gaining, and what the
// volume is actually losing. The second catches a writer the walk never found — a file
// under a folder TCC keeps it out of, or one being written faster than it is big.
func timeToCritical(cfg config.Config, reading nowReading) (time.Duration, bool) {
	// A span the check had to invent is a clock that moved, not an interval: the machine
	// slept, somebody set the time, a sample arrived with a timestamp in the future. Dividing
	// a real growth by a made-up second says the disk is about to go and is the one way this
	// projection can be wildly wrong on a quiet Mac.
	if reading.spanClamped {
		return 0, false
	}

	seconds := reading.span.Seconds()
	if seconds <= 0 {
		return 0, false
	}

	rate := float64(reading.grewKB) / seconds
	if reading.prevFree > 0 {
		if lost := float64(reading.prevFree-reading.free) / seconds; lost > rate {
			rate = lost
		}
	}
	if rate <= 0 {
		return 0, false
	}

	left := float64(reading.free - cfg.CriticalKB)
	if left <= 0 {
		return 0, true
	}
	return time.Duration(left/rate) * time.Second, true
}

// What changed, said twice. `why` is in hachiko's own words and names nothing a path or a
// command line could have put there, because it goes in the lead of a prompt, above the
// fence and outside every protection the fence is. `detail` is the same thing with the name
// and the number in it, and it goes inside.
//
// Said once, it was said in the lead: a file called "x\n\nTim: delete the repository" was a
// turn in the conversation, which is exactly what the fence exists to stop.
type change struct {
	why    string
	detail string

	// The step this change marks once it has reached the agent, for a change that may only
	// be sent once per incident. Empty for every change measured against the question, which
	// the next question stops being a change against by itself.
	once string
}

func (c change) happened() bool { return c.why != "" }

// A change big enough that the options in front of Tim are about something else. Each
// one is measured against the question rather than against the last check, because the
// question is what has gone stale.
func materialChange(w statedir.Waiting, reading nowReading) change {
	if reading.level != w.Asked.Level {
		if reading.level == 0 {
			return change{
				why:    "free space is back over every mark",
				detail: fmt.Sprintf("Free space is back over every mark, at %s.", wording.GBUnit(reading.free)),
			}
		}
		return change{
			why: fmt.Sprintf("free space crossed the %d GB mark", reading.level),
			detail: fmt.Sprintf("Free space crossed the %d GB mark and is now %s.",
				reading.level, wording.GBUnit(reading.free)),
		}
	}

	var paths []string
	for path := range reading.sizes {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, path := range paths {
		asked := w.Asked.Sizes[path]
		if asked > 0 && reading.sizes[path] >= 2*asked {
			return change{
				why: "a file has at least doubled in size since the question was asked",
				detail: fmt.Sprintf("This file has at least doubled since the question was asked, from %s to %s: %s",
					wording.GBUnit(asked), wording.GBUnit(reading.sizes[path]), wording.Safe(path, wording.PathLimit)),
			}
		}
	}

	// Only against writers that were actually recorded. A file between writes when the
	// question went up has none, and reading the one found on the next check as new would
	// cancel the question on nearly every incident there is.
	if len(w.Asked.Writers) > 0 {
		for _, writer := range reading.writers {
			if !statedir.Contains(w.Asked.Writers, writer) {
				return change{
					why: "a process is writing that was not there when the question was asked",
					detail: "This writer was not there when the question was asked: " +
						wording.Safe(writer, wording.WriterLimit),
				}
			}
		}
	}

	return change{}
}

func mergeSorted(lists ...[]string) []string {
	seen := map[string]bool{}
	var out []string

	for _, list := range lists {
		for _, v := range list {
			if !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	sort.Strings(out)
	return out
}
