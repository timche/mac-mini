package watch

import (
	"fmt"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/statedir"
	"github.com/timche/mac-mini/hachiko/internal/update"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// The nightly update's half of the check. A step `hachiko update` could not do is exactly
// what an on-call session is for: somebody has to read the failure, the log of the run it
// came from, the step's own code and what the tool itself is doing, and nobody is at this Mac
// at four in the morning. So the update records what it is failing on and nothing else, and
// this raises it through the same machinery every other incident goes through — the post and
// its thread, `hachiko notify`, Tim's reply out of Discord, the raw details if nothing
// reports, the reminders.
//
// The one thing that is different is that there is nothing to hand over. A disk filling can
// be measured every five minutes and acted on; a failed step cannot be measured at all and
// may not be retried by anything here, since retrying it is moving a version with nobody
// watching. See measured in waiting.go for what that takes out of the clock.
func (s sweeper) updates(state *statedir.State, now time.Time) {
	failing := s.deps.UpdateFailing()
	raised := s.deps.UpdateRaised()

	var fresh []update.Handed
	for _, step := range failing {
		if _, known := raised[step.Step]; !known {
			fresh = append(fresh, step)
		}
	}

	if s.dry {
		if len(fresh) > 0 {
			s.say("would raise %s the nightly update could not do as one incident, and open an on-call session on it",
				wording.CountOf(int64(len(fresh)), "step", "steps"))
		}
		return
	}

	// Every step the update has stopped failing on. The line saying one works again is the
	// update's own, sent as that incident's outcome into the post this opened, so there is
	// nothing to send here — what is left is to stop waiting on an incident nothing is
	// failing under any more.
	still := map[string]bool{}
	for _, step := range failing {
		still[step.Step] = true
	}

	changed := false
	for _, step := range statedir.SortedKeys(raised) {
		if still[step] {
			continue
		}
		s.say("the nightly update is no longer failing on %s", step)
		delete(raised, step)
		changed = true
	}

	// All of a run's new failures as one incident, because they are one night's news and one
	// session's reading: four posts and four agents on one update is four sets of options for
	// Tim to reconcile.
	if len(fresh) > 0 {
		incident := fmt.Sprintf("%s-%d", statedir.KindUpdate, now.Unix())

		if s.raise(state, now, incident, s.updateAlert(fresh, now), false) {
			s.say("raised %s the nightly update could not do as %s",
				wording.CountOf(int64(len(fresh)), "step", "steps"), incident)

			for _, step := range fresh {
				raised[step.Step] = update.Raised{Incident: incident, Thread: state.Threads[incident]}
			}
			changed = true
		}
	}

	s.forgetUpdates(state, raised)

	if changed {
		if err := s.deps.SetUpdateRaised(raised); err != nil {
			s.say("which incident each failed update step was raised as could not be written down, so the next check may raise one of them a second time: %v", err)
		}
	}
}

// What the post says, in the one length it has. A failed step is not urgent and it does not
// move, so there is no short form of it: everything there is to know is in the first message
// whether or not a session is reading it too.
func (s sweeper) updateAlert(failed []update.Handed, now time.Time) alertText {
	details := update.Details(failed, now)

	return alertText{
		marker:   wording.MarkerDegraded,
		sentence: update.Lead(failed),
		short:    details,
		full:     details,
	}
}

// An incident of the update's with no failing step left under it. The update has already said
// in its thread that the step works again and closed the post, so there is nobody left to
// wait for and nothing left to say — and a wait left open would have the listener polling an
// archived post and `hachiko status` calling a resolved incident open for ever.
func (s sweeper) forgetUpdates(state *statedir.State, raised map[string]update.Raised) {
	under := map[string]bool{}
	for _, was := range raised {
		under[was.Incident] = true
	}

	for _, id := range statedir.SortedKeys(state.Pending) {
		if statedir.KindOf(id) == statedir.KindUpdate && !under[id] {
			s.say("nothing is failing under %s any more, so no report on it is owing", id)
			delete(state.Pending, id)
		}
	}

	if w, ok := state.Waiting[statedir.KindUpdate]; ok && !under[w.Incident] {
		s.say("nothing is failing under %s any more, so the wait on it ends", w.Incident)
		delete(state.Waiting, statedir.KindUpdate)
	}
}
