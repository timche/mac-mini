package filters

import (
	"sort"
	"testing"
)

func TestRules(t *testing.T) {
	rules := Rules()
	have := map[string]bool{}
	for _, rule := range rules {
		if have[rule] {
			t.Errorf("%s is listed twice", rule)
		}
		have[rule] = true
	}
	if !sort.StringsAreSorted(rules) {
		t.Errorf("rules are not sorted: %v", rules)
	}

	for _, rule := range []string{
		"Bash(pkill *)", "Bash(killall *)", "Bash(killall5 *)", "Bash(skill *)",
		"Bash(kill *)", "Bash(fuser *)",
		"Bash(cliclick *)", "Bash(osascript *)", "Bash(op *)",
		"Bash(sudo *)", "Bash(doas *)", "Bash(env *)", "Bash(exec *)",
		"Bash(caffeinate *)", "Bash(xargs *)",
		"Bash(sh *)", "Bash(bash *)", "Bash(zsh *)", "Bash(dash *)", "Bash(ksh *)",
	} {
		if !have[rule] {
			t.Errorf("%s is missing", rule)
		}
	}

	// Claude Code sees through these itself, and a finder alone kills nothing.
	for _, rule := range []string{"Bash(timeout *)", "Bash(nohup *)", "Bash(time *)", "Bash(pgrep *)", "Bash(focus-lock *)"} {
		if have[rule] {
			t.Errorf("%s needs no rule of its own", rule)
		}
	}
}
