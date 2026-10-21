package vuln

import (
	"strings"
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// The record the day this check was written was about. Its severity is in `database_specific`
// and its own `severity` array is absent, which is true of most of OSV; and its ranges carry
// two fixed lines at once, only one of which is above the version installed here.
const osvGoVulnJSON = `{
  "id": "GO-2026-6809",
  "summary": "Distrust compromised intermediate certificates in crypto/x509",
  "details": "On most platforms, Go uses the given system root certificates.",
  "aliases": ["CVE-2026-111943", "RUSTSEC-2026-0381"],
  "modified": "2026-10-21T00:55:56.763143468Z",
  "database_specific": {"url": "https://pkg.go.dev/vuln/GO-2026-6809", "review_status": "REVIEWED"},
  "affected": [{
    "package": {"name": "stdlib", "ecosystem": "Go", "purl": "pkg:golang/stdlib"},
    "ranges": [{"type": "SEMVER", "events": [
      {"introduced": "0"}, {"fixed": "1.26.10"}, {"introduced": "1.27.0-0"}, {"fixed": "1.27.3"}]}]
  }],
  "schema_version": "1.9.0"
}`

// The shape OSV-2025-433 against libssh2 has: a git range with an introduced commit and
// nothing after it, so the record names no version anything could be upgraded to.
const osvNoFixJSON = `{
  "id": "PYSEC-2026-4321",
  "summary": "Null-dereference READ in the certificate parser",
  "modified": "2025-06-05T00:02:57.200566Z",
  "affected": [{
    "package": {"name": "certifi", "ecosystem": "PyPI", "purl": "pkg:pypi/certifi"},
    "ranges": [{"type": "GIT", "repo": "https://github.com/certifi/python-certifi.git",
      "events": [{"introduced": "d34d9258b8420b19ec3f97b4cc5bf7aa7d98e35a"}]}],
    "ecosystem_specific": {"severity": null}
  }],
  "schema_version": "1.7.3"
}`

// The python mise manages and what pip says is installed in it, which is the one reading whose
// order is the lockfile's rather than a map's — so a positional answer can be pinned to it.
func (b *box) pipPackages(packages string) {
	b.says(miseLsHome, `{"python":[`+miseEntry("3.15.0", "/installs/python/3.15.0", true)+`]}`)
	b.says("/installs/python/3.15.0/bin/python3 -m pip list --format=json", packages)
}

func (b *box) goAt(toolchain string) {
	b.file(repo+"/hachiko/go.mod", "module hachiko\n\ngo "+toolchain+"\n")
	b.file(repo+"/mochi/go.mod", "module mochi\n")
}

// A batch carrying one ecosystem OSV does not know is refused whole with a 400, so every other
// query in it is lost too.
func TestEachEcosystemIsAskedInABatchOfItsOwn(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.goAt("1.27.0")
	b.pipPackages(`[{"name": "certifi", "version": "2026.9.1"}]`)
	b.file(repo+"/shibuya/package-lock.json",
		`{"packages": {"": {"name": "shibuya"}, "node_modules/wrangler": {"version": "4.147.0"}}}`)
	b.serves(osvBatchURL, `{"results":[{},{}]}`)
	b.thenServes(osvBatchURL, `{"results":[{}]}`)
	b.thenServes(osvBatchURL, `{"results":[{}]}`)

	found := b.look()

	harness.Equal(t, len(b.posts), 3, "batches posted")
	harness.Wants(t, b.posts[0].body, `"ecosystem":"Go"`)
	harness.Wants(t, b.posts[1].body, `{"package":{"name":"certifi","ecosystem":"PyPI"},"version":"2026.9.1"}`)
	harness.Wants(t, b.posts[2].body, `{"package":{"name":"wrangler","ecosystem":"npm"},"version":"4.147.0"}`)
	harness.Equal(t, found.troubles(), "", "what could not be read")

	quiet := newBox(t)
	quiet.clean()
	quiet.look()
	harness.Equal(t, len(quiet.posts), 0, "batches posted about an ecosystem with nothing installed")
}

// The results come back in the order the queries went out, and an entry with no vulns is a
// package with nothing against it.
func TestACleanPackageComesBackAsAnEmptyResultAndNothingIsReadFromIt(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.pipPackages(`[{"name": "pip", "version": "26.2.2"}, {"name": "certifi", "version": "2026.9.1"}]`)
	b.serves(osvBatchURL, `{"results":[{},{"vulns":[{"id":"PYSEC-2026-4320","modified":"2026-10-21T01:00:00Z"}]}]}`)
	b.serves(osvVulnURL+"PYSEC-2026-4320", `{"id": "PYSEC-2026-4320",
		"summary": "certifi bundles emSign root CAs with compromised intermediate CA keys",
		"aliases": ["GHSA-6q48-6c7w-9gw2"], "database_specific": {"severity": "HIGH"},
		"affected": [{"package": {"name": "certifi", "ecosystem": "PyPI"},
			"ranges": [{"type": "ECOSYSTEM", "events": [{"introduced": "0"}, {"fixed": "2026.10.20"}]}]}]}`)

	found := b.look()
	e := found.exposure(t, "PYSEC-2026-4320")

	harness.Equal(t, len(found.Exposures), 1, "exposures, the first package being clean")
	harness.Equal(t, e.Name, "certifi", "the package the second result belongs to")
	harness.Equal(t, e.Version, "2026.9.1", "what is installed")
	harness.Equal(t, e.Fixed, "2026.10.20", "the fix")
	harness.Equal(t, e.Target, "pip:3.15.0:certifi", "the target")
	harness.Equal(t, len(b.asked(osvVulnURL)), 1, "records read in full")

	// OSV's own `severity` array is a CVSS vector and is absent from most records, so the field
	// that actually answers is the one GHSA records keep the word in.
	harness.Equal(t, e.Severity, "HIGH", "the severity, out of database_specific")
	harness.Equal(t, e.Summary, "certifi bundles emSign root CAs with compromised intermediate CA keys",
		"the summary")
}

func TestABatchOSVWouldNotAnswerIsSaidAndNamesTheEcosystem(t *testing.T) {
	// Pairing the wrong result with a package would name the wrong thing as vulnerable.
	t.Run("it answered about fewer than it was asked", func(t *testing.T) {
		b := newBox(t)
		b.clean()
		b.pipPackages(`[{"name": "pip", "version": "26.2.2"}, {"name": "certifi", "version": "2026.9.1"}]`)
		b.serves(osvBatchURL, `{"results":[{"vulns":[{"id":"PYSEC-2026-4320"}]}]}`)

		found := b.look()

		harness.Wants(t, found.troubles(),
			"OSV answered about 1 of the 2 PyPI packages it was asked about, so none of them was read")
		harness.Equal(t, len(found.Exposures), 0, "exposures")
		harness.Equal(t, len(b.asked(osvVulnURL)), 0, "records read in full")
	})

	t.Run("it did not answer", func(t *testing.T) {
		b := newBox(t)
		b.clean()
		b.pipPackages(`[{"name": "certifi", "version": "2026.9.1"}]`)
		b.refuses(osvBatchURL, "it answered 400: invalid ecosystem")

		harness.Wants(t, b.look().troubles(),
			"OSV could not be reached about PyPI: it answered 400: invalid ecosystem")
	})

	t.Run("it answered something else", func(t *testing.T) {
		b := newBox(t)
		b.clean()
		b.pipPackages(`[{"name": "certifi", "version": "2026.9.1"}]`)
		b.serves(osvBatchURL, "<html>502 Bad Gateway</html>")

		harness.Wants(t, b.look().troubles(), "OSV answered something that is not JSON about PyPI")
	})
}

// A record covers several packages and several lines at once, and the fix for another line is
// not a version to tell anybody to upgrade to.
func TestTheFixIsTheLowestVersionAboveWhatIsInstalledForThisVeryPackage(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.goAt("1.27.0")
	b.serves(osvBatchURL, `{"results":[{"vulns":[{"id":"GO-2026-6809"}]},{}]}`)
	b.serves(osvVulnURL+"GO-2026-6809", osvGoVulnJSON)

	e := b.look().exposure(t, "GO-2026-6809")

	harness.Equal(t, e.Name, "stdlib", "what it is about")
	harness.Equal(t, e.Fixed, "1.27.3", "the fix for the line this Mac is on")
	harness.Equal(t, strings.Join(e.Aliases, " "), "CVE-2026-111943 RUSTSEC-2026-0381", "its other spellings")

	// A record covers several packages at once, and another package's fix is no version to
	// tell anybody to upgrade to.
	other := newBox(t)
	other.clean()
	other.pipPackages(`[{"name": "certifi", "version": "2026.9.1"}]`)
	other.serves(osvBatchURL, `{"results":[{"vulns":[{"id":"GHSA-x"}]}]}`)
	other.serves(osvVulnURL+"GHSA-x", `{"id": "GHSA-x", "summary": "a shared root bundle",
		"affected": [
			{"package": {"name": "certifi", "ecosystem": "npm"},
			 "ranges": [{"events": [{"introduced": "0"}, {"fixed": "2026.9.2"}]}]},
			{"package": {"name": "requests", "ecosystem": "PyPI"},
			 "ranges": [{"events": [{"introduced": "0"}, {"fixed": "2026.9.3"}]}]},
			{"package": {"name": "certifi", "ecosystem": "PyPI"},
			 "ranges": [{"events": [{"introduced": "0"}, {"fixed": "2026.10.20"}]}]}]}`)

	harness.Equal(t, other.look().exposure(t, "GHSA-x").Fixed, "2026.10.20", "the fix for certifi on PyPI")
}

// There is nothing to hand the update even where the thing is one the update moves.
func TestARecordWithOnlyAnIntroducedEventHasNothingToUpgradeTo(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.pipPackages(`[{"name": "certifi", "version": "2026.9.1"}]`)
	b.serves(osvBatchURL, `{"results":[{"vulns":[{"id":"PYSEC-2026-4321"}]}]}`)
	b.serves(osvVulnURL+"PYSEC-2026-4321", osvNoFixJSON)

	found := b.look()
	e := found.exposure(t, "PYSEC-2026-4321")

	harness.Equal(t, e.Fixed, "", "a fixed version")
	harness.Equal(t, e.Target, "", "the target, pip being something the update does move")
	harness.Wants(t, e.Needs, "Nothing is released that fixes it")
	harness.Equal(t, len(found.Targets(nil)), 0, "targets")
}

// A module bump is a line in a file somebody means to change, with a commit behind it and
// tests to run, and shibuya's packages are a deploy to Cloudflare.
func TestWhatNothingHereMovesCarriesTheOneLineForTimInsteadOfATarget(t *testing.T) {
	harness.Wants(t, needsFor(Installed{From: fromGo, Name: "stdlib"}, "1.27.3"),
		"mise up -C ~/.mac-mini --local go")
	harness.Wants(t, needsFor(Installed{From: fromGo, Name: "golang.org/x/crypto", Where: "mochi"}, "v0.45.0"),
		"mochi/go.mod")
	harness.Wants(t, needsFor(Installed{From: fromNPM, Name: "wrangler"}, "4.148.0"),
		"a lockfile change and a deploy to Cloudflare")
	harness.Wants(t, needsFor(Installed{From: fromTool, Name: "claude-code"}, "2.1.41"),
		"Claude Code updates itself on the default channel")
	harness.Wants(t, needsFor(Installed{From: fromBrew, Name: "aria2"}, ""),
		"Nothing is released that fixes it")
}

func TestARecordThatCouldNotBeReadIsSaidAndTheRestAreStillRead(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.pipPackages(`[{"name": "certifi", "version": "2026.9.1"}]`)
	b.serves(osvBatchURL, `{"results":[{"vulns":[{"id":"PYSEC-1"},{"id":"PYSEC-2"}]}]}`)
	b.refuses(osvVulnURL+"PYSEC-1", "it answered 429: rate limit exceeded")
	b.serves(osvVulnURL+"PYSEC-2", `{"id": "PYSEC-2", "summary": "a second fault",
		"affected": [{"package": {"name": "certifi", "ecosystem": "PyPI"},
			"ranges": [{"events": [{"fixed": "2026.10.20"}]}]}]}`)

	found := b.look()

	harness.Wants(t, found.troubles(), "OSV would not say what PYSEC-1 is: it answered 429: rate limit exceeded")
	harness.Equal(t, len(found.Exposures), 1, "exposures")
	harness.Equal(t, found.exposure(t, "PYSEC-2").Fixed, "2026.10.20", "the one that was read")

	// Every id arrives from the network and goes into a URL, so a record named with a slash or
	// a dot-dot would be a request to somewhere else on the same host.
	named := newBox(t)
	named.clean()
	named.pipPackages(`[{"name": "certifi", "version": "2026.9.1"}]`)
	named.serves(osvBatchURL, `{"results":[{"vulns":[{"id":"../../etc/passwd"}]}]}`)

	harness.Wants(t, named.look().troubles(), `"../../etc/passwd" is not an advisory id`)
	harness.Equal(t, len(named.asked("api.osv.dev")), 0, "requests to OSV for a record")
}

// A module that has not moved in four years comes back with dozens of records, and every one
// of them is a request of its own.
func TestPastTheRecordLimitTheRestAreSaidToBeUnreadRatherThanDroppedInSilence(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.pipPackages(`[{"name": "certifi", "version": "2026.9.1"}]`)

	var hits []string
	for i := 0; i < osvDetailLimit+3; i++ {
		id := "PYSEC-2026-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		hits = append(hits, `{"id":"`+id+`"}`)
		b.serves(osvVulnURL+id, `{"id": "`+id+`", "summary": "one of many"}`)
	}
	b.serves(osvBatchURL, `{"results":[{"vulns":[`+strings.Join(hits, ",")+`]}]}`)

	found := b.look()

	harness.Equal(t, len(b.asked(osvVulnURL)), osvDetailLimit, "records read in full")
	harness.Wants(t, found.troubles(), "OSV had 3 records against PyPI packages that this pass did not read")
}
