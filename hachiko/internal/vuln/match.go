package vuln

import (
	"regexp"
	"sort"
	"strings"
)

// An advisory id as every source here spells one: `CVE-2026-71832`, `GHSA-6q48-6c7w-9gw2`,
// `OSV-2025-433`, `PYSEC-2026-4320`, `GO-2026-6809`, `RUSTSEC-2026-0381`. Held to this shape
// wherever one goes into a URL or a command line, because every one of them arrives from the
// network: an id with a slash in it would be a request to somewhere else on the same host.
var advisoryID = regexp.MustCompile(`\A[A-Za-z][A-Za-z0-9]*(-[A-Za-z0-9]+)+\z`)

// What an advisory would call something on this Mac that no exact source covers. The list is
// written out rather than derived from the inventory, and it is short on purpose: what the
// feeds are for is the gap the exact sources leave, and every name beyond that gap is a day of
// NVD in front of Tim.
//
// Measured against a real day of it, 1,959 CVEs. An inventory-derived list kept a hundred and
// twenty of them, and almost none was about anything here: NVD's descriptions name the
// ecosystem of the package they are about — "follow-redirects is a Node.js HTTP client",
// "filelock is a file lock for Python" — so `node`, `python`, `pip`, `npm` and `golang` match
// every advisory about every package written in them. Those are the five ecosystems with an
// exact source of their own: node's release index says what a security release was, and OSV
// answers about the npm, PyPI and Go versions really installed. So they are not here.
//
// Nor is `chrome`, for the opposite reason: a Chrome release day is thirty CVEs that Chrome
// has already fixed and updates itself to, so they would arrive as a wall of advisories about
// a browser that is already past them.
//
// What is left is what nothing else can see. Apple's own `/usr/bin/curl` and system python
// read `/etc/ssl/cert.pem` and are in no package index at all, which is exactly how the
// eMudhra distrust reached this Mac; bun has no advisory feed; `cpython` names the
// interpreter rather than the ecosystem; and `crypto/x509` is what the Go release note for
// that distrust actually said, having never said eMudhra, emSign or certificate authority.
var aliases = map[string][]string{
	"curl":                    {"curl", "libcurl"},
	"openssl":                 {"openssl"},
	"libressl":                {"libressl"},
	"macos":                   {"macos", "mac os", "os x"},
	"bun":                     {"bun"},
	"python":                  {"cpython"},
	"the Go standard library": {"go standard library", "crypto/x509"},
	"git":                     {"git"},
	"openssh":                 {"openssh", "ssh"},
	"tailscale":               {"tailscale"},
	"docker":                  {"docker", "orbstack"},
	"homebrew":                {"homebrew"},
	"mise":                    {"mise"},
	"herdr":                   {"herdr"},
	"claude code":             {"claude code", "claude-code"},
}

// The wording of a fault in what a machine decides to trust, which is the other half of why
// this check exists: the advisory that started it is not about a version at all. certifi's
// says "bundles emSign root CAs with compromised intermediate CA keys" and Go's says
// "distrust compromised intermediate certificates in crypto/x509", and a name matcher catches
// neither unless it happens to know the word certifi.
//
// Kept whatever it names, because a trust store is shared: `/etc/ssl/cert.pem` is what
// `/usr/bin/curl` and the system python read, and a root nobody on this Mac installed is
// still a root this Mac trusts.
var trustTerms = []string{
	"certificate authority",
	"certificate authorities",
	"root ca",
	"root certificate",
	"intermediate ca",
	"intermediate certificate",
	"trust store",
	"ca bundle",
	"certificate chain",
	"distrust",
}

// The CVSS score at which an advisory is kept whatever it names, and the sources that rule
// applies to. Not NVD's and not GitHub's unreviewed feed, which are every CVE there is:
// measured against a day of NVD, critical-regardless kept a hundred and eighty-six advisories
// about WordPress plugins and TOTOLINK routers, and a check that asks Tim about those every
// three hours is one he turns off inside a day. Those two are read for names and for what this
// Mac trusts, which is what they are good for.
//
// CISA's catalogue is the one it does apply to, and the reason is what being on that list
// means: somebody is exploiting it now. That is a handful of entries a week rather than
// hundreds a day, so one glance at a critical entry whose product is not obviously here costs
// little and is occasionally the whole point.
const criticalScore = 9.0

