package watch

import (
	"fmt"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
	"github.com/timche/mac-mini/hachiko/internal/vuln"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// The vulnerability check's half of the sweep. Every three hours rather than every five
// minutes, because what it reads is what the world has published and not what this Mac is
// doing: an advisory is published once, the sources behind it are public and rate-limited, and
// the readings it takes cost a dozen subprocesses and four feeds. Eight passes a day is well
// inside every one of those limits and still means nothing published goes unnoticed for a
// working day.
//
// Inside the sweep and under its lock rather than on an agent of its own, which is the shape
// `hachiko update` took for a different reason: an update takes tens of minutes and so has to
// be a process of its own, where a pass of this is bounded at two minutes and has an incident
// to raise at the end of it, which is the sweep's own machinery.
const vulnEvery = 3 * time.Hour

// The labels the line saying a Mac is no longer affected goes under.
const (
	labelAdvisory = "Advisory"
	labelWas      = "Was"
	labelOpen     = "Open since"
)

func (s sweeper) vulns(state *statedir.State, now time.Time) {
	// A dry run says it would look and does not look. Every other half of the sweep takes its
	// readings in a dry run, and this one deliberately does not: the readings are four public
	// feeds and a dozen subprocesses, and `hachiko --dry-run` is a command somebody runs to see
	// what the sweep sees — including the one `test/assert.sh` runs under launchd with twenty
	// seconds to finish in. `hachiko --vuln-check` is the one that really looks.
	if s.dry {
		s.say("would look for published vulnerabilities affecting this Mac")
		return
	}

	if now.Sub(time.Unix(state.VulnsChecked, 0)) < vulnEvery {
		return
	}

	// Written before the pass rather than after it, so a source that hangs or a feed that
	// answers with nonsense costs three hours rather than every five minutes for the life of
	// the Mac. What must not be written on a pass that failed is a feed's own cursor, and that
	// is kept apart from this for exactly that reason.
	state.VulnsChecked = now.Unix()

	found := s.deps.Vulns(state.VulnCursors)

	s.vulnsCleared(state, found, now)

	// Everything this pass found that this Mac has not been told about. One incident for all
	// of them, the way a night's failed steps are one incident: they are one pass's news and
	// one session's reading, and `raise` supersedes any earlier pending incident of the same
	// kind — so a second raise in one sweep would leave the first post with nobody on it.
	var fresh []string
	exposures := make([]vuln.Exposure, 0, len(found.Exposures))
	for _, e := range found.Exposures {
		if _, known := state.Vulns[e.Key()]; !known {
			exposures = append(exposures, e)
			fresh = append(fresh, e.Key())
		}
	}

	leads := make([]vuln.Lead, 0, len(found.Leads))
	for _, l := range found.Leads {
		if _, known := state.Vulns[l.Key()]; !known {
			leads = append(leads, l)
			fresh = append(fresh, l.Key())
		}
	}

	// The upgrade before the message, so the message can name the command that is running
	// rather than one that might be. Across every exposure this pass found and not only the
	// fresh ones: an advisory already raised as something to read, whose version a later pass
	// has matched exactly, is still something to upgrade.
	s.fixVulns(state, &found)

	news := vuln.Found{Exposures: exposures, Leads: leads,
		Troubles: found.Troubles, Started: found.Started}

	if news.Empty() {
		s.readUpTo(state, found)
		s.forgetVulns(state, now)
		return
	}

	incident := fmt.Sprintf("%s-%d", statedir.KindVuln, now.Unix())
	if !s.raise(state, now, incident, s.vulnAlert(news), false) {
		// The cursors stay where they were, so the next pass reads the same feed items again
		// and nothing published is lost to a Discord that would not take the message. What is
		// not held back is the three hours: a pass costs a dozen subprocesses and four feeds,
		// and retrying one every five minutes while Discord is down would be the heaviest thing
		// on this Mac.
		s.say("the published vulnerabilities this pass found did not send, so the feeds are left where they were and the next pass finds them again")
		return
	}

	s.readUpTo(state, found)

	s.say("raised %s as %s",
		wording.CountOf(int64(len(fresh)), "newly published advisory", "newly published advisories"),
		incident)

	for _, e := range exposures {
		state.SetVuln(e.Key(), statedir.Vuln{Incident: incident, Thread: state.Threads[incident],
			Since: now.Unix(), What: e.Name + " " + e.Version, Exact: true, Source: e.Source,
			Fixing: contains(found.Started, e.Target)})
	}
	for _, l := range leads {
		state.SetVuln(l.Key(), statedir.Vuln{Incident: incident, Thread: state.Threads[incident],
			Since: now.Unix(), What: l.Title})
	}

	s.forgetVulns(state, now)
}

// How far each feed has been read, written only once this pass has nothing left it could
// lose: a source read up to now is a source whose items will never come round again, so the
// cursor may not move until whatever came out of it has reached Discord. Only the sources
// the pass actually reached are in the map, which is what keeps one that could not be read
// from recording somebody else's progress as its own.
func (s sweeper) readUpTo(state *statedir.State, found vuln.Found) {
	for source, at := range found.Cursors {
		if state.VulnCursors == nil {
			state.VulnCursors = map[string]int64{}
		}
		state.VulnCursors[source] = at
	}
}

// What the post says, in both lengths it may go out in. An advisory is not urgent in the sense
// the disk is — nothing is about to run out — but it can be long: a module that has not moved
// in four years had twenty-six advisories against it the day this was written, and Discord
// caps a message at two thousand characters. So the short form is one bullet per thing that is
// affected and the full one is one per advisory, which is what the session reads.
func (s sweeper) vulnAlert(found vuln.Found) alertText {
	marker := wording.MarkerDegraded
	if len(found.Exposures) > 0 {
		marker = wording.MarkerDown
	}

	return alertText{
		marker:   marker,
		sentence: vuln.Headline(found),
		short:    vuln.Summary(found),
		full:     vuln.Details(found),
	}
}

// The automatic fix, which is Tim's decision and not a session's: where a source says
// outright that an installed version is affected and names a released version that is not,
// and the thing is one `hachiko update` already moves, the upgrade starts now. Waiting for
// somebody to approve a version bump that the nightly run would have made anyway is a night
// of exposure for nothing.
//
// It is `hachiko update` that moves it and not this, which is the repository's own rule: the
// update is the one thing on this Mac that moves a version. What this does is name the targets
// and write down having named them.
func (s sweeper) fixVulns(state *statedir.State, found *vuln.Found) {
	started := map[string]bool{}
	for key, rec := range state.Vulns {
		started[key] = rec.Fixing
	}

	targets := found.Targets(started)
	if len(targets) == 0 {
		return
	}

	if err := s.deps.FixVulns(targets); err != nil {
		s.say("the security upgrade of %v could not be started, so the next pass asks again: %v", targets, err)
		return
	}

	s.say("started a security upgrade of %v, ahead of the release-age hold", targets)
	found.Started = targets

	// Written down against every advisory the upgrade covers, including ones already raised:
	// an upgrade that did not fix the fault must not be started again every three hours.
	for _, key := range found.Covered(targets) {
		rec := state.Vulns[key]
		rec.Fixing = true
		if rec.Incident != "" {
			state.SetVuln(key, rec)
		}
	}
}

// An advisory whose version this Mac no longer has. Only an exact finding can clear by itself,
// because only an exact finding is something a later pass measures again — a lead is an
// advisory somebody has to read, and it is closed by the session that read it.
//
// The line goes into the thread of the post it was raised in, and as that incident's outcome
// only when it is the last thing left under the post: an outcome retags the post Resolved and
// archives it, and a sibling advisory still open would be left under a post saying it is over.
// That is the nightly update's rule for the same reason.
func (s sweeper) vulnsCleared(state *statedir.State, found vuln.Found, now time.Time) {
	open := map[string]int{}
	for _, rec := range state.Vulns {
		if rec.Incident != "" {
			open[rec.Incident]++
		}
	}

	for _, key := range statedir.SortedKeys(state.Vulns) {
		rec := state.Vulns[key]
		if !rec.Exact || found.Matches(key) {
			continue
		}

		// A reading that is missing is not a reading that is clear, which is the sweep's own
		// rule about the disk and the one that matters most here: a pass where `brew vulns`
		// would not run finds no formula affected, and without this it would post that this Mac
		// is no longer affected by every formula advisory open against it.
		if !found.Read(rec.Source) {
			continue
		}

		open[rec.Incident]--
		if !s.sayVulnCleared(state, key, rec, now, open[rec.Incident] == 0) {
			open[rec.Incident]++
			continue
		}
		delete(state.Vulns, key)
	}
}

func (s sweeper) sayVulnCleared(state *statedir.State, key string, rec statedir.Vuln, now time.Time, last bool) bool {
	// Nobody was ever told, which is an advisory raised by a pass whose message never sent.
	// There is nothing to say is over.
	if rec.Incident == "" {
		return true
	}

	message := wording.Lead(wording.MarkerRecovered, "No longer affected by "+
		wording.Safe(key, wording.NameLimit)).
		Field(labelAdvisory, wording.CodeSpan(wording.Safe(key, wording.NameLimit))).
		Field(labelWas, wording.PlainWords(wording.Safe(rec.What, wording.PathLimit))).
		Field(labelOpen, wording.DurationPhrase(now.Sub(time.Unix(rec.Since, 0)))).
		About(rec.Incident, s.cfg.Host).
		String()

	if _, err := s.deps.Send(discord.Outgoing{
		Text: message, Thread: state.ThreadFor(rec.Incident), Outcome: last,
	}); err != nil {
		s.say("the line saying this Mac is no longer affected by %s did not send and is left to the next pass: %v", key, err)
		return false
	}

	s.say("this Mac is no longer affected by %s, which was %s", key, rec.What)
	return true
}

// A record for an advisory nothing is affected by any more and nothing is waiting on, and the
// wait on an incident with nothing left under it. The second matters the way the update's does:
// a wait left open has the listener polling an archived post and `hachiko status` calling a
// resolved incident open for ever.
func (s sweeper) forgetVulns(state *statedir.State, now time.Time) {
	under := map[string]bool{}
	for _, rec := range state.Vulns {
		under[rec.Incident] = true
	}

	for _, id := range statedir.SortedKeys(state.Pending) {
		if statedir.KindOf(id) == statedir.KindVuln && !under[id] {
			s.say("nothing is open under %s any more, so no report on it is owing", id)
			delete(state.Pending, id)
		}
	}

	if w, ok := state.Waiting[statedir.KindVuln]; ok && !under[w.Incident] {
		s.say("nothing is open under %s any more, so the wait on it ends", w.Incident)
		delete(state.Waiting, statedir.KindVuln)
	}

	// And the record itself, once it is older than the furthest back any feed here is ever
	// read. Until then it is what stops one advisory being raised twice; past it, a cursor
	// rebuilt from nothing cannot surface the advisory again, so there is nothing left for the
	// record to stop and keeping it would be a state file that only ever grows.
	for _, key := range statedir.SortedKeys(state.Vulns) {
		if now.Sub(time.Unix(state.Vulns[key].Since, 0)) > statedir.VulnRemembered {
			delete(state.Vulns, key)
		}
	}
}

func contains(list []string, want string) bool {
	return want != "" && statedir.Contains(list, want)
}
