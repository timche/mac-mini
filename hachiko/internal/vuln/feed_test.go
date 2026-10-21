package vuln

import (
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// One page of NVD, in the shape it answers: the count of everything in the window beside the
// few it put in this page.
func nvdPage(total int, id, description string) string {
	return `{"resultsPerPage": 1, "startIndex": 0, "totalResults": ` + strconv.Itoa(total) + `,
	  "vulnerabilities": [{"cve": {"id": "` + id + `", "sourceIdentifier": "cve@mitre.org",
	    "published": "2026-10-21T09:17:23.400", "vulnStatus": "Awaiting Analysis",
	    "descriptions": [{"lang": "en", "value": "` + description + `"}],
	    "metrics": {"cvssMetricV31": [{"cvssData": {"baseScore": 7.5, "baseSeverity": "HIGH"}}]}}}]}`
}

// One cursor for all four would mean a pass where NVD answered and CISA did not recording
// CISA as read up to now, and everything added to it in between would never be read at all.
func TestASourceThatCouldNotBeReadKeepsItsCursorWhereItWas(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.cursors = map[string]int64{sourceNVD: base.Add(-3 * time.Hour).Unix()}
	b.refuses(kevURL, "it answered 503: Service Unavailable")

	found := b.look()

	harness.Equal(t, found.Cursors[sourceNVD], base.Unix(), "NVD's cursor")
	harness.Equal(t, found.Cursors[sourceGH], base.Unix(), "GitHub's cursor")
	harness.Equal(t, found.Cursors[sourceBun], base.Unix(), "bun's cursor")

	_, moved := found.Cursors[sourceKEV]
	harness.Equal(t, moved, false, "CISA's cursor, its catalogue not having been read")
	harness.Wants(t, found.troubles(), "CISA's exploited-vulnerabilities catalogue could not be read")

	// A feed that answered with nothing has still been read up to now, and taking the newest
	// item's time would have a quiet source read from the same moment for ever.
	quiet := newBox(t)
	quiet.clean()

	harness.Equal(t, len(quiet.look().Cursors), 4, "cursors written back on a pass that found nothing")
}

// A first pass that read four months of back catalogue would be several thousand advisories
// and an incident about none of them, on the one Mac where nobody is there to dismiss it; and
// a Mac that was off for a season cannot ask for more than NVD's own limit on the window.
func TestEachFeedIsReadFromItsOwnCursorAndTheClampsOnIt(t *testing.T) {
	for _, c := range []struct {
		what   string
		cursor int64
		from   time.Time
	}{
		{what: "no cursor", from: base.Add(-firstLook)},
		{what: "a cursor in the future, which is a clock that moved",
			cursor: base.Add(72 * time.Hour).Unix(), from: base.Add(-firstLook)},
		{what: "a cursor older than the window",
			cursor: base.Add(-200 * 24 * time.Hour).Unix(), from: base.Add(-nvdWindow)},
		{what: "a cursor inside the window",
			cursor: base.Add(-3 * time.Hour).Unix(), from: base.Add(-3 * time.Hour)},
	} {
		b := newBox(t)
		b.clean()
		if c.cursor != 0 {
			b.cursors = map[string]int64{sourceNVD: c.cursor}
		}

		b.look()

		harness.Equal(t, len(b.asked("pubStartDate="+url.QueryEscape(nvdTime(c.from)))), 1,
			"pages asked for with "+c.what)
	}
}

// Unauthenticated callers are asked for no more than five requests in thirty seconds, so the
// pages are read a gap apart and from where the last one got to.
func TestNVDIsPagedFromWhatItHasAlreadyReadAndWithAGapBetweenThePages(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.serves(nvdURL, nvdPage(3, "CVE-2026-1", "A flaw in curl before 8.18."))
	b.thenServes(nvdURL, nvdPage(3, "CVE-2026-2", "A flaw in git before 2.52."))
	b.thenServes(nvdURL, nvdPage(3, "CVE-2026-3", "A flaw in a TOTOLINK router."))

	found := b.look()

	harness.Equal(t, len(b.asked(nvdURL)), 3, "pages read")
	harness.Equal(t, len(b.asked("startIndex=0")), 1, "the first page")
	harness.Equal(t, len(b.asked("startIndex=1")), 1, "the second, asked from what the first read")
	harness.Equal(t, len(b.asked("startIndex=2")), 1, "the third")
	harness.Equal(t, len(b.slept), 2, "gaps waited out")
	harness.Equal(t, b.slept[0], nvdGap, "the gap")

	harness.Equal(t, len(found.Leads), 2, "leads, the router being nothing this Mac has")
	harness.Equal(t, found.lead(t, "CVE-2026-1").Severity, "HIGH", "the severity NVD states")
	harness.Equal(t, found.lead(t, "CVE-2026-1").URL, "https://nvd.nist.gov/vuln/detail/CVE-2026-1", "the URL")

	// The cursor moves whether or not every page was read, and what was skipped is said: a
	// cursor that never advances past a busy day is a check stuck on the same four pages for
	// the rest of the Mac's life.
	busy := newBox(t)
	busy.clean()
	busy.serves(nvdURL, nvdPage(1959, "CVE-2026-1", "A flaw in curl before 8.18."))

	day := busy.look()
	harness.Equal(t, len(busy.asked(nvdURL)), nvdPages, "pages read of a day with 1959 in it")
	harness.Wants(t, day.troubles(), "NVD had 1959 CVEs published since the last pass and this one read 4 of them")
	harness.Equal(t, day.Cursors[sourceNVD], base.Unix(), "NVD's cursor")
}

// Measured against a day of NVD, critical-regardless kept a hundred and eighty-six advisories
// about WordPress plugins and TOTOLINK routers, and a check that asks Tim about those every
// three hours is one he turns off inside a day.
func TestNVDIsReadForNamesAndNotForSeverity(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.serves(nvdURL, `{"totalResults": 1, "vulnerabilities": [{"cve": {"id": "CVE-2026-7",
	  "descriptions": [{"lang": "es", "value": "un fallo"}, {"lang": "en", "value": "An unauthenticated upload in a WordPress plugin."}],
	  "metrics": {"cvssMetricV40": [{"cvssData": {"baseScore": 9.8, "baseSeverity": "CRITICAL"}}]}}}]}`)

	harness.Equal(t, len(b.look().Leads), 0, "leads from a critical advisory naming nothing here")

	// A CVE published hours ago has `metrics: {}`, which is how the advisory this check was
	// written for arrived — so a score of nothing is a score of nothing.
	english := newBox(t)
	english.clean()
	english.serves(nvdURL, `{"totalResults": 1, "vulnerabilities": [{"cve": {"id": "CVE-2026-8",
	  "descriptions": [{"lang": "es", "value": "un fallo en curl"}, {"lang": "en", "value": "A flaw in curl."}],
	  "metrics": {}}}]}`)

	found := english.look()
	harness.Equal(t, found.lead(t, "CVE-2026-8").Title, "A flaw in curl.", "the title, in English")
	harness.Equal(t, found.lead(t, "CVE-2026-8").Severity, "", "a severity NVD has not stated yet")
}

// The reviewed advisories about an ecosystem on this Mac already reach it through OSV, which
// mirrors them; what is left is everything GitHub has ingested without curating.
func TestOnlyTheUnreviewedHalfOfGitHubsFeedIsRead(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.serves(ghFeedURL, `[{"ghsa_id": "GHSA-cwgc-9268-23h4", "cve_id": "CVE-2026-70387",
	  "summary": "A command injection in the tailscale client", "description": "the HAL exposes a binder method",
	  "severity": "high", "withdrawn_at": null, "html_url": "https://github.com/advisories/GHSA-cwgc-9268-23h4",
	  "cvss": {"score": 8.4}, "identifiers": [{"value": "CVE-2026-70387", "type": "CVE"}]}]`)

	found := b.look()

	harness.Equal(t, len(b.asked("type=unreviewed")), 1, "requests for the unreviewed half")
	harness.Equal(t, len(b.asked("published="+base.Add(-firstLook).Format("2006-01-02")+".."+
		base.Format("2006-01-02"))), 1, "the window it was asked for")

	l := found.lead(t, "GHSA-cwgc-9268-23h4")
	harness.Wants(t, l.Why, "it says tailscale, and tailscale is installed here")
	harness.Equal(t, l.Key(), "CVE-2026-70387", "the key, which is the lowest of its spellings")
}

// Read whole every time, because it is one file with no date filter, and the cursor is applied
// to each entry's own dateAdded.
func TestCISAEntriesAreFilteredByTheirOwnDateAdded(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.serves(kevURL, `{"title": "CISA Catalog of Known Exploited Vulnerabilities",
	  "catalogVersion": "2026.10.20", "count": 3, "vulnerabilities": [
	  {"cveID": "CVE-2026-106931", "vendorProject": "EG Secure Solutions", "product": "SiteGuard Server Edition",
	   "vulnerabilityName": "SiteGuard OS Command Injection Vulnerability", "dateAdded": "2026-10-21",
	   "shortDescription": "contains an OS command injection vulnerability", "knownRansomwareCampaignUse": "Unknown"},
	  {"cveID": "CVE-2026-100000", "vendorProject": "Acme", "product": "Router",
	   "vulnerabilityName": "Acme Router Authentication Bypass", "dateAdded": "2026-10-18",
	   "shortDescription": "allows an unauthenticated attacker in", "knownRansomwareCampaignUse": "Known"},
	  {"cveID": "CVE-2026-100001", "vendorProject": "Acme", "product": "Router",
	   "vulnerabilityName": "Acme Router Traversal", "dateAdded": "not a date",
	   "shortDescription": "allows a path traversal", "knownRansomwareCampaignUse": "Unknown"}]}`)

	found := b.look()

	harness.Equal(t, len(found.Leads), 1, "entries read, the others being older than the window or undated")
	harness.Equal(t, found.lead(t, "CVE-2026-106931").Title,
		"EG Secure Solutions SiteGuard Server Edition: SiteGuard OS Command Injection Vulnerability", "the title")
	harness.Equal(t, found.lead(t, "CVE-2026-106931").Severity, "exploited", "the severity, which is what the list means")

	// Being on that list means somebody is exploiting it now, which is a handful of entries a
	// week rather than hundreds a day — so one glance at an entry whose product is not
	// obviously here costs little and is occasionally the whole point.
	harness.Wants(t, found.lead(t, "CVE-2026-106931").Why,
		"it is critical, so it is read before anybody knows whether it is about this Mac")
	harness.Wants(t, found.lead(t, "CVE-2026-106931").Why,
		"CISA has it on the list of what is being exploited")
}

// bun publishes no security advisories — the endpoint answers with an empty array — and it is
// a runtime rather than a package, so OSV has no record of it either.
func TestBunsReleasesAreKeptOnlyOnSecurityWording(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.serves(bunURL, `[
	  {"tag_name": "bun-v1.4.3", "name": "Bun v1.4.3", "published_at": "2026-10-21T05:24:54Z",
	   "prerelease": false, "draft": false, "html_url": "https://github.com/oven-sh/bun/releases/tag/bun-v1.4.3",
	   "body": "This release fixes a security issue in the TLS stack."},
	  {"tag_name": "bun-v1.4.2", "name": "Bun v1.4.2", "published_at": "2026-10-21T04:00:00Z",
	   "prerelease": false, "draft": false, "html_url": "x",
	   "body": "To install Bun v1.4.2\n curl -fsSL https://bun.sh/install | bash"}]`)

	found := b.look()

	harness.Equal(t, len(found.Leads), 1, "leads, a release note about nothing of the sort being none")
	harness.Wants(t, found.lead(t, "bun-v1.4.3").Why, "its release notes say security, "+
		"and bun is the one runtime here with no advisory feed of its own")
	harness.Equal(t, found.lead(t, "bun-v1.4.3").Title, "Bun v1.4.3", "the title")

	// The fault this check was written for reached bun as a certificate store that had to
	// change rather than as a CVE.
	harness.Equal(t, securityWording("Updated the bundled root certificates"), "root certificate",
		"a root store change")
	harness.Equal(t, securityWording("Fixes CVE-2026-1 in the parser"), "cve", "a CVE named outright")
	harness.Equal(t, securityWording("Faster JSON parsing"), "", "a release about nothing of the sort")
}

// A canary is not a release anybody is on, and a draft is not published at all.
func TestAPrereleaseOrADraftOfBunsIsNotARelease(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.serves(bunURL, `[
	  {"tag_name": "bun-v1.4.4-canary.1", "name": "canary", "published_at": "2026-10-21T05:00:00Z",
	   "prerelease": true, "draft": false, "body": "fixes a security issue"},
	  {"tag_name": "bun-v1.4.5", "name": "draft", "published_at": "2026-10-21T06:00:00Z",
	   "prerelease": false, "draft": true, "body": "fixes a security issue"},
	  {"tag_name": "bun-v1.4.1", "name": "Bun v1.4.1", "published_at": "2026-10-01T05:00:00Z",
	   "prerelease": false, "draft": false, "body": "fixes a security issue"}]`)

	found := b.look()

	harness.Equal(t, len(found.Leads), 0, "leads, the only released one being older than the window")
	harness.Equal(t, found.Cursors[sourceBun], base.Unix(), "bun's cursor")
}

func TestAFeedAnsweringSomethingThatIsNotJSONLeavesItsCursorWhereItWas(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.serves(nvdURL, "<html>503</html>")
	b.serves(ghFeedURL, `{"message": "API rate limit exceeded"}`)
	b.serves(kevURL, `[]`)
	b.serves(bunURL, `{"message": "Not Found"}`)

	found := b.look()

	harness.Equal(t, len(found.Cursors), 0, "cursors written back")
	harness.Wants(t, found.troubles(), "NVD answered something that is not JSON")
	harness.Wants(t, found.troubles(), "GitHub's advisory feed answered something that is not JSON")
	harness.Wants(t, found.troubles(), "CISA's catalogue answered something that is not JSON")
	harness.Wants(t, found.troubles(), "bun's releases answered something that is not JSON")
}
