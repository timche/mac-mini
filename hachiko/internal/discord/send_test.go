package discord

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/harness"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// ps shows every process's arguments to everybody on the machine, so what op is asked
// to run names the reference and never the value.
func TestWhatOpIsAskedToRunNamesNoSecret(t *testing.T) {
	cfg := config.Config{
		EnvFile: "/somewhere/hachiko.env.op",
		Discord: config.DiscordConfig{ChannelID: "123", UserID: "456"},
	}
	args := opArgs(cfg, "/cache/hachiko", Outgoing{})

	for _, arg := range args {
		if strings.Contains(arg, "HACHIKO_DISCORD_BOT_TOKEN") && !strings.HasSuffix(arg, ".env.op") {
			t.Errorf("the token reached a command line: %v", args)
		}
	}
	harness.Equal(t, strings.Join(args, " "),
		"op run --env-file /somewhere/hachiko.env.op -- /cache/hachiko --send --channel 123",
		"what op is asked to run")
}

// What `op run --send` prints, which is what the sweep records against the incident, so
// every message after the first goes into the post the first one opened.
func TestSendModeAnswersWithThePostItOpened(t *testing.T) {
	bot, _ := fakeBot(t, map[string]string{
		"GET /channels/chan":          `{"id":"chan","type":15}`,
		"POST /channels/chan/threads": `{"id":"800"}`,
	})
	botPointedAt(t, bot.API)
	t.Setenv("HACHIKO_DISCORD_BOT_TOKEN", fakeBotToken)

	thread, err := SendMode(strings.NewReader("Disk: filling"), Outgoing{OpenThread: "disk-1"}, "chan")
	if err != nil {
		t.Fatal(err)
	}
	harness.Equal(t, thread, "800", "the post the sweep records")
}

// A field whose value ends in a newline is a header net/http refuses outright, which would
// turn a configured bot into an alert nobody receives.
func TestSendModeTrimsTheResolvedReference(t *testing.T) {
	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Write([]byte(`{"id":"msg-1"}`))
	}))
	defer server.Close()

	botPointedAt(t, server.URL)
	t.Setenv("HACHIKO_DISCORD_BOT_TOKEN", fakeBotToken+"\n")

	if _, err := SendMode(strings.NewReader("the disk is filling"), Outgoing{}, "chan"); err != nil {
		t.Fatalf("a reference with a trailing newline was not sent: %v", err)
	}
	harness.Equal(t, auth, "Bot "+fakeBotToken, "the header the message carried")
}

// There is nowhere else for a message to go, so each of these is a send that failed and an
// incident the next check raises again rather than one quietly dropped.
func TestSendModeRefusesWithNothingToSendAndWithNoChannelOrToken(t *testing.T) {
	t.Setenv("HACHIKO_DISCORD_BOT_TOKEN", fakeBotToken)
	_, err := SendMode(strings.NewReader("   \n"), Outgoing{}, "chan")
	if err == nil {
		t.Fatal("an empty message was not reported")
	}
	harness.Wants(t, err.Error(), "nothing to send")

	_, err = SendMode(strings.NewReader("something"), Outgoing{}, "")
	if err == nil {
		t.Fatal("a message with no channel to send it to was not reported")
	}
	harness.Wants(t, err.Error(), "no Discord channel is configured")

	t.Setenv("HACHIKO_DISCORD_BOT_TOKEN", "")
	_, err = SendMode(strings.NewReader("something"), Outgoing{}, "chan")
	if err == nil {
		t.Fatal("an unresolved reference was not reported")
	}
	harness.Wants(t, err.Error(), "did not resolve")
}

// And the same before `op run` is spent at all: a check against the service account's day
// for a message with nowhere to go.
func TestSendingWithNoChannelConfiguredFailsBeforeOpIsCalled(t *testing.T) {
	cfg := config.Config{EnvFile: "/somewhere/hachiko.env.op"}

	_, err := SendThroughOP(cfg, Outgoing{Text: "the disk is filling"})
	if err == nil {
		t.Fatal("a send with no channel configured was not reported")
	}
	harness.Wants(t, err.Error(), "no Discord channel is configured")
}

func TestAThreadNameIsOneLineAndFitsDiscordsLimit(t *testing.T) {
	name := ThreadName(strings.Repeat("x", 300) + "\nsecond line")
	if len(name) > 100 || strings.Contains(name, "\n") {
		t.Fatalf("thread name of %d bytes: %q", len(name), name)
	}
	harness.Equal(t, ThreadName("\n\n"), "hachiko", "an empty message's thread name")

	// A lead of hachiko's own, however long the sentence it was given.
	lead := wording.Lead(wording.MarkerDown, strings.Repeat("x", 300)).String()
	if len(ThreadName(lead)) > 100 {
		t.Errorf("a thread name of %d bytes: %q", len(ThreadName(lead)), ThreadName(lead))
	}

	// The escaping that keeps a file called `**x**` from styling a message body is a
	// backslash in a title, which Discord renders as plain text — so it comes back out.
	harness.Equal(t, ThreadName(wording.PlainWords("💾 Disk filling: **x**.log is growing fast")+"\n**Free space:** 1 GB"),
		"💾 Disk filling: **x**.log is growing fast", "the post's name")
}

