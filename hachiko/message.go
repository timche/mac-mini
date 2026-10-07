package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Every message hachiko sends has one shape, and this file is the whole of it: a lead
// line Tim can read off a locked phone, bold-labelled detail lines under it in a fixed
// order, one line of what he can do, and a subtext line of machine detail he does not
// act on. shibuya writes the same shape in TypeScript, so a change to the style here is
// a change there too.
//
// The lead is also the forum post title — threadName takes the first line — which is why
// it carries no code span, no incident id and no full stop.

// One marker per message and only at the start of the lead. Six, because a glance at a
// notification has to say which of these it is before anything else is read.
const (
	markerDown      = "🔴"
	markerRecovered = "🟢"
	markerDegraded  = "⚠️"
	markerInfo      = "ℹ️"
	markerDisk      = "💾"
	markerBusy      = "🔥"
)

// The labels a group of findings goes under, so the same wording reaches a first alert,
// a reminder three hours later and the prompt that hands the decision over.
const (
	labelGrowing   = "Growing fast"
	labelBusy      = "Busy processes"
	labelFreeSpace = "Free space"
	labelEmptied   = "Emptied to keep the Mac going"
	labelNow       = "Now"
	labelWaiting   = "Waiting"
	labelWhy       = "Why"
	labelFallback  = "If no answer"
)

// A headline rather than a sentence of prose, and short enough to survive a lock screen
// and Discord's 100-character post title both.
const leadLimit = 90

// A process or file name in a lead, which has the rest of the lead to share the limit
// above with.
const nameLimit = 48

// message is one Discord message under construction. Nothing here joins with a blank
// line except the one before the action, and nothing leaves a trailing one.
type message struct {
	lead    string
	blocks  []string
	action  string
	subtext string
}

func lead(marker, sentence string) *message {
	return &message{lead: marker + " " + clip(strings.TrimSpace(sentence), leadLimit)}
}

// A `**Label:** value` line, dropped when there is nothing to say about it: a message is
// only the findings there were, not a form with blanks in it.
func (m *message) field(label, value string) *message {
	if value == "" {
		return m
	}
	m.blocks = append(m.blocks, "**"+label+":** "+value)
	return m
}

func (m *message) bullets(label string, items []string) *message {
	return m.block(section(label, items))
}

// A section built elsewhere — the disk and CPU findings are assembled once per sweep and
// reach several messages.
func (m *message) block(text string) *message {
	if text != "" {
		m.blocks = append(m.blocks, text)
	}
	return m
}

func (m *message) can(action string) *message {
	m.action = action
	return m
}

// The last line, as Discord subtext: the incident id and the machine, which are what a
// later question needs and what Tim never acts on.
func (m *message) about(incident, host string) *message {
	var parts []string
	if incident != "" {
		parts = append(parts, "Incident "+safe(incident, nameLimit))
	}
	if host != "" {
		parts = append(parts, safe(host, nameLimit))
	}
	if len(parts) > 0 {
		m.subtext = "-# " + strings.Join(parts, " · ")
	}
	return m
}

func (m *message) String() string {
	out := append([]string{m.lead}, m.blocks...)

	var tail []string
	if m.action != "" {
		tail = append(tail, m.action)
	}
	if m.subtext != "" {
		tail = append(tail, m.subtext)
	}
	if len(tail) > 0 {
		out = append(out, "")
		out = append(out, tail...)
	}
	return strings.Join(out, "\n")
}

// Several findings of one kind: the label on its own line and one bullet each, since a
// list of three files on one line is a line nobody reads to the end of. An item may carry
// its own indented continuation line, which is how a command line gets a line of its own
// without becoming a bullet of its own.
func section(label string, items []string) string {
	if len(items) == 0 {
		return ""
	}
	return "**" + label + ":**\n- " + strings.Join(items, "\n- ")
}

