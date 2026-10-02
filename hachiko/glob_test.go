package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A regression: a shell `case` pattern follows the locale's collation, where an
// uppercase letter falls inside [a-z] — so an allowlist meant for lowercase names
// silenced processes it was never about. A regexp over bytes is the range it spells.
func TestACharacterClassIsTheRangeItSpellsAndNotTheLocalesIdeaOfIt(t *testing.T) {
	if globMatch("[a-z]*", "Node") {
		t.Error("[a-z] matched an uppercase letter")
	}
	if !globMatch("[a-z]*", "node") {
		t.Error("[a-z] did not match a lowercase letter")
	}
	if !globMatch("[!a-z]*", "Node") {
		t.Error("a negated class did not match")
	}
}

// path.Match's `*` stops at a separator, and the allowlist names whole paths.
func TestAStarCrossesPathSeparators(t *testing.T) {
	if !globMatch("*/OrbStack.app/*", "/Applications/OrbStack.app/Contents/MacOS/OrbStack Helper") {
		t.Error("a path pattern did not match a path several directories deep")
	}
	if !globMatch("qemu-system-*", "qemu-system-aarch64") {
		t.Error("a prefix pattern did not match")
	}
	if globMatch("mds", "mds_stores") {
		t.Error("a pattern matched more than the whole name")
	}
}

func TestTheAllowlistSkipsBlankLinesAndReasons(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cpu-allow")
	contents := "# the docker VM\n\nOrbStack\n*/OrbStack.app/*\n  mds  \n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}

	patterns := readAllowlist(path)
	equal(t, len(patterns), 3, "patterns read")

	if !allowed(patterns, "/usr/libexec/mds", "mds") {
		t.Error("a name in the allowlist was not allowed")
	}
	if !allowed(patterns, "/Applications/OrbStack.app/Contents/MacOS/x", "x") {
		t.Error("a path in the allowlist was not allowed")
	}
	if allowed(patterns, "/usr/local/bin/node", "node") {
		t.Error("something not in the allowlist was allowed")
	}
}

func TestAMissingAllowlistAllowsNothing(t *testing.T) {
	patterns := readAllowlist(filepath.Join(t.TempDir(), "absent"))
	if allowed(patterns, "/usr/local/bin/node", "node") {
		t.Error("a missing allowlist allowed a process")
	}
}