// An error text that carried the token would be in the log every session reads, and a value
// resolved from 1Password is spelled differently in a message from how it is in the
// environment: %q escapes a newline and any byte outside ASCII.
func TestRedactTakesEverySpellingOfTheTokenOut(t *testing.T) {
	const secret = "NOT-A-REAL-BOT-TOKEN"
	stored := "MTIzNDU2." + secret + "-ü\n"
	token := strings.TrimSpace(stored)

	for _, text := range []string{
		`Post "https://discord.com/api/v10/channels/1/messages": dial tcp: connection refused, Bot ` + token,
		"authorization Bot " + strconv.Quote(stored) + " was refused",
		"plain " + token + " and nothing else",
	} {
		got := redact(text, stored)
		harness.Lacks(t, got, secret)
		harness.Wants(t, got, "the bot token")
	}
}

// Discord takes 2,000 characters. What is over that is detail, and the on-call tab has
// all of it.
func TestALongMessageIsCappedAndSaysWhereTheRestIs(t *testing.T) {
	got := capMessage(strings.Repeat("x", 5000))

	if len(got) >= Limit {
		t.Errorf("the message is %d characters, which Discord refuses", len(got))
	}
	harness.Wants(t, got, "[truncated — the rest is in the on-call tab in herdr]")

	short := "one line"
	harness.Equal(t, capMessage(short), short, "a short message")
}

// The cut falls on a line boundary, because a cut in the middle of one leaves an inline code
// span open — and Discord then renders everything after it as code, the line saying the
// message was truncated included.
func TestTheCapCutsWholeLinesAndLeavesNoCodeSpanOpen(t *testing.T) {
	m := wording.Lead(wording.MarkerDisk, "Disk filling: devbackend.log is growing fast").
		Field(wording.LabelFreeSpace, "500 GB")

	// Enough bullets that the cut lands in the middle of one of them, each carrying a path in
	// a code span.
	var bullets []string
	for i := range 40 {
		bullets = append(bullets, wording.CodeSpan(fmt.Sprintf("/private/tmp/worker-%02d.log", i))+
			" — 4.3 GB, up 4.3 GB in 5 minutes (about 52 GB an hour), written by sleep (pid 5073)")
	}
	long := m.Bullets(wording.LabelGrowing, bullets).
		Can("Attach in herdr: workspace `.mac-mini`, tab `disk-1200`.").
		About("disk-1700000000", "mac-mini").
		String()

	if len(long) <= messageLimit {
		t.Fatalf("the message under test is only %d characters", len(long))
	}

	got := capMessage(long)
	if len(got) >= Limit {
		t.Errorf("the capped message is %d characters, which Discord refuses", len(got))
	}
	harness.Wants(t, got, truncatedTail)

	// Every backtick fence that was opened was closed, so nothing after the cut renders as
	// code — and the cut fell between lines, so every line that is left is one hachiko wrote.
	harness.Equal(t, strings.Count(got, "`")%2, 0, "unclosed backtick fences")
	for _, line := range strings.Split(got, "\n") {
		if line == truncatedTail {
			continue
		}
		if !strings.Contains(long, line) {
			t.Errorf("a line the cut left half of: %q", line)
		}
	}

	// The subtext is the last line and so the first thing to go, which is right: it is the
	// one line Tim never acts on.
	harness.Lacks(t, got, "-# Incident disk-1700000000")
}

// And the subtext alone goes when that is all there is to drop, rather than the message
// losing a finding to make room for it: it is the one line Tim never acts on.
func TestTheCapDropsTheSubtextFirst(t *testing.T) {
	build := func(pad int) *wording.Message {
		return wording.Lead(wording.MarkerDisk, "Disk filling: worker.log is growing fast").
			Bullets(wording.LabelGrowing, []string{
				wording.CodeSpan("/private/tmp/worker.log") + " — " + strings.Repeat("x", pad)}).
			Can("Attach in herdr.")
	}

	// Sized so that everything but the subtext fits inside what the cap keeps, and the
	// subtext is the thing that takes the message over Discord's limit.
	pad := 0
	for len(build(pad).String()) < messageKeep-4 {
		pad++
	}

	full := build(pad).About("disk-1700000000", "mac-mini.fritz.box").String()
	if len(full) <= messageLimit {
		t.Fatalf("the message under test is only %d characters", len(full))
	}

	harness.Equal(t, capMessage(full), build(pad).String()+"\n\n"+truncatedTail, "what the cap kept")
}

// One line longer than the whole budget is a session's own report rather than anything
// hachiko writes, and there is nowhere to cut it but the middle: its backticks go, which is
// the one way to end a partial line with nothing left open.
func TestASingleOverlongLineIsCutWithItsBackticksTakenOut(t *testing.T) {
	got := capMessage("`" + strings.Repeat("x", 5000) + "`")

	if len(got) >= Limit {
		t.Errorf("the capped message is %d characters, which Discord refuses", len(got))
	}
	harness.Equal(t, strings.Count(got, "`"), 0, "backticks left in a cut line")
	harness.Wants(t, got, truncatedTail)

	// And a cut that lands inside a multi-byte character leaves no half of one behind.
	wide := capMessage(strings.Repeat("é", 5000))
	harness.Equal(t, utf8.ValidString(wide), true, "whether the capped message is valid UTF-8")
}
