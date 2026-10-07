package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/oncall"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// Every message hachiko can send, rendered in full and printed:
//
//	go test -run TestMessages -v
//
// reads them all the way Discord would show them, which is the only way to see that fifteen
// messages written to one style still read as one voice. Each case asserts the handful of
// things that have to be true of it — the marker, the lead, the one line for Tim — and the
// printed message is the review.
func TestMessages(t *testing.T) {
	for _, kind := range messageKinds {
		t.Run(kind.name, func(t *testing.T) {
			text := kind.render(t)
			t.Logf("\n\n%s\n", text)

			if strings.HasSuffix(text, "\n") || strings.Contains(text, "\n\n\n") {
				t.Errorf("a blank line where there should be none:\n%q", text)
			}
			if len(text) > discord.Limit {
				t.Errorf("%d characters, which is over Discord's limit", len(text))
			}

			first, _, _ := strings.Cut(text, "\n")
			if !strings.HasPrefix(first, kind.marker+" ") {
				t.Errorf("the lead does not open with %s: %q", kind.marker, first)
			}
			if strings.HasSuffix(first, ".") || strings.Contains(first, "`") {
				t.Errorf("the lead is not a headline: %q", first)
			}
			for _, banned := range bannedWords {
				if strings.Contains(strings.ToLower(text), banned) {
					t.Errorf("%q is in a message Tim reads:\n%s", banned, text)
				}
			}
			for _, want := range kind.wants {
				wants(t, text, want)
			}
		})
	}
}

// None of these says anything to somebody who is not reading the source, and every one of
// them has a plain word that does.
var bannedWords = []string{
	" hot ", "sweep", "sample", "statfs", " cwd", "resident", "ppid", "orphaned",
	"gb/h", "snowflake", "incident id", "threshold",
}

type messageKind struct {
	name   string
	marker string
	wants  []string
	render func(t *testing.T) string
}

// 4.3 GB in the five minutes between two checks, which is about what the worker that caused
// all this managed, and the path it chose because it was convenient.
const (
	growingKB = 4508877
	logPath   = "/private/tmp/devbackend.log"
)

// Paths as the real machine spells them and sizes no test could write, so what is printed is
// the message Tim would get rather than one about a temporary directory.
func messageFixture(t *testing.T) *fixture {
	t.Helper()

	f := newFixture(t)
	f.cfg.BigKB = config.GiB
	f.cfg.GrowthKB = 2 * config.GiB
	f.cfg.TmpRoot = "/private/tmp"
	f.noTruncate = true
	f.writer = "sleep (pid 5073), bash (pid 91330)"
	return f
}

// A file that was not there on the check before, which is the shape of the failure this
// whole thing watches for: the whole of it arrived in one interval.
func growingDisk(f *fixture) {
	f.at(0).sweep()
	f.claim(logPath, growingKB)
}

// Thirteen checks five minutes apart at 80% of a core, which is the hour the rule is about,
// with a start time the message says "yesterday" about.
func busyProcess(f *fixture, system bool, command string, pid int, alsoGrowing bool) {
	start := f.now.Add(-26 * time.Hour).Format("Mon Jan 2 15:04:05 2006")

	for i := range 13 {
		if system {
			f.systemProc(pid, float64(i)*240, start, command)
		} else {
			f.proc(pid, float64(i)*240, start, command)
		}
		if alsoGrowing && i == 12 {
			f.claim(logPath, growingKB)
		}
		f.at(int64(i) * 300).sweep()
	}
}

// A session on its question, with its own fallback option named and the file still going
// under it: that is where every message about a wait starts from, and a trigger that has
// stopped is a different piece of news again.
//
// The file is 14 GB before the watch ever sees it and gains a little over the growth mark on
// each check, so it is still growing fast every time without ever doubling on its own.
func waitingOnAQuestion(t *testing.T) *fixture {
	t.Helper()

	f := messageFixture(t)
	f.cfg.RemindAfter = remindAt * time.Second
	f.cfg.WarnAfter = warnAt * time.Second
	f.cfg.HandoverAfter = handoverAt * time.Second
	f.claimedKB = 14 * config.GiB
	f.keepClaiming, f.claimStep = logPath, 2202010

	f.at(0).sweep()
	f.at(300).sweep()

	f.notifyWithFallback(f.onlyPendingID(), "stop pid 5073 and empty the log")
	f.blocks("disk")
	f.at(600).sweep()

	return f
}

