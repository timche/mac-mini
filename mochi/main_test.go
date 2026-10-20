package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/timche/mac-mini/mochi/internal/config"
	"path/filepath"
	"testing"
)

func TestHistoryTakesNoChannel(t *testing.T) {
	for _, args := range [][]string{
		{"--channel", "1300000000000000002"},
		{"1300000000000000002"},
		{"--before", "1300000000000000002", "--after", "1300000000000000003"},
	} {
		if _, err := historyArgs(args); err == nil {
			t.Errorf("%v parsed", args)
		}
	}
	q, err := historyArgs([]string{"--around", "1300000000000000002", "--limit", "100"})
	if err != nil || q.Around != "1300000000000000002" || q.Limit != 100 || q.Post {
		t.Errorf("%+v, %v", q, err)
	}
	if q, err := historyArgs([]string{"--post"}); err != nil || !q.Post {
		t.Errorf("--post: %+v, %v", q, err)
	}
}

func TestSplitEvalTakesAModelAndAnEffort(t *testing.T) {
	base := config.Split{Model: "sonnet", Effort: "medium"}
	s, err := splitArgs(base, []string{"--model", "haiku", "--effort", "low"})
	if err != nil || s != (config.Split{Model: "haiku", Effort: "low"}) {
		t.Errorf("%+v, %v", s, err)
	}
	if s, _ := splitArgs(base, nil); s != base {
		t.Errorf("the defaults are not the config's: %+v", s)
	}
	for _, args := range [][]string{{"--model", "--tools"}, {"cases.jsonl"}, {"--effort", ""}} {
		if _, err := splitArgs(base, args); err == nil {
			t.Errorf("%v parsed", args)
		}
	}
}

func TestPostTakesTextFilesAStateAndAPR(t *testing.T) {
	shot := filepath.Join(t.TempDir(), "shot.png")
	os.WriteFile(shot, []byte("png"), 0o644)

	p, err := postArgs([]string{"--review", "--pr", "https://github.com/timche/repeek/pull/42", "--file", shot, "--file", shot, "see", "this"})
	if err != nil || len(p.Files) != 2 || p.Text != "see this" || p.Settles != "❓" || p.PR != "https://github.com/timche/repeek/pull/42" {
		t.Errorf("%+v %v", p, err)
	}
	if p, _ := postArgs([]string{"--done", "merged"}); p.Settles != "✅" {
		t.Errorf("--done settles %q", p.Settles)
	}
	if p, _ := postArgs([]string{"a note"}); p.Settles != "" {
		t.Errorf("a plain post settles %q", p.Settles)
	}
	if p, err := postArgs([]string{"--done", "--close", "merged"}); err != nil || !p.Close || p.Settles != "✅" {
		t.Errorf("--done --close: %+v, %v", p, err)
	}
	if p, err := postArgs([]string{"--type", "bug"}); err != nil || p.Type != "bug" || p.Text != "" {
		t.Errorf("--type alone: %+v, %v", p, err)
	}
	for _, args := range [][]string{
		{},
		{"--file", shot},
		{"--file", "/nowhere", "hi"},
		{"see", "https://example.com"},
		{"--review", "--done", "x"},
		{"--pr", "https://github.com/timche/repeek/issues/42", "x"},
		{"--type", "chore"},
		{"--close", "x"},
		{"--review", "--close", "x"},
	} {
		if _, err := postArgs(args); err == nil {
			t.Errorf("%v parsed", args)
		}
	}
}

// Outside a session there is no thread to post to, and saying so costs no 1Password read.
func TestPostAndHistoryOutsideASessionStopBeforeOp(t *testing.T) {
	t.Setenv("MOCHI_THREAD", "")
	t.Setenv("MOCHI_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	os.WriteFile(os.Getenv("MOCHI_CONFIG"), []byte("user = \"94004892942741504\"\n"), 0o644)
	t.Setenv("PATH", t.TempDir())

	for _, args := range [][]string{{"post", "hi"}, {"history"}} {
		if err := run(args); err == nil || err.Error() == "no op on PATH, so the bot token cannot be resolved" {
			t.Errorf("%v: %v", args, err)
		}
	}
}

// A post of several lines comes on stdin, and its lines reach the post as written.
func TestPostTextCanComeOnStdin(t *testing.T) {
	text := "**Average elo shows for each team**\n\n**What changed**\n- The lobby shows each team's average elo\n"
	args, err := postStdin([]string{"--review"}, strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	p, err := postArgs(args)
	if err != nil || p.Text != strings.TrimSpace(text) || p.Settles != "❓" {
		t.Errorf("%+v %v", p, err)
	}

	// Text given as an argument leaves stdin alone.
	args, _ = postStdin([]string{"inline"}, strings.NewReader("not read"))
	if p, _ := postArgs(args); p.Text != "inline" {
		t.Errorf("text = %q", p.Text)
	}
	if args, _ := postStdin(nil, strings.NewReader("   ")); args != nil {
		if _, err := postArgs(args); err == nil {
			t.Error("an empty stdin was a post")
		}
	}
}

// The hook's command: no output, no config, nothing written outside a mochi session.
func TestSessionEventIsSilentAndNeedsNoConfig(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("MOCHI_CACHE_DIR", cache)
	t.Setenv("MOCHI_CONFIG", filepath.Join(t.TempDir(), "missing.toml"))

	t.Setenv("MOCHI_THREAD", "")
	if err := run([]string{"session-event", "stop"}); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Join(cache, "events")); len(entries) != 0 {
		t.Errorf("wrote %v outside a session", entries)
	}

	t.Setenv("MOCHI_THREAD", "1562147652450394154")
	if err := run([]string{"session-event", "ask"}); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Join(cache, "events")); len(entries) != 1 {
		t.Errorf("events = %v", entries)
	}
}

// The scratch folder is named as Claude Code names it: the project's path with every character
// but a letter or a digit a dash, under the user's id.
func TestTheScratchFolderIsNamedAsClaudeCodeNamesIt(t *testing.T) {
	t.Setenv("CLAUDE_CODE_TMPDIR", "/somewhere/else")
	want := fmt.Sprintf("/Users/timche/.cache/claude-tmp/claude-%d/-Users-timche--mac-mini", os.Getuid())
	if got := scratch("/Users/timche", "/Users/timche/.mac-mini"); got != want {
		t.Errorf("%q, want %q", got, want)
	}
	if got := scratch("/Users/x", "/Users/x/projects/repeek"); !strings.HasSuffix(got, "/-Users-x-projects-repeek") {
		t.Errorf("%q", got)
	}
}