// Whether severity alone is enough to keep an advisory from this source. See criticalScore.
const (
	anySeverity = true
	namedOnly   = false
)

// How short a name may be before it is left out. Three letters and under are words — `jq`,
// `go`, `lz4`, `icu` — and a feed matcher given one keeps everything; the short names that are
// genuinely worth watching are written out in the aliases above.
const shortestName = 4

// The terms a published advisory is read for: the aliases above, and the Homebrew formulae
// Homebrew's own scanner could not read. Those are the gap in the one exact source that covers
// formulae — `brew vulns` matches a bottle's bill of materials against Homebrew's advisory
// data, and a formula whose bottle carries no such bill is one it skips and says it skipped.
// Eight of this Mac's thirty-eight were in that state the day this was written, `sqlite`,
// `readline` and `ca-certificates` among them.
//
// Nothing else of the inventory. Every other formula is scanned exactly; shibuya's lockfile
// pins two hundred transitive npm packages called things like `ms` and `debug`; the compiled
// tools' requirements are Go import paths; and all of those are matched exactly by OSV, which
// is the whole reason they are in the inventory at all. A name matcher given them would put
// every advisory mentioning a millisecond in front of Tim.
func terms(inv Inventory, unscanned []string) map[string][]string {
	watched := map[string][]string{}
	for what, spellings := range aliases {
		watched[what] = spellings
	}

	for _, formula := range unscanned {
		name := strings.ToLower(formula)
		if len(name) < shortestName || watched[name] != nil {
			continue
		}
		watched[name] = []string{name}
	}
	return watched
}

// Why an advisory was kept, or the empty string for one that was not. Three rules, in the
// order they are read: it names something that is here, it is about what this Mac trusts, or —
// from a source where that means something — it is bad enough to read before anybody knows
// what it is about.
func why(text string, score float64, watched map[string][]string, onSeverity bool) string {
	for _, what := range sortedTerms(watched) {
		for _, term := range watched[what] {
			if mentions(text, term) {
				return "it says " + term + ", and " + what + " is installed here"
			}
		}
	}

	for _, term := range trustTerms {
		if mentions(text, term) {
			return "it says " + term + ", and what this Mac trusts is shared by curl, python and every agent on it"
		}
	}

	if onSeverity && score >= criticalScore {
		return "it is critical, so it is read before anybody knows whether it is about this Mac"
	}
	return ""
}

func sortedTerms(watched map[string][]string) []string {
	out := make([]string, 0, len(watched))
	for what := range watched {
		out = append(out, what)
	}
	// Stable, so the reason a message gives for keeping one advisory is the same on every pass
	// rather than whichever name the map happened to hand over first.
	sort.Strings(out)
	return out
}

// Whether the text says the term as a word rather than as part of one. Without the boundary,
// `ssh` is in `sshd`, `openssh` and `libssh2`, `curl` is in `curlopt`, and `git` is in
// `gitops` and `digit`; with it, a term is matched only where what sits either side of it is
// not a letter or a digit.
//
// A trailing `s` is allowed on the last word of the term, because an advisory says "root CAs"
// and "intermediate certificates" as readily as the singular — which is exactly how certifi's
// own summary is written.
func mentions(text, term string) bool {
	body, want := strings.ToLower(text), strings.ToLower(term)
	if want == "" {
		return false
	}

	for at := 0; ; {
		cut := strings.Index(body[at:], want)
		if cut < 0 {
			return false
		}
		start := at + cut
		end := start + len(want)

		if end < len(body) && (body[end] == 's' || body[end] == 'S') {
			end++
		}
		if !wordByte(body, start-1) && !wordByte(body, end) {
			return true
		}
		at = start + 1
	}
}

// Whether the byte at that offset is one a word is made of. Off either end of the string is
// not, which is what makes a term at the very start or end of the text a match.
func wordByte(body string, at int) bool {
	if at < 0 || at >= len(body) {
		return false
	}
	c := body[at]
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
