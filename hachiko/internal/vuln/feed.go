package vuln

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// The four feeds of what has been published, read forward from a cursor of their own. This is
// the half of the check that has compared nothing: a feed says what the world found, and
// whether one of those is about this Mac is a judgement — so what comes out of here goes to an
// agent to read rather than to an upgrade.
//
// A cursor each, and each advanced only on a pass that actually read that source. One cursor
// for all four would mean a pass where NVD answered and the KEV did not recording the KEV as
// read up to now, and everything added to it in between would never be read at all.
const (
	sourceNVD = "nvd"
	sourceGH  = "github"
	sourceKEV = "kev"
	sourceBun = "bun"
)

const (
	nvdURL    = "https://services.nvd.nist.gov/rest/json/cves/2.0"
	ghFeedURL = "https://api.github.com/advisories"
	kevURL    = "https://www.cisa.gov/sites/default/files/feeds/known_exploited_vulnerabilities.json"
	bunURL    = "https://api.github.com/repos/oven-sh/bun/releases?per_page=20"
)

// How far back a source with no cursor is read. A day and not the hundred and twenty NVD
// allows, because the point of this is noticing what is published from now on: a first pass
// that read four months of back catalogue would be several thousand advisories and an incident
// about none of them, on the one Mac where nobody is there to dismiss it.
const firstLook = 24 * time.Hour

// NVD's own limit on the window one request may span, which is also the furthest back a
// cursor is ever honoured: a state file that was lost or a Mac that was off for a season
// cannot ask for more than this, so it asks for what it may and says what it skipped.
const nvdWindow = 120 * 24 * time.Hour

// How much of NVD one pass reads, and the gap between its requests. NVD publishes around two
// thousand CVEs a day, so a three-hour window is a few hundred and one page; the pages are
// there for a first pass and for a Mac that was off. Unauthenticated callers are asked for no
// more than five requests in thirty seconds, so six seconds apart is well inside it.
const (
	nvdPageSize = 200
	nvdPages    = 4
	nvdGap      = 6 * time.Second
)

func (c checker) feed(inv Inventory, found *Found) {
	watched := terms(inv, found.unscanned)

	c.nvd(watched, found)
	c.githubFeed(watched, found)
	c.kev(watched, found)
	c.bun(found)
}

// The cursor a source is read from, and the clamp on it. A cursor in the future is a clock
// that moved and is read as no cursor at all.
func (c checker) since(source string) time.Time {
	now := c.deps.Now()
	at := time.Unix(c.cursors[source], 0)

	if c.cursors[source] <= 0 || at.After(now) {
		return now.Add(-firstLook)
	}
	if oldest := now.Add(-nvdWindow); at.Before(oldest) {
		return oldest
	}
	return at
}

// Recorded as read up to this pass's own clock rather than to the newest item it saw: a feed
// that answered with nothing has still been read up to now, and taking the newest item's time
// would have a quiet source read from the same moment for ever.
func (c checker) read(source string, found *Found) {
	if found.Cursors == nil {
		found.Cursors = map[string]int64{}
	}
	found.Cursors[source] = c.deps.Now().Unix()
}

// NVD, which is every CVE there is and the only source here that carries a score for most of
// them. Asked by publication date, which is what makes a cursor work: `pubStartDate` and
// `pubEndDate` are both required and the span between them may be no more than a hundred and
// twenty days.
func (c checker) nvd(watched map[string][]string, found *Found) {
	from, to := c.since(sourceNVD), c.deps.Now()
	total, got := 0, 0

	for page := 0; page < nvdPages; page++ {
		if page > 0 {
			c.deps.Sleep(nvdGap)
		}

		query := url.Values{}
		query.Set("pubStartDate", nvdTime(from))
		query.Set("pubEndDate", nvdTime(to))
		query.Set("resultsPerPage", fmt.Sprint(nvdPageSize))
		query.Set("startIndex", fmt.Sprint(got))

		body, err := c.deps.Get(nvdURL + "?" + query.Encode())
		if err != nil {
			found.Troubles = append(found.Troubles, "NVD could not be read: "+oneLine(err.Error()))
			return
		}

		answer := nvdAnswer{}
		if json.Unmarshal(body, &answer) != nil {
			found.Troubles = append(found.Troubles, "NVD answered something that is not JSON")
			return
		}

		total = answer.TotalResults
		got += len(answer.Vulnerabilities)

		for _, v := range answer.Vulnerabilities {
			described := v.CVE.english()
			score, severity := v.CVE.Metrics.worst()

			if reason := why(v.CVE.ID+" "+described, score, watched, namedOnly); reason != "" {
				found.Leads = append(found.Leads, Lead{
					ID: v.CVE.ID, Source: "NVD", Title: described,
					Severity: severity, URL: "https://nvd.nist.gov/vuln/detail/" + v.CVE.ID, Why: reason,
				})
			}
		}

		if len(answer.Vulnerabilities) == 0 || got >= total {
			break
		}
	}

	// The cursor moves whether or not every page was read, and what was skipped is said. The
	// alternative is a cursor that never advances past a busy day, which is a check stuck on
	// the same four pages for the rest of the Mac's life — and the normal case is a three-hour
	// window of a few hundred CVEs in one page.
	if got < total {
		found.Troubles = append(found.Troubles, fmt.Sprintf(
			"NVD had %s published since the last pass and this one read %d of them",
			wording.CountOf(int64(total), "CVE", "CVEs"), got))
	}
	c.read(sourceNVD, found)
}

