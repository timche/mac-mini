// Package filters is the list of `if` rules settings.json registers guard under. Claude
// Code starts the hook only for a command one of them matches, so a program a check reads
// that is missing from settings.json is a program that check never sees. The checks own
// the names; settings.json is a copy of what this prints, and test/assert.sh fails when
// the two differ.
package filters

import (
	"sort"

	"github.com/timche/mac-mini/guard/internal/focus"
	"github.com/timche/mac-mini/guard/internal/killbypid"
	"github.com/timche/mac-mini/guard/internal/shell"
)

// Rules are the `if` rules, sorted and each once.
func Rules() []string {
	seen := map[string]bool{}
	var rules []string
	for _, names := range [][]string{killbypid.Commands(), focus.Commands(), shell.Runners()} {
		for _, name := range names {
			rule := "Bash(" + name + " *)"
			if !seen[rule] {
				seen[rule] = true
				rules = append(rules, rule)
			}
		}
	}
	sort.Strings(rules)
	return rules
}
