package vuln

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Three of the things on this Mac come from their authors' own installers rather than from
// mise's or Homebrew's index, so nothing between them and this check knows their versions —
// and two of the three publish security advisories on their own repository, which is the only
// place those faults are stated. mise says `<= 2026.9.18` and leaves the comparison to
// whoever is asking, which is what version.go is for.
//
// Only the repositories that actually publish. `herdrdev/herdr` and `oven-sh/bun` both answer
// with an empty array, and the unauthenticated GitHub API gives this Mac sixty requests an
// hour across every call it makes — so a request that has never once returned anything is one
// the feed below spends better. bun is read from its releases instead, which is where it says
// what a release fixed.
var repoAdvisories = []struct {
	// The repository, and the package name its own advisories use for the thing this Mac has
	// installed. Matched by string equality, because a repository publishes advisories about
	// more than one thing: anthropics/claude-code carries Claude Desktop's as well, which is
	// not on this Mac at all.
	repo string
	pkg  string
	from string
	name string
}{
	{repo: "jdx/mise", pkg: "mise", from: fromTool, name: "mise"},
	{repo: "anthropics/claude-code", pkg: "@anthropic-ai/claude-code", from: fromTool, name: "claude-code"},
}

// One page each, which is a hundred advisories and several times what either repository has.
// No paging: a second page is a second request against an hour's sixty, and an advisory a
// hundred deep is one from years before the version this Mac is on.
const (
	repoAdvisoryURL = "https://api.github.com/repos/%s/security-advisories?per_page=100"
	sourceGitHub    = "GitHub's advisories for "
)

// What a repository's own advisories say, in both the shapes GitHub uses: `patched_versions`
// here, and `first_patched_version` in the global feed.
type ghAdvisory struct {
	GHSA        string `json:"ghsa_id"`
	CVE         string `json:"cve_id"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Severity    string `json:"severity"`
	Published   string `json:"published_at"`
	Withdrawn   string `json:"withdrawn_at"`
	HTMLURL     string `json:"html_url"`

	CVSS struct {
		Score float64 `json:"score"`
	} `json:"cvss"`
	Severities struct {
		V4 struct {
			Score float64 `json:"score"`
		} `json:"cvss_v4"`
	} `json:"cvss_severities"`

	Identifiers []struct {
		Value string `json:"value"`
		Type  string `json:"type"`
	} `json:"identifiers"`

	Vulnerabilities []struct {
		Package struct {
			Ecosystem string `json:"ecosystem"`
			Name      string `json:"name"`
		} `json:"package"`
		Range   string `json:"vulnerable_version_range"`
		Patched string `json:"patched_versions"`
		First   struct {
			Identifier string `json:"identifier"`
		} `json:"first_patched_version"`
	} `json:"vulnerabilities"`
}

// Every spelling of the fault, which is what makes one issue one record however many sources
// saw it. GitHub gives both the GHSA and the CVE, and `identifiers` repeats them with
// anything else it knows.
func (a ghAdvisory) aliases() []string {
	seen := map[string]bool{a.GHSA: true}
	var aliases []string

	for _, id := range append([]string{a.CVE}, identifierValues(a.Identifiers)...) {
		if id != "" && !seen[id] {
			seen[id] = true
			aliases = append(aliases, id)
		}
	}
	return aliases
}

func identifierValues(ids []struct {
	Value string `json:"value"`
	Type  string `json:"type"`
}) []string {
	var values []string
	for _, id := range ids {
		values = append(values, id.Value)
	}
	return values
}

// The worst CVSS score GitHub states for it, whichever version of CVSS it stated. Used by the
// feed's severity rule, which keeps anything critical whether or not it names something on
// this Mac.
func (a ghAdvisory) score() float64 {
	score := a.CVSS.Score
	if a.Severities.V4.Score > score {
		score = a.Severities.V4.Score
	}
	return score
}

func (c checker) repoAdvisories(inv Inventory, found *Found) {
	for _, watched := range repoAdvisories {
		installed := inv.version(watched.from, watched.name)
		if installed == "" {
			continue
		}

		source := sourceGitHub + watched.repo
		body, err := c.deps.Get(fmt.Sprintf(repoAdvisoryURL, watched.repo))
		if err != nil {
			found.Troubles = append(found.Troubles, source+" could not be read: "+oneLine(err.Error()))
			continue
		}

		var advisories []ghAdvisory
		if json.Unmarshal(body, &advisories) != nil {
			found.Troubles = append(found.Troubles, source+" answered something that is not JSON")
			continue
		}

		found.read(source)

		for _, advisory := range advisories {
			// A withdrawn advisory is one GitHub has taken back, so it is not a fault any more.
			if advisory.Withdrawn != "" {
				continue
			}
			c.matchAdvisory(advisory, source, watched.pkg, watched.name, installed, found)
		}
	}
}

// Whether one advisory is about the version installed. Each `vulnerabilities` entry is one
// package and one range, and only the entry naming the package this Mac has is read: mise's
// own advisories name `mise-sigstore` beside `mise`, and Claude Code's name Claude Desktop.
//
// A range this cannot read is not a range saying the version is safe. mise has published one
// as a bare `v2026.9.6` with no operator at all, so a spelling that does not parse goes to
// the agent as something to read rather than being dropped — the one thing that may not
// happen is a shape nobody anticipated turning into silence.
func (c checker) matchAdvisory(advisory ghAdvisory, source, pkg, name, installed string, found *Found) {
	for _, v := range advisory.Vulnerabilities {
		if v.Package.Name != pkg {
			continue
		}

		affected, read := inRange(installed, v.Range)
		switch {
		case !read:
			found.Leads = append(found.Leads, Lead{
				ID: advisory.GHSA, Aliases: advisory.aliases(), Source: source,
				Title: advisory.Summary, Severity: advisory.Severity, URL: advisory.HTMLURL,
				Why: fmt.Sprintf("it is about %s and states its affected versions as %q, which hachiko could not read — so whether %s is affected is yours to judge",
					name, oneLine(v.Range), installed),
			})
		case affected:
			fixed := firstPatched(v.Patched)
			if fixed == "" {
				fixed = strings.TrimSpace(v.First.Identifier)
			}

			// The version it was fixed in overrides the range, because the ranges are written by
			// hand and some of them have no upper bound at all: mise's own
			// GHSA-r9rm-ppfv-c993 says `>= 2025.9.13` and `patched_versions: 2026.10.0`, which
			// taken as a range makes every mise since affected for ever. A version at or above
			// what the advisory itself says fixed it is not affected, whatever the range says.
			if fixed != "" && compare(installed, fixed) >= 0 {
				continue
			}

			e := Exposure{
				ID: advisory.GHSA, Aliases: advisory.aliases(), Source: source,
				Severity: advisory.Severity, Summary: advisory.Summary, URL: advisory.HTMLURL,
				Name: name, Version: installed, Fixed: fixed,
			}
			if fixed != "" {
				e.Target = c.targetOf(name)
			}
			if e.Target == "" {
				e.Needs = needsFor(Installed{From: fromTool, Name: name}, fixed)
			}
			found.Exposures = append(found.Exposures, e)
		}
	}
}

// What the update is given for one of the three installer-shipped tools, read back off the
// inventory rather than written again here: the inventory is the one place that knows how a
// thing was installed, and Claude Code's is deliberately empty.
func (c checker) targetOf(name string) string {
	for _, in := range c.inv.from(fromTool) {
		if in.Name == name {
			return in.Target
		}
	}
	return ""
}
