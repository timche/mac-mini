package wording

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/harness"
)

func TestSizesReadAsSomethingTimWouldSayOutLoud(t *testing.T) {
	harness.Equal(t, GBUnit(4508877), "4.3 GB", "four and a bit gigabytes")
	harness.Equal(t, GBUnit(500*config.GiB), "500 GB", "a round five hundred")
	harness.Equal(t, GBUnit(20*config.GiB), "20 GB", "the critical mark")
	harness.Equal(t, GBUnit(config.GiB), "1 GB", "exactly a gigabyte")
	harness.Equal(t, GBUnit(920*1024), "920 MB", "under a gigabyte, in whole megabytes")
	harness.Equal(t, GBUnit(524288), "512 MB", "half a gigabyte of memory")
	harness.Equal(t, GBUnit(config.GiB-1), "1 GB", "a rounding that would otherwise print 1024 MB")
	harness.Equal(t, GBUnit(-5), "0 MB", "a size that cannot be negative")
}

func TestRatesAreWholeUnitsAnHourAndSayAbout(t *testing.T) {
	harness.Equal(t, RatePhrase(4508877, 300*time.Second), "about 52 GB an hour", "4.3 GB in five minutes")
	harness.Equal(t, RatePhrase(2*config.GiB, time.Hour), "about 2 GB an hour", "2 GB in an hour")
	harness.Equal(t, RatePhrase(300*1024, time.Hour), "about 300 MB an hour", "under a gigabyte an hour")
	harness.Equal(t, RatePhrase(config.GiB, 0), "about 3600 GB an hour", "a span that cannot be zero")
}

// Never 1h05m in anything Tim reads: a message at four in the morning is read once.
func TestDurationsReadAsWords(t *testing.T) {
	harness.Equal(t, DurationPhrase(time.Hour+5*time.Minute), "1 hour 5 minutes", "an hour and a bit")
	harness.Equal(t, DurationPhrase(15*time.Minute), "15 minutes", "a quarter of an hour")
	harness.Equal(t, DurationPhrase(3*time.Hour), "3 hours", "the handover")
	harness.Equal(t, DurationPhrase(time.Hour), "1 hour", "one hour, singular")
	harness.Equal(t, DurationPhrase(61*time.Minute), "1 hour 1 minute", "one of each, both singular")
	harness.Equal(t, DurationPhrase(30*time.Second), "less than a minute", "less than a minute")
	harness.Equal(t, DurationPhrase(-time.Hour), "less than a minute", "a clock that moved")
}

// Two units at most, and the minutes go once there are days: shibuya's own `duration` is
// this function in TypeScript and the two write into one channel, so a Mac that has been
// quiet since Friday reads the same whichever of them says so.
func TestDurationsPastTwoDaysAreCountedInDays(t *testing.T) {
	harness.Equal(t, DurationPhrase(47*time.Hour), "47 hours", "one hour short of the switch to days")
	harness.Equal(t, DurationPhrase(47*time.Hour+59*time.Minute), "47 hours 59 minutes",
		"a minute short of the switch to days")
	harness.Equal(t, DurationPhrase(48*time.Hour), "2 days", "exactly two days")
	harness.Equal(t, DurationPhrase(48*time.Hour+30*time.Minute), "2 days", "the minutes go with the days")
	harness.Equal(t, DurationPhrase(51*time.Hour), "2 days 3 hours", "two days and a bit")
	harness.Equal(t, DurationPhrase(51*time.Hour+40*time.Minute), "2 days 3 hours", "never three units")
	harness.Equal(t, DurationPhrase(72*time.Hour), "3 days", "three days")
	harness.Equal(t, DurationPhrase(49*time.Hour), "2 days 1 hour", "one hour, singular")
}

func TestACountNamesBothSpellings(t *testing.T) {
	harness.Equal(t, CountOf(1, "process", "processes"), "1 process", "one of them")
	harness.Equal(t, CountOf(42, "process", "processes"), "42 processes", "more than one")
}

