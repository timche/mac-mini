// Package logs is the one log every part of hachiko writes into: the LaunchAgent's
// stdout, read only when something is wrong. So a line is one thing that happened and
// nothing is said about a quiet check, and a secret that reached an error text on the way
// here is taken out before it is written down.
package logs

import (
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const logTime = "2006-01-02T15:04:05-0700"

// One line per thing that happened and nothing at all on a quiet check: this runs
// every five minutes forever, into a log somebody reads only when something is wrong.
//
// Name is what the line is attributed to, `hachiko` when it is empty. A command with a log
// of its own says so there, since `hachiko gc` and the watch write into two files and a
// line that named the binary would be the same word in both.
type Logger struct {
	Out  io.Writer
	Now  func() time.Time
	Name string
}

func (l Logger) Say(format string, args ...any) {
	name := l.Name
	if name == "" {
		name = "hachiko"
	}
	fmt.Fprintf(l.Out, "%s %s: %s\n", l.Now().Format(logTime), name, fmt.Sprintf(format, args...))
}

// What a secret is called instead of what it is. net/http names the URL it failed on, and
// an alert that could not be sent is logged where everything else is — so every spelling
// of it a message could carry goes. A *url.Error prints the target through %q, which
// escapes a newline or a byte outside ASCII and so spells the same URL differently;
// net/url hands back the percent-escaped forms. Longest first, so a prefix of one does not
// break the match for another.
func Redact(text, secret, name string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return text
	}

	quoted := strconv.Quote(secret)
	forms := []string{
		secret,
		quoted[1 : len(quoted)-1],
		url.QueryEscape(secret),
		url.PathEscape(secret),
	}
	sort.Slice(forms, func(i, j int) bool { return len(forms[i]) > len(forms[j]) })

	for _, form := range forms {
		if form != "" {
			text = strings.ReplaceAll(text, form, name)
		}
	}
	return text
}
