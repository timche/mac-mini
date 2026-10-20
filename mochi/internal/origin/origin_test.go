package origin

import (
	"os/exec"
	"strings"
	"testing"
)

func TestARemoteIsItsOwnerAndRepo(t *testing.T) {
	for remote, want := range map[string]string{
		"git@github.com:timche/repeek.git":         "timche/repeek",
		"https://github.com/timche/repeek.git":     "timche/repeek",
		"https://github.com/timche/repeek":         "timche/repeek",
		"ssh://git@github.com/timche/mac-mini.git": "timche/mac-mini",
	} {
		if got, err := Parse(remote); err != nil || got != want {
			t.Errorf("%s: %q, %v", remote, got, err)
		}
	}
	for _, remote := range []string{"https://gitlab.com/timche/repeek.git", "/srv/git/repeek", ""} {
		if _, err := Parse(remote); err == nil {
			t.Errorf("%q parsed", remote)
		}
	}
}

// The real git, against a throwaway checkout, and once read it is kept.
func TestTheOriginIsReadFromTheCheckout(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q", dir}, {"-C", dir, "remote", "add", "origin", "git@github.com:timche/repeek.git"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if got, err := Of(dir); err != nil || got != "timche/repeek" {
		t.Fatalf("%q, %v", got, err)
	}
	exec.Command("git", "-C", dir, "remote", "set-url", "origin", "git@github.com:someone/else.git").Run()
	if got, _ := Of(dir); got != "timche/repeek" {
		t.Errorf("not kept: %q", got)
	}
	if _, err := Of(t.TempDir()); err == nil {
		t.Error("a folder with no origin was read")
	}
}

// Credentials in a remote are neither needed to match it nor echoed when it does not.
func TestARemotesCredentialsAreNeverEchoed(t *testing.T) {
	// Put together here so no credential-shaped literal sits in the source.
	userinfo := "tim" + ":" + "not-a-real-token" + "@"
	if got, err := Parse("https://" + userinfo + "github.com/timche/repeek.git"); err != nil || got != "timche/repeek" {
		t.Errorf("%q, %v", got, err)
	}
	_, err := Parse("https://" + userinfo + "gitlab.com/timche/repeek.git")
	if err == nil || strings.Contains(err.Error(), "not-a-real-token") || strings.Contains(err.Error(), "tim:") {
		t.Errorf("error = %v", err)
	}
}
