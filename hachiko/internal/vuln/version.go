package vuln

import (
	"strconv"
	"strings"
)

// Comparing two versions, for the one source that hands over a range rather than an answer:
// a repository's own security advisories say `<= 2026.9.18` and leave the comparison to
// whoever is asking. Every other source here compares for itself — OSV is asked about a
// version and answers about that version, Homebrew's scanner has already decided, and node's
// release index is in order — so this is deliberately not a general version library.
//
// Dot-separated segments, numeric where both sides are, which is what mise's calendar
// versions, Claude Code's, herdr's and node's all are. A leading `v` goes, and anything after
// a `-` or a `+` is a prerelease, which sorts below the release it is a prerelease of: that is
// semver's rule and also the only reading that is safe here, since calling a release
// candidate newer than the release would read a fixed Mac as affected.
func compare(a, b string) int {
	aNum, aPre := split(a)
	bNum, bPre := split(b)

	for i := 0; i < len(aNum) || i < len(bNum); i++ {
		if n := compareSegment(at(aNum, i), at(bNum, i)); n != 0 {
			return n
		}
	}

	switch {
	case aPre == bPre:
		return 0
	case aPre == "":
		return 1
	case bPre == "":
		return -1
	case aPre < bPre:
		return -1
	default:
		return 1
	}
}

func split(version string) (segments []string, prerelease string) {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")

	if cut := strings.IndexAny(version, "-+"); cut >= 0 {
		version, prerelease = version[:cut], version[cut+1:]
	}
	return strings.Split(version, "."), prerelease
}

func at(segments []string, i int) string {
	if i < len(segments) {
		return segments[i]
	}
	return "0"
}

// A segment missing from one side is a zero, so 1.27 and 1.27.0 are one version. Two
// segments that are both numbers compare as numbers, which is the whole point — "10" is
// above "9" and a string comparison says otherwise.
func compareSegment(a, b string) int {
	an, aErr := strconv.Atoi(a)
	bn, bErr := strconv.Atoi(b)

	switch {
	case aErr == nil && bErr == nil:
		switch {
		case an < bn:
			return -1
		case an > bn:
			return 1
		default:
			return 0
		}
	case a == b:
		return 0
	case a < b:
		return -1
	default:
		return 1
	}
}

// Whether an installed version falls in the range a GitHub advisory states, which is a
// comma-separated list of clauses that all have to hold: `>= 1.0.0, < 1.2.3`, `<= 2026.9.18`,
// `= 2.1.0`.
//
// The second value is whether the range could be read at all. A range in a shape this does
// not know is not a range saying the version is safe, so it is answered as unreadable and the
// advisory goes to the agent to read instead — the one thing that may not happen is a new
// spelling upstream turning into silence here.
func inRange(version, spec string) (affected, read bool) {
	clauses := strings.Split(spec, ",")
	if strings.TrimSpace(spec) == "" {
		return false, false
	}

	for _, clause := range clauses {
		// The operator and the version, with or without a space between them: GitHub's own
		// advisories are written both ways — `<= 2026.9.18` on one of mise's and `<2026.3.10`
		// on another — so the operator is read as the leading run of its own characters rather
		// than as a field.
		clause = strings.TrimSpace(clause)
		cut := strings.IndexFunc(clause, func(r rune) bool { return !strings.ContainsRune("<>=!", r) })
		if cut <= 0 {
			return false, false
		}

		op, want := clause[:cut], strings.TrimSpace(clause[cut:])
		if want == "" {
			return false, false
		}

		n := compare(version, want)
		switch op {
		case "<":
			affected = n < 0
		case "<=":
			affected = n <= 0
		case ">":
			affected = n > 0
		case ">=":
			affected = n >= 0
		case "=", "==":
			affected = n == 0
		default:
			return false, false
		}

		if !affected {
			return false, true
		}
	}
	return true, true
}

// The lowest version in a GitHub advisory's `patched_versions`, which is a comma-separated
// list of the versions each affected line was fixed in. The lowest, because that is the one
// an upgrade has to reach at least; the rest are later lines' fixes.
func firstPatched(patched string) string {
	lowest := ""

	for _, version := range strings.Split(patched, ",") {
		version = strings.TrimSpace(version)
		// A fix stated as a range rather than a version — GitHub allows both — is a fix whose
		// version this cannot name, and naming the wrong one would be a message telling Tim to
		// upgrade to something that does not exist.
		if version == "" || strings.ContainsAny(version, "<>= ") {
			continue
		}
		if lowest == "" || compare(version, lowest) < 0 {
			lowest = version
		}
	}
	return lowest
}
