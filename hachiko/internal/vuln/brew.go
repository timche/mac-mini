package vuln

import (
	"encoding/json"
	"fmt"

	"github.com/timche/mac-mini/hachiko/internal/update"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// Homebrew scans what it installed itself. `brew vulns` reads the SBOM of every bottle on
// this Mac and matches it against the advisory data Homebrew publishes, which is work nothing
// here could do as well: a formula has no ecosystem of its own in OSV — aria2 comes back as a
// Debian package and libssh2 only under an OSS-Fuzz version string of `libssh2-1.11.1` — so
// asking OSV about formula names by hand would be guessing at other distributions' packaging.
//
// Its default is kept: an advisory Homebrew has already patched in its own bottle is not a
// finding here, which is what `--no-ignore-patches` would turn off. Fourteen of libssh2's
// fifteen were in that state the day this was written, and reporting them would be an
// incident about a Mac that already has the fix.
const brewNoAutoUpdate = "HOMEBREW_NO_AUTO_UPDATE=1"

// Where it is read from, for the line that says a source was not read.
const sourceBrew = "brew vulns"

type brewReport struct {
	Findings []struct {
		Formula         string `json:"formula"`
		Version         string `json:"version"`
		Vulnerabilities []struct {
			ID       string   `json:"id"`
			Severity string   `json:"severity"`
			Summary  string   `json:"summary"`
			Aliases  []string `json:"aliases"`
		} `json:"vulnerabilities"`
	} `json:"findings"`

	// What the scan could not cover, which is the difference between a formula that is clear
	// and one nothing looked at. Homebrew says so itself rather than leaving it to be inferred
	// from a count.
	Skipped  []string `json:"skipped_formulae"`
	Checked  int      `json:"checked"`
	Complete bool     `json:"complete"`
}

func (c checker) brew(found *Found) {
	out := c.deps.Run(Command{
		Name: "brew", Args: []string{"vulns", "--json"},
		Env: []string{brewNoAutoUpdate}, Timeout: brewScan,
	})
	// Not the exit status: `brew vulns` exits 1 when it has found something, so a reading
	// thrown away for being non-zero would be a Mac that goes quiet exactly when it has
	// something to say. What makes it a failure is JSON that will not parse.
	if !out.OK {
		found.Troubles = append(found.Troubles, sourceBrew+" could not run: "+oneLine(out.Text))
		return
	}

	report := brewReport{}
	if err := json.Unmarshal([]byte(out.Stdout), &report); err != nil {
		found.Troubles = append(found.Troubles,
			sourceBrew+" answered something that is not JSON: "+oneLine(out.Text))
		return
	}

	found.unscanned = report.Skipped

	// A scan that skipped something is still a reading of everything else, so it counts as
	// read: the formulae it skipped are the ones the feed matches by name instead, and holding
	// the whole source unread would leave every formula advisory open for ever.
	found.read(sourceBrew)

	if !report.Complete || len(report.Skipped) > 0 {
		found.Troubles = append(found.Troubles, fmt.Sprintf(
			"%s scanned %s and skipped %s, which have no bill of materials to match against",
			sourceBrew, wording.CountOf(int64(report.Checked), "formula", "formulae"),
			wording.CountOf(int64(len(report.Skipped)), "formula", "formulae")))
	}

	if len(report.Findings) == 0 {
		return
	}

	// Read once and only where there is something to route, because it is a second brew
	// invocation and this runs inside the five-minute check. Never `brew update` first: fetching
	// Homebrew's formulae is the nightly update's step, and a check that did it as a side
	// effect would be moving what the update is answerable for.
	newer := c.brewOutdated(found)

	for _, finding := range report.Findings {
		for _, v := range finding.Vulnerabilities {
			e := Exposure{
				ID:       v.ID,
				Aliases:  v.Aliases,
				Source:   sourceBrew,
				Severity: v.Severity,
				Summary:  v.Summary,
				Name:     finding.Formula,
				Version:  finding.Version,
			}

			// Homebrew's advisory data names a fixed version only sometimes, and when it does it
			// is as often an upstream commit as a release — fourteen of libssh2's patched entries
			// were a 40-character SHA. So what decides whether there is anything to upgrade to is
			// Homebrew itself: a formula it lists as outdated has a newer bottle, and a formula it
			// does not is one where the fix has not reached Homebrew yet whatever upstream has
			// tagged.
			if fixed, ok := newer[finding.Formula]; ok {
				e.Fixed, e.Target = fixed, update.BrewTarget(finding.Formula)
			} else {
				e.Needs = "Homebrew has nothing newer than this, so there is nothing to upgrade to yet."
			}
			found.Exposures = append(found.Exposures, e)
		}
	}
}

// Every formula and cask Homebrew has a newer version of, by name. A reading that fails
// leaves every finding routed to the agent rather than to an upgrade, which is the safe way
// round: an upgrade of something Homebrew has nothing newer of does nothing at all and
// reports that it did.
func (c checker) brewOutdated(found *Found) map[string]string {
	newer := map[string]string{}

	out := c.deps.Run(Command{
		Name: "brew", Args: []string{"outdated", "--json=v2"},
		Env: []string{brewNoAutoUpdate}, Timeout: listing,
	})
	if !out.OK || out.Code != 0 {
		found.Troubles = append(found.Troubles,
			"brew could not say what it has newer versions of, so nothing of Homebrew's is being upgraded: "+
				oneLine(out.Text))
		return newer
	}

	var outdated struct {
		Formulae []struct {
			Name    string `json:"name"`
			Current string `json:"current_version"`
			Pinned  bool   `json:"pinned"`
		} `json:"formulae"`
		Casks []struct {
			Name    string `json:"name"`
			Current string `json:"current_version"`
		} `json:"casks"`
	}
	if json.Unmarshal([]byte(out.Stdout), &outdated) != nil {
		found.Troubles = append(found.Troubles,
			"brew answered something that is not JSON about what it has newer versions of")
		return newer
	}

	for _, formula := range outdated.Formulae {
		// A pinned formula is one somebody held back on purpose, and a check is not what
		// overrides that.
		if !formula.Pinned {
			newer[formula.Name] = formula.Current
		}
	}
	for _, cask := range outdated.Casks {
		newer[cask.Name] = cask.Current
	}
	return newer
}
