package sync

import (
	"strings"
	"testing"
)

// A repository with a model configured and something staged in it, which is the one shape
// every test here starts from.
func writing(t *testing.T) *fixture {
	f := newFixture(t)
	f.subjectModel = "haiku"
	f.repo.Pull = false
	f.tree.status = " M home/.claude/CLAUDE.md\x00"
	f.tree.staged = []string{"home/.claude/CLAUDE.md"}
	f.tree.stat = " home/.claude/CLAUDE.md | 2 +-\n"
	f.tree.diff = "@@ -1 +1 @@\n-before\n+after\n"
	f.tree.subjects = []string{"A heartbeat is a pass that reached Discord", "The time of a prune is written down"}
	return f
}

func TestASubjectAModelWroteIsTheCommitSubject(t *testing.T) {
	f := writing(t)
	f.subjectSays = "Say what a session may commit here\n"

	result, log := f.pass(pushNow)

	if result.Subject != "Say what a session may commit here" {
		t.Fatalf("subject %q", result.Subject)
	}
	if !f.ranAny("commit -m Say what a session may commit here") {
		t.Errorf("calls: %v", f.calls)
	}
	if !strings.Contains(log, "pushed `Say what a session may commit here`") {
		t.Errorf("log: %s", log)
	}
}

// What the model is given: the files, the diff of exactly what is being committed, and the
// repository's own subjects as the style to write in.
func TestTheModelIsGivenTheStatTheDiffAndTheHouseStyle(t *testing.T) {
	f := writing(t)
	f.subjectSays = "Something"

	f.pass(pushNow)

	asked := f.lastAsked()
	for _, part := range []string{
		"haiku",
		"under 72 characters",
		"A heartbeat is a pass that reached Discord",
		" home/.claude/CLAUDE.md | 2 +-",
		"+after",
	} {
		if !strings.Contains(asked, part) {
			t.Errorf("the prompt does not carry %q:\n%s", part, asked)
		}
	}
}

// A diff as long as somebody's edit is cut off, and the stat above it is what still names
// every file that changed.
func TestALongDiffIsCutOffAndSaysSo(t *testing.T) {
	f := writing(t)
	f.subjectSays = "Something"
	f.tree.diff = strings.Repeat("+a line of somebody's edit\n", 2000)

	f.pass(pushNow)

	asked := f.lastAsked()
	if len(asked) > diffCap+4096 {
		t.Errorf("the prompt is %d bytes", len(asked))
	}
	if !strings.Contains(asked, "the rest of the diff is cut off") {
		t.Error("a cut-off diff does not say so")
	}
	if !strings.Contains(asked, " home/.claude/CLAUDE.md | 2 +-") {
		t.Error("the stat went with it")
	}
}

// Every way the answer can be unusable, and each one is the file list instead. Never a
// message to Discord and never a commit held up: a subject is cosmetic, and the work landing
// is not.
func TestAnAnswerThatIsNotASubjectFallsBackToTheFileList(t *testing.T) {
	for _, tc := range []struct {
		name   string
		says   string
		fails  string
		reason string
	}{
		{name: "nothing at all", says: "", reason: "not a subject"},
		{name: "only whitespace", says: "   \n\n", reason: "not a subject"},
		{name: "a paragraph", says: strings.Repeat("a sentence about the change ", 10), reason: "not a subject"},
		{name: "it failed", fails: "signal: killed", reason: "could not be written"},
		{name: "it timed out", fails: "context deadline exceeded", reason: "could not be written"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := writing(t)
			f.subjectSays, f.subjectErr = tc.says, tc.fails

			result, log := f.pass(pushNow)

			if result.Subject != "Update home/.claude/CLAUDE.md" {
				t.Fatalf("subject %q", result.Subject)
			}
			if result.Outcome != Pushed {
				t.Errorf("outcome %v", result.Outcome)
			}
			if len(f.sent) != 0 {
				t.Errorf("it posted about a subject: %v", f.sent)
			}
			if !strings.Contains(log, tc.reason) {
				t.Errorf("log: %s", log)
			}
		})
	}
}

// The first line that says anything, with what wraps it taken off: a model that answers in a
// code fence, with a quoted line or with a line of reasoning under it still wrote a subject.
func TestOneSubjectIsTakenOutOfWhateverCameBack(t *testing.T) {
	for _, tc := range []struct{ answer, want string }{
		{"Update the rules\n", "Update the rules"},
		{"\n\nUpdate the rules\n\nIt also does other things.\n", "Update the rules"},
		{"`Update the rules`", "Update the rules"},
		{"\"Update the rules\"", "Update the rules"},
		{"Update the rules.", "Update the rules"},
		{"Update the\trules", "Update the\trules"},
	} {
		if got := cleanSubject(tc.answer); got != tc.want {
			t.Errorf("%q read as %q, want %q", tc.answer, got, tc.want)
		}
	}

	for _, answer := range []string{"", "\n \n", strings.Repeat("x", subjectLimit+1)} {
		if got := cleanSubject(answer); got != "" {
			t.Errorf("%q read as %q", answer, got)
		}
	}
}

// A dry run reaches nothing outside this process, a model included, and says which subject it
// would have fallen back to.
func TestADryRunAsksNoModel(t *testing.T) {
	f := writing(t)
	f.dry = true

	_, l := f.loop()
	f.log.Reset()
	write(f, l, " M home/.claude/CLAUDE.md\x00", "home/.claude/CLAUDE.md")

	if len(f.asked) != 0 {
		t.Errorf("a dry run asked for a subject: %v", f.asked)
	}
	if !strings.Contains(f.log.String(), "would commit `Update home/.claude/CLAUDE.md`, or the subject haiku writes") {
		t.Errorf("log: %s", f.log.String())
	}
}

// No model configured is the subject sync wrote before any of this, and nothing started.
func TestWithNoModelConfiguredTheSubjectNamesTheFiles(t *testing.T) {
	f := writing(t)
	f.subjectModel = ""

	result, _ := f.pass(pushNow)

	if result.Subject != "Update home/.claude/CLAUDE.md" {
		t.Errorf("subject %q", result.Subject)
	}
	if len(f.asked) != 0 {
		t.Errorf("it asked anyway: %v", f.asked)
	}
}
