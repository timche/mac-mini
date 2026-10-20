package leaks

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fake(out string, exit int, err error) Runner {
	return func(context.Context, []string, io.Reader) ([]byte, int, error) { return []byte(out), exit, err }
}

func TestAFindingIsItsRuleAndWhereNeverTheSecret(t *testing.T) {
	found, err := Scanner{Run: fake(`[{"RuleID":"aws-access-token","File":"n.zip!note.txt","Secret":"REDACTED"}]`, 1, nil)}.File("n.zip")
	if err != nil || len(found) != 1 || found[0] != (Finding{RuleID: "aws-access-token", File: "n.zip!note.txt"}) {
		t.Errorf("found %+v, %v", found, err)
	}
}

func TestACleanScanFindsNothing(t *testing.T) {
	if found, err := (Scanner{Run: fake("[]\n", 0, nil)}).Text("hello"); err != nil || len(found) != 0 {
		t.Errorf("found %+v, %v", found, err)
	}
}

// Anything but a clean run or a report of findings is an error, so a post it could not check
// is not sent.
func TestTheScanFailsClosed(t *testing.T) {
	for name, run := range map[string]Runner{
		"missing":       fake("", 0, errors.New("betterleaks is not on PATH")),
		"timed out":     fake("", 0, errors.New("it took longer than 1m0s")),
		"crashed":       fake("panic", 2, nil),
		"not a report":  fake("garbage", 0, nil),
		"leak, no name": fake("[]", 1, nil),
	} {
		if _, err := (Scanner{Run: run}).Text("x"); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// The project's own configuration goes in by flag, and the session's variables pointing at
// another do not reach betterleaks.
func TestTheConfigIsTheProjectsAndNoVariableOverridesIt(t *testing.T) {
	var got []string
	run := func(_ context.Context, args []string, _ io.Reader) ([]byte, int, error) { got = args; return []byte("[]"), 0, nil }
	Scanner{Run: run, Config: "/repo/.gitleaks.toml"}.File("/repo/a.zip")
	if strings.Join(got, " ") != "dir --no-banner --redact --report-format json --report-path - --config /repo/.gitleaks.toml /repo/a.zip" {
		t.Errorf("args = %q", got)
	}
	env := scrub([]string{"PATH=/bin", "BETTERLEAKS_CONFIG=/x", "GITLEAKS_CONFIG_TOML=y", "HOME=/h"})
	if strings.Join(env, " ") != "PATH=/bin HOME=/h" {
		t.Errorf("env = %q", env)
	}
}

// A key shaped like AWS's, made at run time so the repository's own scan never sees one, is
// found in a file and inside a zip, with the secret redacted.
func TestTheRealScannerFindsAKeyInAFileAndAZip(t *testing.T) {
	if _, err := exec.LookPath("betterleaks"); err != nil {
		t.Skip("no betterleaks on PATH")
	}
	r := rand.New(rand.NewSource(7))
	pick := func(set string, n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = set[r.Intn(len(set))]
		}
		return string(b)
	}
	key := "AKIA" + pick("ABCDEFGHIJKLMNOPQRSTUVWXYZ234567", 16)
	secret := pick("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/+", 40)
	body := "aws_access_key_id = " + key + "\naws_secret_access_key = " + secret + "\n"

	dir := t.TempDir()
	note := filepath.Join(dir, "note.txt")
	os.WriteFile(note, []byte(body), 0o600)
	archive := filepath.Join(dir, "build.zip")
	f, _ := os.Create(archive)
	w := zip.NewWriter(f)
	inner, _ := w.Create("dist/config.txt")
	inner.Write([]byte(body))
	w.Close()
	f.Close()
	clean := filepath.Join(dir, "clean.txt")
	os.WriteFile(clean, []byte("nothing to see\n"), 0o600)

	s := Scanner{Run: CLI}
	for _, path := range []string{note, archive} {
		found, err := s.File(path)
		if err != nil || len(found) == 0 || found[0].RuleID == "" {
			t.Errorf("%s: found %+v, %v", path, found, err)
		}
	}
	if found, err := s.File(clean); err != nil || len(found) != 0 {
		t.Errorf("a clean file: %+v, %v", found, err)
	}
	if found, err := s.Text("the overlay works again"); err != nil || len(found) != 0 {
		t.Errorf("clean text: %+v, %v", found, err)
	}
	if found, err := s.Text(body); err != nil || len(found) == 0 {
		t.Errorf("text with a key: %+v, %v", found, err)
	}
}
