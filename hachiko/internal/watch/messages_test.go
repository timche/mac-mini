package watch

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/harness"
	"github.com/timche/mac-mini/hachiko/internal/oncall"
	"github.com/timche/mac-mini/hachiko/internal/session"
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
				harness.Wants(t, text, want)
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

// Thirteen checks five minutes apart at 80% of a core, which is the hour the rule is about,
// with a start time the message says "yesterday" about.
func busyProcess(f *fixture, system bool, command string, pid int, alsoLowOnSpace bool) {
	start := f.now.Add(-26 * time.Hour).Format("Mon Jan 2 15:04:05 2006")

	for i := range 13 {
		if system {
			f.systemProc(pid, float64(i)*240, start, command)
		} else {
			f.proc(pid, float64(i)*240, start, command)
		}
		if alsoLowOnSpace && i == 12 {
			f.freeGB = 46
		}
		f.at(int64(i) * 300).sweep()
	}
}

// A session on its question, with its own fallback option named and the volume still under
// the mark beneath it: that is where every message about a wait starts from, and a trigger
// that has stopped is a different piece of news again.
func waitingOnAQuestion(t *testing.T) *fixture {
	t.Helper()

	f := newFixture(t)
	f.cfg.RemindAfter = remindAt * time.Second
	f.cfg.WarnAfter = warnAt * time.Second
	f.cfg.HandoverAfter = handoverAt * time.Second
	f.freeGB = 46

	f.at(0).sweep()
	f.at(300).sweep()

	f.notifyWithFallback(f.onlyPendingID(), "stop pid 5073 and empty the log")
	f.blocks("disk")
	f.at(600).sweep()

	return f
}

var messageKinds = []messageKind{{
	name:   "low-space",
	marker: wording.MarkerDisk,
	wants: []string{
		"💾 Low disk space: 46 GB free",
		"**Free space:** 46 GB, under the 100 GB mark",
		"An agent is looking into it — attach in herdr: workspace `.mac-mini`, tab `disk-0000`. Details to follow.",
	},
	render: func(t *testing.T) string {
		f := newFixture(t)
		f.freeGB = 46
		f.at(0).sweep()
		return f.lastSent()
	},
}, {
	name:   "disk-critical",
	marker: wording.MarkerDown,
	wants: []string{
		"🔴 Disk critical: 1 GB free",
		"**Free space:** 1 GB, under the 20 GB mark",
	},
	render: func(t *testing.T) string {
		f := newFixture(t)
		f.freeGB = 1
		f.at(0).sweep()
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
		f := newFixture(t)
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
		f := newFixture(t)
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
		f := newFixture(t)
		busyProcess(f, true, "/usr/libexec/dasd", 147, false)
		return f.lastSent()
	},
}, {
	name:   "disk-and-cpu-in-one-message",
	marker: wording.MarkerDisk,
	wants: []string{
		"💾 Low disk space: 46 GB free",
		"**Free space:** 46 GB, under the 100 GB mark",
		"**Busy processes:**",
	},
	render: func(t *testing.T) string {
		f := newFixture(t)
		busyProcess(f, false, "/usr/local/bin/node worker.js", 7018, true)
		return f.lastSent()
	},
}, {
	name:   "nightly-update-one-step",
	marker: wording.MarkerDegraded,
	wants: []string{
		"⚠️ The nightly update could not upgrade the tools mise manages",
		"**Step:** `mise up`",
		"**Failing since:** less than a minute",
		"**Needs you:** nothing here tries it again before tomorrow night.",
		"An agent is looking into it — attach in herdr: workspace `.mac-mini`, tab `update-0000`. Details to follow.",
	},
	render: func(t *testing.T) string {
		f := failingUpdate(t)
		f.at(0).sweep()
		return f.lastSent()
	},
}, {
	name:   "nightly-update-several-steps",
	marker: wording.MarkerDegraded,
	wants: []string{
		"⚠️ The nightly update failed 2 steps",
		"**Step:** `brew cleanup` — could not clear what Homebrew replaced",
		"**Step:** `mise up` — could not upgrade the tools mise manages",
	},
	render: func(t *testing.T) string {
		f := failingUpdate(t)
		f.updateFailing = append(f.updateFailing,
			handed("brew cleanup", "clear what Homebrew replaced",
				"Permission denied @ apply2files - /opt/homebrew/Cellar/tailscale/1.104.1/bin/tailscaled",
				"**You can run:** `sudo rm -rf /opt/homebrew/Cellar/tailscale/1.104.1` — it is root's because `sudo brew services` ran out of it.",
				f.now))
		f.at(0).sweep()
		return f.lastSent()
	},
}, {
	name:   "nightly-update-nobody-answered",
	marker: wording.MarkerDegraded,
	wants: []string{
		"⚠️ No answer on the nightly update, and nothing retries it",
		"**Why:** Nothing here moves a version on its own",
		"**Waiting:** 33 minutes",
		"**If no answer:** leave it to tomorrow night's run",
		"Answer in herdr: workspace `.mac-mini`, tab `update-0000`.",
	},
	render: func(t *testing.T) string {
		f := failingUpdate(t)
		f.at(0).sweep()
		f.notifyWithFallback(f.onlyPendingID(), "leave it to tomorrow night's run")
		f.blocks("update")
		f.at(300).sweep()
		f.at(300 + remindAt).sweep()
		f.at(300 + warnAt).sweep()
		return f.lastSent()
	},
}, {
	name:   "no-report-from-the-agent",
	marker: wording.MarkerDegraded,
	wants: []string{
		"⚠️ No report from the agent on the disk incident after 10 minutes",
		"**Free space:** 46 GB, under the 100 GB mark",
		"Attach in herdr: workspace `.mac-mini`, tab `disk-0000`.",
	},
	render: func(t *testing.T) string {
		f := newFixture(t)
		f.freeGB = 46
		f.at(0).sweep()
		f.at(600).sweep()
		return f.lastSent()
	},
}, {
	name:   "reminder-at-an-hour",
	marker: wording.MarkerDegraded,
	wants: []string{
		"⚠️ Still no answer on the disk incident after 12 minutes",
		"**Now:** 46 GB free, under the 100 GB mark",
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
		"**Now:** 46 GB free, under the 100 GB mark",
	},
	render: func(t *testing.T) string {
		f := waitingOnAQuestion(t)
		f.at(600 + remindAt).sweep()
		f.at(600 + warnAt).sweep()

		text := f.lastSent()
		harness.Equal(t, session.FallbackOption(text), "stop pid 5073 and empty the log", "the option hachiko reads back")
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

	// Free space back over the mark is a change of its own, which reaches the agent first;
	// the news that nothing is firing any more takes two readings after it.
	f.freeGB = 500
	f.at(900).sweep()
	f.blocks("disk")
	f.at(1200).sweep()
	f.blocks("disk")
	f.at(1500).sweep()

	prompt := f.lastInterrupt()
	t.Logf("\n\nthe prompt that carries it:\n%s\n", prompt)

	harness.Wants(t, prompt, "Free space is back over every mark, at 500 GB.")
	harness.Wants(t, prompt, "What fired this incident is no longer firing")
	harness.Wants(t, prompt, "Check for yourself whether it has really resolved")
	harness.Wants(t, prompt, incident)
}
