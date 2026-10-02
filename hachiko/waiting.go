package main

import (
	"fmt"
	"sort"
	"time"
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
	stepHandover = "handover"
)

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
	report   string
}

func (r nowReading) snapshot(now time.Time) Asked {
	sizes := make(map[string]int64, len(r.sizes))
	for path, kb := range r.sizes {
		sizes[path] = kb
	}
	return Asked{
		At:      now.Unix(),
		FreeKB:  r.free,
		Level:   r.level,
		Sizes:   sizes,
		Writers: append([]string(nil), r.writers...),
	}
}

func (r nowReading) numbers(host string) string {
	return fmt.Sprintf("Now on %s: %s GB free.%s", host, gbStr(r.free), r.report)
}

// One `herdr agent get` per kind and no more, because there is one on-call agent per
// kind: what it is doing is the whole of how hachiko knows whether anybody answered.
func (s sweeper) chaseAnswers(state *State, now time.Time, reading nowReading) {
	for _, kind := range sortedKeys(state.Waiting) {
		w := state.Waiting[kind]

		status, err := s.deps.AgentStatus(kind)
		if err != nil {
			s.say("herdr did not say what the %s on-call agent is doing, so the wait on %s is left as it was: %v",
				kind, w.Incident, err)
			continue
		}

		// A second report under the same id is the outcome: whatever the question was
		// waiting for has happened, so there is nothing left to chase.
		if s.store.Reported(w.Incident) {
			s.say("the %s on-call agent reported the outcome of %s", kind, w.Incident)
			s.store.ClearReported(w.Incident)
			delete(state.Waiting, kind)
			continue
		}

		switch {
		case status == statusGone:
			s.noAgent(state, now, kind, w, reading)

		case status == statusBlocked:
			state.Waiting[kind] = s.escalate(now, kind, w, reading)

		case status == statusWorking && w.Nudged != 0:
			// hachiko took the question away itself and the agent is working on what it
			// was handed instead. It will ask again or report, and the clock goes on
			// running meanwhile — the wait is Tim's, not the agent's.

		case w.Since != 0:
			// It was on a question and it is not any more, and hachiko did not take the
			// question away — so that was Tim, and the clock he was being waited for stops
			// with it.
			if w.Nudged == 0 {
				s.say("%s was answered after %s, so the %s on-call agent is no longer waiting",
					w.Incident, hmStr(now.Sub(time.Unix(w.Since, 0))), kind)
			}
			delete(state.Waiting, kind)

		case now.Sub(time.Unix(w.Opened, 0)) >= s.cfg.OncallDeadline:
			// It was briefed and never got as far as a question in the time it was given to
			// report in, so it is not going to ask one. The deadline has already said so
			// to Tim in its own message.
			delete(state.Waiting, kind)
		}
	}
}

// The clock, and the one message per step on it. The order is deliberate: a handover
// that is due makes a reminder noise, and a question that is about to be cancelled and
// asked again is not one to remind him about either.
func (s sweeper) escalate(now time.Time, kind string, w Waiting, reading nowReading) Waiting {
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
	w.Nudged = 0

	waited := now.Sub(time.Unix(w.Since, 0))
	done := contains(w.Steps, stepHandover)

	if worse := s.worseningFast(w, reading, waited); worse != "" && !done {
		return s.handOver(now, kind, w, reading, worse)
	}
	if waited >= s.cfg.HandoverAfter && !done {
		return s.handOver(now, kind, w, reading, "")
	}
	if done {
		return w
	}

	if changed := materialChange(w, reading); changed != "" {
		return s.refresh(now, kind, w, reading, changed)
	}

	switch {
	case waited >= s.cfg.WarnAfter && !contains(w.Steps, stepWarn):
		return s.warn(w, reading, waited)
	case waited >= s.cfg.RemindAfter && !contains(w.Steps, stepRemind):
		return s.remind(w, reading, waited)
	}
	return w
}

func (s sweeper) remind(w Waiting, reading nowReading, waited time.Duration) Waiting {
	message := fmt.Sprintf(`%s is still waiting for you after %s, and still waiting for you in herdr (workspace %s, tab %s).

%s`, w.Incident, hmStr(waited), s.cfg.WorkspaceLabel(), w.Tab, reading.numbers(s.cfg.Host))

	return s.step(w, stepRemind, message,
		fmt.Sprintf("reminded about %s after %s", w.Incident, hmStr(waited)))
}

