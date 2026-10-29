package herdrtab

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	tab       = "wK:t2"
	workspace = "wK"
)

// A case is an event, the title the transcript carries, the label herdr says the tab has and
// the record this hook left last time; what it wants is the herdr commands that came out of
// it, in order, and the record left behind.
type testCase struct {
	about string

	// The payload. event is the hook event name, Stop unless the case says otherwise,
	// and source the SessionStart source.
	event  string
	source string

	// title is an ai-title line at the end of the transcript; far puts a session's
	// worth of lines after it instead, transcript is the whole file, for a case about
	// what is in it, and absent leaves no file at all.
	title      string
	far        bool
	transcript string
	absent     bool

	// outside leaves HERDR_ENV unset, which is every terminal herdr did not start;
	// mochi is a tab mochi made for a thread of its own; untabbed is a herdr too old
	// to tell a process its tab.
	outside  bool
	mochi    bool
	untabbed bool

	// label is what herdr answers that the tab is called, its number unless the case
	// says otherwise; before is the record this hook left last time. replies answers a
	// herdr command differently from the canned answer, and omit leaves one
	// unanswered, which is how herdr refuses.
	label   string
	before  *string
	replies map[string]string
	omit    []string

	want  []string
	after *string
}

// What herdr 0.9.5 answers, trimmed to the fields that are read. Keyed by the first two
// words of the command, which is how the fake picks an answer.
var canned = map[string]string{
	"tab get": `{"id":"cli:tab:get","result":{"type":"tab_info","tab":{"tab_id":"wK:t2",` +
		`"workspace_id":"wK","label":"LABEL","number":2,"focused":true}}}`,
	"tab list": `{"id":"cli:tab:list","result":{"type":"tab_list","tabs":[` +
		`{"tab_id":"wJ:t1","workspace_id":"wJ","label":"other"},` +
		`{"tab_id":"wK:t1","workspace_id":"wK","label":"1"},` +
		`{"tab_id":"wK:t2","workspace_id":"wK","label":"fix-the-hook"}]}}`,
	"tab rename": `{"id":"cli:tab:rename","result":{"type":"ok"}}`,
}

func named(label string) *string { return &label }

