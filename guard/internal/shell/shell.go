// Package shell reads a command line the way Bash splits it: the words of each segment
// with their quotes taken off, every command substitution kept apart as the command it
// runs, and what a redirect feeds a segment from. It is a reader and not an interpreter —
// it expands no variable, globs nothing and runs nothing — and it exists so that a check
// on what a command runs can tell a command from text that only looks like one: a pkill
// in a commit message, a grep pattern or a heredoc body is an argument and goes through.
//
// Heredoc bodies and comments are dropped, neither being run as a command.
package shell

import (
	"errors"
	"strings"
)

// ErrParse is every way of not being able to read a command: an unterminated quote, an
// unclosed substitution, a nesting deeper than anyone writes. A guard on every session's
// shell that refused what it could not read would cost them all their shell, so a caller
// turns this into "allowed" and never into a refusal.
var ErrParse = errors.New("the command cannot be read the way the shell would split it")

// A substitution inside a substitution inside a substitution recurses, and Go answers a
// runaway nesting with a stack overflow that exits 2 — which to a PreToolUse hook means
// "block this command", the one thing a guard that cannot read its input must not do. So
// the depth is counted and a command nested past anything a person writes is unreadable
// instead.
const MaxDepth = 64

// A word of a command, its text as the shell would hand it to the command, the source it
// came from, and each command substitution in it as the command that substitution runs.
// The source matters because `$pid` and `"$pid"` name a variable while the text of either
// is whatever it holds, which is nothing here.
type Word struct {
	Text string
	Raw  string
	Subs []string
}

// What a segment reads from besides its arguments: a process substitution, or the
// substitutions in a redirect's target such as a here-string.
type Feed struct {
	Subs []string
}

type Token struct {
	Word *Word
	Feed *Feed
	Op   string
}

// One command of a line, the operator that ended it, and what it reads from. The operator
// is empty for the last segment, which nothing ended.
type Segment struct {
	Words []*Word
	End   string
	Feeds []*Feed
}

var Shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true}

// Commands that run the command after them, each with the flags of its own that take a
// value, since `sudo -u x pkill` runs pkill and not x.
var prefixes = map[string]map[string]bool{
	"sudo":       set("-u", "-g", "-C", "-D", "-h", "-p", "-r", "-t", "-U", "-T"),
	"doas":       set("-u", "-C"),
	"env":        set("-u", "-C", "-P", "-S"),
	"xargs":      set("-I", "-n", "-P", "-L", "-l", "-d", "-E", "-e", "-s", "-a", "-R", "-S", "-J"),
	"nohup":      set(),
	"time":       set(),
	"command":    set(),
	"builtin":    set(),
	"exec":       set("-a"),
	"nice":       set("-n"),
	"timeout":    set("-s", "-k", "--signal", "--kill-after"),
	"caffeinate": set("-t", "-w"),
	"stdbuf":     set(),
}

// Claude Code takes these off a command before it matches a hook's `if` rule (its
// permissions docs, under Wrappers), so the rule for the command they run covers them. Bare
// xargs is taken off too, but not one with flags, so xargs is not here.
var strippedByClaudeCode = set("timeout", "time", "nice", "nohup", "stdbuf", "command", "builtin", "noglob")

// Runners are the commands this package reads through to the command they run and Claude
// Code's `if` matching does not, so each needs a rule of its own for the hook to see what
// they run.
func Runners() []string {
	var names []string
	for name := range prefixes {
		if !strippedByClaudeCode[name] {
			names = append(names, name)
		}
	}
	for name := range Shells {
		names = append(names, name)
	}
	return names
}

var keywords = set("do", "then", "else", "elif", "!", "{", "while", "until", "if")
var openers = set("while", "until", "if", "for", "select", "case", "{")
var closers = set("done", "fi", "esac", "}")

var operators = []string{"&&", "||", "|&", ";;", "|", ";", "&", "(", ")", "\n"}
var redirects = []string{"&>>", "&>", "<<<", "<<-", "<<", "<>", "<&", ">&", ">>", ">|", "<", ">"}

