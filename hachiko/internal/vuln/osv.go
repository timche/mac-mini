package vuln

import (
	"encoding/json"
	"fmt"

	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// OSV is the one source that answers about a version rather than about a range: it is asked
// what is installed and comes back with the faults that version has, which is the comparison
// done by the database that holds the data rather than by this check. It covers the three
// ecosystems on this Mac that have one — the Go standard library and the modules the two
// compiled tools require, the npm packages mise and shibuya's lockfile pin, and pip.
const (
	osvBatchURL = "https://api.osv.dev/v1/querybatch"
	osvVulnURL  = "https://api.osv.dev/v1/vulns/"
	sourceOSV   = "OSV"
)

// How many records one pass reads in full. querybatch answers with ids and nothing else, so
// everything a message says about one — what it is, how bad, which version fixed it — is a
// request of its own, and a module that has not moved in four years comes back with dozens:
// `golang.org/x/crypto` at the pseudo-version mochi requires answered with eighteen.
//
// Fifty, measured: twenty took this pass sixteen seconds end to end, so fifty is well inside
// the two minutes it has, and it is more than a Mac whose dependencies are being kept up to
// date ever has. Past it the records are said to be unread rather than dropped in silence.
const osvDetailLimit = 50

type osvQuery struct {
	Package struct {
		Name      string `json:"name"`
		Ecosystem string `json:"ecosystem"`
	} `json:"package"`
	Version string `json:"version"`
}

type osvVuln struct {
	ID      string   `json:"id"`
	Summary string   `json:"summary"`
	Aliases []string `json:"aliases"`

	// Where a GHSA record keeps the severity word. OSV's own `severity` array is a CVSS vector
	// and is absent from most records, including both of the ones the day this was written was
	// about, so this is the field that actually answers.
	DatabaseSpecific struct {
		Severity string `json:"severity"`
	} `json:"database_specific"`

	Affected []struct {
		Package struct {
			Name      string `json:"name"`
			Ecosystem string `json:"ecosystem"`
		} `json:"package"`
		Ranges []struct {
			Events []struct {
				Introduced string `json:"introduced"`
				Fixed      string `json:"fixed"`
			} `json:"events"`
		} `json:"ranges"`
	} `json:"affected"`
}

// One batch per ecosystem rather than one query per package, which is what querybatch is for:
// a Mac with two hundred npm packages in a lockfile is one request. Per ecosystem and not one
// batch for all three, because a batch carrying an ecosystem OSV does not know is refused
// whole with a 400 — `pip` for `PyPI` costs every other query in it — so a spelling that goes
// wrong costs one ecosystem and says so.
func (c checker) osv(inv Inventory, found *Found) {
	whole := true

	for _, ecosystem := range []string{ecosystemGo, ecosystemPyPI, ecosystemNPM} {
		installed := inv.inEcosystem(ecosystem)
		if len(installed) == 0 {
			continue
		}
		if !c.osvBatch(ecosystem, installed, found) {
			whole = false
		}
	}

	// Only when every ecosystem answered and every record it named was read. An advisory the
	// detail limit left unread is one this pass knows nothing about, and calling the source
	// read would have the check post that this Mac is no longer affected by it.
	if whole {
		found.read(sourceOSV)
	}
}

// Whether this ecosystem was heard in full, which is both that OSV answered and that nothing
// it named was left unread.
func (c checker) osvBatch(ecosystem string, installed []Installed, found *Found) bool {
	queries := make([]osvQuery, 0, len(installed))
	for _, in := range installed {
		q := osvQuery{Version: in.Version}
		q.Package.Name, q.Package.Ecosystem = in.Name, in.Ecosystem
		queries = append(queries, q)
	}

	body, err := json.Marshal(map[string]any{"queries": queries})
	if err != nil {
		found.Troubles = append(found.Troubles, sourceOSV+" could not be asked about "+ecosystem)
		return false
	}

	answer, err := c.deps.Post(osvBatchURL, body)
	if err != nil {
		found.Troubles = append(found.Troubles,
			sourceOSV+" could not be reached about "+ecosystem+": "+oneLine(err.Error()))
		return false
	}

	var batch struct {
		Results []struct {
			Vulns []struct {
				ID string `json:"id"`
			} `json:"vulns"`
		} `json:"results"`
	}
	if json.Unmarshal(answer, &batch) != nil {
		found.Troubles = append(found.Troubles,
			sourceOSV+" answered something that is not JSON about "+ecosystem)
		return false
	}

	// Positional: the results come back in the order the queries went out, and an entry with
	// no vulns is a package with nothing against it. A shorter answer than the question is
	// not something to guess at — pairing the wrong result with a package would name the
	// wrong thing as vulnerable — so it is said and nothing is read from it.
	if len(batch.Results) != len(queries) {
		found.Troubles = append(found.Troubles, fmt.Sprintf(
			"%s answered about %d of the %d %s packages it was asked about, so none of them was read",
			sourceOSV, len(batch.Results), len(queries), ecosystem))
		return false
	}

	read, unread, failed := 0, 0, false
	for i, result := range batch.Results {
		for _, hit := range result.Vulns {
			if read >= osvDetailLimit {
				unread++
				continue
			}
			read++

			vuln, err := c.osvDetail(hit.ID)
			if err != nil {
				found.Troubles = append(found.Troubles,
					sourceOSV+" would not say what "+oneLine(hit.ID)+" is: "+oneLine(err.Error()))
				failed = true
				continue
			}
			found.Exposures = append(found.Exposures, exposureOf(installed[i], vuln))
		}
	}

	if unread > 0 {
		found.Troubles = append(found.Troubles, fmt.Sprintf(
			"%s had %s against %s packages that this pass did not read, having already read %d",
			sourceOSV, wording.CountOf(int64(unread), "record", "records"), ecosystem, osvDetailLimit))
	}
	return unread == 0 && !failed
}

func (c checker) osvDetail(id string) (osvVuln, error) {
	vuln := osvVuln{}

	// The id is OSV's own and goes into a URL, so it is held to the shape an advisory id has
	// rather than trusted: a record named with a slash or a dot-dot would be a request to
	// somewhere else on the same host.
	if !advisoryID.MatchString(id) {
		return vuln, fmt.Errorf("%q is not an advisory id", oneLine(id))
	}

	body, err := c.deps.Get(osvVulnURL + id)
	if err != nil {
		return vuln, err
	}
	if err := json.Unmarshal(body, &vuln); err != nil {
		return vuln, fmt.Errorf("it answered something that is not JSON")
	}
	return vuln, nil
}

func exposureOf(in Installed, vuln osvVuln) Exposure {
	e := Exposure{
		ID:       vuln.ID,
		Aliases:  vuln.Aliases,
		Source:   sourceOSV,
		Severity: vuln.DatabaseSpecific.Severity,
		Summary:  vuln.Summary,
		Name:     in.Name,
		Version:  in.Version,
		Fixed:    fixedFor(in, vuln),
		Target:   in.Target,
	}

	// A record with no fixed version is a fault nothing can be upgraded away from — OSV-2025-433
	// against libssh2 is a git range with an `introduced` commit and no `fixed` one — so there is
	// nothing to hand the update even where the thing is one the update moves.
	if e.Fixed == "" {
		e.Target = ""
	}
	if e.Target == "" {
		e.Needs = needsFor(in, e.Fixed)
	}
	return e
}

// The lowest version above what is installed that OSV says is fixed, out of the affected
// entries for this very package: a record covers several packages and several lines at once,
// and the fix for another line is not a version to tell anybody to upgrade to.
//
// Lowest rather than newest, because it is the least an upgrade has to reach, and an upgrade
// here takes whatever is newest in the range anyway. Above what is installed, because a
// `fixed` event below it belongs to an earlier line that was fixed and broken again.
func fixedFor(in Installed, vuln osvVuln) string {
	lowest := ""

	for _, affected := range vuln.Affected {
		if affected.Package.Name != in.Name || affected.Package.Ecosystem != in.Ecosystem {
			continue
		}
		for _, r := range affected.Ranges {
			for _, event := range r.Events {
				fixed := event.Fixed
				if fixed == "" || compare(fixed, in.Version) <= 0 {
					continue
				}
				if lowest == "" || compare(fixed, lowest) < 0 {
					lowest = fixed
				}
			}
		}
	}
	return lowest
}

// The one line for Tim about something nothing here moves, which is the whole of why each of
// these is not a target: a Go module is a commit somebody means to make, an npm package in
// shibuya's lockfile is a deploy to Cloudflare, and Claude Code moves itself.
func needsFor(in Installed, fixed string) string {
	switch {
	case fixed == "":
		return "Nothing is released that fixes it, so this one is a workaround or waiting."
	case in.From == fromGo && in.Name == "stdlib":
		return "The fix is a Go toolchain this Mac has not got: " +
			wording.CodeSpan("mise up -C ~/.mac-mini --local go") + " moves it, and then the tools rebuild."
	case in.From == fromGo:
		return "A module bump in " + wording.CodeSpan(in.Where+"/go.mod") +
			" is a commit somebody means to make, so it is not one this starts."
	case in.From == fromNPM:
		return "shibuya's packages move with a lockfile change and a deploy to Cloudflare, which is nobody's to start unattended."
	case in.Name == "claude-code":
		return "Claude Code updates itself on the default channel, so what this needs is for it to have done so."
	default:
		return "Nothing here moves this one, so the upgrade is somebody's to run."
	}
}
