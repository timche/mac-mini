package sync

import (
	"fmt"
	"testing"
)

func names(n int) []string {
	var out []string
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("f%d.md", i))
	}
	return out
}

func TestTheCommitSubject(t *testing.T) {
	for _, c := range []struct {
		files []string
		want  string
	}{
		{names(1), "Update f0.md"},
		{names(2), "Update f0.md, f1.md"},
		{names(3), "Update f0.md, f1.md, f2.md"},
		{names(4), "Update f0.md, f1.md, f2.md and 1 more"},
		{names(10), "Update f0.md, f1.md, f2.md and 7 more"},

		// Paths as git gave them: not capitalised, not reordered, not shortened to a base name.
		{[]string{"docs/z.md", "a.md"}, "Update docs/z.md, a.md"},
	} {
		if got := commitSubject(c.files); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

// `-z` hands a path over as the bytes it is rather than in git's quoted spelling, so a
// newline in one would otherwise be a subject that is two lines and the second of them a
// commit body.
func TestAPathWithANewlineStaysOneSubject(t *testing.T) {
	if got := commitSubject([]string{"a\nb.md"}); got != "Update a b.md" {
		t.Errorf("got %q", got)
	}
}
