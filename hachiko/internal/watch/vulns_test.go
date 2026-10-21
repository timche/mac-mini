package watch

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/harness"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
	"github.com/timche/mac-mini/hachiko/internal/vuln"
)

// Every one of these walks the clock in three-hour steps, which is the one thing a pass of
// this is counted in. So the question's own clock is put far out of the way: a reminder after
// twelve minutes is about a disk somebody has been asked about, and an advisory is neither
// measured nor retried between passes.
func vulnCheck(t *testing.T) *fixture {
	t.Helper()

	f := newFixture(t)
	f.threads = true
	f.cfg.RemindAfter = 48 * time.Hour
	f.cfg.WarnAfter = 72 * time.Hour
	f.cfg.HandoverAfter = 96 * time.Hour

	return f
}

// A sweep n passes on from the first, which is a sweep that looks again. Anything inside the
// three hours is a sweep that does not.
func (f *fixture) vulnPass(n int64) string {
	f.t.Helper()
	return f.at(n * int64(vulnEvery/time.Second)).sweep()
}

// The names a source's findings carry, spelled as the vuln package spells them: a record
// already raised is measured against the source that found it, so these are literals here
// rather than anything this package could import.
const (
	fromOSV  = "OSV"
	fromBrew = "brew vulns"
)

// A version a source says outright is affected, in the shape a source hands one over: a
// target where `hachiko update` is what moves it, and the one line for Tim where nothing
// here does — which is also what a source leaves when no fixed version is released.
func exposed(id, name, version, fixed, target string) vuln.Exposure {
	e := vuln.Exposure{
		ID: id, Source: fromOSV, Severity: "high",
		Summary: "a heap overflow in the tar reader",
		Name:    name, Version: version, Fixed: fixed, Target: target,
	}
	if target == "" {
		e.Needs = "**Needs you:** nothing on this Mac moves it."
	}
	return e
}

func watchFor(id, title string) vuln.Lead {
	return vuln.Lead{
		ID: id, Source: "nvd", Title: title, Severity: "critical",
		Why: "it names something installed here and no version was compared",
	}
}

// Four feeds and a dozen subprocesses, against a sweep that runs every five minutes: what
// this reads is what the world has published, which is published once.
func TestTheVulnerabilityCheckLooksEveryThreeHours(t *testing.T) {
	f := vulnCheck(t)

	f.at(0).sweep()
	harness.Equal(t, len(f.vulnCursors), 1, "passes that looked")
	harness.Equal(t, f.state().VulnsChecked, base.Unix(), "when it last looked")

	for _, at := range []int64{300, 1800, 10799} {
		f.at(at).sweep()
	}
	harness.Equal(t, len(f.vulnCursors), 1, "passes that looked")

	f.vulnPass(1)
	harness.Equal(t, len(f.vulnCursors), 2, "passes that looked")
	harness.Equal(t, f.state().VulnsChecked, base.Add(vulnEvery).Unix(), "when it last looked")
}

// The one half of the sweep that takes none of its readings in a dry run: `hachiko --dry-run`
// is a command somebody runs to see what the sweep sees, including the one `test/assert.sh`
// runs under launchd with twenty seconds to finish in.
func TestADryRunLooksForNoAdvisories(t *testing.T) {
	f := vulnCheck(t)
	f.vulnPasses = []vuln.Found{{Exposures: []vuln.Exposure{
		exposed("GHSA-AAAA-1111", "node", "24.21.0", "24.22.1", "mise:node")}}}

	out := f.dryRun()

	harness.Wants(t, out, "would look for published vulnerabilities affecting this Mac")
	harness.Equal(t, len(f.vulnCursors), 0, "passes that looked")
	harness.Equal(t, len(f.fixed), 0, "upgrades started")
	harness.Equal(t, f.oncallCalls, 0, "sessions opened")
	harness.Equal(t, f.sentCount(), 0, "messages sent")
}

