// Package harness is the test support every package here shares: the three assertions
// its tests are written in, and the fakes for the two things hachiko talks to that no
// test may actually reach. It is imported by _test.go files alone, so nothing in it
// reaches the binary.
package harness

import (
	"os"
	"strings"
	"testing"
)

func Wants(t *testing.T, text, want string) {
	t.Helper()
	if !strings.Contains(text, want) {
		t.Errorf("expected %q in:\n%s", want, text)
	}
}

func Lacks(t *testing.T, text, unwanted string) {
	t.Helper()
	if strings.Contains(text, unwanted) {
		t.Errorf("did not expect %q in:\n%s", unwanted, text)
	}
}

func Equal[T comparable](t *testing.T, got, want T, what string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v, want %v", what, got, want)
	}
}

func WriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
