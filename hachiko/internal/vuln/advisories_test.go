package vuln

import (
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

func (b *box) toolAt(line string) {
	b.says("mise --version", line)
}

// mise's own advisories, as GitHub really answers them: one entry per package and one range,
// with mise-sigstore beside mise, and both spacings of the operator.
const miseAdvisoriesJSON = `[
  {
    "ghsa_id": "GHSA-fh4m-2w73-96r9",
    "cve_id": null,
    "summary": "Keyless cosign verification accepts bundles signed by any Fulcio identity",
    "severity": "high",
    "published_at": "2026-10-01T10:00:00Z",
    "withdrawn_at": null,
    "html_url": "https://github.com/jdx/mise/security/advisories/GHSA-fh4m-2w73-96r9",
    "cvss": {"score": 7.5},
    "identifiers": [{"value": "GHSA-fh4m-2w73-96r9", "type": "GHSA"}],
    "vulnerabilities": [
      {"package": {"ecosystem": "rust", "name": "mise"},
       "vulnerable_version_range": "<= 2026.9.18", "patched_versions": "2026.10.0",
       "first_patched_version": null},
      {"package": {"ecosystem": "rust", "name": "mise-sigstore"},
       "vulnerable_version_range": ">= 0.1.0", "patched_versions": ""}
    ]
  },
  {
    "ghsa_id": "GHSA-9mm4-fgvc-x7rp",
    "cve_id": "CVE-2026-50001",
    "summary": "Incorrect file ownership when mise is installed by the root user",
    "severity": "medium",
    "published_at": "2026-07-01T10:00:00Z",
    "withdrawn_at": null,
    "html_url": "https://github.com/jdx/mise/security/advisories/GHSA-9mm4-fgvc-x7rp",
    "cvss": {"score": 5.5},
    "identifiers": [{"value": "GHSA-9mm4-fgvc-x7rp", "type": "GHSA"},
                    {"value": "CVE-2026-50001", "type": "CVE"}],
    "vulnerabilities": [
      {"package": {"ecosystem": "rust", "name": "mise"},
       "vulnerable_version_range": "<2026.7.1", "patched_versions": "2026.7.1"}
    ]
  }
]`

// mise says `<= 2026.9.18` and leaves the comparison to whoever is asking, and the two
// spacings are both GitHub's own.
func TestARangeWrittenWithAndWithoutASpaceAreBothRead(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.toolAt("2026.6.12 macos-arm64")
	b.serves(miseAdvisories, miseAdvisoriesJSON)

	found := b.look()

	harness.Equal(t, len(found.Exposures), 2, "advisories about the mise installed here")
	harness.Equal(t, found.exposure(t, "GHSA-fh4m-2w73-96r9").Fixed, "2026.10.0", "the spaced range's fix")
	harness.Equal(t, found.exposure(t, "GHSA-9mm4-fgvc-x7rp").Fixed, "2026.7.1", "the unspaced range's fix")
	harness.Equal(t, found.exposure(t, "GHSA-9mm4-fgvc-x7rp").Target, "mise-self", "the target")
	harness.Equal(t, found.exposure(t, "GHSA-fh4m-2w73-96r9").Version, "2026.6.12", "what is installed")

	above := newBox(t)
	above.clean()
	above.toolAt("2026.10.11 macos-arm64")
	above.serves(miseAdvisories, miseAdvisoriesJSON)

	harness.Equal(t, len(above.look().Exposures), 0, "exposures against a mise above every range")
}

// mise's own advisories name mise-sigstore beside mise, and Claude Code's name Claude Desktop,
// and neither is installed here.
func TestOnlyTheEntryNamingThePackageThisMacHasIsRead(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.toolAt("2026.6.12 macos-arm64")
	b.serves(miseAdvisories, miseAdvisoriesJSON)
	b.says("claude --version", "2.1.100 (Claude Code)")
	b.serves(claudeAdvisories, `[
	  {"ghsa_id": "GHSA-v234-4jrq-mgg6", "severity": "high", "summary": "Claude Desktop local escalation",
	   "withdrawn_at": null, "identifiers": [{"value": "GHSA-v234-4jrq-mgg6", "type": "GHSA"}],
	   "vulnerabilities": [{"package": {"ecosystem": "other", "name": "Claude Desktop"},
	     "vulnerable_version_range": ">= 1.1.3918, < 1.15962.0", "patched_versions": "1.15962.0"}]},
	  {"ghsa_id": "GHSA-pq7j-f95f-qfcv", "cve_id": "CVE-2026-86063", "severity": "high",
	   "summary": "Command injection in the IDE extension", "withdrawn_at": null,
	   "identifiers": [{"value": "GHSA-pq7j-f95f-qfcv", "type": "GHSA"}, {"value": "CVE-2026-86063", "type": "CVE"}],
	   "vulnerabilities": [{"package": {"ecosystem": "npm", "name": "@anthropic-ai/claude-code"},
	     "vulnerable_version_range": "< 2.1.179", "patched_versions": "2.1.179"}]}]`)

	found := b.look()

	for _, e := range found.Exposures {
		harness.Equal(t, e.ID == "GHSA-v234-4jrq-mgg6", false, "an advisory about something not installed here")
	}
	harness.Equal(t, found.exposure(t, "GHSA-pq7j-f95f-qfcv").Name, "claude-code", "what it is about")

	// A withdrawn advisory is one GitHub has taken back, so it is not a fault any more.
	gone := newBox(t)
	gone.clean()
	gone.toolAt("2026.6.12 macos-arm64")
	gone.serves(miseAdvisories, `[{"ghsa_id": "GHSA-withdrawn", "severity": "critical",
	  "summary": "it turned out not to be a fault", "withdrawn_at": "2026-10-02T10:00:00Z",
	  "identifiers": [], "vulnerabilities": [{"package": {"ecosystem": "rust", "name": "mise"},
	    "vulnerable_version_range": "<= 2026.9.18", "patched_versions": "2026.10.0"}]}]`)

	taken := gone.look()
	harness.Equal(t, len(taken.Exposures), 0, "exposures from a withdrawn advisory")
	harness.Equal(t, len(taken.Leads), 0, "leads from one")
}

// Claude Code updates itself on the default channel, so pinning it would mean turning its
// updater off and then owning what the updater was doing.
func TestClaudeCodeIsAnExposureWithNoTargetBecauseItUpdatesItself(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.says("claude --version", "2.1.100 (Claude Code)")
	b.serves(claudeAdvisories, `[{"ghsa_id": "GHSA-pq7j-f95f-qfcv", "severity": "high",
	  "summary": "Command injection in the IDE extension", "withdrawn_at": null, "identifiers": [],
	  "vulnerabilities": [{"package": {"ecosystem": "npm", "name": "@anthropic-ai/claude-code"},
	    "vulnerable_version_range": "< 2.1.179", "patched_versions": "2.1.179"}]}]`)

	found := b.look()
	e := found.exposure(t, "GHSA-pq7j-f95f-qfcv")

	harness.Equal(t, e.Fixed, "2.1.179", "the fix")
	harness.Equal(t, e.Target, "", "the target")
	harness.Wants(t, e.Needs, "Claude Code updates itself on the default channel")
	harness.Equal(t, len(found.Targets(nil)), 0, "targets")
}

// GHSA-r9rm-ppfv-c993 says `>= 2025.9.13` and `patched_versions: 2026.10.0`, which taken as a
// range makes every mise since affected for ever — this was a real false positive.
// The range with no upper bound at all, and the version the advisory itself says fixed it.
const unboundedAdvisoryJSON = `[{"ghsa_id": "GHSA-r9rm-ppfv-c993", "severity": "high",
  "summary": "Unanchored substring match in GitHub-attestations signer-workflow verification",
  "withdrawn_at": null, "identifiers": [],
  "vulnerabilities": [{"package": {"ecosystem": "rust", "name": "mise"},
    "vulnerable_version_range": ">= 2025.9.13", "patched_versions": "2026.10.0"}]}]`

func TestThePatchedVersionOverridesAnUnboundedRange(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.toolAt("2026.10.15 macos-arm64")
	b.serves(miseAdvisories, unboundedAdvisoryJSON)

	found := b.look()

	harness.Equal(t, len(found.Exposures), 0, "exposures, 2026.10.15 being above the version that fixed it")
	harness.Equal(t, len(found.Leads), 0, "leads")

	below := newBox(t)
	below.clean()
	below.toolAt("2026.9.20 macos-arm64")
	below.serves(miseAdvisories, unboundedAdvisoryJSON)

	harness.Equal(t, below.look().exposure(t, "GHSA-r9rm-ppfv-c993").Fixed, "2026.10.0",
		"the fix a mise below it is still waiting for")
}

// mise has published a range as a bare `v2026.9.6` with no operator at all, and a shape
// nobody anticipated may not turn into silence.
func TestARangeThatCouldNotBeReadGoesToTheAgentAsSomethingToRead(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.toolAt("2026.9.5 macos-arm64")
	b.serves(miseAdvisories, `[{"ghsa_id": "GHSA-6986-cq7v-7cj9", "severity": "high",
	  "summary": "Implicit project trust enables arbitrary command execution",
	  "withdrawn_at": null, "identifiers": [{"value": "GHSA-6986-cq7v-7cj9", "type": "GHSA"}],
	  "vulnerabilities": [{"package": {"ecosystem": "rust", "name": "mise"},
	    "vulnerable_version_range": "v2026.9.6", "patched_versions": "v2026.9.7"}]}]`)

	found := b.look()
	l := found.lead(t, "GHSA-6986-cq7v-7cj9")

	harness.Equal(t, len(found.Exposures), 0, "exposures")
	harness.Wants(t, l.Why, `it is about mise and states its affected versions as "v2026.9.6", `+
		`which hachiko could not read — so whether 2026.9.5 is affected is yours to judge`)
	harness.Equal(t, l.Severity, "high", "the severity")
}

func TestAdvisoriesThatCouldNotBeReadAreSaidAndNameTheRepository(t *testing.T) {
	// The unauthenticated API gives this Mac sixty requests an hour across every call it
	// makes, so a repository is not asked about a tool nothing said the version of.
	t.Run("the tool is not installed", func(t *testing.T) {
		b := newBox(t)
		b.clean()
		b.toolAt("2026.10.11 macos-arm64")

		b.look()

		harness.Equal(t, len(b.asked("jdx/mise")), 1, "requests for mise's advisories")
		harness.Equal(t, len(b.asked("anthropics/claude-code")), 0, "requests for Claude Code's")
	})

	t.Run("it did not answer", func(t *testing.T) {
		b := newBox(t)
		b.clean()
		b.toolAt("2026.6.12 macos-arm64")
		b.refuses(miseAdvisories, "it answered 403: API rate limit exceeded")

		harness.Wants(t, b.look().troubles(), "GitHub's advisories for jdx/mise could not be read: "+
			"it answered 403: API rate limit exceeded")
	})

	t.Run("it answered something else", func(t *testing.T) {
		b := newBox(t)
		b.clean()
		b.toolAt("2026.6.12 macos-arm64")
		b.serves(miseAdvisories, `{"message": "Not Found"}`)

		harness.Wants(t, b.look().troubles(),
			"GitHub's advisories for jdx/mise answered something that is not JSON")
	})
}

func TestEverySpellingOfOneAdvisoryIsCarriedWithItAndTheWorseScoreIsTheScore(t *testing.T) {
	a := ghAdvisory{GHSA: "GHSA-9mm4-fgvc-x7rp", CVE: "CVE-2026-50001"}
	a.Identifiers = []struct {
		Value string `json:"value"`
		Type  string `json:"type"`
	}{{Value: "GHSA-9mm4-fgvc-x7rp", Type: "GHSA"}, {Value: "CVE-2026-50001", Type: "CVE"}}

	harness.Equal(t, len(a.aliases()), 1, "aliases, the GHSA being the id and each spelling once")
	harness.Equal(t, a.aliases()[0], "CVE-2026-50001", "the CVE")

	// The score is what the feed's severity rule reads, whichever version of CVSS GitHub
	// stated it in.
	a.CVSS.Score = 7.5
	a.Severities.V4.Score = 9.3
	harness.Equal(t, a.score(), 9.3, "the worse of the two")

	a.Severities.V4.Score = 0
	harness.Equal(t, a.score(), 7.5, "a score stated in one version alone")
}
