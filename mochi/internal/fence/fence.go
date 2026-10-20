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
)

var labels = []string{Instruction, Context, History}

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

// Backticks go because a session quotes this into shell commands, and anything resembling
// a marker goes because the markers are the whole of the boundary.
func Data(data, nonce string) string {
	data = strings.ReplaceAll(data, "`", "'")
	data = strings.ReplaceAll(data, nonce, "<nonce>")
	for _, marker := range labels {
		data = strings.ReplaceAll(data, "----- BEGIN "+marker, "- BEGIN "+marker)
		data = strings.ReplaceAll(data, "----- END "+marker, "- END "+marker)
	}
	return strings.TrimRight(data, "\n")
}