// A source that hung or answered nonsense costs three hours rather than every five minutes
// for the life of the Mac — and costs its own cursor nothing, since everything published
// while it was down is still ahead of where this Mac has read to.
func TestASourceThatFailedStillCountsAsALook(t *testing.T) {
	f := vulnCheck(t)
	f.vulnPasses = []vuln.Found{
		{Cursors: map[string]int64{"osv": 10, "nvd": 20}},
		{Cursors: map[string]int64{"osv": 30}, Troubles: []string{"NVD did not answer"}},
	}

	f.at(0).sweep()
	f.vulnPass(1)

	state := f.state()
	harness.Equal(t, state.VulnsChecked, base.Add(vulnEvery).Unix(), "when it last looked")
	harness.Equal(t, state.VulnCursors["osv"], int64(30), "how far into OSV it has read")
	harness.Equal(t, state.VulnCursors["nvd"], int64(20), "how far into NVD it has read")
	harness.Equal(t, f.sentCount(), 0, "messages sent")
}

// One pass's findings are one pass's news and one session's reading, and raising twice in one
// sweep would supersede the first post and leave it with nobody on it.
func TestEveryFindingOfOnePassGoesInOneIncident(t *testing.T) {
	f := vulnCheck(t)
	f.vulnPasses = []vuln.Found{{
		Exposures: []vuln.Exposure{
			exposed("GHSA-AAAA-1111", "node", "24.21.0", "24.22.1", ""),
			exposed("GHSA-BBBB-2222", "libarchive", "3.7.4", "3.7.9", ""),
		},
		Leads: []vuln.Lead{
			watchFor("CVE-2026-0001", "a flaw somewhere in curl"),
			watchFor("CVE-2026-0002", "a certificate authority nobody should trust"),
		},
	}}

	out := f.at(0).sweep()

	harness.Equal(t, f.oncallName, statedir.KindVuln, "the on-call agent's kind")
	harness.Equal(t, f.oncallCalls, 1, "sessions opened")
	harness.Equal(t, f.sentCount(), 1, "messages sent")

	incident := f.onlyPendingID()
	harness.Equal(t, incident, "vuln-"+strconv.FormatInt(base.Unix(), 10), "the incident id")
	harness.Equal(t, statedir.KindOf(incident), statedir.KindVuln, "the kind the id splits into")
	harness.Equal(t, len(f.opened), 1, "posts opened")
	harness.Wants(t, out, "raised 4 newly published advisories as "+incident)

	state := f.state()
	harness.Equal(t, len(state.Vulns), 4, "advisories written down")
	for _, key := range []string{"GHSA-AAAA-1111", "GHSA-BBBB-2222", "CVE-2026-0001", "CVE-2026-0002"} {
		harness.Equal(t, state.Vulns[key].Incident, incident, "the incident "+key+" went out as")
		harness.Equal(t, state.Vulns[key].Thread, "thread-"+incident, "the thread it went out in")
	}

	// Which half of the pass each came from, since only an exact finding is something a later
	// pass measures again.
	harness.Equal(t, state.Vulns["GHSA-AAAA-1111"].Exact, true, "an exact finding")
	harness.Equal(t, state.Vulns["GHSA-AAAA-1111"].What, "node 24.21.0", "what it was about")
	harness.Equal(t, state.Vulns["CVE-2026-0001"].Exact, false, "a lead")
}

func TestAnAdvisoryIsPostedOnceWhileItLasts(t *testing.T) {
	f := vulnCheck(t)
	f.vulnPasses = []vuln.Found{{Exposures: []vuln.Exposure{
		exposed("GHSA-AAAA-1111", "node", "24.21.0", "24.22.1", "")}}}

	f.at(0).sweep()
	f.notify(f.onlyPendingID())

	for n := int64(1); n <= 4; n++ {
		harness.Lacks(t, f.vulnPass(n), "raised 1 newly published advisory")
	}

	harness.Equal(t, f.sentCount(), 1, "messages sent")
	harness.Equal(t, f.oncallCalls, 1, "sessions opened")
	harness.Equal(t, f.state().Vulns["GHSA-AAAA-1111"].Since, base.Unix(), "when it was first raised")
}