// Sizes read as the numbers Tim would say out loud: one decimal of a gigabyte, whole
// megabytes under one, and no trailing zero on either.
func gbUnit(kb int64) string {
	if kb < 0 {
		kb = 0
	}
	if kb < gib {
		if mb := (kb + 512) / 1024; mb < 1024 {
			return fmt.Sprintf("%d MB", mb)
		}
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(kb)/gib), ".0") + " GB"
}

// What says whether a file is a nuisance or an emergency, in whole units because the
// number is an extrapolation from one interval and "about" is the honest word for it.
func ratePhrase(grewKB int64, span time.Duration) string {
	seconds := span.Seconds()
	if seconds <= 0 {
		seconds = 1
	}

	perHour := float64(grewKB) * 3600 / seconds
	if perHour < gib {
		return fmt.Sprintf("about %.0f MB an hour", perHour/1024)
	}
	return fmt.Sprintf("about %.0f GB an hour", perHour/gib)
}

// Never 1h05m in anything Tim reads: a message at four in the morning is read once. Two
// units at most, and the minutes go once there are days — a Mac that has been off since
// Friday is not news by the minute, and "51 hours" is a number he would have to divide.
//
// shibuya's `duration` is this function in TypeScript, so a change to either is a change to
// both: the two write into one channel and a Mac that has been quiet for two days has to
// read the same whichever of them says so.
func durationPhrase(d time.Duration) string {
	minutes := int64(d.Minutes())
	if minutes < 1 {
		return "less than a minute"
	}
	if minutes < 60 {
		return countOf(minutes, "minute", "minutes")
	}

	hours := minutes / 60
	if hours >= 48 {
		return twoUnits(hours/24, "day", "days", hours%24, "hour", "hours")
	}
	return twoUnits(hours, "hour", "hours", minutes%60, "minute", "minutes")
}

func twoUnits(big int64, bigOne, bigMany string, small int64, smallOne, smallMany string) string {
	if small == 0 {
		return countOf(big, bigOne, bigMany)
	}
	return countOf(big, bigOne, bigMany) + " " + countOf(small, smallOne, smallMany)
}

// Both spellings rather than an appended "s", because that is what gives "2 processs", and
// shibuya's own `plural` takes both for the same reason.
func countOf(n int64, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// A clock time is enough for today, and anything older needs the day as well — a process
// that started "at 20:06" is a different thing from one that started last Tuesday.
func timePhrase(at, now time.Time) string {
	at = at.In(now.Location())
	clock := at.Format("15:04")

	if sameDay(at, now) {
		return "at " + clock
	}
	if sameDay(at, now.AddDate(0, 0, -1)) {
		return "yesterday at " + clock
	}
	return "on " + at.Format("Mon 2 Jan") + " at " + clock
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// Name first and the pid in parentheses, because the name is what Tim recognises and the
// pid is what he would type.
func processLabel(name string, pid int) string { return pidLabel(name, strconv.Itoa(pid)) }

// The same for a pid that arrived as text, which is how lsof prints one.
//
// The name is escaped here rather than at each place one of these labels is written, so a
// label is message-safe by construction: the parentheses are hachiko's own and stay as they
// are, and a command called `**node**` cannot take the rest of the line with it.
func pidLabel(name, pid string) string { return plainWords(name) + " (pid " + pid + ")" }

// A path, a command line or a label in an inline code span, so Discord renders none of
// the markdown in it. The fence is one backtick longer than the longest run inside, and
// content holding a backtick is padded with spaces — which together are the whole of why
// a file named "a`b" cannot break out of the span and style the rest of the message.
//
// Everything reaching this has been through safe() first: a span is no protection against
// a newline, which ends it whatever the fence is.
func codeSpan(s string) string {
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
			continue
		}
		run = 0
	}

	fence := strings.Repeat("`", longest+1)
	if longest > 0 {
		return fence + " " + s + " " + fence
	}
	return fence + s + fence
}

// The file a lead names, by its base name: the whole path is in the detail below, and a
// lead is read at a glance.
func baseLabel(path string) string { return plainWords(clip(filepath.Base(path), nameLimit)) }