var cases = []testCase{
	{
		about: "the session's title becomes the tab's name",
		title: "Fix the hook",
		want:  []string{"tab get wK:t2", "tab rename wK:t2 fix-the-hook"},
		after: named("fix-the-hook"),
	},
	{
		about: "a title longer than four words is cut to four",
		title: "One two three four five six",
		want:  []string{"tab get wK:t2", "tab rename wK:t2 one-two-three-four"},
		after: named("one-two-three-four"),
	},
	{
		// A version or an extension keeps its dot, and the punctuation a sentence
		// was written with goes.
		about: "punctuation goes and an apostrophe joins the word it was in",
		title: "Don't ship v1.2 — yet!",
		want:  []string{"tab get wK:t2", "tab rename wK:t2 dont-ship-v1.2-yet"},
		after: named("dont-ship-v1.2-yet"),
	},
	{
		// Go's standard library has no NFKD, so an accented letter is dropped rather
		// than folded onto the letter it is built from: "Café" is "caf" and not
		// "cafe". A title Claude Code writes is English, and what is left still
		// tells the tab apart.
		about: "a title with letters outside ASCII keeps only the ASCII ones",
		title: "Café ☕ résumé",
		want:  []string{"tab get wK:t2", "tab rename wK:t2 caf-r-sum"},
		after: named("caf-r-sum"),
	},
	{
		about:  "the tab follows the title as it moves",
		title:  "Rename herdr tabs",
		label:  "fix-the-hook",
		before: named("fix-the-hook"),
		want:   []string{"tab get wK:t2", "tab rename wK:t2 rename-herdr-tabs"},
		after:  named("rename-herdr-tabs"),
	},
	{
		// It runs after every tool call, and the title moves a handful of times a
		// session.
		about:  "a name this hook has already given the tab asks herdr nothing",
		title:  "Fix the hook",
		label:  "fix-the-hook",
		before: named("fix-the-hook"),
		after:  named("fix-the-hook"),
	},
	{
		// By hand, by the lead skill or by mochi: herdr keeps no record of who named
		// a tab, so a label that is neither the number nor the one this hook left is
		// somebody else's.
		about: "a tab somebody else named is left alone",
		title: "Fix the hook",
		label: "review-the-pr",
		want:  []string{"tab get wK:t2"},
	},
	{
		about:  "a tab renamed after this hook named it is left alone",
		title:  "Rename herdr tabs",
		label:  "review-the-pr",
		before: named("fix-the-hook"),
		want:   []string{"tab get wK:t2"},
		after:  named("fix-the-hook"),
	},
	{
		about: "a tab that already carries the name is recorded rather than renamed",
		title: "1984",
		label: "1984",
		want:  []string{"tab get wK:t2"},
		after: named("1984"),
	},
	{
		about: "the title a session is on is the last one the transcript carries",
		transcript: strings.Join([]string{
			`{"type":"ai-title","aiTitle":"First subject"}`,
			`{"type":"user","message":{"role":"user","content":"carry on"}}`,
			`{"type":"ai-title","aiTitle":"Second subject"}`,
			`{"type":"assistant","message":{"role":"assistant","content":"done"}}`,
		}, "\n"),
		want:  []string{"tab get wK:t2", "tab rename wK:t2 second-subject"},
		after: named("second-subject"),
	},
	{
		about: "a transcript with no title in it asks herdr nothing",
		transcript: strings.Join([]string{
			`{"type":"user","message":{"role":"user","content":"go"}}`,
			`{"type":"assistant","message":{"role":"assistant","content":"done"}}`,
		}, "\n"),
	},
	{
		// A line of a transcript this never read, since a tool call cannot wait on a
		// session's whole history.
		about: "a title further back than the tail of the transcript is not read",
		title: "Fix the hook",
		far:   true,
	},
	{
		about:  "a transcript that is not there asks herdr nothing",
		absent: true,
	},
	{
		// The tab's number, which is its 1-based position among the tabs of its own
		// workspace: a clear starts another task, and the name was the last one's.
		about:  "clearing the session puts the tab's number back",
		event:  "SessionStart",
		source: "clear",
		title:  "Fix the hook",
		label:  "fix-the-hook",
		before: named("fix-the-hook"),
		want:   []string{"tab list", "tab rename wK:t2 2"},
	},
	{
		about:  "a session that starts without clearing keeps the name the tab has",
		event:  "SessionStart",
		source: "resume",
		title:  "Fix the hook",
		label:  "fix-the-hook",
		before: named("fix-the-hook"),
		after:  named("fix-the-hook"),
	},
	{
		about:  "a clear whose tab herdr does not list leaves the name alone",
		event:  "SessionStart",
		source: "clear",
		before: named("fix-the-hook"),
		replies: map[string]string{
			"tab list": `{"id":"cli:tab:list","result":{"tabs":[` +
				`{"tab_id":"wK:t1","workspace_id":"wK","label":"1"}]}}`,
		},
		want:  []string{"tab list"},
		after: named("fix-the-hook"),
	},
	{
		about: "a prompt names the tab as a tool call does",
		event: "UserPromptSubmit",
		title: "Fix the hook",
		want:  []string{"tab get wK:t2", "tab rename wK:t2 fix-the-hook"},
		after: named("fix-the-hook"),
	},
	{
		about: "a tool call names the tab as a prompt does",
		event: "PostToolUse",
		title: "Fix the hook",
		want:  []string{"tab get wK:t2", "tab rename wK:t2 fix-the-hook"},
		after: named("fix-the-hook"),
	},
	{
		about: "an event this hook is not registered for is not a title that moved",
		event: "PreToolUse",
		title: "Fix the hook",
	},
	{
		about:   "nothing happens in a terminal herdr did not start",
		title:   "Fix the hook",
		outside: true,
	},
	{
		// mochi names the tab for the thread it made it for, which is not a name a
		// session may take over.
		about: "a tab mochi made for a thread of its own is not this hook's to name",
		title: "Fix the hook",
		mochi: true,
	},
	{
		about:    "a herdr that does not say what tab this is leaves the tab alone",
		title:    "Fix the hook",
		untabbed: true,
	},
	{
		// A hook is not worth a session's tool call, so a herdr that refuses is the
		// end of it and nothing is recorded as named.
		about: "a herdr that refuses the rename records nothing",
		title: "Fix the hook",
		omit:  []string{"tab rename"},
		want:  []string{"tab get wK:t2", "tab rename wK:t2 fix-the-hook"},
	},
	{
		about: "a herdr that will not say what the tab is called is the end of it",
		title: "Fix the hook",
		omit:  []string{"tab get"},
		want:  []string{"tab get wK:t2"},
	},
}