// A lead is an advisory somebody has to read, and nothing here measures it a second time: it
// is closed by the session that read it and by nothing else. So its record names no source,
// and a pass that heard every source in full still clears nothing of it.
func TestALeadNeverClearsByItself(t *testing.T) {
	f := vulnCheck(t)
	f.vulnPasses = []vuln.Found{
		{Leads: []vuln.Lead{watchFor("CVE-2026-0001", "a flaw somewhere in curl")},
			Sources: []string{fromOSV, fromBrew}},
		{Sources: []string{fromOSV, fromBrew}},
	}

	f.at(0).sweep()
	f.notify(f.onlyPendingID())
	harness.Equal(t, f.state().Vulns["CVE-2026-0001"].Source, "", "the source a lead names")

	for n := int64(1); n <= 3; n++ {
		f.vulnPass(n)
	}

	harness.Equal(t, f.sentCount(), 1, "messages sent")
	harness.Equal(t, len(f.state().Vulns), 1, "advisories still open")
}

// An exact finding a later pass no longer returns is a Mac that has the fix. The line saying
// so goes into the thread of the post it was raised in, and as that incident's outcome only
// when it is the last thing left under the post: an outcome retags the post Resolved and
// archives it, and a sibling advisory still open would be left under one saying it is over.
func TestAnExactFindingThatStopsMatchingClearsItself(t *testing.T) {
	f := vulnCheck(t)
	archive := exposed("GHSA-BBBB-2222", "libarchive", "3.7.4", "3.7.9", "")
	f.vulnPasses = []vuln.Found{
		{Exposures: []vuln.Exposure{exposed("GHSA-AAAA-1111", "node", "24.21.0", "24.22.1", ""), archive},
			Sources: []string{fromOSV}},
		{Exposures: []vuln.Exposure{archive}, Sources: []string{fromOSV}},
		{Sources: []string{fromOSV}},
	}

	f.at(0).sweep()
	incident := f.onlyPendingID()
	f.notify(incident)

	out := f.vulnPass(1)

	harness.Wants(t, out, "this Mac is no longer affected by GHSA-AAAA-1111, which was node 24.21.0")
	harness.Wants(t, f.lastSent(), "🟢 No longer affected by GHSA-AAAA-1111")
	harness.Wants(t, f.lastSent(), "**Advisory:** `GHSA-AAAA-1111`")
	harness.Wants(t, f.lastSent(), "**Was:** node 24.21.0")
	harness.Wants(t, f.lastSent(), "**Open since:** 3 hours")
	harness.Equal(t, f.sentTo[len(f.sentTo)-1], "thread-"+incident, "the thread it went into")
	harness.Equal(t, f.outcomes[len(f.outcomes)-1], false, "a clear with a sibling still open as the outcome")
	harness.Equal(t, len(f.state().Vulns), 1, "advisories still open")

	out = f.vulnPass(2)

	harness.Wants(t, out, "this Mac is no longer affected by GHSA-BBBB-2222")
	harness.Equal(t, f.outcomes[len(f.outcomes)-1], true, "the last clear as the outcome")
	harness.Equal(t, len(f.state().Vulns), 0, "advisories still open")
}