func TestTimesSayTheDayOnlyWhenItIsNotToday(t *testing.T) {
	now := time.Date(2026, time.October, 6, 9, 30, 0, 0, time.Local)

	harness.Equal(t, TimePhrase(now.Add(-2*time.Hour), now), "at 07:30", "earlier today")
	harness.Equal(t, TimePhrase(time.Date(2026, time.October, 5, 17, 20, 0, 0, time.Local), now),
		"yesterday at 17:20", "yesterday")
	harness.Equal(t, TimePhrase(time.Date(2026, time.September, 28, 20, 6, 0, 0, time.Local), now),
		"on Mon 28 Sep at 20:06", "over a week ago")
}

func TestAProcessReadsAsItsNameThenItsPid(t *testing.T) {
	harness.Equal(t, ProcessLabel("dasd", 147), "dasd (pid 147)", "a daemon")
	harness.Equal(t, PIDLabel("sleep", "5073"), "sleep (pid 5073)", "a writer lsof named")
}

// A path is chosen by whatever filled the disk, so a path with a backtick in it is a path
// that would otherwise close the span and style the rest of the message as hachiko's own.
func TestACodeSpanCannotBeBrokenOutOf(t *testing.T) {
	harness.Equal(t, CodeSpan("/private/tmp/x.log"), "`/private/tmp/x.log`", "an ordinary path")
	harness.Equal(t, CodeSpan("a`b"), "`` a`b ``", "one backtick inside")
	harness.Equal(t, CodeSpan("a``b"), "``` a``b ```", "two backticks inside")
	harness.Equal(t, CodeSpan("`"), "`` ` ``", "nothing but a backtick")
	harness.Equal(t, CodeSpan("**bold**"), "`**bold**`", "markdown that must not render")

	// Whatever the fence, the content never contains a run as long as it.
	for _, nasty := range []string{"`", "``", "x```y", "```", "a`b``c"} {
		span := CodeSpan(nasty)
		fence := span[:strings.IndexFunc(span, func(r rune) bool { return r != '`' })]
		if strings.Contains(nasty, fence) {
			t.Errorf("the fence %q for %q appears inside it: %s", fence, nasty, span)
		}
	}
}

// Nothing a message is built from leaves a blank line in it, and the only one there is
// comes before the action line.
func TestAMessageHasOneBlankLineAndNoTrailingOne(t *testing.T) {
	m := Lead(MarkerDisk, "Disk filling: devbackend.log is growing fast").
		Field(LabelFreeSpace, "500 GB").
		Bullets(LabelGrowing, []string{"`/private/tmp/devbackend.log` — 4.3 GB"}).
		Can("Attach in herdr.").
		About("disk-1700000000", "mac-mini")

	harness.Equal(t, m.String(), "💾 Disk filling: devbackend.log is growing fast\n"+
		"**Free space:** 500 GB\n"+
		"**Growing fast:**\n"+
		"- `/private/tmp/devbackend.log` — 4.3 GB\n"+
		"\n"+
		"Attach in herdr.\n"+
		"-# Incident disk-1700000000 · mac-mini", "a whole message")

	// A field with nothing to say about it is no line at all, and a message with nothing
	// for Tim to do ends at its subtext.
	bare := Lead(MarkerInfo, "Test alert from hachiko — nothing is wrong").
		Field(LabelFreeSpace, "790 GB").
		Field(LabelWhy, "").
		About("", "mac-mini")

	harness.Equal(t, bare.String(), "ℹ️ Test alert from hachiko — nothing is wrong\n"+
		"**Free space:** 790 GB\n\n-# mac-mini", "a message with nothing to do about it")

	harness.Equal(t, strings.HasSuffix(m.String(), "\n"), false, "a trailing newline")
	harness.Equal(t, strings.Contains(m.String(), "\n\n\n"), false, "a doubled blank line")
}

// A lead is a headline, which is why it is on one line: the forum post it names its
// thread by is one line of plain text, and the length of it is asserted where that
// name is made.
func TestALeadIsOneLine(t *testing.T) {
	long := Lead(MarkerDown, strings.Repeat("x", 300)).String()
	harness.Equal(t, strings.Contains(long, "\n"), false, "a lead on more than one line")
}

