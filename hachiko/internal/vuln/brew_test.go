package vuln

import (
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// What Homebrew's scanner really answers: a summary that is null, a severity in capitals, and
// the entries it has already patched in its own bottle listed beside the ones it has not.
const brewReportJSON = `{
  "findings": [
    {
      "formula": "aria2",
      "version": "1.37.0",
      "tag": "release-1.37.0",
      "repo_url": "https://github.com/aria2/aria2",
      "vulnerabilities": [
        {"id": "CVE-2026-71832", "severity": "MEDIUM", "summary": null, "aliases": [], "fixed_versions": []}
      ],
      "patched": []
    },
    {
      "formula": "libssh2",
      "version": "1.11.1",
      "vulnerabilities": [
        {"id": "OSV-2025-433", "severity": "UNKNOWN",
         "summary": "Null-dereference READ in _libssh2_packet_add", "aliases": [], "fixed_versions": []}
      ],
      "patched": [
        {"id": "CVE-2025-15661", "severity": "UNKNOWN", "summary": "Heap Buffer Over-read via sftp_symlink()",
         "aliases": [], "fixed_versions": ["2dae3024897e1898d389835151f4e9606227721d"]}
      ]
    }
  ],
  "skipped_formulae": [],
  "checked": 38,
  "complete": true
}`

// `brew vulns` exits 1 when it has found something, so a reading thrown away for being
// non-zero would be a Mac that goes quiet exactly when it has something to say.
func TestAScanThatExitedOneBecauseItFoundSomethingIsStillRead(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.exits(brewVulns, 1, brewReportJSON)
	b.says(brewOutdated, `{"formulae": [], "casks": []}`)

	found := b.look()

	harness.Equal(t, len(found.Exposures), 2, "findings read")
	harness.Equal(t, found.exposure(t, "CVE-2026-71832").Name, "aria2", "the formula")
	harness.Equal(t, found.exposure(t, "CVE-2026-71832").Version, "1.37.0", "the version installed")
	harness.Equal(t, found.exposure(t, "CVE-2026-71832").Severity, "MEDIUM", "the severity")
	harness.Equal(t, found.exposure(t, "CVE-2026-71832").Summary, "", "a summary Homebrew states as null")
	harness.Equal(t, found.exposure(t, "OSV-2025-433").Summary,
		"Null-dereference READ in _libssh2_packet_add", "a summary it states")

	// Fourteen of libssh2's fifteen were patched in Homebrew's own bottle the day this was
	// written, and reporting one of those would be an incident about a Mac that has the fix.
	for _, e := range found.Exposures {
		harness.Equal(t, e.ID == "CVE-2025-15661", false, "a patched entry reported as a finding")
	}
	harness.Equal(t, len(found.Leads), 0, "leads from a patched entry")
}

// Homebrew's advisory data names a fixed version as often as an upstream commit — fourteen of
// libssh2's were a 40-character SHA — so what decides whether there is anything to upgrade to
// is Homebrew itself.
func TestAFindingHomebrewHasANewerBottleOrCaskOfIsAnUpgrade(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.exits(brewVulns, 1, brewReportJSON)
	b.says(brewOutdated, `{"formulae": [{"name": "aria2", "installed_versions": ["1.37.0"],
		"current_version": "1.37.1", "pinned": false, "pinned_version": null}],
		"casks": [{"name": "orbstack", "current_version": "2.2.0"}]}`)

	e := b.look().exposure(t, "CVE-2026-71832")

	harness.Equal(t, e.Fixed, "1.37.1", "the version Homebrew has")
	harness.Equal(t, e.Target, "brew:aria2", "the target")
	harness.Equal(t, e.Needs, "", "a line for Tim about something being upgraded")

	// A pinned formula is one somebody held back on purpose, and a check is not what overrides
	// that.
	pinned := newBox(t)
	pinned.clean()
	pinned.exits(brewVulns, 1, brewReportJSON)
	pinned.says(brewOutdated, `{"formulae": [{"name": "aria2", "current_version": "1.37.1", "pinned": true}],
		"casks": []}`)

	held := pinned.look().exposure(t, "CVE-2026-71832")
	harness.Equal(t, held.Target, "", "the target of a pinned formula")
	harness.Wants(t, held.Needs, "Homebrew has nothing newer than this")
}

func TestABrewFindingWithNothingNewerGoesToTheAgentRatherThanAnUpgrade(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.exits(brewVulns, 1, brewReportJSON)
	b.says(brewOutdated, `{"formulae": [], "casks": []}`)

	found := b.look()
	e := found.exposure(t, "OSV-2025-433")

	harness.Equal(t, e.Fixed, "", "a fixed version")
	harness.Equal(t, e.Target, "", "the target")
	harness.Wants(t, e.Needs, "Homebrew has nothing newer than this")
	harness.Equal(t, len(found.Targets(nil)), 0, "targets an upgrade would be started with")
}

// A formula whose bottle carries no bill of materials is one the scanner skips and says it
// skipped, and those are the gap the feeds cover by name.
func TestTheFormulaeTheScanSkippedAreSaidAndAreWhatTheFeedsMatchByName(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.says(brewVulns, `{"findings": [], "skipped_formulae": ["sqlite", "readline", "ca-certificates"],
		"checked": 38, "complete": false}`)
	b.serves(nvdURL, `{"totalResults": 1, "vulnerabilities": [{"cve": {"id": "CVE-2026-9",
		"descriptions": [{"lang": "en", "value": "A write past the end of a page in sqlite before 3.52."}],
		"metrics": {}}}]}`)

	found := b.look()

	harness.Wants(t, found.troubles(),
		"brew vulns scanned 38 formulae and skipped 3 formulae, which have no bill of materials to match against")
	harness.Wants(t, found.lead(t, "CVE-2026-9").Why, "it says sqlite, and sqlite is installed here")
}

func TestAScanThatFoundNothingAndCoveredEverythingSaysNothing(t *testing.T) {
	b := newBox(t)
	b.clean()

	found := b.look()

	harness.Equal(t, found.troubles(), "", "what could not be read")
	harness.Equal(t, found.Empty(), true, "anything worth raising")
	harness.Equal(t, b.ran(brewOutdated), false, "a second brew invocation with nothing to route")

	for _, cmd := range b.run {
		if cmd.Name != "brew" {
			continue
		}
		harness.Equal(t, contains(cmd.Env, brewNoAutoUpdate), cmd.line() == brewVulns,
			"HOMEBREW_NO_AUTO_UPDATE on "+cmd.line())
	}
	// Fetching Homebrew's formulae is the nightly update's step, and a check that did it as a
	// side effect would be moving what the update is answerable for.
	harness.Equal(t, b.ran("brew update"), false, "a brew update of its own")
}

// What makes it a failure is JSON that will not parse, or a scan that never ran at all.
func TestAScanThatCouldNotBeReadIsSaidRatherThanTakenAsClear(t *testing.T) {
	t.Run("it never ran", func(t *testing.T) {
		b := newBox(t)
		b.clean()
		b.fails(brewVulns, "Error: Unknown command: vulns")

		harness.Wants(t, b.look().troubles(), "brew vulns could not run: Error: Unknown command: vulns")
	})

	t.Run("it answered something else", func(t *testing.T) {
		b := newBox(t)
		b.clean()
		b.exits(brewVulns, 1, "Error: a bottle has no SBOM")

		found := b.look()
		harness.Wants(t, found.troubles(), "brew vulns answered something that is not JSON: Error: a bottle has no SBOM")
		harness.Equal(t, len(found.Exposures), 0, "findings read from it")
	})
}

// An upgrade of something Homebrew has nothing newer of does nothing at all and reports that
// it did, so a reading that fails leaves every finding with the agent.
func TestAnOutdatedReadingThatFailedLeavesEveryFindingWithTheAgent(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.exits(brewVulns, 1, brewReportJSON)
	b.fails(brewOutdated, "Error: Another active Homebrew process is already in progress")

	found := b.look()

	harness.Wants(t, found.troubles(), "brew could not say what it has newer versions of, "+
		"so nothing of Homebrew's is being upgraded")
	harness.Equal(t, len(found.Targets(nil)), 0, "targets")
	for _, e := range found.Exposures {
		harness.Equal(t, e.Target, "", "the target of "+e.ID)
	}
}
