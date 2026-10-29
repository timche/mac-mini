package shell

import (
	"errors"
	"testing"
)

func commandOf(t *testing.T, command string) string {
	t.Helper()
	tokens, err := Lex(command)
	if err != nil {
		t.Fatalf("%s could not be read: %v", command, err)
	}
	parts := Segments(tokens)
	at := CommandOf(parts[0].Words)
	if at < 0 {
		return ""
	}
	return Base(parts[0].Words[at].Text)
}

// A prefix runs the command after it, so what a segment runs is never the first word by
// itself.
func TestTheCommandASegmentRunsIsFoundPastItsPrefixes(t *testing.T) {
	for command, want := range map[string]string{
		"pkill -f vite":               "pkill",
		"/usr/bin/pkill -f vite":      "pkill",
		"FOO=1 BAR=2 pkill":           "pkill",
		"sudo -u root killall":        "killall",
		"env FOO=1 pkill":             "pkill",
		"timeout 5 pkill":             "pkill",
		"timeout -k 2 5 pkill":        "pkill",
		"nice -n 10 pkill":            "pkill",
		"xargs -n 1 kill":             "kill",
		"nohup caffeinate -t 5 pkill": "pkill",
		"if pkill; then":              "pkill",
		"command -v pkill":            "",
		"FOO=1":                       "",
		"sudo":                        "",
	} {
		if got := commandOf(t, command); got != want {
			t.Errorf("%s runs %q and not %q", command, got, want)
		}
	}
}

// Quotes come off, a substitution is kept apart as the command it runs, and a heredoc body
// is not a command at all.
func TestAWordCarriesItsTextItsSourceAndWhatItSubstitutes(t *testing.T) {
	tokens, err := Lex(`kill "$(pgrep node)" '$x' 2>/dev/null`)
	if err != nil {
		t.Fatalf("the command could not be read: %v", err)
	}
	words := Segments(tokens)[0].Words
	if len(words) != 3 {
		t.Fatalf("the segment has %d words and not three", len(words))
	}
	if words[1].Text != "$(pgrep node)" || words[1].Raw != `"$(pgrep node)"` {
		t.Errorf("the substitution reads as %q from %q", words[1].Text, words[1].Raw)
	}
	if len(words[1].Subs) != 1 || words[1].Subs[0] != "pgrep node" {
		t.Errorf("the word substitutes %q", words[1].Subs)
	}
	if name, ok := PlainVar(words[2].Raw); ok {
		t.Errorf("a single-quoted $x names the variable %q", name)
	}

	tokens, err = Lex("cat <<EOF\npkill -f vite\nEOF\necho done")
	if err != nil {
		t.Fatalf("the heredoc could not be read: %v", err)
	}
	for _, token := range tokens {
		if token.Word != nil && token.Word.Text == "pkill" {
			t.Error("a heredoc body reads as a command")
		}
	}
}

func TestWhatCannotBeReadComesBackAsSuch(t *testing.T) {
	for _, command := range []string{
		`echo "unterminated`,
		"echo 'unterminated",
		"echo $(unclosed",
		"echo `unclosed",
		"echo <(unclosed",
	} {
		if _, err := Lex(command); !errors.Is(err, ErrParse) {
			t.Errorf("%s came back as readable", command)
		}
	}
}

func TestAVariableIsNamedOnlyByAWordThatIsNothingElse(t *testing.T) {
	for raw, want := range map[string]string{
		"$pid":       "pid",
		`"$pid"`:     "pid",
		"${pid}":     "pid",
		`"${pid}"`:   "pid",
		"$pid.txt":   "",
		"pid":        "",
		"$(cat pid)": "",
		"%1":         "",
		"$":          "",
	} {
		got, ok := PlainVar(raw)
		if !ok {
			got = ""
		}
		if got != want {
			t.Errorf("%s names %q and not %q", raw, got, want)
		}
	}
}
