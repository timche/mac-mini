package attribution

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What the hook answers with, as a case in the table names it.
type want int

const (
	// Nothing: the command runs as it was written. A body file may still have been
	// rewritten, which is what rewrites names.
	nothing want = iota
	deny
	rewrite
)

const (
	attributed = "Fixes the thing.\n\n🤖 Generated with [Claude Code](https://claude.com/claude-code)\n"
	coauthored = "A commit message\n\nCo-Authored-By: Claude <noreply@anthropic.com>\n"
	plain      = "Fixes the thing.\n"
)

// A case of the table: the command with {dir} standing for a directory of its own, the
// files that directory starts with, what the hook answers, and which of those files the
// attribution came out of.
type testcase struct {
	name     string
	command  string
	want     want
	files    map[string]string
	rewrites []string
}

var cases = []testcase{{
	name: "a body written as a heredoc in the command itself",
	command: "gh pr create --head x --title \"T\" --body \"$(cat <<'EOF'\nBody\n" +
		"🤖 Generated with [Claude Code](https://claude.com/claude-code)\nEOF\n)\"",
	want: rewrite,
}, {
	name:    "a body with nothing of Claude's in it",
	command: `gh pr create --head x --title "T" --body "Body"`,
	want:    nothing,
}, {
	name:     "a body file named outright",
	command:  "gh pr edit 1232 --body-file {dir}/pr-body.md",
	files:    map[string]string{"pr-body.md": attributed},
	rewrites: []string{"pr-body.md"},
}, {
	name:     "a body file the line assigned to a variable",
	command:  "B={dir}/body.md; gh pr edit 1232 --body-file $B",
	files:    map[string]string{"body.md": attributed},
	rewrites: []string{"body.md"},
}, {
	name:     "a body file under a directory the line assigned",
	command:  "SP={dir}; gh pr edit 1233 --body-file $SP/pr-body.md",
	files:    map[string]string{"pr-body.md": attributed},
	rewrites: []string{"pr-body.md"},
}, {
	name:     "a body file beside other flags",
	command:  `gh pr edit 163 --title "x" --body-file {dir}/body163.md`,
	files:    map[string]string{"body163.md": attributed},
	rewrites: []string{"body163.md"},
}, {
	name:    "a body read from stdin",
	command: "gh pr create --body-file=-",
	want:    nothing,
}, {
	name:     "a file field of a gh api call",
	command:  "gh api repos/o/r/issues -F body=@{dir}/b.md",
	files:    map[string]string{"b.md": attributed},
	rewrites: []string{"b.md"},
}, {
	name:     "a whole request body of a gh api call",
	command:  "gh api repos/o/r/issues --input {dir}/payload.json",
	files:    map[string]string{"payload.json": `{"body": "Fixes it.\n\nCo-Authored-By: Claude <noreply@anthropic.com>"}`},
	rewrites: []string{"payload.json"},
}, {
	name:     "a body file of the gh at the end of a chain",
	command:  `cd {dir} && git push -u origin br 2>&1 | tail -2 && gh pr create --head br --title "T" --body-file {dir}/pr-b.md`,
	files:    map[string]string{"pr-b.md": attributed},
	rewrites: []string{"pr-b.md"},
}, {
	name:    "a body read back out of a pull request",
	command: "gh pr view 1233 --json body -q .body",
	want:    nothing,
}, {
	name:    "a merge",
	command: "gh pr merge 1233 --squash --delete-branch",
	want:    nothing,
}, {
	name:    "a gh in a loop's condition",
	command: "until s=$(gh pr checks 1232 --json name,bucket); do sleep 30; done",
	want:    nothing,
}, {
	name:    "a gh in a loop's body",
	command: "for n in 1 2; do gh issue view $n -R o/r --json state; done",
	want:    nothing,
}, {
	// The file the heredoc writes is written after the hook has answered, and the gh
	// in it is a line of a workflow rather than a command this line runs.
	name:    "a gh written into a file by a heredoc",
	command: "cat > {dir}/wf.yml <<'EOF'\n  run: gh pr comment 1 --body-file {dir}/x.md\nEOF",
	files:   map[string]string{"x.md": attributed},
	want:    nothing,
}, {
	name:    "a commit and a push with the hooks left on",
	command: `git commit -q -m "msg" && git push -q -u origin HEAD`,
	want:    nothing,
}, {
	name:    "a commit message file, which git's own hook strips",
	command: "git commit --amend -F {dir}/msg",
	files:   map[string]string{"msg": coauthored},
	want:    nothing,
}, {
	name:    "a commit message file beside a gh command",
	command: `git commit -F {dir}/msg && gh pr create --title "T" --body "B"`,
	files:   map[string]string{"msg": coauthored},
	want:    nothing,
}, {
	name:    "a commit that skips the hooks",
	command: `git commit --no-verify -m "x"`,
	want:    deny,
}, {
	name:    "a push that skips the hooks",
	command: "git push --no-verify",
	want:    deny,
}, {
	name:    "a commit whose -n is bundled with another flag",
	command: `cd {dir} && git commit -qn -m "x"`,
	want:    deny,
}, {
	name:    "a commit elsewhere that skips the hooks",
	command: `git -C {dir} commit -n -m "x"`,
	want:    deny,
}, {
	name:    "a push elsewhere that skips the hooks",
	command: "git -C {dir} push --no-verify",
	want:    deny,
}, {
	name:    "a commit message that talks about the flag",
	command: `git commit -q -m "add a --no-verify guard"`,
	want:    nothing,
}, {
	name:    "a grep whose pattern is a subcommand",
	command: `git grep -n "commit" -- .`,
	want:    nothing,
}, {
	name:    "a log with a count",
	command: "git log --oneline -n 20",
	want:    nothing,
}, {
	name:    "a commit piped into a grep that counts lines",
	command: `git commit -m "x" | grep -n foo`,
	want:    nothing,
}, {
	name:    "a grep for the words this hook acts on",
	command: `grep -n -i "attribution\|no-verify" test/assert.sh`,
	want:    nothing,
}, {
	name:    "an attribution written straight into a body",
	command: `gh pr edit 9 --body "Co-authored-by: Claude <noreply@anthropic.com>"`,
	want:    rewrite,
}, {
	// The unterminated quote is in a heredoc body, which is not a command and is never
	// read as one.
	name: "a body file written by a heredoc whose body does not parse",
	command: "S={dir}\ncat > $S/body.md <<EOF\nand an unterminated \" quote\nEOF\n" +
		"gh pr edit 9 --body-file $S/body.md",
	files:    map[string]string{"body.md": attributed},
	rewrites: []string{"body.md"},
}, {
	name:    "a dry run of a push, which -n is there",
	command: "git push -n",
	want:    nothing,
}, {
	name:    "the flag as the text of another command",
	command: `echo "git commit --no-verify"`,
	want:    nothing,
}, {
	name:    "a commit that skips the hooks inside another shell",
	command: `bash -c 'git commit --no-verify -m x'`,
	want:    deny,
}, {
	// Unreadable, so the whole line is read as text — the one thing that must not let a
	// skipped hook through.
	name:    "a commit that skips the hooks in a command that cannot be read",
	command: `git commit --no-verify -m "unterminated`,
	want:    deny,
}, {
	name:    "a body file with nothing of Claude's in it",
	command: "gh pr edit 1 --body-file {dir}/body.md",
	files:   map[string]string{"body.md": plain},
	want:    nothing,
}, {
	name:    "a body file that is not there",
	command: "gh pr edit 1 --body-file {dir}/missing.md",
	want:    nothing,
}, {
	name:    "a variable in a path the shell would not have expanded",
	command: "B={dir}/body.md; gh pr edit 1 --body-file '$B'",
	files:   map[string]string{"body.md": attributed},
	want:    nothing,
}, {
	name:    "a variable the line never assigned",
	command: "gh pr edit 1 --body-file $UNSET/body.md",
	files:   map[string]string{"body.md": attributed},
	want:    nothing,
}, {
	name:     "a body file of a gh exported into the line's variables",
	command:  "export B={dir}/body.md\ngh pr comment 1 --body-file ${B}",
	files:    map[string]string{"body.md": attributed},
	rewrites: []string{"body.md"},
}, {
	name:    "a raw field of a gh api call, which names no file",
	command: "gh api repos/o/r/issues -f body=@{dir}/b.md",
	files:   map[string]string{"b.md": attributed},
	want:    nothing,
}, {
	name:    "a -F of a program that is not gh",
	command: "curl -F file=@{dir}/b.md https://example.com",
	files:   map[string]string{"b.md": attributed},
	want:    nothing,
}, {
	name:    "a commit whose message is the flag itself",
	command: `git commit -m "-n"`,
	want:    nothing,
}, {
	name:    "a commit whose message is bundled into the flag",
	command: `git commit -qmn`,
	want:    nothing,
}, {
	name:    "a flag after the end of the options",
	command: `git commit -m "x" -- --no-verify`,
	want:    nothing,
}, {
	name:    "a gh behind a prefix that runs it",
	command: `env GH_TOKEN=x gh pr edit 9 --body "🤖 Generated with [Claude Code](https://claude.com/claude-code)"`,
	want:    rewrite,
}, {
	name:    "an attribution in a command that runs no gh",
	command: `echo "🤖 Generated with [Claude Code](https://claude.com/claude-code)" > /dev/null`,
	want:    nothing,
}}