// NVD's own format for a date, which is ISO 8601 with milliseconds and no zone: it reads one
// without a zone as UTC, so the times go out as UTC rather than as this Mac's local clock.
func nvdTime(at time.Time) string {
	return at.UTC().Format("2006-01-02T15:04:05.000")
}

// NVD's own shape, named rather than declared where it is read: the metrics nest three deep
// and twice over, and the two versions of CVSS have to be compared against each other.
type nvdAnswer struct {
	TotalResults    int `json:"totalResults"`
	Vulnerabilities []struct {
		CVE nvdCVE `json:"cve"`
	} `json:"vulnerabilities"`
}

type nvdCVE struct {
	ID           string     `json:"id"`
	Descriptions []nvdText  `json:"descriptions"`
	Metrics      nvdMetrics `json:"metrics"`
}

type nvdText struct {
	Lang  string `json:"lang"`
	Value string `json:"value"`
}

type nvdMetrics struct {
	V31 []nvdMetric `json:"cvssMetricV31"`
	V40 []nvdMetric `json:"cvssMetricV40"`
}

type nvdMetric struct {
	Data struct {
		BaseScore    float64 `json:"baseScore"`
		BaseSeverity string  `json:"baseSeverity"`
	} `json:"cvssData"`
}

// The English description, or the first one there is: every CVE has one in English in
// practice, and a translation is still better than nothing to match a name against.
func (c nvdCVE) english() string {
	for _, d := range c.Descriptions {
		if d.Lang == "en" {
			return d.Value
		}
	}
	if len(c.Descriptions) > 0 {
		return c.Descriptions[0].Value
	}
	return ""
}

// The worse of the two CVSS versions NVD states, and the word that goes with it. Both are
// often absent — a CVE published hours ago has `metrics: {}`, which is how the advisory this
// check was written for arrived — so a score of nothing is a score of nothing rather than a
// zero that means safe.
func (m nvdMetrics) worst() (float64, string) {
	score, severity := 0.0, ""

	for _, metric := range append(append([]nvdMetric{}, m.V31...), m.V40...) {
		if metric.Data.BaseScore > score {
			score, severity = metric.Data.BaseScore, metric.Data.BaseSeverity
		}
	}
	return score, severity
}

// GitHub's global advisory feed, read for the unreviewed half of it: the reviewed ones about
// an ecosystem on this Mac already reach it through OSV, which mirrors them, and what is left
// is everything GitHub has ingested from NVD and elsewhere without curating. Published by day,
// which is the granularity its filter takes.
func (c checker) githubFeed(watched map[string][]string, found *Found) {
	from, to := c.since(sourceGH), c.deps.Now()

	query := url.Values{}
	query.Set("type", "unreviewed")
	query.Set("per_page", "100")
	query.Set("published", from.UTC().Format("2006-01-02")+".."+to.UTC().Format("2006-01-02"))

	body, err := c.deps.Get(ghFeedURL + "?" + query.Encode())
	if err != nil {
		found.Troubles = append(found.Troubles,
			"GitHub's advisory feed could not be read: "+oneLine(err.Error()))
		return
	}

	var advisories []ghAdvisory
	if json.Unmarshal(body, &advisories) != nil {
		found.Troubles = append(found.Troubles, "GitHub's advisory feed answered something that is not JSON")
		return
	}

	for _, advisory := range advisories {
		if advisory.Withdrawn != "" {
			continue
		}

		text := advisory.GHSA + " " + advisory.CVE + " " + advisory.Summary + " " + advisory.Description
		if reason := why(text, advisory.score(), watched, namedOnly); reason != "" {
			found.Leads = append(found.Leads, Lead{
				ID: advisory.GHSA, Aliases: advisory.aliases(), Source: "GitHub",
				Title: advisory.Summary, Severity: advisory.Severity, URL: advisory.HTMLURL, Why: reason,
			})
		}
	}
	c.read(sourceGH, found)
}