// A quarter of an hour, which is long enough to answer from a phone and short enough
// that it is not a second reminder.
func (s sweeper) warn(w Waiting, reading nowReading, waited time.Duration) Waiting {
	fallback := w.Default
	if fallback == "" {
		fallback = "the agent named no fallback option, so it will decide when it re-checks"
	}

	message := fmt.Sprintf(`No answer yet on %s after %s. In %s the agent will decide and act on its own, within its limits.

If no answer: %s

%s

It is waiting for you in herdr (workspace %s, tab %s).`,
		w.Incident, hmStr(waited), hmStr(s.cfg.HandoverAfter-s.cfg.WarnAfter), fallback,
		reading.numbers(s.cfg.Host), s.cfg.WorkspaceLabel(), w.Tab)

	return s.step(w, stepWarn, message,
		fmt.Sprintf("warned that %s is %s from the handover", w.Incident, hmStr(s.cfg.HandoverAfter-waited)))
}

// A step is done only once the message has actually left the machine, and the steps
// before it are marked with it: a check that comes back after an outage has no reason to
// send an hour's reminder about a question that is already past its deadline.
func (s sweeper) step(w Waiting, step, message, said string) Waiting {
	if err := s.deps.Send(message); err != nil {
		s.say("the %s step on %s did not send and is left to the next check: %v", step, w.Incident, err)
		return w
	}

	s.say("%s", said)
	switch step {
	case stepRemind:
		w.Steps = mergeSorted(w.Steps, []string{stepRemind})
	case stepWarn:
		w.Steps = mergeSorted(w.Steps, []string{stepRemind, stepWarn})
	}
	return w
}

// The handover itself: the question goes, and the session is told to decide. Nothing of
// hachiko's own goes to the channel here — the session's own message is what says what
// it did and why, and two messages about one decision would be one too many.
func (s sweeper) handOver(now time.Time, kind string, w Waiting, reading nowReading, worse string) Waiting {
	waited := now.Sub(time.Unix(w.Since, 0))

	lead := fmt.Sprintf(`Tim has not answered for %s, so the autonomy in your standing orders is handed over to you now. Re-check the situation from scratch first — the numbers below are this minute's, not the ones you asked about — then pick and carry out the least destructive option that resolves it, inside the limits those orders give you. Spawn the oncall-partner agent with your proposed action first and act only if it agrees. Verify it worked, send one message with what you did, why, which limit allowed it and what the partner said, write the incident note, and stop.`,
		hmStr(waited))

	if worse != "" {
		lead = fmt.Sprintf(`The incident is getting worse rapidly: %s. Tim has not answered for %s, and the three-hour handover would come too late, so the autonomy in your standing orders is handed over to you now, early.

Re-check the situation from scratch — the numbers below are this minute's — and judge it yourself. If you agree that waiting costs more than acting, pick and carry out the least destructive option that resolves it, inside the limits those orders give you, with the oncall-partner agent's agreement first. If you judge instead that it is about to stop by itself or that acting costs more than the fault does, ask again with fresh options. Either way send one message saying which you chose and why.`,
			worse, hmStr(waited))
	}

	if err := s.deps.Interrupt(kind, lead, s.handoverData(w, reading)); err != nil {
		s.say("the decision on %s could not be handed to the %s on-call agent, so the next check tries again: %v",
			w.Incident, kind, err)
		return w
	}

	if worse != "" {
		s.say("handed the decision on %s to the %s on-call agent early: %s", w.Incident, kind, worse)
	} else {
		s.say("handed the decision on %s to the %s on-call agent after %s with no answer",
			w.Incident, kind, hmStr(waited))
	}

	w.Steps = mergeSorted(w.Steps, []string{stepRemind, stepWarn, stepHandover})
	w.Nudged = now.Unix()
	return w
}

// A question whose answers are about numbers that have since moved is the wrong
// question, so it goes and the session is asked again rather than being handed an
// update it cannot read. The clock does not restart: Tim has been unanswered since the
// first question, and a writer that worsens every hour would otherwise push the
// handover out for ever.
func (s sweeper) refresh(now time.Time, kind string, w Waiting, reading nowReading, changed string) Waiting {
	waited := now.Sub(time.Unix(w.Since, 0))

	lead := fmt.Sprintf(`The incident changed while you were waiting, so your question has been cancelled: %s. Re-check the situation — the numbers below are this minute's — and ask again with options that fit what it is now. Tim has been waiting %s and the handover at %s is still counted from the first question, not from this one, so say in your question what you would do if he does not answer.`,
		changed, hmStr(waited), hmStr(s.cfg.HandoverAfter))

	if err := s.deps.Interrupt(kind, lead, s.handoverData(w, reading)); err != nil {
		s.say("%s changed while the %s on-call agent was waiting, and its question could not be cancelled: %v",
			w.Incident, kind, err)
		return w
	}

	s.say("%s changed while the %s on-call agent was waiting (%s), so its question was cancelled and it was asked again",
		w.Incident, kind, changed)

	w.Asked = reading.snapshot(now)
	w.Nudged = now.Unix()
	return w
}