func TestWhatTheHookAnswersAndWhichFilesItRewrites(t *testing.T) {
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, text := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			command := strings.ReplaceAll(tc.command, "{dir}", dir)

			answer := Decide(command)
			switch tc.want {
			case deny:
				if !answer.Deny || answer.Reason != DenyReason || answer.Command != "" {
					t.Errorf("%s was answered with %+v and not a deny", command, answer)
				}
			case rewrite:
				if answer.Deny || answer.Command == "" || answer.Reason != RewriteReason {
					t.Fatalf("%s was answered with %+v and not a rewrite", command, answer)
				}
				if hasAttribution(answer.Command) {
					t.Errorf("the attribution is still in %q", answer.Command)
				}
			case nothing:
				if answer.Deny || answer.Command != "" || answer.Reason != "" {
					t.Errorf("%s was answered with %+v rather than with nothing", command, answer)
				}
			}

			rewritten := map[string]bool{}
			for _, name := range tc.rewrites {
				rewritten[name] = true
			}
			for name, before := range tc.files {
				path := filepath.Join(dir, name)
				after, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				switch {
				case !rewritten[name]:
					if string(after) != before {
						t.Errorf("%s was rewritten to %q and should have been left alone", name, after)
					}
				case string(after) == before:
					t.Errorf("%s was left as it was", name)
				case hasAttribution(string(after)):
					t.Errorf("the attribution is still in %s: %q", name, after)
				case !strings.HasPrefix(string(after), "Fixes the thing."):
					if !strings.Contains(string(after), "Fixes it.") {
						t.Errorf("%s lost more than the attribution: %q", name, after)
					}
				}
			}
		})
	}
}