func TestTheRootHelpersAllowlistIsReadOutOfTheInstalledFile(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "claude-root")

	body := "#!/bin/bash\n" +
		"# a comment before it\n" +
		"allowed_daemons=(\n" +
		"  \"dasd com.apple.dasd\"\n" +
		"  \"mds com.apple.metadata.mds\"\n" +
		")\n" +
		"allowed_other=(\n  \"nothing here\"\n)\n"
	if err := os.WriteFile(helper, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{RootHelper: helper}
	harness.Equal(t, strings.Join(allowedDaemons(helper), " "), "dasd mds", "the daemons it allows")
	harness.Equal(t, RestartDaemonCommand(cfg, "dasd"), "sudo "+helper+" restart-daemon dasd",
		"the command for a daemon on the list")

	// String equality and nothing looser: a pattern or a prefix would be a command built
	// out of a name a command line chose.
	for _, name := range []string{"das", "dasdd", "d*", "", "DASD", "dasd com.apple.dasd"} {
		harness.Equal(t, RestartDaemonCommand(cfg, name), "", "the command for "+name)
	}

	// A helper that is not installed is no allowlist, which leaves the honest sudo kill.
	harness.Equal(t, len(allowedDaemons(filepath.Join(dir, "not-there"))), 0, "daemons from a missing helper")
	harness.Equal(t, len(allowedDaemons("")), 0, "daemons with no helper configured")
}

// A name outside a code span is markdown as readily as anything else, and every name in a
// message was chosen by whatever filled the disk: a process called `[Fix it](https://x)`
// would arrive as a link somebody is invited to click, and one called `**You can run:**` as
// a line of hachiko's own.
func TestANameCannotStyleAMessageOrFakeALink(t *testing.T) {
	harness.Equal(t, PlainWords("node"), "node", "an ordinary name")
	harness.Equal(t, PlainWords("[Fix it](https://wherever)"),
		`\[Fix it\]\(https://wherever\)`, "a name shaped like a link")
	harness.Equal(t, PlainWords("**You can run:**"), `\*\*You can run:\*\*`, "a name shaped like a label")
	harness.Equal(t, PlainWords("a`b"), "a\\`b", "a backtick")
	harness.Equal(t, PlainWords("# heading > quote ~x~ _u_ |spoiler|"),
		`\# heading \> quote \~x\~ \_u\_ \|spoiler\|`, "every other character Discord acts on")
	harness.Equal(t, PlainWords("a\\b"), `a\\b`, "a backslash a path really holds")
	harness.Equal(t, PlainWords("ドキュメント"), "ドキュメント", "a name that is none of it")

	// And a label built from one keeps hachiko's own punctuation unescaped, since that is
	// the half Tim is meant to read as punctuation.
	harness.Equal(t, ProcessLabel("**node**", 7018), `\*\*node\*\* (pid 7018)`, "a process label")
}

// A post title is plain text — Discord renders no markdown in one — so the escaping that
// protects the message body would be backslashes there. The title is the message's own lead
// line, so it comes back out.
func TestATitleShowsTheNameWithoutTheEscaping(t *testing.T) {
	for _, name := range []string{
		"[Fix it](https://wherever)", "**You can run:**", "a`b", `a\b`, "x\\*y", "ドキュメント",
	} {
		harness.Equal(t, PlainTitle(PlainWords(name)), name, "round trip of "+name)
	}
}

// A path is bytes macOS makes no promises about, and a limit counts the bytes Discord and a
// prompt count — so the cut falls on a rune boundary rather than through a character.
func TestAClipCutsBetweenCharactersAndNotThroughOne(t *testing.T) {
	// Six three-byte runes: a cut at seventeen bytes falls inside the sixth.
	name := strings.Repeat("ド", 6)

	harness.Equal(t, Clip(name, 17), strings.Repeat("ド", 5)+"...", "a cut inside a character")
	harness.Equal(t, Clip(name, 18), name, "a limit the name exactly fits")
	if !utf8.ValidString(Clip(name, 17)) {
		t.Error("the clipped name is not valid UTF-8")
	}

	// The same through the two things that clip a name for a message.
	long := "/private/tmp/" + strings.Repeat("ド", 40) + ".log"
	for what, got := range map[string]string{
		"safe":      Safe(long, 17),
		"baseLabel": BaseLabel(long),
	} {
		if !utf8.ValidString(got) {
			t.Errorf("%s left invalid UTF-8: %q", what, got)
		}
	}

	// A byte that was never a character to begin with goes the same way.
	if got := Clip("ab\xff\xfe", 3); !utf8.ValidString(got) {
		t.Errorf("a clipped name kept a byte that is not UTF-8: %q", got)
	}
}