// The characters Discord gives a meaning to in a message body. A backslash before each of
// them is how Discord is told to render the character and nothing else.
const markdownChars = `\*_~|` + "`" + `>#[]()`

// A name that goes in a message outside a code span. Everything in one of those was chosen
// by whatever filled the disk, and a name is markdown as readily as anything else: a process
// called `[Fix it](https://wherever)` arrives as a link to somewhere Tim is invited to click,
// and one called `**You can run:**` arrives as a line of hachiko's own. So every character
// Discord acts on is escaped, which is what makes the name arrive as the name.
//
// A code span needs none of this and must not have it — the span is what stops the rendering
// there, and a backslash inside one is a backslash.
func plainWords(s string) string {
	var out strings.Builder
	out.Grow(len(s))

	for _, r := range s {
		if r < utf8.RuneSelf && strings.ContainsRune(markdownChars, r) {
			out.WriteByte('\\')
		}
		out.WriteRune(r)
	}
	return out.String()
}

// The same name in a forum post's title, which is plain text: Discord renders no markdown
// there, so an escape that is invisible in the message is a backslash in the title. The
// title is the message's own lead line, so this takes the escaping back out rather than
// building the name a second way — and a backslash a path really holds survives it, since
// that one was escaped too.
//
// Byte by byte, which is safe because every character it looks for is ASCII and no byte of
// a multi-byte rune is.
func plainTitle(s string) string {
	var out strings.Builder
	out.Grow(len(s))

	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && strings.IndexByte(markdownChars, s[i+1]) >= 0 {
			i++
		}
		out.WriteByte(s[i])
	}
	return out.String()
}

// What to call an incident in a lead. The id itself is machine detail and goes in the
// subtext line, since nothing Tim does with a message needs it.
func incidentWords(kind string) string {
	switch kind {
	case "cpu":
		return "the CPU incident"
	case "disk":
		return "the disk incident"
	default:
		return "the " + safe(kind, nameLimit) + " incident"
	}
}

// Where the session is, in the two words herdr names things by.
func herdrWhere(cfg Config, tab string) string {
	return fmt.Sprintf("workspace %s, tab %s",
		codeSpan(safe(cfg.WorkspaceLabel(), pathLimit)), codeSpan(safe(tab, pathLimit)))
}

func attachAction(cfg Config, tab string) string {
	return "Attach in herdr: " + herdrWhere(cfg, tab) + "."
}

func answerAction(cfg Config, tab string) string {
	return "Answer in herdr: " + herdrWhere(cfg, tab) + "."
}

// The daemons /usr/local/libexec/claude-root restarts with no password, read out of the
// installed helper itself rather than listed again here: the list that decides is the one
// sudoers actually reaches, and a copy here would go stale the first time Tim edits it.
//
// A helper that is missing or unreadable is no allowlist at all, which leaves the honest
// `sudo kill` fallback.
func allowedDaemons(helper string) []string {
	if helper == "" {
		return nil
	}

	body, err := os.ReadFile(helper)
	if err != nil {
		return nil
	}

	var names []string
	inside := false
	for _, line := range strings.Split(string(body), "\n") {
		entry := strings.TrimSpace(line)
		switch {
		case !inside:
			inside = entry == "allowed_daemons=("
		case entry == ")":
			return names
		default:
			if name, _, _ := strings.Cut(strings.Trim(entry, `"`), " "); name != "" {
				names = append(names, name)
			}
		}
	}
	return names
}

// The one command Tim can run without thinking about it, when the busy daemon happens to
// be one the helper allows. Matched by string equality and nothing looser, and the command
// is spelled from the allowlist's own entry rather than from the name a process reported —
// so nothing a command line chose can reach the line Tim is being told to run.
func restartDaemonCommand(cfg Config, name string) string {
	if name == "" {
		return ""
	}
	for _, allowed := range allowedDaemons(cfg.RootHelper) {
		if allowed == name {
			return fmt.Sprintf("sudo %s restart-daemon %s", cfg.RootHelper, allowed)
		}
	}
	return ""
}