// The session is closed and the question went with it, so there is nobody to hand
// anything to. This is the one place in the wait where hachiko speaks for itself.
func (s sweeper) noAgent(state *State, now time.Time, kind string, w Waiting, reading nowReading) {
	if w.Since == 0 || contains(w.Steps, stepHandover) {
		delete(state.Waiting, kind)
		return
	}

	waited := now.Sub(time.Unix(w.Since, 0))
	if waited < s.cfg.HandoverAfter {
		return
	}

	message := fmt.Sprintf(`No answer on %s after %s and no on-call agent left to decide: the %s session is closed, so nothing was done about it.

%s

Open a session on it yourself, or leave it to the next check to raise again.`,
		w.Incident, hmStr(waited), kind, reading.numbers(s.cfg.Host))

	if err := s.deps.Send(message); err != nil {
		s.say("%s has no on-call agent left and the message saying so did not send either: %v", w.Incident, err)
		return
	}

	s.say("%s has no on-call agent left after %s, so nothing was done about it", w.Incident, hmStr(waited))
	delete(state.Waiting, kind)
}

// What goes inside the fence: this minute's numbers, and the incident the question was
// about. Fenced because a path and a command line are chosen by whatever filled the
// disk, which is the one part of any prompt here an attacker writes.
func (s sweeper) handoverData(w Waiting, reading nowReading) string {
	data := reading.numbers(s.cfg.Host)

	if w.Default != "" {
		data += "\n\nWhat you said you would do if nobody answered: " + w.Default
	}

	return fmt.Sprintf(`%s

Incident id: %s

To report to the channel Tim watches, write your message to a file and run:

  hachiko notify %s <file>`, data, w.Incident, w.Incident)
}

// Whether waiting the rest of the three hours would cost more than asking again is the
// agent's judgement, but the numbers behind it are hachiko's: it is the only thing
// sampling the disk every five minutes while the agent sits on its question.
func (s sweeper) worseningFast(w Waiting, reading nowReading, waited time.Duration) string {
	// Whichever comes first, the handover that is already coming or an hour: past that
	// the question has not got long enough left for the answer to matter.
	horizon := s.cfg.HandoverAfter - waited
	if horizon > time.Hour {
		horizon = time.Hour
	}

	if until, ok := timeToCritical(s.cfg, reading); ok && until < horizon {
		return fmt.Sprintf("free space reaches %d GB in about %s at the rate it is going, which is sooner than the handover",
			s.cfg.CriticalGB(), hmStr(until))
	}

	// A quarter of what was there when he was asked. The options in the question were
	// written against that number, so by here they are about a different disk.
	if quarter := w.Asked.FreeKB / 4; quarter > 0 && reading.free <= w.Asked.FreeKB-quarter {
		return fmt.Sprintf("%s GB of the %s GB free when the question was asked is already gone",
			gbStr(w.Asked.FreeKB-reading.free), gbStr(w.Asked.FreeKB))
	}

	return ""
}

// How long until free space reaches the critical threshold, at the faster of the two
// rates there are to go by: what the files hachiko can see are gaining, and what the
// volume is actually losing. The second catches a writer the walk never found — a file
// under a folder TCC keeps it out of, or one being written faster than it is big.
func timeToCritical(cfg Config, reading nowReading) (time.Duration, bool) {
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

// A change big enough that the options in front of Tim are about something else. Each
// one is measured against the question rather than against the last check, because the
// question is what has gone stale.
func materialChange(w Waiting, reading nowReading) string {
	if reading.level != w.Asked.Level {
		switch {
		case reading.level == 0:
			return "free space is back over every threshold"
		default:
			return fmt.Sprintf("free space crossed the %d GB threshold", reading.level)
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
			return fmt.Sprintf("%s has doubled to %s GB since the question was asked",
				clip(path, pathLimit), gbStr(reading.sizes[path]))
		}
	}

	// Only against writers that were actually recorded. A file between writes when the
	// question went up has none, and reading the one found on the next check as new would
	// cancel the question on nearly every incident there is.
	if len(w.Asked.Writers) > 0 {
		for _, writer := range reading.writers {
			if !contains(w.Asked.Writers, writer) {
				return "a writer that was not there when the question was asked: " + clip(writer, writerLimit)
			}
		}
	}

	return ""
}
