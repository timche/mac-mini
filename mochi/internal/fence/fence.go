// Package fence is how anything written in Discord reaches a session: between two markers
// carrying a nonce only that prompt knows, so a message reading "ignore your orders" is
// data rather than a turn in the conversation. The same logic as hachiko's on-call prompt,
// copied rather than imported because the two tools are separate modules.
package fence

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

// Every label any mochi prompt uses, so a message quoting one block's marker cannot close
// another.
const (
	Instruction = "INSTRUCTION FROM TIM"
	Context     = "DISCORD CONTEXT"
	History     = "DISCORD HISTORY"
	Project     = "PROJECT INSTRUCTIONS (from the repository's .mochi/instructions.md)"

	// A repository with no .mochi/instructions.md yet, whose MOCHI.md stands in for it.
	LegacyProject = "PROJECT INSTRUCTIONS (from the repository's MOCHI.md)"
)

var labels = []string{Instruction, Context, History, Project, LegacyProject}

func Nonce() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "0000000000000000"
	}
	return hex.EncodeToString(b)
}

func Begin(label, nonce string) string { return "----- BEGIN " + label + " " + nonce + " -----" }
func End(label, nonce string) string   { return "----- END " + label + " " + nonce + " -----" }

func Block(label, nonce, data string) string {
	return Begin(label, nonce) + "\n" + Data(data, nonce) + "\n" + End(label, nonce)
}

// Trusted is a block of the repository's own text, not Discord's: its backticks stay, since
// its commands are meant to be run, but nothing in it can close or open another block.
func Trusted(label, nonce, data string) string {
	data = strings.ReplaceAll(Clean(data), nonce, "<nonce>")
	for _, marker := range labels {
		data = strings.ReplaceAll(data, "----- BEGIN "+marker, "- BEGIN "+marker)
		data = strings.ReplaceAll(data, "----- END "+marker, "- END "+marker)
	}
	return Begin(label, nonce) + "\n" + strings.TrimRight(data, "\n") + "\n" + End(label, nonce)
}

// Backticks go because a session quotes this into shell commands, and anything resembling
// a marker goes because the markers are the whole of the boundary.
func Data(data, nonce string) string {
	data = Clean(data)
	data = strings.ReplaceAll(data, "`", "'")
	data = strings.ReplaceAll(data, nonce, "<nonce>")
	for _, marker := range labels {
		data = strings.ReplaceAll(data, "----- BEGIN "+marker, "- BEGIN "+marker)
		data = strings.ReplaceAll(data, "----- END "+marker, "- END "+marker)
	}
	return strings.TrimRight(data, "\n")
}

// herdr hands a prompt to Claude Code as a bracketed paste, so an escape sequence in it can
// end the paste and type whatever follows: every control character goes but the newline and
// the tab, and so do the two Unicode line separators a terminal may treat as one.
func Clean(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f, r == '\u2028', r == '\u2029':
			return -1
		}
		return r
	}, s)
}
