package origin

import (
	"os"
	"os/exec"
	"path/filepath"
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
	if _, err := Parse("https://" + userinfo + "gitlab.com/timche/repeek.git"); err != ErrNotGitHub {
		t.Errorf("error = %v", err)
	}

	// A remote whose credentials are too malformed to strip is still never named: the error is
	// about the checkout alone.
	dir := t.TempDir()
	malformed := "https://tim:" + "not-a-real" + "@token@" + "gitlab.com/timche/repeek.git"
	for _, args := range [][]string{{"init", "-q", dir}, {"-C", dir, "remote", "add", "origin", malformed}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	_, err := Of(dir)
	if err == nil || err.Error() != "the origin of "+dir+" is not a GitHub remote" || strings.Contains(err.Error(), "not-a-real") {
		t.Errorf("error = %v", err)
	}
}

func checkout(t *testing.T, remote string) string {
	t.Helper()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	for _, args := range [][]string{{"init", "-q", dir}, {"-C", dir, "remote", "add", "origin", remote}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return dir
}

// A folder deep in a checkout is matched by the checkout's origin, whatever its path, to the
// mapped repository with the same one; any other checkout, or none, matches nothing.
func TestACheckoutIsMatchedToTheRepositoryWithItsOrigin(t *testing.T) {
	mapped := []string{checkout(t, "git@github.com:timche/mac-mini.git"), checkout(t, "git@github.com:timche/repeek.git")}

	clone := checkout(t, "https://github.com/Timche/Repeek")
	deep := filepath.Join(clone, "src", "app")
	os.MkdirAll(deep, 0o755)
	root, i, err := Match(deep, mapped)
	if err != nil || i != 1 || root != clone {
		t.Errorf("%q, %d, %v", root, i, err)
	}

	if _, _, err := Match(checkout(t, "git@github.com:someone/else.git"), mapped); err == nil || !strings.Contains(err.Error(), "someone/else") {
		t.Errorf("another repository: %v", err)
	}
	if _, _, err := Match(t.TempDir(), mapped); err == nil {
		t.Error("a folder in no checkout matched")
	}
	if _, _, err := Match(checkout(t, "https://gitlab.com/timche/repeek.git"), mapped); err == nil {
		t.Error("a checkout with no GitHub origin matched")
	}
}