func hasAttribution(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "generated with") || strings.Contains(lower, "co-authored-by")
}

// A body file keeps the mode it had, since the next thing to open it is gh.
func TestARewrittenBodyFileKeepsItsMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "body.md")
	if err := os.WriteFile(path, []byte(attributed), 0o640); err != nil {
		t.Fatal(err)
	}
	Decide("gh pr edit 1 --body-file " + path)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("the file is now %v", info.Mode().Perm())
	}
}

// Nothing is written to a file the attribution was never in, so what a session sees of it
// — its time, and anything watching it — does not move.
func TestABodyFileWithoutTheAttributionIsNotWrittenTo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "body.md")
	if err := os.WriteFile(path, []byte(plain), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	Decide("gh pr edit 1 --body-file " + path)
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("the file was written to: %v became %v", before.ModTime(), after.ModTime())
	}
}

// A path a word starts with `~/` is the account's home, which is where the shell would
// have looked.
func TestAHomeRelativeBodyFileIsRewritten(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	path := filepath.Join(dir, "body.md")
	if err := os.WriteFile(path, []byte(attributed), 0o600); err != nil {
		t.Fatal(err)
	}
	Decide("gh pr edit 1 --body-file ~/body.md")
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if hasAttribution(string(after)) {
		t.Errorf("the attribution is still in %q", after)
	}
}

// Nesting past anything a person writes is unreadable rather than a stack overflow, which
// would exit 2 and block the command.
func TestACommandNestedPastReadingIsStillRefusedWhenItSkipsTheHooks(t *testing.T) {
	command := strings.Repeat("$(", 200) + "git commit --no-verify -m x" + strings.Repeat(")", 200)
	if answer := Decide(command); !answer.Deny {
		t.Errorf("the nested command was answered with %+v", answer)
	}
}

func TestTheBlankLinesTheAttributionLeavesBehindAreTidied(t *testing.T) {
	for text, want := range map[string]string{
		"Body\n\n🤖 Generated with [Claude Code](https://claude.com/claude-code)\n\nMore\n": "Body\n\nMore\n",
		`Body\n\n🤖 Generated with [Claude Code](https://claude.com/claude-code)\nMore`:     `Body\n\nMore`,
		"Body\n\nCo-Authored-By: Claude <noreply@anthropic.com>\n":                         "Body\n\n",
		"Body\n": "Body\n",
	} {
		if got := strip(text); got != want {
			t.Errorf("%q was stripped to %q and not to %q", text, got, want)
		}
	}
}
