// Package attribution takes Claude's attribution out of what a command sends to GitHub,
// and refuses a command that would skip the git hooks that do the same on their side.
//
// A pull request, issue or comment body goes from gh straight to GitHub and never passes
// through git's commit-msg hook, and Claude Code's attribution setting only changes what
// the model is asked to write. So the attribution comes out of any gh command, and of any
// body file that command names, before the command runs. Rewritten rather than refused,
// since a refused command is one an agent retries and works around.
//
// What is refused is --no-verify, which skips the commit-msg and pre-push hooks that keep
// the attribution out, and a repository's own pre-commit with them. On commit, -n is the
// same flag; on push it is --dry-run. A permissions deny rule would match only a command
// that starts with `git push`, which `git -C <dir> push` and a `cd … &&` chain are not.
//
// Which command a word belongs to is read with internal/shell, so that `git commit -m "add
// a --no-verify guard"` is a message and not a flag, a gh in a heredoc body is text, and
// only a path a gh command itself names is ever written to.
package attribution

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/timche/mac-mini/claude-hooks/internal/hook"
	"github.com/timche/mac-mini/claude-hooks/internal/shell"
)

const DenyReason = "Skipping git hooks (--no-verify, or -n on commit) is not allowed: they keep Claude's " +
	"attribution off commits and GitHub, and run the repository's own checks. Run it without the flag " +
	"and fix what a hook reports."

const RewriteReason = "Claude attribution removed from the gh command"

var attribution = regexp.MustCompile(
	`(?i)[ \t]*(?:🤖[ \t]*)?Generated with \[?Claude Code\]?(?:\([^)\s]*\))?[ \t]*` +
		`|[ \t]*Co-Authored-By:[^\n'"]*(?:Claude|anthropic\.com)[^\n'"]*`)

// What the attribution leaves behind: the blank lines that held it apart from the body,
// escaped in a quoted command string as well as written out.
var (
	escapedBlanks = regexp.MustCompile(`(?:\\n){3,}`)
	blankLines    = regexp.MustCompile("\n[ \t]*\n(?:[ \t]*\n)+")
)

// What a command is read as when internal/shell cannot read it: the text of the whole line
// rather than the arguments of the commands in it. A command that cannot be parsed is rare
// and usually a mistake, but a guard that let one through would be a way around it.
var (
	textNoVerify = regexp.MustCompile(
		`\bgit\b[^;&|\n]*\s(?:push|commit)\b[^;&|\n]*\s--no-verify\b` +
			`|\bgit\b[^;&|\n]*\scommit\b[^;&|\n]*\s-[a-zA-Z]*n[a-zA-Z]*\b`)
	textGH = regexp.MustCompile(`(?:^|[\s;&|(])gh\s`)
)

// Decide reads the command, rewrites the body files the gh commands in it name, and says
// what the hook answers with. The rewriting happens here rather than in the caller because
// a body file is decided on exactly as the command is: by what gh was given.
func Decide(command string) hook.Answer {
	line := &line{vars: map[string]string{}}
	if err := line.read(command, 0); err != nil {
		return fromText(command)
	}
	if line.skipsHooks {
		return hook.Answer{Deny: true, Reason: DenyReason}
	}
	if !line.runsGH {
		return hook.Answer{}
	}
	for _, path := range line.bodyFiles {
		rewriteFile(path)
	}
	cleaned := strip(command)
	if cleaned == command {
		return hook.Answer{}
	}
	return hook.Answer{Reason: RewriteReason, Command: cleaned}
}

func fromText(command string) hook.Answer {
	if textNoVerify.MatchString(command) {
		return hook.Answer{Deny: true, Reason: DenyReason}
	}
	if !textGH.MatchString(command) {
		return hook.Answer{}
	}
	cleaned := strip(command)
	if cleaned == command {
		return hook.Answer{}
	}
	return hook.Answer{Reason: RewriteReason, Command: cleaned}
}

func strip(text string) string {
	cleaned := attribution.ReplaceAllString(text, "")
	cleaned = escapedBlanks.ReplaceAllString(cleaned, `\n\n`)
	return blankLines.ReplaceAllString(cleaned, "\n\n")
}

// A file that cannot be read is skipped rather than reported: the command is about to run
// and gh will say what is wrong with its own argument.
func rewriteFile(path string) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return
	}
	text, err := os.ReadFile(path)
	if err != nil {
		return
	}
	cleaned := strip(string(text))
	if cleaned == string(text) {
		return
	}
	os.WriteFile(path, []byte(cleaned), info.Mode().Perm())
}