var heredocQuotes = strings.NewReplacer("'", "", `"`, "", `\`, "")

func set(names ...string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = true
	}
	return out
}

func IsOpener(word string) bool { return openers[word] }
func IsCloser(word string) bool { return closers[word] }
func IsPipe(op string) bool     { return op == "|" || op == "|&" }

// The command of a path, with none of filepath.Base's answers for an empty word or a
// trailing slash: `/usr/bin/pkill` is pkill wherever it was spelled from.
func Base(word string) string {
	if i := strings.LastIndexByte(word, '/'); i >= 0 {
		return word[i+1:]
	}
	return word
}

func isWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// Whether a word is an assignment, and the name it assigns to.
func AssignName(text string) (string, bool) {
	for i := 0; i < len(text); i++ {
		if text[i] == '=' {
			return text[:i], i > 0
		}
		if !isWordByte(text[i]) {
			return "", false
		}
	}
	return "", false
}

// The variable a word names when the word is nothing but that variable: `$pid`, `"$pid"`,
// `${pid}` or `"${pid}"`. Anything around it and the word is no longer a bare pid.
func PlainVar(raw string) (string, bool) {
	s := strings.TrimPrefix(raw, `"`)
	s = strings.TrimSuffix(s, `"`)
	if !strings.HasPrefix(s, "$") {
		return "", false
	}
	s = s[1:]
	if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") && len(s) >= 2 {
		s = s[1 : len(s)-1]
	}
	if s == "" {
		return "", false
	}
	for i := 0; i < len(s); i++ {
		if !isWordByte(s[i]) {
			return "", false
		}
	}
	return s, true
}

// A shell's flag that takes the next argument as a command: `-c`, and the combinations it
// is written in such as `-lc` or `-ec`.
func IsDashC(arg string) bool {
	if !strings.HasPrefix(arg, "-") {
		return false
	}
	found := false
	for i := 1; i < len(arg); i++ {
		if !isWordByte(arg[i]) {
			return false
		}
		if arg[i] == 'c' {
			found = true
		}
	}
	return found
}

// Whether a flag carries a letter, bundled with others or on its own, so that `-9k` asks
// fuser for the same thing `-k` does.
func HasFlagLetter(arg string, letter byte) bool {
	if !strings.HasPrefix(arg, "-") {
		return false
	}
	for i := 1; i < len(arg) && isWordByte(arg[i]); i++ {
		if arg[i] == letter {
			return true
		}
	}
	return false
}

// The command a segment runs, past assignments, shell keywords and the prefixes that run
// another command, or -1 when the segment runs nothing.
func CommandOf(words []*Word) int {
	i := 0
	for i < len(words) {
		word := words[i].Text
		if _, ok := AssignName(word); ok || keywords[word] {
			i++
			continue
		}
		name := Base(word)
		valueFlags, isPrefix := prefixes[name]
		if !isPrefix {
			return i
		}
		// `command -v pkill` asks where pkill is and runs nothing.
		if name == "command" && i+1 < len(words) &&
			(words[i+1].Text == "-v" || words[i+1].Text == "-V") {
			return -1
		}
		i++
		for i < len(words) {
			arg := words[i].Text
			if _, ok := AssignName(arg); name == "env" && ok {
				i++
			} else if strings.HasPrefix(arg, "-") && len(arg) > 1 {
				if valueFlags[arg] {
					i += 2
				} else {
					i++
				}
			} else {
				break
			}
		}
		// The duration, which is an argument of timeout's and not a command.
		if name == "timeout" && i < len(words) {
			i++
		}
	}
	return -1
}

func Segments(tokens []Token) []Segment {
	out := []Segment{}
	var words []*Word
	var feeds []*Feed
	for _, token := range tokens {
		switch {
		case token.Word != nil:
			words = append(words, token.Word)
		case token.Feed != nil:
			feeds = append(feeds, token.Feed)
		default:
			out = append(out, Segment{Words: words, End: token.Op, Feeds: feeds})
			words, feeds = nil, nil
		}
	}
	return append(out, Segment{Words: words, Feeds: feeds})
}