// A reading that is missing is not a reading that is clear, which is the sweep's own rule
// about the disk and the one that matters most here: a pass where `brew vulns` would not run
// finds no formula affected because it looked at no formula, and without this it would post
// that this Mac is no longer affected by every formula advisory open against it.
func TestASourceThisPassDidNotHearClearsNothingOfItsOwn(t *testing.T) {
	f := vulnCheck(t)
	formula := exposed("GHSA-CCCC-3333", "libarchive", "3.7.4", "3.7.9", "")
	formula.Source = fromBrew

	f.vulnPasses = []vuln.Found{
		{Exposures: []vuln.Exposure{formula}, Sources: []string{fromBrew}},
		{Sources: []string{fromOSV}},
		{Sources: []string{fromOSV, fromBrew}},
	}

	f.at(0).sweep()
	f.notify(f.onlyPendingID())

	out := f.vulnPass(1)

	harness.Lacks(t, out, "no longer affected")
	harness.Equal(t, f.sentCount(), 1, "messages sent")
	harness.Equal(t, len(f.state().Vulns), 1, "advisories still open")

	f.vulnPass(2)

	harness.Wants(t, f.lastSent(), "🟢 No longer affected by GHSA-CCCC-3333")
	harness.Equal(t, len(f.state().Vulns), 0, "advisories still open")
}

func TestAClearThatDidNotSendIsSaidOnTheNextPass(t *testing.T) {
	f := vulnCheck(t)
	f.vulnPasses = []vuln.Found{
		{Exposures: []vuln.Exposure{exposed("GHSA-AAAA-1111", "node", "24.21.0", "24.22.1", "")},
			Sources: []string{fromOSV}},
		{Sources: []string{fromOSV}},
	}

	f.at(0).sweep()
	f.notify(f.onlyPendingID())

	f.sendErr = errSendFailed
	out := f.vulnPass(1)

	harness.Wants(t, out, "no longer affected by GHSA-AAAA-1111 did not send and is left to the next pass")
	harness.Equal(t, len(f.state().Vulns), 1, "advisories still open")

	f.sendErr = nil
	f.vulnPass(2)

	harness.Wants(t, f.lastSent(), "🟢 No longer affected by GHSA-AAAA-1111")
	harness.Equal(t, len(f.state().Vulns), 0, "advisories still open")
}

// A post that did not send is an advisory nobody has heard of, so nothing about it is written
// down: not the record that would stop it being raised again, and not the cursor that would
// carry the feed past the item it came from.
func TestAPassWhoseMessageDidNotSendLeavesTheFeedsWhereTheyWere(t *testing.T) {
	f := vulnCheck(t)
	f.vulnPasses = []vuln.Found{{
		Exposures: []vuln.Exposure{exposed("GHSA-AAAA-1111", "node", "24.21.0", "24.22.1", "")},
		Cursors:   map[string]int64{"osv": 42},
	}}
	f.sendErr = errSendFailed

	out := f.at(0).sweep()

	harness.Wants(t, out, "did not send, so the feeds are left where they were and the next pass finds them again")
	harness.Equal(t, len(f.state().Vulns), 0, "advisories written down")
	harness.Equal(t, len(f.state().VulnCursors), 0, "cursors written")

	// And the three hours are not held back with them: a pass costs a dozen subprocesses and
	// four feeds, and retrying one every five minutes would be the heaviest thing on this Mac.
	harness.Equal(t, f.state().VulnsChecked, base.Unix(), "when it last looked")

	f.sendErr = nil
	f.vulnPass(1)

	harness.Wants(t, f.lastSent(), "🔴 Vulnerable: node 24.21.0")
	harness.Equal(t, f.state().VulnCursors["osv"], int64(42), "how far into OSV it has read")
}