// What one command line runs: whether a git command in it skips the hooks, whether a gh
// command runs at all, the body files those gh commands name, and the variables the line
// set itself, which are the only ones a path is resolved from.
type line struct {
	skipsHooks bool
	runsGH     bool
	bodyFiles  []string
	vars       map[string]string
}

func (l *line) read(command string, depth int) error {
	if depth > shell.MaxDepth {
		return shell.ErrParse
	}
	tokens, err := shell.Lex(command)
	if err != nil {
		return err
	}

	for _, part := range shell.Segments(tokens) {
		for _, word := range part.Words {
			for _, sub := range word.Subs {
				if err := l.read(sub, depth+1); err != nil {
					return err
				}
			}
		}
		for _, feed := range part.Feeds {
			for _, sub := range feed.Subs {
				if err := l.read(sub, depth+1); err != nil {
					return err
				}
			}
		}

		at := shell.CommandOf(part.Words)
		if at < 0 {
			// `FOO=1 gh …` sets FOO for gh alone and the shell expanded `$FOO` before
			// that, so only a segment that runs nothing assigns anything here.
			for _, word := range part.Words {
				l.assign(word)
			}
			continue
		}

		name, args := shell.Base(part.Words[at].Text), part.Words[at+1:]
		switch {
		case name == "export":
			for _, word := range args {
				l.assign(word)
			}
		case name == "git":
			if gitSkipsHooks(args) {
				l.skipsHooks = true
			}
		case name == "gh":
			l.runsGH = true
			l.readBodyFiles(args)
		case shell.Shells[name]:
			for j := 0; j+1 < len(args); j++ {
				if shell.IsDashC(args[j].Text) {
					if err := l.read(args[j+1].Text, depth+1); err != nil {
						return err
					}
					break
				}
			}
		}
	}
	return nil
}

func (l *line) assign(word *shell.Word) {
	name, ok := shell.AssignName(word.Text)
	if !ok {
		return
	}
	value, ok := expand(strings.TrimPrefix(word.Raw, name+"="), l.vars)
	if !ok {
		return
	}
	l.vars[name] = value
}

// gh's -F is --body-file on pr, issue and release, and --field on api, where a file is
// only what `key=@path` points at. --input is a whole request body on api.
func (l *line) readBodyFiles(args []*shell.Word) {
	api := false
	for _, arg := range args {
		if !strings.HasPrefix(arg.Text, "-") {
			api = arg.Text == "api"
			break
		}
	}

	for i := 0; i < len(args); i++ {
		flag, _, _ := strings.Cut(args[i].Text, "=")
		field := api && (flag == "-F" || flag == "--field")
		if !field && flag != "--input" && !(!api && (flag == "-F" || flag == "--body-file")) {
			continue
		}

		value, next, ok := flagValue(args, i, l.vars)
		i = next
		if !ok {
			continue
		}
		if field {
			at := strings.Index(value, "=@")
			if at < 0 {
				continue
			}
			value = value[at+2:]
		}
		// `-` is stdin, which is the session's own and not a file.
		if value == "" || value == "-" {
			continue
		}
		l.bodyFiles = append(l.bodyFiles, value)
	}
}

// The value of the flag at i, written as `--flag=value` or as the word after it, and the
// index of the last word it took. A value that cannot be resolved still consumes its word,
// so the flag after it is read as a flag.
func flagValue(args []*shell.Word, i int, vars map[string]string) (string, int, bool) {
	if eq := strings.IndexByte(args[i].Text, '='); eq >= 0 {
		prefix := args[i].Text[:eq+1]
		value, ok := expand(args[i].Raw, vars)
		if !ok || !strings.HasPrefix(value, prefix) {
			return "", i, false
		}
		return value[len(prefix):], i, true
	}
	if i+1 >= len(args) {
		return "", i, false
	}
	value, ok := expand(args[i+1].Raw, vars)
	if ok {
		value = untilde(value, args[i+1].Raw)
	}
	return value, i + 1, ok
}

// A `~` the shell would have expanded: one that starts an unquoted word.
func untilde(value, raw string) string {
	if !strings.HasPrefix(value, "~/") || !strings.HasPrefix(raw, "~") {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return value
	}
	return filepath.Join(home, value[2:])
}

func isNameByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// expand takes the quotes off a word and puts in what the line assigned, and reports
// whether every part of it is known. A variable the line did not assign is unknown — the
// environment is not read, since a path this writes to is a path the command itself named
// and nothing else. Reading the word's source and not its text is what keeps `'$B'` a
// literal name, which is what the shell would pass gh.
func expand(raw string, vars map[string]string) (string, bool) {
	var out strings.Builder
	for i := 0; i < len(raw); {
		switch c := raw[i]; {
		case c == '\'':
			end := strings.IndexByte(raw[i+1:], '\'')
			if end < 0 {
				return "", false
			}
			out.WriteString(raw[i+1 : i+1+end])
			i += end + 2
		case c == '"':
			i++
			for i < len(raw) && raw[i] != '"' {
				switch {
				case raw[i] == '\\' && i+1 < len(raw):
					out.WriteByte(raw[i+1])
					i += 2
				case raw[i] == '$':
					value, next, ok := variable(raw, i, vars)
					if !ok {
						return "", false
					}
					out.WriteString(value)
					i = next
				default:
					out.WriteByte(raw[i])
					i++
				}
			}
			if i >= len(raw) {
				return "", false
			}
			i++
		case c == '\\':
			if i+1 >= len(raw) {
				return "", false
			}
			out.WriteByte(raw[i+1])
			i += 2
		case c == '$':
			value, next, ok := variable(raw, i, vars)
			if !ok {
				return "", false
			}
			out.WriteString(value)
			i = next
		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.String(), true
}

func variable(raw string, i int, vars map[string]string) (string, int, bool) {
	j := i + 1
	braced := j < len(raw) && raw[j] == '{'
	if braced {
		j++
	}
	start := j
	for j < len(raw) && isNameByte(raw[j]) {
		j++
	}
	name := raw[start:j]
	if braced {
		if j >= len(raw) || raw[j] != '}' {
			return "", 0, false
		}
		j++
	}
	value, ok := vars[name]
	if name == "" || !ok {
		return "", 0, false
	}
	return value, j, true
}

// git's own options come before the subcommand, and `-C <dir>`, `-c <k=v>` and the rest
// of them take the word after as a value rather than being one.
var gitValueOptions = map[string]bool{
	"-C": true, "-c": true, "--git-dir": true, "--work-tree": true, "--namespace": true,
	"--exec-path": true, "--super-prefix": true, "--config-env": true,
}

// An argument after these is a message or a file and not a flag, so `git commit -m "-n"`
// and `git commit -m "add a --no-verify guard"` are commits like any other.
var commitValueFlags = map[string]bool{"-m": true, "--message": true, "-F": true, "--file": true}

var pushValueFlags = map[string]bool{
	"--repo": true, "-o": true, "--push-option": true, "--receive-pack": true, "--exec": true,
}

func gitSkipsHooks(args []*shell.Word) bool {
	for i := 0; i < len(args); i++ {
		arg := args[i].Text
		if arg == "--" {
			return false
		}
		if !strings.HasPrefix(arg, "-") {
			switch arg {
			case "commit":
				return commitSkipsHooks(args[i+1:])
			case "push":
				return pushSkipsHooks(args[i+1:])
			}
			return false
		}
		if name, _, eq := strings.Cut(arg, "="); !eq && gitValueOptions[name] {
			i++
		}
	}
	return false
}

func commitSkipsHooks(args []*shell.Word) bool {
	for i := 0; i < len(args); i++ {
		arg := args[i].Text
		switch {
		case arg == "--":
			return false
		case strings.HasPrefix(arg, "--"):
			if name, _, _ := strings.Cut(arg, "="); name == "--no-verify" {
				return true
			}
			if commitValueFlags[arg] {
				i++
			}
		case strings.HasPrefix(arg, "-") && len(arg) > 1:
			// A cluster, where -n is --no-verify wherever it is written: -n, -qn, -nq.
			// The letters after an -m or an -F are that flag's value, not more flags.
			for k := 1; k < len(arg); k++ {
				if arg[k] == 'n' {
					return true
				}
				if commitValueFlags["-"+string(arg[k])] {
					if k == len(arg)-1 {
						i++
					}
					break
				}
				if !isNameByte(arg[k]) {
					break
				}
			}
		}
	}
	return false
}

// -n on push is --dry-run and runs no hooks at all, so only the long flag is refused.
func pushSkipsHooks(args []*shell.Word) bool {
	for i := 0; i < len(args); i++ {
		arg := args[i].Text
		if arg == "--" {
			return false
		}
		if arg == "--no-verify" {
			return true
		}
		if pushValueFlags[arg] {
			i++
		}
	}
	return false
}