func byteAt(s string, i int) (byte, error) {
	if i < 0 || i >= len(s) {
		return 0, ErrParse
	}
	return s[i], nil
}

func indexFrom(s string, b byte, from int) (int, error) {
	if from < 0 || from > len(s) {
		return 0, ErrParse
	}
	j := strings.IndexByte(s[from:], b)
	if j < 0 {
		return 0, ErrParse
	}
	return from + j, nil
}

// The `)` that closes the `(` at i, with quotes and nested parentheses counted.
func closingParen(s string, i int) (int, error) {
	depth := 0
	for i < len(s) {
		c := s[i]
		if c == '\\' {
			i += 2
			continue
		}
		switch c {
		case '\'':
			end, err := indexFrom(s, '\'', i+1)
			if err != nil {
				return 0, err
			}
			i = end
		case '"':
			i++
			for {
				c, err := byteAt(s, i)
				if err != nil {
					return 0, err
				}
				if c == '"' {
					break
				}
				if c == '\\' {
					i += 2
				} else {
					i++
				}
			}
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i, nil
			}
		}
		i++
	}
	return 0, ErrParse
}

func closingBacktick(s string, i int) (int, error) {
	i++
	for {
		c, err := byteAt(s, i)
		if err != nil {
			return 0, err
		}
		if c == '`' {
			return i, nil
		}
		if c == '\\' {
			i += 2
		} else {
			i++
		}
	}
}

func wordEnd(s string, i int) (int, error) {
	for i < len(s) && !strings.ContainsRune(" \t\n;&|()<>", rune(s[i])) {
		switch {
		case strings.HasPrefix(s[i:], "$("):
			end, err := closingParen(s, i+1)
			if err != nil {
				return 0, err
			}
			i = end
		case s[i] == '`':
			end, err := closingBacktick(s, i)
			if err != nil {
				return 0, err
			}
			i = end
		case s[i] == '\'':
			end, err := indexFrom(s, '\'', i+1)
			if err != nil {
				return 0, err
			}
			i = end
		case s[i] == '"':
			i++
			for {
				c, err := byteAt(s, i)
				if err != nil {
					return 0, err
				}
				if c == '"' {
					break
				}
				if strings.HasPrefix(s[i:], "$(") {
					end, err := closingParen(s, i+1)
					if err != nil {
						return 0, err
					}
					i = end
				}
				if s[i] == '\\' {
					i += 2
				} else {
					i++
				}
			}
		case s[i] == '\\':
			i++
		}
		i++
	}
	return i, nil
}

func match(s string, from int, candidates []string) string {
	for _, candidate := range candidates {
		if strings.HasPrefix(s[from:], candidate) {
			return candidate
		}
	}
	return ""
}