var messageKinds = []messageKind{{
	name:   "disk-growing",
	marker: wording.MarkerDisk,
	wants: []string{
		"💾 Disk filling: devbackend.log is growing fast",
		"**Free space:** 500 GB",
		"- `/private/tmp/devbackend.log` — 4.3 GB, up 4.3 GB in 5 minutes (about 52 GB an hour), written by sleep (pid 5073), bash (pid 91330)",
		"An agent is looking into it — attach in herdr: workspace `.mac-mini`, tab `disk-0000`. Details to follow.",
	},
	render: func(t *testing.T) string {
		f := messageFixture(t)
		growingDisk(f)
		f.at(300).sweep()
		return f.lastSent()
	},
}, {
	name:   "low-space",
	marker: wording.MarkerDisk,
	wants: []string{
		"💾 Low disk space: 46 GB free",
		"**Free space:** 46 GB, under the 100 GB mark",
	},
	render: func(t *testing.T) string {
		f := messageFixture(t)
		f.freeGB = 46
		f.at(0).sweep()
		return f.lastSent()
	},
}, {
	name:   "disk-critical-and-emptied",
	marker: wording.MarkerDown,
	wants: []string{
		"🔴 Disk critical: 1 GB free, and devbackend.log was emptied",
		"**Free space:** 1 GB, under the 20 GB mark",
		"**Emptied to keep the Mac going:** `/private/tmp/devbackend.log` — its writer was left running, so the space is back now",
		"**Growing fast:**",
	},
	render: func(t *testing.T) string {
		f := messageFixture(t)
		f.freeGB = 1
		growingDisk(f)
		f.at(300).sweep()
		return f.lastSent()
	},
}, {
	name:   "cpu-busy",
	marker: wording.MarkerBusy,
	wants: []string{
		"🔥 node is busy: 80% of a core for 1 hour",
		"**Busy processes:**",
		"- node (pid 7018) — 80% of a core for 1 hour, 512 MB memory, started yesterday at",
		"\n  `/usr/local/bin/node worker.js`",
	},
	render: func(t *testing.T) string {
		f := messageFixture(t)
		busyProcess(f, false, "/usr/local/bin/node worker.js", 7018, false)
		return f.lastSent()
	},
}, {
	name:   "cpu-busy-system-process-the-helper-allows",
	marker: wording.MarkerBusy,
	wants: []string{
		"🔥 dasd is busy: 80% of a core for 1 hour",
		"a system process owned by root",
		"restart-daemon dasd` — it restarts the daemon with no password needed.",
	},
	render: func(t *testing.T) string {
		f := messageFixture(t)
		f.rootHelper("dasd")
		busyProcess(f, true, "/usr/libexec/dasd", 147, false)
		return f.lastSent()
	},
}, {
	name:   "cpu-busy-system-process-the-helper-does-not",
	marker: wording.MarkerBusy,
	wants: []string{
		"a system process owned by root",
		"**Needs you:** `sudo kill 147` — stopping a system process needs sudo, and launchd starts most daemons again.",
	},
	render: func(t *testing.T) string {
		f := messageFixture(t)
		busyProcess(f, true, "/usr/libexec/dasd", 147, false)
		return f.lastSent()
	},
}, {
	name:   "disk-and-cpu-in-one-message",
	marker: wording.MarkerDisk,
	wants: []string{
		"💾 Disk filling: devbackend.log is growing fast",
		"**Growing fast:**",
		"**Busy processes:**",
	},
	render: func(t *testing.T) string {
		f := messageFixture(t)
		busyProcess(f, false, "/usr/local/bin/node worker.js", 7018, true)
		return f.lastSent()
	},
}, {
	name:   "no-report-from-the-agent",
	marker: wording.MarkerDegraded,
	wants: []string{
		"⚠️ No report from the agent on the disk incident after 10 minutes",
		"**Growing fast:**",
		"written by sleep (pid 5073), bash (pid 91330)",
		"Attach in herdr: workspace `.mac-mini`, tab `disk-0000`.",
	},
	render: func(t *testing.T) string {
		f := messageFixture(t)
		growingDisk(f)
		f.at(300).sweep()
		f.at(900).sweep()
		return f.lastSent()
	},
}, {
	name:   "reminder-at-an-hour",
	marker: wording.MarkerDegraded,
	wants: []string{
		"⚠️ Still no answer on the disk incident after 12 minutes",
		"**Now:** 500 GB free",
		"**Growing fast:**",
		"Answer in herdr: workspace `.mac-mini`, tab `disk-0000`.",
	},
	render: func(t *testing.T) string {
		f := waitingOnAQuestion(t)
		f.at(600 + remindAt).sweep()
		return f.lastSent()
	},
}, {
	name:   "warning-before-the-handover",
	marker: wording.MarkerDegraded,
	wants: []string{
		"⚠️ No answer on the disk incident — the agent decides in 3 minutes",
		"**Waiting:** 33 minutes",
		"**If no answer:** stop pid 5073 and empty the log",
		"**Now:** 500 GB free",
	},
	render: func(t *testing.T) string {
		f := waitingOnAQuestion(t)
		f.at(600 + remindAt).sweep()
		f.at(600 + warnAt).sweep()

		text := f.lastSent()
		equal(t, fallbackOption(text), "stop pid 5073 and empty the log", "the option hachiko reads back")
		return text
	},
}, {
	name:   "handover-that-reached-nobody",
	marker: wording.MarkerDown,
	wants: []string{
		"🔴 The decision on the disk incident reached no agent",
		"**Why:** its question could not be cancelled, so nothing was prompted",
		"Nothing has acted on it, and hachiko tries again every five minutes.",
	},
	render: func(t *testing.T) string {
		f := waitingOnAQuestion(t)
		f.interruptErr = errors.New("the agent is still on its question after the esc")
		f.at(600 + handoverAt).sweep()
		return f.lastSent()
	},
}, {
	name:   "a-wait-that-ended-with-no-outcome",
	marker: wording.MarkerDegraded,
	wants: []string{
		"⚠️ No outcome reported on the disk incident",
		"**Why:** the agent was handed the decision and went quiet without reporting an outcome",
		"hachiko has stopped waiting and does not know whether anything was done.",
	},
	render: func(t *testing.T) string {
		f := waitingOnAQuestion(t)
		f.at(600 + handoverAt).sweep()

		// Handed the decision and then nothing at all: no question up, nothing in flight, and
		// no outcome after the minutes a session is given to report in.
		f.status["disk"] = "idle"
		f.at(600 + handoverAt + 300).sweep()
		f.at(600 + handoverAt + 900).sweep()
		return f.lastSent()
	},
}, {
	name:   "a-session-closed-after-the-handover",
	marker: wording.MarkerDegraded,
	wants: []string{
		"⚠️ No outcome reported on the disk incident",
		"**Why:** the session was handed the decision and has since been closed",
	},
	render: func(t *testing.T) string {
		f := waitingOnAQuestion(t)
		f.at(600 + handoverAt).sweep()

		f.status["disk"] = oncall.StatusGone
		f.at(600 + handoverAt + 300).sweep()
		return f.lastSent()
	},
}, {
	name:   "no-agent-left-to-decide",
	marker: wording.MarkerDown,
	wants: []string{
		"🔴 No answer on the disk incident, and no agent left to decide",
		"**Waiting:** 36 minutes",
		"**Why:** the session is closed, so nothing was done about it",
		"Open a session on it yourself",
	},
	render: func(t *testing.T) string {
		f := waitingOnAQuestion(t)
		f.status["disk"] = oncall.StatusGone
		f.at(600 + handoverAt).sweep()
		return f.lastSent()
	},
}, {
	name:   "herdr-unreachable",
	marker: wording.MarkerDown,
	wants: []string{
		"🔴 Cannot reach the on-call session on the disk incident",
		"**Why:** herdr has not answered for 10 minutes",
		"Nothing is being worked and nothing can be handed to it.",
	},
	render: func(t *testing.T) string {
		f := waitingOnAQuestion(t)
		f.statusErr = errors.New("no server is listening")
		f.at(900).sweep()
		f.at(1500).sweep()
		return f.lastSent()
	},
}, {
	name:   "test-alert",
	marker: wording.MarkerInfo,
	wants: []string{
		"ℹ️ Test alert from hachiko — nothing is wrong",
		"**Free space:** 790 GB",
		"-# mac-mini",
	},
	render: func(t *testing.T) string {
		return wording.Lead(wording.MarkerInfo, "Test alert from hachiko — nothing is wrong").
			Field(wording.LabelFreeSpace, wording.GBUnit(790*config.GiB)).
			About("", "mac-mini").
			String()
	},
}}

// What fired has stopped by itself, which is the one piece of news that goes to the session
// rather than to the channel: it is a reason to close an incident rather than an incident.
func TestTheClearedReadingIsPlainWords(t *testing.T) {
	f := waitingOnAQuestion(t)
	incident := f.state().Waiting["disk"].Incident

	f.keepClaiming, f.claimed = "", []FileSize{}
	f.at(900).sweep()
	f.at(1200).sweep()

	prompt := f.lastInterrupt()
	t.Logf("\n\nthe prompt that carries it:\n%s\n", prompt)

	wants(t, prompt, "Nothing is growing fast any more, and free space is back over every mark at 500 GB.")
	wants(t, prompt, "What fired this incident is no longer firing")
	wants(t, prompt, "Check for yourself whether it has really resolved")
	wants(t, prompt, incident)
}
