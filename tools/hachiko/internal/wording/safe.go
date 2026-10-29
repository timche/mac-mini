package wording

import (
	"strings"
	"unicode/utf8"
)

// Every string in a message or a prompt that something other than hachiko chose: a
// path a worker made up, a process name, a command line. Discord caps a message
// at 2,000 characters, and one of these at a megabyte would be the whole of it.
const (
	PathLimit     = 200
	ArgsLimit     = 200
	FallbackLimit = 200

	// An account name, which is the one of these macOS itself keeps short.
	UserLimit = 64

	// A command's own output, which has a block of its own and so more of the message.
	OutputLimit = 600
)

// A byte count, because what the limits are protecting is Discord's own and a prompt's own,
// both of which count bytes. The cut falls on a rune boundary all the same: a path is a
// string of bytes macOS makes no promises about, and half of a multi-byte character is a
// replacement glyph in a message and invalid JSON on the way to one. Trailing bytes that
// were never a character to begin with go the same way.
func Clip(s string, max int) string {
	if len(s) <= max {
		return s
	}

	cut := s[:max]
	for len(cut) > 0 {
		if r, size := utf8.DecodeLastRuneInString(cut); r != utf8.RuneError || size > 1 {
			break
		}
		cut = cut[:len(cut)-1]
	}
	return cut + "..."
}

// The same, for a string that goes anywhere near a prompt. A path may hold a newline, and
// a newline is how a line of data becomes a line of conversation — so a name chosen by
// whatever filled the disk is one line before it is clipped to one length. Every other
// control character goes with it: none of them says anything about a file, and all of them
// can make a message read as something it is not.
func Safe(s string, max int) string {
	clean := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '\t' || (r >= 0x20 && r != 0x7f) {
			clean = append(clean, r)
		} else {
			clean = append(clean, ' ')
		}
	}
	return Clip(strings.TrimSpace(string(clean)), max)
}

// The same for a fenced block, which keeps its line breaks: inside a fence a newline is
// only a line, so the one thing that could end the block early is a run of backticks, and
// a zero-width space between every two of them leaves none.
func CodeBlock(s string, max int) string {
	clean := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '\n' || r == '\t' || (r >= 0x20 && r != 0x7f) {
			clean = append(clean, r)
		} else {
			clean = append(clean, ' ')
		}
	}

	lines := strings.Split(string(clean), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	body := Clip(strings.Trim(strings.Join(lines, "\n"), "\n"), max)
	if strings.TrimSpace(body) == "" {
		return ""
	}

	for strings.Contains(body, "``") {
		body = strings.ReplaceAll(body, "``", "`\u200b`")
	}
	return "```\n" + body + "\n```"
}

// A reply is at most this long by the time it reaches the agent. It is Tim's own text
// rather than an incident's, but it is still a string from the network going into a
// prompt.
const ReplyLimit = 1000