func allDigits(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

type heredoc struct {
	delimiter string
	tabs      bool
}

// Lex splits a command into its words, its operators and what feeds it.
func Lex(s string) ([]Token, error) {
	var tokens []Token
	var text []byte
	var subs []string
	var heredocs []heredoc
	inWord, start := false, 0
	i, n := 0, len(s)

	flush := func() {
		if inWord {
			end := i
			if end > n {
				end = n
			}
			tokens = append(tokens, Token{Word: &Word{Text: string(text), Raw: s[start:end], Subs: subs}})
		}
		text, subs, inWord = nil, nil, false
	}
	begin := func() {
		if !inWord {
			text, subs, inWord, start = nil, nil, true, i
		}
	}

	for i < n {
		c := s[i]

		if !inWord && c == '#' {
			for i < n && s[i] != '\n' {
				i++
			}
			continue
		}
		if c == ' ' || c == '\t' {
			flush()
			i++
			continue
		}
		if c == '\\' && i+1 < n {
			begin()
			if s[i+1] != '\n' {
				text = append(text, s[i+1])
			}
			i += 2
			continue
		}
		if c == '\'' {
			begin()
			end, err := indexFrom(s, '\'', i+1)
			if err != nil {
				return nil, err
			}
			text = append(text, s[i+1:end]...)
			i = end + 1
			continue
		}
		if c == '"' {
			begin()
			i++
			for {
				c, err := byteAt(s, i)
				if err != nil {
					return nil, err
				}
				if c == '"' {
					break
				}
				switch {
				case c == '\\':
					next, err := byteAt(s, i+1)
					if err != nil {
						return nil, err
					}
					text = append(text, next)
					i += 2
				case strings.HasPrefix(s[i:], "$("):
					end, err := closingParen(s, i+1)
					if err != nil {
						return nil, err
					}
					subs = append(subs, s[i+2:end])
					text = append(text, s[i:end+1]...)
					i = end + 1
				case c == '`':
					end, err := closingBacktick(s, i)
					if err != nil {
						return nil, err
					}
					subs = append(subs, s[i+1:end])
					text = append(text, s[i:end+1]...)
					i = end + 1
				default:
					text = append(text, c)
					i++
				}
			}
			i++
			continue
		}
		if strings.HasPrefix(s[i:], "$(") {
			begin()
			end, err := closingParen(s, i+1)
			if err != nil {
				return nil, err
			}
			subs = append(subs, s[i+2:end])
			text = append(text, s[i:end+1]...)
			i = end + 1
			continue
		}
		if c == '`' {
			begin()
			end, err := closingBacktick(s, i)
			if err != nil {
				return nil, err
			}
			subs = append(subs, s[i+1:end])
			text = append(text, s[i:end+1]...)
			i = end + 1
			continue
		}
		if !inWord && (strings.HasPrefix(s[i:], "<(") || strings.HasPrefix(s[i:], ">(")) {
			end, err := closingParen(s, i+1)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, Token{Feed: &Feed{Subs: []string{s[i+2 : end]}}})
			i = end + 1
			continue
		}
		if c == '<' || c == '>' || strings.HasPrefix(s[i:], "&>") {
			// A redirect's fd (`2>`) is not an argument, and nor is what it points at.
			if inWord && allDigits(text) {
				text, subs, inWord = nil, nil, false
			}
			flush()
			op := match(s, i, redirects)
			i += len(op)
			for i < n && (s[i] == ' ' || s[i] == '\t') {
				i++
			}
			if strings.HasPrefix(s[i:], "<(") {
				continue
			}
			end, err := wordEnd(s, i)
			if err != nil {
				return nil, err
			}
			target := s[i:end]
			switch {
			case op == "<<" || op == "<<-":
				heredocs = append(heredocs, heredoc{heredocQuotes.Replace(target), op == "<<-"})
			case strings.Contains(target, "$(") || strings.Contains(target, "`"):
				inner, err := Lex(target)
				if err != nil {
					return nil, err
				}
				feed := &Feed{}
				for _, token := range inner {
					if token.Word != nil {
						feed.Subs = append(feed.Subs, token.Word.Subs...)
					}
				}
				tokens = append(tokens, Token{Feed: feed})
			}
			i = end
			continue
		}
		if op := match(s, i, operators); op != "" {
			flush()
			tokens = append(tokens, Token{Op: op})
			i += len(op)
			if op == "\n" && len(heredocs) > 0 {
				for _, doc := range heredocs {
					for i < n {
						line := ""
						if end := strings.IndexByte(s[i:], '\n'); end < 0 {
							line, i = s[i:], n+1
						} else {
							line, i = s[i:i+end], i+end+1
						}
						if doc.tabs {
							line = strings.TrimLeft(line, "\t")
						}
						if line == doc.delimiter {
							break
						}
					}
				}
				heredocs = nil
			}
			continue
		}
		begin()
		text = append(text, c)
		i++
	}
	flush()
	return tokens, nil
}