// Waiting for somebody to approve a version bump the nightly run would have made anyway is a
// night of exposure for nothing — but an upgrade that did not fix the fault must not be
// started again every three hours for the life of the Mac.
func TestAnExposureWithAReleasedFixStartsOneUpgrade(t *testing.T) {
	f := vulnCheck(t)
	f.vulnPasses = []vuln.Found{{Exposures: []vuln.Exposure{
		exposed("GHSA-AAAA-1111", "node", "24.21.0", "24.22.1", "mise:node")}}}

	out := f.at(0).sweep()

	harness.Equal(t, len(f.fixed), 1, "upgrades started")
	harness.Equal(t, strings.Join(f.fixed[0], " "), "mise:node", "what it was started with")
	harness.Wants(t, out, "started a security upgrade of [mise:node], ahead of the release-age hold")
	harness.Wants(t, f.lastSent(), "`hachiko update --security mise:node` is running")
	harness.Equal(t, f.state().Vulns["GHSA-AAAA-1111"].Fixing, true, "the upgrade written down")

	f.notify(f.onlyPendingID())
	f.vulnPass(1)

	harness.Equal(t, len(f.fixed), 1, "upgrades started")
}

// Nothing is handed to the update where the source named no released version to reach, or
// where the thing was not installed by anything the update moves: that is the on-call
// agent's, the fix being a decision rather than a version.
func TestAnExposureNothingHereMovesStartsNoUpgrade(t *testing.T) {
	f := vulnCheck(t)
	f.vulnPasses = []vuln.Found{{Exposures: []vuln.Exposure{
		exposed("GHSA-AAAA-1111", "libssh2", "1.11.0", "", "")}}}

	out := f.at(0).sweep()

	harness.Equal(t, len(f.fixed), 0, "upgrades started")
	harness.Lacks(t, out, "started a security upgrade")
	harness.Wants(t, f.lastSent(), "no released fix")
	harness.Lacks(t, f.lastSent(), "Being fixed now")
	harness.Equal(t, f.state().Vulns["GHSA-AAAA-1111"].Fixing, false, "an upgrade written down")
}

func TestAnUpgradeThatCouldNotBeStartedIsAskedAgain(t *testing.T) {
	f := vulnCheck(t)
	f.vulnPasses = []vuln.Found{{Exposures: []vuln.Exposure{
		exposed("GHSA-AAAA-1111", "node", "24.21.0", "24.22.1", "mise:node")}}}
	f.fixedErr = errors.New("the update refused the target")

	out := f.at(0).sweep()

	harness.Wants(t, out, "the security upgrade of [mise:node] could not be started, so the next pass asks again")
	harness.Equal(t, len(f.fixed), 0, "upgrades started")
	harness.Lacks(t, f.lastSent(), "Being fixed now")
	harness.Equal(t, f.state().Vulns["GHSA-AAAA-1111"].Fixing, false, "an upgrade written down")

	f.fixedErr = nil
	f.notify(f.onlyPendingID())
	f.vulnPass(1)

	harness.Equal(t, len(f.fixed), 1, "upgrades started")
	harness.Equal(t, f.state().Vulns["GHSA-AAAA-1111"].Fixing, true, "the upgrade written down")
}

// An advisory already raised as something to read, whose version a later pass has matched
// exactly, is still something to upgrade — even though there is nothing new to raise.
func TestAnExposureForAnAdvisoryAlreadyRaisedStillStartsItsUpgrade(t *testing.T) {
	f := vulnCheck(t)
	f.vulnPasses = []vuln.Found{
		{Leads: []vuln.Lead{watchFor("CVE-2026-0001", "a flaw in node's tar reader")}},
		{Exposures: []vuln.Exposure{exposed("CVE-2026-0001", "node", "24.21.0", "24.22.1", "mise:node")}},
	}

	f.at(0).sweep()
	f.notify(f.onlyPendingID())

	out := f.vulnPass(1)

	harness.Equal(t, len(f.fixed), 1, "upgrades started")
	harness.Wants(t, out, "started a security upgrade of [mise:node]")
	harness.Lacks(t, out, "raised 1 new published advisory")
	harness.Equal(t, f.sentCount(), 1, "messages sent")
	harness.Equal(t, f.state().Vulns["CVE-2026-0001"].Fixing, true, "the upgrade written down")
}

