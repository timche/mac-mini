// Package focus refuses a command that takes the Mac's one foreground without the lock
// that serialises it.
//
// The whole GUI session has one frontmost app, one key window and one pointer, and many
// Claude sessions run in it at once: a System Events keystroke goes to whatever is in
// front and a cliclick clicks whatever is under the pointer, so two sessions doing either
// at the same time type and click into each other's apps. `focus-lock <command>` is what
// makes a sequence wait for another session's, so a command that needs the foreground is
// allowed under it and refused outside it. Everything an app can be reached by without the
// foreground — CDP, accessibility actions, UTM's AppleScript input, VNC — needs no lock
// and is left alone, which is why this reads what an osascript script does rather than
// refusing osascript.
package focus

import (
	"errors"
	"os"
	"strings"

	"github.com/timche/mac-mini/claude-hooks/internal/shell"
)

const Reason = "This takes the Mac's one foreground (frontmost app, key window, pointer), which every " +
	"Claude session shares, so run it as one command under `focus-lock <command>`, keeping it " +
	"short, or reach the app without the foreground (CDP, accessibility actions, UTM's " +
	"AppleScript input, VNC). $PROJECT_DOCS_DIR/shared/macos-gui-automation.md has which call " +
	"is which."

var denied = errors.New("taking the Mac's one foreground")

// A script osascript is handed as a file is read, and a file this size is not a script.
const maxScript = 1 << 20

// Denies reports whether a command takes the foreground outside focus-lock. A command it
// cannot read goes through, as the kill guard's does: a guard that refused what it could
// not parse would cost every session its shell.
func Denies(command string) bool {
	return errors.Is(check(command, 0), denied)
}

func check(command string, depth int) error {
	if depth > shell.MaxDepth {
		return shell.ErrParse
	}
	tokens, err := shell.Lex(command)
	if err != nil {
		return err
	}
	for _, part := range shell.Segments(tokens) {
		if err := checkSegment(part.Words, depth); err != nil {
			return err
		}
	}
	return nil
}

func checkSegment(words []*shell.Word, depth int) error {
	at := shell.CommandOf(words)
	if at < 0 {
		return checkSubs(words, depth)
	}
	name, args := shell.Base(words[at].Text), words[at+1:]

	// The lock itself, with the command it holds the foreground for.
	if name == "focus-lock" {
		return nil
	}
	// `op run --env-file x -- <command>` runs that command with the secrets resolved, so
	// what it runs is what matters and `op run` itself is nothing.
	if name == "op" {
		if tail, ok := opRunTail(args); ok {
			return checkSegment(tail, depth)
		}
		return checkSubs(words, depth)
	}

	if err := checkSubs(words, depth); err != nil {
		return err
	}

	switch {
	case name == "cliclick":
		if !onlyAsks(args) {
			return denied
		}
	case name == "osascript":
		if script, ok := scriptOf(args); ok && foreground(script) {
			return denied
		}
	case shell.Shells[name]:
		for j := 0; j+1 < len(args); j++ {
			if shell.IsDashC(args[j].Text) {
				return check(args[j+1].Text, depth+1)
			}
		}
	}
	return nil
}

