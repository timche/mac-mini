package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSizesReadAsSomethingTimWouldSayOutLoud(t *testing.T) {
	equal(t, gbUnit(4508877), "4.3 GB", "four and a bit gigabytes")
	equal(t, gbUnit(500*gib), "500 GB", "a round five hundred")
	equal(t, gbUnit(20*gib), "20 GB", "the critical mark")
	equal(t, gbUnit(gib), "1 GB", "exactly a gigabyte")
	equal(t, gbUnit(920*1024), "920 MB", "under a gigabyte, in whole megabytes")
	equal(t, gbUnit(524288), "512 MB", "half a gigabyte of memory")
	equal(t, gbUnit(gib-1), "1 GB", "a rounding that would otherwise print 1024 MB")
	equal(t, gbUnit(-5), "0 MB", "a size that cannot be negative")
}

func TestRatesAreWholeUnitsAnHourAndSayAbout(t *testing.T) {
	equal(t, ratePhrase(4508877, 300*time.Second), "about 52 GB an hour", "4.3 GB in five minutes")
	equal(t, ratePhrase(2*gib, time.Hour), "about 2 GB an hour", "2 GB in an hour")
	equal(t, ratePhrase(300*1024, time.Hour), "about 300 MB an hour", "under a gigabyte an hour")
	equal(t, ratePhrase(gib, 0), "about 3600 GB an hour", "a span that cannot be zero")
}

// Never 1h05m in anything Tim reads: a message at four in the morning is read once.
func TestDurationsReadAsWords(t *testing.T) {
	equal(t, durationPhrase(time.Hour+5*time.Minute), "1 hour 5 minutes", "an hour and a bit")
	equal(t, durationPhrase(15*time.Minute), "15 minutes", "a quarter of an hour")
	equal(t, durationPhrase(3*time.Hour), "3 hours", "the handover")
	equal(t, durationPhrase(time.Hour), "1 hour", "one hour, singular")
	equal(t, durationPhrase(61*time.Minute), "1 hour 1 minute", "one of each, both singular")
	equal(t, durationPhrase(30*time.Second), "less than a minute", "less than a minute")
	equal(t, durationPhrase(-time.Hour), "less than a minute", "a clock that moved")
}

// Two units at most, and the minutes go once there are days: shibuya's own `duration` is
// this function in TypeScript and the two write into one channel, so a Mac that has been
// quiet since Friday reads the same whichever of them says so.
func TestDurationsPastTwoDaysAreCountedInDays(t *testing.T) {
	equal(t, durationPhrase(47*time.Hour), "47 hours", "one hour short of the switch to days")
	equal(t, durationPhrase(47*time.Hour+59*time.Minute), "47 hours 59 minutes",
		"a minute short of the switch to days")
	equal(t, durationPhrase(48*time.Hour), "2 days", "exactly two days")
	equal(t, durationPhrase(48*time.Hour+30*time.Minute), "2 days", "the minutes go with the days")
	equal(t, durationPhrase(51*time.Hour), "2 days 3 hours", "two days and a bit")
	equal(t, durationPhrase(51*time.Hour+40*time.Minute), "2 days 3 hours", "never three units")
	equal(t, durationPhrase(72*time.Hour), "3 days", "three days")
	equal(t, durationPhrase(49*time.Hour), "2 days 1 hour", "one hour, singular")
}

func TestACountNamesBothSpellings(t *testing.T) {
	equal(t, countOf(1, "process", "processes"), "1 process", "one of them")
	equal(t, countOf(42, "process", "processes"), "42 processes", "more than one")
}

func TestTimesSayTheDayOnlyWhenItIsNotToday(t *testing.T) {
	now := time.Date(2026, time.October, 6, 9, 30, 0, 0, time.Local)

	equal(t, timePhrase(now.Add(-2*time.Hour), now), "at 07:30", "earlier today")
	equal(t, timePhrase(time.Date(2026, time.October, 5, 17, 20, 0, 0, time.Local), now),
		"yesterday at 17:20", "yesterday")
	equal(t, timePhrase(time.Date(2026, time.September, 28, 20, 6, 0, 0, time.Local), now),
		"on Mon 28 Sep at 20:06", "over a week ago")
}

func TestAProcessReadsAsItsNameThenItsPid(t *testing.T) {
	equal(t, processLabel("dasd", 147), "dasd (pid 147)", "a daemon")
	equal(t, pidLabel("sleep", "5073"), "sleep (pid 5073)", "a writer lsof named")
}