func TestObserve(t *testing.T) {
	for _, c := range cases {
		t.Run(c.about, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("CLAUDE_HOOKS_CACHE_DIR", filepath.Join(home, "cache"))

			log := filepath.Join(t.TempDir(), "argv")
			t.Setenv("HERDR_FAKE_LOG", log)
			t.Setenv("HERDR_FAKE_REPLIES", answers(t, c))
			t.Setenv("HERDR_BIN_PATH", fake(t))
			t.Setenv("HERDR_ENV", "1")
			if c.outside {
				t.Setenv("HERDR_ENV", "")
			}
			t.Setenv("HERDR_TAB_ID", tab)
			if c.untabbed {
				t.Setenv("HERDR_TAB_ID", "")
			}
			t.Setenv("HERDR_WORKSPACE_ID", workspace)
			if c.mochi {
				t.Setenv("MOCHI_THREAD", "th_1")
			}

			if c.before != nil {
				remember(tab, *c.before)
			}

			Observe(strings.NewReader(payload(t, c)))

			if got := called(t, log); !same(got, c.want) {
				t.Errorf("herdr was asked\n  %s\nand not\n  %s",
					strings.Join(got, "\n  "), strings.Join(c.want, "\n  "))
			}

			got, kept := recall(tab)
			switch {
			case c.after == nil && kept:
				t.Errorf("the record says %q and should be gone", got)
			case c.after != nil && !kept:
				t.Errorf("the record is gone and should say %q", *c.after)
			case c.after != nil && got != *c.after:
				t.Errorf("the record says %q and not %q", got, *c.after)
			}
		})
	}
}

// A transcript as Claude Code writes one, and the path of one that is not there.
func transcript(t *testing.T, c testCase) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "session.jsonl")
	if c.absent {
		return path
	}

	body := c.transcript
	if body == "" && c.title != "" {
		line, err := json.Marshal(map[string]any{"type": "ai-title", "aiTitle": c.title})
		if err != nil {
			t.Fatal(err)
		}
		body = string(line)
		// A session long enough for the tail to be a window and not the whole file.
		if c.far {
			body += "\n" + filler()
		}
	}
	if err := os.WriteFile(path, []byte(body+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func filler() string {
	line := `{"type":"assistant","message":{"role":"assistant","content":"` +
		strings.Repeat("x", 200) + `"}}`
	return strings.Repeat(line+"\n", (tailBytes/len(line))+16)
}

// A herdr that writes down what it was asked and answers from a file, so what a case pins is
// the commands and not the socket.
func fake(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "herdr")
	script := `#!/bin/sh
printf '%s\n' "$*" >>"$HERDR_FAKE_LOG"
if [ -f "$HERDR_FAKE_REPLIES/$1 $2" ]; then
  cat "$HERDR_FAKE_REPLIES/$1 $2"
  exit 0
fi
printf '{"id":"cli","error":{"code":"refused","message":"no answer for %s"}}\n' "$1 $2"
exit 1
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func answers(t *testing.T, c testCase) string {
	t.Helper()

	label := c.label
	if label == "" {
		label = "2"
	}

	dir := t.TempDir()
	for command, reply := range canned {
		if given, ok := c.replies[command]; ok {
			reply = given
		}
		if omitted(c.omit, command) {
			continue
		}
		reply = strings.Replace(reply, "LABEL", label, 1)
		if err := os.WriteFile(filepath.Join(dir, command), []byte(reply), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func omitted(omit []string, command string) bool {
	for _, name := range omit {
		if name == command {
			return true
		}
	}
	return false
}

func payload(t *testing.T, c testCase) string {
	t.Helper()

	event := c.event
	if event == "" {
		event = "Stop"
	}
	body := map[string]any{
		"session_id":      "s1",
		"hook_event_name": event,
		"transcript_path": transcript(t, c),
	}
	if c.source != "" {
		body["source"] = c.source
	}
	out, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func called(t *testing.T, log string) []string {
	t.Helper()

	body, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(body), "\n"), "\n")
}

func same(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