// A wait left open has the listener polling an archived post and `hachiko status` calling a
// resolved incident open for ever.
func TestNothingLeftUnderAVulnIncidentEndsTheWaitOnIt(t *testing.T) {
	f := vulnCheck(t)

	// The state a Mac is left in once the last advisory under an incident is gone and the
	// session on it closed nothing: the report still owing and the wait still counting.
	state := f.state()
	state.Pending = map[string]statedir.Pending{"vuln-1": {OpenedAt: base.Unix()}}
	state.Waiting = map[string]statedir.Waiting{
		statedir.KindVuln: {Incident: "vuln-1", Opened: base.Unix(), Since: base.Unix()},
	}
	if err := f.store.Save(state); err != nil {
		t.Fatal(err)
	}

	out := f.at(0).sweep()

	harness.Wants(t, out, "nothing is open under vuln-1 any more, so no report on it is owing")
	harness.Wants(t, out, "nothing is open under vuln-1 any more, so the wait on it ends")
	harness.Equal(t, len(f.state().Pending), 0, "reports still owing")
	harness.Equal(t, len(f.state().Waiting), 0, "waits still open")
	harness.Equal(t, f.sentCount(), 0, "messages sent")
}

// Past the furthest back any feed here is read, a cursor rebuilt from nothing cannot surface
// the advisory again — so there is nothing left for the record to stop, and keeping it would
// be a state file that only ever grows.
func TestAnAdvisoryIsForgottenOnceNoFeedCouldSurfaceItAgain(t *testing.T) {
	f := vulnCheck(t)
	f.vulnPasses = []vuln.Found{
		{Leads: []vuln.Lead{watchFor("CVE-2026-0001", "a flaw somewhere in curl")}},
		{},
	}

	f.at(0).sweep()
	f.notify(f.onlyPendingID())
	harness.Equal(t, len(f.state().Vulns), 1, "advisories written down")

	f.at(int64((statedir.VulnRemembered + time.Hour) / time.Second)).sweep()

	harness.Equal(t, len(f.state().Vulns), 0, "advisories written down")
	harness.Equal(t, f.sentCount(), 1, "messages sent")
}

func TestThePostNamesWhatIsAffectedAndWhatIsBeingDone(t *testing.T) {
	f := vulnCheck(t)
	second := exposed("GHSA-BBBB-2222", "node", "24.21.0", "24.22.0", "mise:node")
	second.Severity = "moderate"

	f.vulnPasses = []vuln.Found{{
		Exposures: []vuln.Exposure{
			exposed("GHSA-AAAA-1111", "node", "24.21.0", "24.22.1", "mise:node"), second},
		Leads:    []vuln.Lead{watchFor("CVE-2026-0001", "a flaw somewhere in curl")},
		Troubles: []string{"NVD did not answer"},
	}}

	f.at(0).sweep()
	incident := f.onlyPendingID()
	message := f.lastSent()

	harness.Wants(t, message, "🔴 Vulnerable: node 24.21.0")
	// One bullet per thing rather than per advisory: eighteen advisories against one module
	// that has not moved in four years is one thing to deal with.
	harness.Wants(t, message, "**Affected:**\n- `node` — 2 advisories, worst high: GHSA-AAAA-1111, GHSA-BBBB-2222")
	harness.Wants(t, message, "\n  fixed by 24.22.1")
	harness.Wants(t, message, "**Worth reading:**\n- `CVE-2026-0001` critical — a flaw somewhere in curl")
	harness.Wants(t, message, "**Not read this time:**\n- NVD did not answer")
	harness.Wants(t, message, "**Being fixed now:** `hachiko update --security mise:node` is running")
	harness.Wants(t, message, "-# Incident "+incident+" · mac-mini")

	// The session gets every advisory rather than the grouped form, since triaging one is
	// reading each of them.
	harness.Wants(t, f.oncallBrief, "`GHSA-BBBB-2222`")
	harness.Wants(t, f.oncallBrief, "fixed in 24.22.0")
	harness.Wants(t, f.oncallBrief, "Incident id: "+incident)
}