// A path is chosen by whatever filled the disk, so a path with a backtick in it is a path
// that would otherwise close the span and style the rest of the message as hachiko's own.
func TestACodeSpanCannotBeBrokenOutOf(t *testing.T) {
	equal(t, codeSpan("/private/tmp/x.log"), "`/private/tmp/x.log`", "an ordinary path")
	equal(t, codeSpan("a`b"), "`` a`b ``", "one backtick inside")
	equal(t, codeSpan("a``b"), "``` a``b ```", "two backticks inside")
	equal(t, codeSpan("`"), "`` ` ``", "nothing but a backtick")
	equal(t, codeSpan("**bold**"), "`**bold**`", "markdown that must not render")

	// Whatever the fence, the content never contains a run as long as it.
	for _, nasty := range []string{"`", "``", "x```y", "```", "a`b``c"} {
		span := codeSpan(nasty)
		fence := span[:strings.IndexFunc(span, func(r rune) bool { return r != '`' })]
		if strings.Contains(nasty, fence) {
			t.Errorf("the fence %q for %q appears inside it: %s", fence, nasty, span)
		}
	}
}

// Nothing a message is built from leaves a blank line in it, and the only one there is
// comes before the action line.
func TestAMessageHasOneBlankLineAndNoTrailingOne(t *testing.T) {
	m := lead(markerDisk, "Disk filling: devbackend.log is growing fast").
		field(labelFreeSpace, "500 GB").
		bullets(labelGrowing, []string{"`/private/tmp/devbackend.log` — 4.3 GB"}).
		can("Attach in herdr.").
		about("disk-1700000000", "mac-mini")

	equal(t, m.String(), "💾 Disk filling: devbackend.log is growing fast\n"+
		"**Free space:** 500 GB\n"+
		"**Growing fast:**\n"+
		"- `/private/tmp/devbackend.log` — 4.3 GB\n"+
		"\n"+
		"Attach in herdr.\n"+
		"-# Incident disk-1700000000 · mac-mini", "a whole message")

	// A field with nothing to say about it is no line at all, and a message with nothing
	// for Tim to do ends at its subtext.
	bare := lead(markerInfo, "Test alert from hachiko — nothing is wrong").
		field(labelFreeSpace, "790 GB").
		field(labelWhy, "").
		about("", "mac-mini")

	equal(t, bare.String(), "ℹ️ Test alert from hachiko — nothing is wrong\n"+
		"**Free space:** 790 GB\n\n-# mac-mini", "a message with nothing to do about it")

	equal(t, strings.HasSuffix(m.String(), "\n"), false, "a trailing newline")
	equal(t, strings.Contains(m.String(), "\n\n\n"), false, "a doubled blank line")
}

// A lead is a headline, and Discord caps a forum post's name at 100 characters.
func TestALeadIsClippedToAHeadline(t *testing.T) {
	long := lead(markerDown, strings.Repeat("x", 300)).String()
	if len(threadName(long)) > 100 {
		t.Errorf("a thread name of %d bytes: %q", len(threadName(long)), threadName(long))
	}
	equal(t, strings.Contains(long, "\n"), false, "a lead on more than one line")
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

	cfg := Config{RootHelper: helper}
	equal(t, strings.Join(allowedDaemons(helper), " "), "dasd mds", "the daemons it allows")
	equal(t, restartDaemonCommand(cfg, "dasd"), "sudo "+helper+" restart-daemon dasd",
		"the command for a daemon on the list")

	// String equality and nothing looser: a pattern or a prefix would be a command built
	// out of a name a command line chose.
	for _, name := range []string{"das", "dasdd", "d*", "", "DASD", "dasd com.apple.dasd"} {
		equal(t, restartDaemonCommand(cfg, name), "", "the command for "+name)
	}

	// A helper that is not installed is no allowlist, which leaves the honest sudo kill.
	equal(t, len(allowedDaemons(filepath.Join(dir, "not-there"))), 0, "daemons from a missing helper")
	equal(t, len(allowedDaemons("")), 0, "daemons with no helper configured")
}

// The line the warning quotes is the one a session wrote, and hachiko has to be able to
// read its own warning back: the bold form is what it sends now.
func TestTheFallbackOptionSurvivesTheWarningsOwnFormat(t *testing.T) {
	warning := lead(markerDegraded, "No answer on the disk incident — the agent decides in 15 minutes").
		field(labelWaiting, "2 hours 45 minutes").
		field(labelFallback, "stop pid 4242 and empty the log").
		block("**Now:** 1 GB free").
		can("Answer in herdr: workspace `.mac-mini`, tab `disk-1200`.").
		about("disk-1700000000", "mac-mini").
		String()

	equal(t, fallbackOption(warning), "stop pid 4242 and empty the log", "the option read back")

	// And every other spelling a session might reach for.
	for _, line := range []string{
		"If no answer: stop the worker",
		"**If no answer:** stop the worker",
		"- If no answer: stop the worker",
		"- **If no answer:** stop the worker",
		"> If no answer: stop the worker",
	} {
		equal(t, fallbackOption("some report\n"+line+"\nmore report"), "stop the worker", line)
	}
}
