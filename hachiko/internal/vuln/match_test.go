package vuln

import (
	"sort"
	"strings"
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// Without the boundary a feed matcher keeps an advisory about anything whose name happens to
// hold one of these as a syllable, which on a day of NVD is most of them.
func TestATermIsMatchedAsAWordAndNotAsPartOfOne(t *testing.T) {
	for _, text := range []string{"sshd would not restart", "openssh 10.2", "a flush of the cache", "pushstate was called"} {
		harness.Equal(t, mentions(text, "ssh"), false, "ssh in "+text)
	}
	harness.Equal(t, mentions("golang http client", "go"), false, "go in golang")
	harness.Equal(t, mentions("curl before 8.18 mishandles a redirect", "curl"), true, "curl as a word")
	harness.Equal(t, mentions("libcurl and curl share the parser", "libcurl"), true, "libcurl as a word")

	// Off either end of the text is not a word byte, which is what makes a term at the very
	// start or end of it a match.
	harness.Equal(t, mentions("curl is affected", "curl"), true, "at the start")
	harness.Equal(t, mentions("the fault is in curl", "curl"), true, "at the end")
	harness.Equal(t, mentions("curl", "curl"), true, "the whole of it")
	harness.Equal(t, mentions("anything at all", ""), false, "no term")
}

// certifi's own summary is written this way: "bundles emSign root CAs with compromised
// intermediate CA keys", and the singular is the term.
func TestATrailingPluralIsStillTheTerm(t *testing.T) {
	harness.Equal(t, mentions("bundles emSign root CAs with compromised intermediate CA keys", "root ca"), true,
		"root CAs")
	harness.Equal(t, mentions("distrust compromised intermediate certificates in crypto/x509", "intermediate certificate"),
		true, "intermediate certificates")
	harness.Equal(t, mentions("two certificate authorities were removed", "certificate authority"), false,
		"a plural that is not a trailing s")
}

// Measured against a real day of NVD: the ecosystem words matched a hundred and twenty
// advisories about packages written in them rather than about the thing, because NVD's
// descriptions name the ecosystem of whatever they are about.
func TestTheEcosystemWordsAreNoTermsAndTheFormulaeBrewSkippedAre(t *testing.T) {
	watched := terms(Inventory{}, []string{"sqlite", "readline", "ca-certificates", "dav1d", "lame", "jq", "lz4", "curl"})
	spelt := spellings(watched)

	for _, word := range []string{"node", "python", "pip", "npm", "golang", "chrome"} {
		harness.Equal(t, contains(spelt, word), false, word+" is a term")
	}

	for _, formula := range []string{"sqlite", "readline", "ca-certificates", "dav1d", "lame"} {
		harness.Equal(t, watched[formula] != nil, true, formula+" is watched")
	}

	// Three letters and under are words, and a matcher given one keeps everything.
	for _, formula := range []string{"jq", "lz4"} {
		harness.Equal(t, watched[formula] == nil, true, formula+" is left out")
	}

	harness.Equal(t, strings.Join(watched["curl"], " "), "curl libcurl",
		"a skipped formula the aliases already name keeps its own spellings")
	harness.Equal(t, strings.Join(watched["the Go standard library"], " "), "go standard library crypto/x509",
		"the term the Go release note for the distrust actually said")
}

func TestAnAdvisoryIsKeptForANameForWhatThisMacTrustsOrForBeingCritical(t *testing.T) {
	watched := terms(Inventory{}, []string{"sqlite"})

	harness.Wants(t, why("CVE-2026-71832 curl mishandles a redirect", 0, watched, namedOnly),
		"it says curl, and curl is installed here")
	harness.Wants(t, why("GHSA-x a write past the end of a sqlite page", 0, watched, namedOnly),
		"it says sqlite, and sqlite is installed here")
	harness.Wants(t, why("bundles emSign root CAs with compromised intermediate CA keys", 0, watched, namedOnly),
		"it says root ca, and what this Mac trusts is shared by curl, python and every agent on it")

	// The reason a message gives has to be the same on every pass rather than whichever of the
	// names it mentions the map handed over first.
	harness.Wants(t, why("a flaw in curl and in git and in sqlite", 0, watched, namedOnly), "it says curl")

	harness.Equal(t, why("an unauthenticated upload in a TOTOLINK router", 9.8, watched, namedOnly), "",
		"a critical advisory from a source severity alone keeps nothing from")
	harness.Wants(t, why("an unauthenticated upload in a TOTOLINK router", 9.8, watched, anySeverity),
		"it is critical, so it is read before anybody knows whether it is about this Mac")
	harness.Equal(t, why("an unauthenticated upload in a TOTOLINK router", 8.8, watched, anySeverity), "",
		"a high one from the same source")
}

func spellings(watched map[string][]string) []string {
	var out []string
	for _, terms := range watched {
		out = append(out, terms...)
	}
	sort.Strings(out)
	return out
}
