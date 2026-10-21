package vuln

import (
	"strings"
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// certifi's fault is GHSA-6q48-6c7w-9gw2 to GitHub and PYSEC-2026-4320 to OSV, and the Go one
// is CVE-2026-111943 to NVD and GO-2026-6809 to OSV. Taking the lowest of the whole set gives
// both sources the same answer whichever of them saw it first.
func TestEverySpellingOfOneFaultIsOneKey(t *testing.T) {
	fromGitHub := Exposure{ID: "GHSA-6q48-6c7w-9gw2", Aliases: []string{"PYSEC-2026-4320", "CVE-2026-1"}}
	fromOSV := Exposure{ID: "PYSEC-2026-4320", Aliases: []string{"CVE-2026-1", "GHSA-6q48-6c7w-9gw2"}}

	harness.Equal(t, fromGitHub.Key(), fromOSV.Key(), "the key both sources arrive at")
	harness.Equal(t, fromGitHub.Key(), "CVE-2026-1", "the lowest of the spellings")
	harness.Equal(t, Lead{ID: "ghsa-x"}.Key(), "GHSA-X", "a key is read in one case")
	harness.Equal(t, Lead{ID: "GO-2026-6809", Aliases: []string{" cve-2026-111943 "}}.Key(),
		"CVE-2026-111943", "an alias with spaces around it")
}

// A version this Mac is running that a source names is the whole of what a lead was asking
// somebody to find out.
func TestAnExactMatchReplacesALeadAboutTheSameAdvisory(t *testing.T) {
	f := Found{
		Exposures: []Exposure{{ID: "GO-2026-6809", Aliases: []string{"CVE-2026-111943"}, Name: "stdlib"}},
		Leads: []Lead{
			{ID: "CVE-2026-111943", Source: "NVD"},
			{ID: "CVE-2026-9", Source: "NVD"},
		},
	}

	f.dedupe()

	harness.Equal(t, len(f.Leads), 1, "leads")
	harness.Equal(t, f.Leads[0].ID, "CVE-2026-9", "the one nothing compared a version against")

	twice := Found{Leads: []Lead{
		{ID: "GHSA-x", Aliases: []string{"CVE-2026-9"}, Source: "GitHub"},
		{ID: "CVE-2026-9", Source: "NVD"},
	}}
	twice.dedupe()

	harness.Equal(t, len(twice.Leads), 1, "leads, two sources having seen one advisory")
	harness.Equal(t, twice.Leads[0].Source, "GitHub", "the first of them")
}

// A CVE against two formulae is two upgrades, and the same CVE seen twice against one thing is
// one.
func TestTwoExposuresOfOneAdvisoryStayOnlyWhereTheyAreAboutDifferentThings(t *testing.T) {
	f := Found{Exposures: []Exposure{
		{ID: "CVE-2026-1", Name: "curl", Source: sourceBrew},
		{ID: "CVE-2026-1", Name: "libcurl", Source: sourceBrew},
		{ID: "CVE-2026-1", Name: "curl", Source: sourceOSV},
	}}

	f.dedupe()

	harness.Equal(t, len(f.Exposures), 2, "exposures")
	harness.Equal(t, f.Exposures[0].Source, sourceBrew, "the first reading of it is the one kept")
}

// Nothing is happening about one of those and nothing is going to, where everything below it
// is a version somebody or something can reach.
func TestWhatHasNoReleasedFixIsListedFirstWhateverItsSeverity(t *testing.T) {
	f := Found{Exposures: []Exposure{
		{ID: "CVE-2026-2", Name: "curl", Severity: "critical", Fixed: "8.18.1"},
		{ID: "CVE-2026-1", Name: "libssh2", Severity: "low"},
		{ID: "CVE-2026-3", Name: "aria2", Severity: "high", Fixed: "1.37.1"},
	}}

	f.sortFindings()

	harness.Equal(t, strings.Join(f.ids(), " "), "CVE-2026-1 CVE-2026-2 CVE-2026-3", "the order")
}

// Then worst first, and within one severity by what is installed, so two findings about one
// formula sit together — and an unknown severity sorts last, a source that stated none having
// said nothing about how bad it is.
func TestBelowThatItIsWorstFirstAndTwoFindingsAboutOneThingSitTogether(t *testing.T) {
	f := Found{
		Exposures: []Exposure{
			{ID: "CVE-2026-1", Name: "zlib", Severity: "", Fixed: "1.3.2"},
			{ID: "CVE-2026-3", Name: "curl", Severity: "moderate", Fixed: "8.18.1"},
			{ID: "CVE-2026-2", Name: "curl", Severity: "moderate", Fixed: "8.18.1"},
		},
		Leads: []Lead{
			{ID: "CVE-2026-4", Severity: ""},
			{ID: "CVE-2026-5", Severity: "critical"},
			{ID: "CVE-2026-6", Severity: "medium"},
		},
	}

	f.sortFindings()

	harness.Equal(t, strings.Join(f.ids(), " "), "CVE-2026-2 CVE-2026-3 CVE-2026-1", "the exposures")
	harness.Equal(t, strings.Join(f.leadIDs(), " "), "CVE-2026-5 CVE-2026-6 CVE-2026-4", "the leads")
}

// Eighteen advisories against one module that has not moved in four years is one thing to deal
// with, and a title saying eighteen reads as eighteen.
func TestTheHeadlineCountsThingsRatherThanAdvisories(t *testing.T) {
	one := Found{Exposures: []Exposure{{ID: "CVE-2026-1", Name: "aria2", Version: "1.37.0"}}}
	harness.Equal(t, Headline(one), "Vulnerable: aria2 1.37.0", "one exposure")

	many := Found{Exposures: []Exposure{
		{ID: "CVE-2026-1", Name: "stdlib", Version: "1.27.0"},
		{ID: "CVE-2026-2", Name: "stdlib", Version: "1.27.0"},
		{ID: "CVE-2026-3", Name: "curl", Version: "8.18.0"},
	}}
	harness.Equal(t, Headline(many), "Vulnerable: 2 things a published advisory names", "two things, three advisories")

	harness.Equal(t, Headline(Found{Leads: []Lead{{ID: "CVE-2026-1"}}}),
		"One published advisory may be about this Mac", "one lead")
	harness.Equal(t, Headline(Found{Leads: []Lead{{ID: "CVE-2026-1"}, {ID: "CVE-2026-2"}}}),
		"2 published advisories may be about this Mac", "two leads")
}

// A group whose first advisory has no fix read as "fixed by 0.56.0 — nothing is released that
// fixes it", which is two sentences contradicting each other about the same thing.
func TestOneThingsBulletDoesNotSayItIsFixedAndUnfixedAtOnce(t *testing.T) {
	f := Found{Exposures: []Exposure{
		{ID: "CVE-2026-1", Name: "aria2", Version: "1.37.0", Severity: "high",
			Needs: "Nothing is released that fixes it, so this one is a workaround or waiting."},
		{ID: "CVE-2026-2", Name: "aria2", Version: "1.37.0", Severity: "medium", Fixed: "0.56.0",
			Needs: "Homebrew has nothing newer than this, so there is nothing to upgrade to yet."},
	}}

	bullet := f.thing("aria2")

	harness.Wants(t, bullet, "fixed by 0.56.0")
	harness.Lacks(t, bullet, "nothing is released that fixes it")
	harness.Wants(t, bullet, "Homebrew has nothing newer than this")
	harness.Wants(t, bullet, "2 advisories, worst high: CVE-2026-1, CVE-2026-2")

	unfixed := Found{Exposures: []Exposure{
		{ID: "CVE-2026-1", Name: "libssh2", Version: "1.11.1",
			Needs: "Nothing is released that fixes it, so this one is a workaround or waiting."},
	}}

	harness.Wants(t, unfixed.thing("libssh2"),
		"1 advisory: CVE-2026-1\n  no released fix — Nothing is released that fixes it")
}

// The full form has every advisory and the short one has a screen's worth, since Discord caps a
// message at two thousand characters.
func TestTheShortFormCountsOutOnlyAFewIdsAndTheFullFormHasThemAll(t *testing.T) {
	f := Found{}
	for _, id := range []string{"GO-1", "GO-2", "GO-3", "GO-4", "GO-5"} {
		f.Exposures = append(f.Exposures, Exposure{ID: id, Name: "golang.org/x/crypto",
			Version: "v0.0.0-20220314234659-1baeb1ce4c0b", Severity: "high", Fixed: "v0.45.0"})
	}

	for _, id := range []string{"CVE-1", "CVE-2", "CVE-3", "CVE-4", "CVE-5"} {
		f.Leads = append(f.Leads, Lead{ID: id, Title: "a flaw in something", Why: "it says curl"})
	}

	harness.Wants(t, Summary(f), "**"+LabelAffected+":**")
	harness.Wants(t, Summary(f), "5 advisories, worst high: GO-1, GO-2, GO-3, and 2 more")
	harness.Wants(t, Summary(f), "2 more advisories in the brief the agent has")
	harness.Lacks(t, Summary(f), "CVE-5")
	harness.Wants(t, Details(f), "GO-5")
	harness.Wants(t, Details(f), "CVE-5")

	// A message cannot say an upgrade is running that never started, so what is being fixed is
	// set after the fact rather than worked out from the targets.
	harness.Lacks(t, Summary(f), LabelFixing)

	f.Started = []string{"brew:aria2"}
	f.Troubles = []string{"brew vulns scanned 38 formulae and skipped 8 formulae"}

	harness.Wants(t, Summary(f), "**"+LabelFixing+":** `hachiko update --security brew:aria2` is running")
	harness.Wants(t, Summary(f), "**"+LabelUnread+":**\n- brew vulns scanned 38 formulae and skipped 8 formulae")
}

// One bullet each saying what is installed, what the advisory is and what is to happen — and
// for a lead, the reason it was kept, which is what an agent triaging one needs first.
func TestABulletSaysWhatIsInstalledWhatTheAdvisoryIsAndWhatIsToHappen(t *testing.T) {
	e := Exposure{ID: "GHSA-fh4m-2w73-96r9", Name: "mise", Version: "2026.6.12", Severity: "high",
		Summary: "Keyless cosign verification accepts bundles signed by any Fulcio identity",
		Fixed:   "2026.10.0", Target: "mise-self"}

	harness.Wants(t, e.bullet(), "`mise` 2026.6.12 — `GHSA-fh4m-2w73-96r9`, high, "+
		"Keyless cosign verification accepts bundles signed by any Fulcio identity")
	harness.Wants(t, e.bullet(), "fixed in 2026.10.0")

	l := Lead{ID: "CVE-2026-71832", Severity: "MEDIUM", Title: "curl mishandles a redirect",
		Why: "it says curl, and curl is installed here"}

	harness.Wants(t, l.bullet(), "`CVE-2026-71832` medium — curl mishandles a redirect")
	harness.Wants(t, l.bullet(), "it says curl, and curl is installed here")
}

// An upgrade that did not fix the fault would otherwise be started again every three hours for
// ever.
func TestTargetsSkipWhatIsRecordedAsAlreadyBeingFixed(t *testing.T) {
	f := Found{Exposures: []Exposure{
		{ID: "CVE-2026-1", Name: "aria2", Target: "brew:aria2"},
		{ID: "CVE-2026-2", Name: "aria2", Target: "brew:aria2"},
		{ID: "CVE-2026-3", Name: "node", Target: "mise:node"},
		{ID: "CVE-2026-4", Name: "libssh2"},
	}}

	harness.Equal(t, strings.Join(f.Targets(nil), " "), "brew:aria2 mise:node", "each target once")
	harness.Equal(t, strings.Join(f.Targets(map[string]bool{"CVE-2026-1": true}), " "),
		"brew:aria2 mise:node", "a target another advisory still wants")
	harness.Equal(t, strings.Join(f.Targets(map[string]bool{"CVE-2026-3": true}), " "),
		"brew:aria2", "a target nothing else wants")

	// And which advisories those targets cover, which is what gets written down as having had
	// an upgrade started for it.
	harness.Equal(t, strings.Join(f.Covered([]string{"brew:aria2"}), " "), "CVE-2026-1 CVE-2026-2",
		"what the one target covers")
	harness.Equal(t, len(f.Covered(nil)), 0, "what no target covers")

	// An exact match that has stopped matching is a Mac that has the fix, and the only kind of
	// finding here that clears by itself.
	harness.Equal(t, f.Matches("CVE-2026-1"), true, "an exposure under the key it was raised as")
	harness.Equal(t, f.Matches("CVE-2026-9"), false, "an advisory no source names any more")
	harness.Equal(t, f.Empty(), false, "anything worth raising")
	harness.Equal(t, Found{}.Empty(), true, "a pass that found nothing")
}

// This runs eight times a day for ever, and what the line says is how much of the Mac it
// managed to look at.
func TestTheLogLineSaysWhatWasFoundAndWhatWasNotRead(t *testing.T) {
	b := newBox(t)
	b.clean()

	b.look()

	harness.Wants(t, b.log.String(), "looked for published vulnerabilities: "+
		"0 installed version(s) a source names, 0 advisor(ies) worth reading")
	harness.Lacks(t, b.log.String(), "not read:")

	short := newBox(t)
	short.clean()
	short.refuses(kevURL, "it answered 503")
	short.look()

	harness.Wants(t, short.log.String(),
		"; not read: CISA's exploited-vulnerabilities catalogue could not be read")
}
