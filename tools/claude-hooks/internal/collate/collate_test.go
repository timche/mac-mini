package collate

import (
	"sort"
	"strings"
	"testing"
)

// Each case is an order `sort` really put these names in, in a session's locale, with the
// names given here already in it.
func TestNamesSortTheWayTheShellSortsThem(t *testing.T) {
	cases := []struct {
		about string
		names []string
	}{
		{
			// The one that shows most in a docs folder: a case-folded
			// comparison puts README.md among the r's and not above every
			// lowercase name, as a byte order would.
			about: "case is not what a name is grouped by",
			names: []string{"prs.md", "README.md", "secrets.md"},
		},
		{
			about: "a name that differs in case alone has the lowercase first",
			names: []string{"readme.md", "Readme.md", "README.md"},
		},
		{
			about: "case decides per character, left to right",
			names: []string{"ab", "aB", "Ab", "AB"},
		},
		{
			// Why mac-mini.md comes before mac.md, which is the pair this
			// listing has: the weights of - and _ are below the dot's.
			about: "punctuation has weights of its own, and they are not ASCII's",
			names: []string{"mac mini.md", "mac_mini.md", "mac-mini.md", "mac.md", "macmini.md"},
		},
		{
			about: "punctuation sorts before digits, and digits before letters",
			names: []string{"-dash", ".dot", "0zero", "9nine", "alpha"},
		},
		{
			about: "a digit is a character and not a number",
			names: []string{"02-plan.md", "1-plan.md", "10-plan.md", "2-plan.md"},
		},
		{
			about: "a name every character of which ties is the shorter one",
			names: []string{"a", "a-", "a.markdown", "a.md", "ab"},
		},
		{
			about: "a leading dot is a character like any other",
			names: []string{".md", "a.md"},
		},
	}

	for _, c := range cases {
		t.Run(c.about, func(t *testing.T) {
			shuffled := []string{}
			for i := len(c.names) - 1; i >= 0; i-- {
				shuffled = append(shuffled, c.names[i])
			}
			sort.Slice(shuffled, func(i, j int) bool {
				return Compare(shuffled[i], shuffled[j]) < 0
			})
			if strings.Join(shuffled, " ") != strings.Join(c.names, " ") {
				t.Errorf("sorted to %v and not to %v", shuffled, c.names)
			}
		})
	}
}

func TestANameSortsEqualToItself(t *testing.T) {
	if Compare("decisions.md", "decisions.md") != 0 {
		t.Error("a name does not sort equal to itself")
	}
}