// CISA's catalogue of what is being exploited, which is the one source here that says a fault
// is not theoretical. Read whole every time, because it is one file with no date filter and
// the cursor is applied to each entry's own `dateAdded`.
func (c checker) kev(watched map[string][]string, found *Found) {
	from := c.since(sourceKEV)

	body, err := c.deps.Get(kevURL)
	if err != nil {
		found.Troubles = append(found.Troubles,
			"CISA's exploited-vulnerabilities catalogue could not be read: "+oneLine(err.Error()))
		return
	}

	var catalogue struct {
		Vulnerabilities []struct {
			CVE        string `json:"cveID"`
			Vendor     string `json:"vendorProject"`
			Product    string `json:"product"`
			Name       string `json:"vulnerabilityName"`
			Added      string `json:"dateAdded"`
			Short      string `json:"shortDescription"`
			Ransomware string `json:"knownRansomwareCampaignUse"`
		} `json:"vulnerabilities"`
	}
	if json.Unmarshal(body, &catalogue) != nil {
		found.Troubles = append(found.Troubles,
			"CISA's catalogue answered something that is not JSON")
		return
	}

	// CISA dates an entry by the day it was added and nothing finer, so the cursor is read back
	// to the start of its own day: compared against an instant, an entry added earlier today
	// parses as this morning's midnight and is dropped as older than a cursor set at noon.
	since := from.Truncate(24 * time.Hour)

	for _, v := range catalogue.Vulnerabilities {
		added, err := time.Parse("2006-01-02", v.Added)
		if err != nil || added.Before(since) {
			continue
		}

		text := strings.Join([]string{v.CVE, v.Vendor, v.Product, v.Name, v.Short}, " ")
		// The one source severity alone keeps an entry from, and the score is the catalogue's own
		// meaning rather than a number it states: everything on this list is being exploited.
		if reason := why(text, criticalScore, watched, anySeverity); reason != "" {
			exploited := ", and CISA has it on the list of what is being exploited"
			if strings.EqualFold(v.Ransomware, "known") {
				exploited += ", by a known ransomware campaign"
			}

			found.Leads = append(found.Leads, Lead{
				ID: v.CVE, Source: "CISA's exploited catalogue",
				Title:    v.Vendor + " " + v.Product + ": " + v.Name,
				Severity: "exploited",
				URL:      "https://nvd.nist.gov/vuln/detail/" + v.CVE,
				Why:      reason + exploited,
			})
		}
	}
	c.read(sourceKEV, found)
}

// bun's releases, because bun says what a release fixed and nowhere else does: it publishes no
// security advisories on its repository — the endpoint answers with an empty array — and it is
// a runtime rather than a package, so OSV has no record of it either. So the release notes are
// the source, read for the wording of a security fix.
//
// A lead and never an exposure, however plainly a note is worded: a release note is prose, and
// reading "this release fixes a certificate validation bug" as a version comparison would be
// this check deciding what bun shipped.
func (c checker) bun(found *Found) {
	from := c.since(sourceBun)

	body, err := c.deps.Get(bunURL)
	if err != nil {
		found.Troubles = append(found.Troubles, "bun's releases could not be read: "+oneLine(err.Error()))
		return
	}

	var releases []struct {
		Tag        string `json:"tag_name"`
		Name       string `json:"name"`
		Body       string `json:"body"`
		Published  string `json:"published_at"`
		Prerelease bool   `json:"prerelease"`
		Draft      bool   `json:"draft"`
		URL        string `json:"html_url"`
	}
	if json.Unmarshal(body, &releases) != nil {
		found.Troubles = append(found.Troubles, "bun's releases answered something that is not JSON")
		return
	}

	for _, release := range releases {
		at, err := time.Parse(time.RFC3339, release.Published)
		if err != nil || at.Before(from) || release.Prerelease || release.Draft {
			continue
		}

		if reason := securityWording(release.Body); reason != "" {
			found.Leads = append(found.Leads, Lead{
				ID: release.Tag, Source: "bun's releases", Title: release.Name,
				URL: release.URL,
				Why: "its release notes say " + reason +
					", and bun is the one runtime here with no advisory feed of its own",
			})
		}
	}
	c.read(sourceBun, found)
}

// The words a release note uses when it has fixed something security-shaped. The trust terms
// as well as the obvious ones, because the fault this check was written for reached bun as a
// certificate store that had to change rather than as a CVE.
func securityWording(body string) string {
	for _, term := range append([]string{"security", "vulnerability", "cve", "ghsa"}, trustTerms...) {
		if mentions(body, term) {
			return term
		}
	}
	return ""
}
