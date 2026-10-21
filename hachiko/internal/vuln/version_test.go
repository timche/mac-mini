package vuln

import (
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// A string comparison says 1.9 is above 1.10, and a missing segment read as anything but a
// zero makes 1.27 and 1.27.0 two versions.
func TestASegmentIsComparedAsANumberAndOneThatIsMissingIsAZero(t *testing.T) {
	harness.Equal(t, compare("1.10", "1.9"), 1, "1.10 against 1.9")
	harness.Equal(t, compare("1.9", "1.10"), -1, "1.9 against 1.10")
	harness.Equal(t, compare("2026.10.0", "2026.9.18"), 1, "mise's calendar versions")
	harness.Equal(t, compare("24.22.1", "24.22.1"), 0, "a version against itself")
	harness.Equal(t, compare("1.27", "1.27.0"), 0, "1.27 against 1.27.0")
	harness.Equal(t, compare("1.27.3", "1.27"), 1, "1.27.3 against 1.27")
	harness.Equal(t, compare("v24.22.1", "24.22.1"), 0, "a leading v")
	harness.Equal(t, major("v24.22.1"), "24", "the line a release belongs to")
}

// Reading a release candidate as newer than the release would have a Mac on the release read
// as affected by what the release fixed.
func TestAPrereleaseIsBelowTheReleaseItIsAPrereleaseOf(t *testing.T) {
	harness.Equal(t, compare("1.27.0-0", "1.27.0"), -1, "OSV's own lower bound for a major")
	harness.Equal(t, compare("1.4.3-canary.1", "1.4.3"), -1, "a canary against its release")
	harness.Equal(t, compare("1.4.3", "1.4.3+build7"), 1, "a release against a build of it")
	harness.Equal(t, compare("1.4.3-rc.1", "1.4.3-rc.2"), -1, "two prereleases")
}

func TestARangeHoldsOnlyWhereEveryOneOfItsClausesDoes(t *testing.T) {
	for _, c := range []struct {
		version, spec string
		affected      bool
	}{
		{"2.1.100", ">= 2.0.68, < 2.1.260", true},
		{"2.1.260", ">= 2.0.68, < 2.1.260", false},
		{"2.0.1", ">= 2.0.68, < 2.1.260", false},
		{"2026.9.18", "<= 2026.9.18", true},
		{"2026.9.19", "<= 2026.9.18", false},
		{"2026.2.1", "<2026.3.10", true},
		{"2026.3.10", "<2026.3.10", false},
		{"2.1.0", "= 2.1.0", true},
		{"2.1.1", "= 2.1.0", false},
		{"2026.10.15", ">= 2025.9.13", true},
	} {
		affected, read := inRange(c.version, c.spec)
		harness.Equal(t, read, true, c.spec+" could be read")
		harness.Equal(t, affected, c.affected, c.version+" against "+c.spec)
	}
}

// mise has published a range as a bare `v2026.9.6` with no operator at all, and the one thing
// that may not happen is a shape nobody anticipated turning into silence.
func TestARangeInAShapeThisCannotReadIsNotOneSayingTheVersionIsSafe(t *testing.T) {
	for _, spec := range []string{"v2026.9.6", "2026.9.6", "", "   ", ">=", ">= ", "~> 1.2", ">< 1.2"} {
		affected, read := inRange("2026.9.5", spec)
		harness.Equal(t, read, false, "whether "+spec+" could be read")
		harness.Equal(t, affected, false, "whether "+spec+" was taken as affected")
	}
}

// The lowest is the one an upgrade has to reach at least, the rest being later lines' fixes —
// and GitHub allows a fix stated as a range, which names no version to upgrade to at all.
func TestTheLowestPatchedVersionIsTheOneAnUpgradeHasToReach(t *testing.T) {
	harness.Equal(t, firstPatched("2026.9.18, 2026.10.0"), "2026.9.18", "two fixed lines")
	harness.Equal(t, firstPatched("2026.10.0"), "2026.10.0", "one")
	harness.Equal(t, firstPatched(""), "", "none")
	harness.Equal(t, firstPatched(">= 2026.10.0"), "", "a range on its own")
	harness.Equal(t, firstPatched(">= 2026.10.0, 2026.9.18"), "2026.9.18", "a range beside a version")
}