func checkSubs(words []*shell.Word, depth int) error {
	for _, word := range words {
		for _, sub := range word.Subs {
			if err := check(sub, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// What cliclick is asked that moves no pointer.
func onlyAsks(args []*shell.Word) bool {
	for _, arg := range args {
		switch arg.Text {
		case "-h", "--help", "-V":
			return true
		}
	}
	return false
}

func opRunTail(args []*shell.Word) ([]*shell.Word, bool) {
	run := false
	for i, arg := range args {
		switch {
		case arg.Text == "run":
			run = true
		case run && arg.Text == "--":
			return args[i+1:], true
		}
	}
	return nil, false
}

// The script osascript runs: its -e statements as one script, or the contents of the
// script file it was given, when that is a file here and readable as text.
func scriptOf(args []*shell.Word) (string, bool) {
	var statements []string
	file := ""

	for i := 0; i < len(args); i++ {
		arg := args[i].Text
		switch {
		case arg == "-e":
			if i+1 < len(args) {
				statements = append(statements, args[i+1].Text)
				i++
			}
		case arg == "-l" || arg == "-s":
			i++
		case arg == "--":
			if i+1 < len(args) && file == "" {
				file = args[i+1].Text
			}
			i = len(args)
		case len(arg) > 1 && arg[0] == '-':
		case file == "":
			file = arg
		}
	}

	if len(statements) > 0 {
		return strings.Join(statements, "\n"), true
	}
	if file == "" {
		return "", false
	}
	info, err := os.Stat(file)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxScript {
		return "", false
	}
	body, err := os.ReadFile(file)
	if err != nil || strings.ContainsRune(string(body), 0) {
		return "", false
	}
	return string(body), true
}

// Whether a script does something only the frontmost app can do. Reading the foreground
// (`get frontmost`, `whose frontmost is true`), pressing a control through accessibility,
// asking a window where it is, and UTM's input into a guest all reach an app that is not
// in front, and none of them is here.
func foreground(script string) bool {
	s := lower(script)

	// UTM's `input keystroke` goes to a guest and not to whatever is in front.
	for _, keyword := range []string{"keystroke", "key"} {
		for _, at := range words(s, keyword) {
			if keyword == "key" && nextWord(s, at+len(keyword)) != "code" {
				continue
			}
			if prevWord(s, at) == "input" {
				continue
			}
			return true
		}
	}

	// Setting the frontmost app raises it; reading it does not.
	for _, at := range words(s, "frontmost") {
		if prevWord(s, at) == "set" {
			return true
		}
	}

	if strings.Contains(s, "axraise") {
		return true
	}

	// `click at {x, y}` clicks the screen, while UTM's `mouse click … at` clicks a guest.
	for _, at := range words(s, "click") {
		if nextWord(s, at+len("click")) != "at" {
			continue
		}
		if prevWord(s, at) != "mouse" {
			return true
		}
	}

	// activate as a command raises its app. Anywhere else the word is a property's name.
	for _, at := range words(s, "activate") {
		if prevWord(s, at) == "to" || nextWord(s, at+len("activate")) == "application" ||
			strings.TrimSpace(lineAt(s, at)) == "activate" {
			return true
		}
	}

	// A shell command the script runs is still a command, and cliclick in one is a click.
	if rest := after(s, "do shell script"); rest != "" && strings.Contains(rest, "cliclick") {
		return true
	}
	return false
}

func after(s, keyword string) string {
	if at := strings.Index(s, keyword); at >= 0 {
		return s[at+len(keyword):]
	}
	return ""
}

// Where in s the word appears, whole rather than inside a longer name.
func words(s, word string) []int {
	var out []int
	for i := 0; i+len(word) <= len(s); {
		at := strings.Index(s[i:], word)
		if at < 0 {
			break
		}
		at += i
		before := at == 0 || !isWordByte(s[at-1])
		after := at+len(word) == len(s) || !isWordByte(s[at+len(word)])
		if before && after {
			out = append(out, at)
		}
		i = at + 1
	}
	return out
}

func prevWord(s string, at int) string {
	i := at
	for i > 0 && isSpaceByte(s[i-1]) {
		i--
	}
	j := i
	for j > 0 && isWordByte(s[j-1]) {
		j--
	}
	return s[j:i]
}

func nextWord(s string, from int) string {
	i := from
	for i < len(s) && isSpaceByte(s[i]) {
		i++
	}
	j := i
	for j < len(s) && isWordByte(s[j]) {
		j++
	}
	return s[i:j]
}

func lineAt(s string, at int) string {
	start := strings.LastIndexByte(s[:at], '\n') + 1
	end := strings.IndexByte(s[at:], '\n')
	if end < 0 {
		return s[start:]
	}
	return s[start : at+end]
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func isWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// Lowercase for the ASCII a script's keywords are written in, and byte for byte, so an
// offset into the result is an offset into the script.
func lower(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'A' && c <= 'Z' {
			out[i] = c + 'a' - 'A'
		}
	}
	return string(out)
}
