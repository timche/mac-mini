package update

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/harness"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
)

func at(hour, minute int) time.Time {
	return time.Date(2026, 10, 18, hour, minute, 0, 0, time.UTC)
}

// The whole of the schedule: one run a night, counted from when the last one finished, and a
// Mac that was off at four catching up rather than waiting a day.
func TestDue(t *testing.T) {
	cases := []struct {
		name string
		now  time.Time
		last time.Time
		want bool
	}{
		{"before the hour", at(3, 59), at(0, 0).AddDate(0, 0, -1), false},
		{"on the hour with yesterday's run behind it", at(4, 0), at(4, 1).AddDate(0, 0, -1), true},
		{"already run today", at(6, 0), at(4, 2), false},
		{"run today but before the hour, so still owing", at(6, 0), at(3, 30), true},
		{"a Mac that was off until the afternoon", at(15, 0), at(4, 5).AddDate(0, 0, -1), true},
		{"a Mac that has never updated", at(4, 30), time.Time{}, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			harness.Equal(t, Due(c.now, c.last, 4), c.want, "due")
		})
	}
}

func TestNightlyStartsAnUpdateOfItsOwn(t *testing.T) {
	b := newBox(t)
	b.now = at(4, 30)

	if err := nightly(b.cfg, b.deps(), false); err != nil {
		t.Fatal(err)
	}

	harness.Equal(t, len(b.spawned), 1, "updates started")
	harness.Equal(t, strings.Join(b.spawned[0], " "), "update", "what it started")
	harness.Wants(t, b.log.String(), "started the nightly update")
}

func TestNightlyDoesNothingBeforeTheHour(t *testing.T) {
	b := newBox(t)
	b.now = at(3, 0)

	if err := nightly(b.cfg, b.deps(), false); err != nil {
		t.Fatal(err)
	}
	harness.Equal(t, len(b.spawned), 0, "updates started")
	harness.Equal(t, b.log.String(), "", "what it said")
}

func TestNightlyDryRunStartsNothing(t *testing.T) {
	b := newBox(t)
	b.now = at(4, 30)

	if err := nightly(b.cfg, b.deps(), true); err != nil {
		t.Fatal(err)
	}
	harness.Equal(t, len(b.spawned), 0, "updates started")
	harness.Wants(t, b.log.String(), "would start one")
}

// A run that finished is what the next check reads, so an update that has just run is not
// one the check starts again five minutes later.
func TestAFinishedUpdateStopsTheNextCheckStartingOne(t *testing.T) {
	b := newBox(t)
	b.current()
	b.update(false)

	if err := nightly(b.cfg, b.deps(), false); err != nil {
		t.Fatal(err)
	}
	harness.Equal(t, len(b.spawned), 0, "updates started")
}

// An upgrade takes tens of minutes and the check that started it runs every five, so the
// second one has to find the first and leave it alone.
func TestAnUpdateHoldsOffAnother(t *testing.T) {
	b := newBox(t)
	b.current()

	lock, _, err := statedir.LockDir(b.cfg.UpdateLock, lockStale, b.now, "nobody")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	b.update(false)

	harness.Equal(t, len(b.calls), 0, "commands run")
	harness.Wants(t, b.log.String(), "another update is running")

	if _, err := os.Stat(b.cfg.UpdateStamp()); err == nil {
		t.Error("the stamp was written by an update that did nothing")
	}
}

func TestEveryStepRunsInOrder(t *testing.T) {
	b := newBox(t)
	b.current()
	b.update(false)

	want := []string{
		"mise self-update --yes",
		miseUpGlobal,
		miseUpRepo,
		"brew update",
		"brew upgrade",
		"brew cleanup",
		"herdr update",
		"git -C " + home + "/.oh-my-zsh pull --ff-only",
		"softwareupdate --list",
	}

	order := 0
	for _, call := range b.calls {
		if order < len(want) && call == want[order] {
			order++
		}
	}
	if order != len(want) {
		t.Errorf("stopped at %q; ran:\n%s", want[order], strings.Join(b.calls, "\n"))
	}
}

func TestADryRunChangesNothing(t *testing.T) {
	b := newBox(t)
	b.current()
	b.says(miseOutdatedGlobal, outdatedLine("node", "lts", "24.21.0", "24.22.0"))
	b.says("brew outdated", "tailscale\n")

	b.update(true)

	for _, call := range b.calls {
		for _, forbidden := range []string{"up -C", "self-update", "brew upgrade", "brew cleanup",
			"herdr update", "pull", "commit", "push", "claude-root"} {
			if strings.Contains(call, forbidden) {
				t.Errorf("a dry run ran %q", call)
			}
		}
	}

	harness.Equal(t, len(b.sent), 0, "messages sent")
	harness.Wants(t, b.log.String(), "would upgrade node from 24.21.0 to 24.22.0")
	harness.Wants(t, b.log.String(), "would upgrade tailscale")
	harness.Wants(t, b.log.String(), "tailscaled would be restarted")

	if _, err := os.Stat(b.cfg.UpdateStamp()); err == nil {
		t.Error("a dry run wrote the stamp")
	}
}

// What moved is read off what is outdated either side of the upgrade rather than off what a
// command printed, and a tool still outdated at the version it was did not move at all.
func TestWhatMovedIsReadBack(t *testing.T) {
	b := newBox(t)
	b.current()
	b.says(miseOutdatedGlobal,
		outdatedLine("node", "lts", "24.21.0", "24.22.0")+outdatedLine("python", "latest", "3.14.7", "3.15.0"))
	b.saysNext(miseOutdatedGlobal, outdatedLine("python", "latest", "3.14.7", "3.15.0"))
	b.says(miseOutdatedRepo, outdatedLine("go", "1.27", "1.27.1", "1.27.2"))
	b.saysNext(miseOutdatedRepo, "")

	u := b.update(false)

	harness.Equal(t, len(u.moved), 2, "things that moved")
	harness.Wants(t, b.summary(), "`node` 24.21.0 → 24.22.0")
	harness.Wants(t, b.summary(), "`go` 1.27.1 → 1.27.2")
	harness.Lacks(t, b.summary(), "python")
}

func TestTheSummarySaysWhatMoved(t *testing.T) {
	b := newBox(t)
	b.current()
	b.moveTool(outdatedLine("npm:portless", "latest", "0.15.6", "0.15.8"))

	u := b.update(false)

	harness.Equal(t, len(u.moved), 1, "things that moved")
	harness.Equal(t, u.moved[0].What, "npm:portless", "what moved")

	summary := b.summary()
	harness.Wants(t, summary, "ℹ️ Nightly update: 1 version moved")
	harness.Wants(t, summary, "**Moved:**")
	harness.Wants(t, summary, "`npm:portless` 0.15.6 → 0.15.8")
	harness.Wants(t, summary, "mac-mini")
}

func TestNothingMovedSaysNothingToDiscord(t *testing.T) {
	b := newBox(t)
	b.current()
	b.update(false)

	harness.Equal(t, len(b.sent), 0, "messages sent")
	harness.Wants(t, b.log.String(), "nothing moved")
}

func TestWithNoUpdatesChannelTheSummaryStaysInTheLog(t *testing.T) {
	b := newBox(t)
	b.cfg.Discord.UpdatesID = ""
	b.current()
	b.moveTool(outdatedLine("bun", "latest", "1.4.2", "1.4.3"))

	b.update(false)

	harness.Equal(t, len(b.sent), 0, "messages sent")
	harness.Wants(t, b.log.String(), "bun moved from 1.4.2 to 1.4.3")
	harness.Wants(t, b.log.String(), "no updates channel is configured")
}

// The proxy daemon is root running a versioned path, so it is put back when — and only
// when — the node or the portless under it moved.
func TestTheProxyDaemonIsReinstalledWhenNodeMoves(t *testing.T) {
	for _, c := range []struct {
		name  string
		line  string
		again bool
	}{
		{"node moved", outdatedLine("node", "lts", "24.21.0", "24.22.0"), true},
		{"portless moved", outdatedLine("npm:portless", "latest", "0.15.6", "0.15.8"), true},
		{"something else moved", outdatedLine("python", "latest", "3.14.7", "3.15.0"), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := newBox(t)
			b.current()
			b.moveTool(c.line)

			b.update(false)

			harness.Equal(t,
				b.ran("sudo -n /usr/local/libexec/claude-root reinstall-portless"), c.again, "reinstalled")
		})
	}
}

func TestAProxyReinstallThatFailsIsAnIncident(t *testing.T) {
	b := newBox(t)
	b.current()
	b.moveTool(outdatedLine("node", "lts", "24.21.0", "24.22.0"))
	b.fails("sudo -n /usr/local/libexec/claude-root reinstall-portless", "sudo: a password is required")

	b.update(false)

	incidents := b.incidents()
	harness.Equal(t, len(incidents), 1, "incidents")
	harness.Wants(t, incidents[0].Text, "could not put portless's proxy daemon back")
	harness.Wants(t, incidents[0].Text, "sudo: a password is required")
	harness.Wants(t, incidents[0].Text, "portless service install")
}

// launchd reads tailscale's plist once and the plist names the opt symlink, so the daemon
// stays on the binary it exec'd until something restarts it — and nothing else here does.
func TestTailscaledIsRestartedOnlyWhenItsFormulaMoved(t *testing.T) {
	restart := "sudo -n /usr/local/libexec/claude-root restart-daemon tailscale"

	t.Run("it moved", func(t *testing.T) {
		b := newBox(t)
		b.current()
		b.moveBrew("tailscale", "1.104.1", "1.106.0")
		b.says("tailscale status --json", `{"BackendState":"Running"}`)

		b.update(false)

		harness.Equal(t, b.ran(restart), true, "restarted")
		harness.Equal(t, len(b.incidents()), 0, "incidents")
		harness.Wants(t, b.summary(), "tailscaled was restarted")
	})

	t.Run("something else moved", func(t *testing.T) {
		b := newBox(t)
		b.current()
		b.moveBrew("starship", "1.25.0", "1.26.0")

		b.update(false)
		harness.Equal(t, b.ran(restart), false, "restarted")
	})

	t.Run("it did not come back", func(t *testing.T) {
		b := newBox(t)
		b.current()
		b.moveBrew("tailscale", "1.104.1", "1.106.0")
		b.says("tailscale status --json", `{"BackendState":"NoState"}`)

		b.update(false)

		incidents := b.incidents()
		harness.Equal(t, len(incidents), 1, "incidents")
		harness.Wants(t, incidents[0].Text, "could not restart tailscaled after its upgrade")
		harness.Wants(t, incidents[0].Text, "sudo brew services restart tailscale")
		harness.Wants(t, incidents[0].Text, "from the LAN")
	})
}

// `sudo brew services start` takes root ownership of the keg it runs tailscaled out of, so
// the first cleanup after a tailscale upgrade stops on a directory it cannot remove — and
// what Tim needs is the one command that clears it.
func TestACleanupThatFailsNamesTheKeg(t *testing.T) {
	b := newBox(t)
	b.current()
	b.moveBrew("tailscale", "1.104.1", "1.106.0")
	b.says("tailscale status --json", `{"BackendState":"Running"}`)
	b.fails("brew cleanup", "Permission denied @ apply2files - /opt/homebrew/Cellar/tailscale/1.104.1/bin/tailscaled")

	b.update(false)

	incidents := b.incidents()
	harness.Equal(t, len(incidents), 1, "incidents")
	harness.Wants(t, incidents[0].Text, "could not clear what Homebrew replaced")
	harness.Wants(t, incidents[0].Text, "sudo rm -rf /opt/homebrew/Cellar/tailscale/1.104.1")
}

func TestHerdrIsUpdatedWithoutRestartingTheServer(t *testing.T) {
	b := newBox(t)
	b.current()
	b.moveHerdr("0.9.5", "0.9.6")

	b.update(false)

	harness.Wants(t, b.summary(), "`herdr` 0.9.5 → 0.9.6")
	harness.Wants(t, b.summary(), "takes the new binary at its next restart")

	for _, call := range b.calls {
		if strings.Contains(call, "herdr server") || strings.Contains(call, "kickstart") {
			t.Errorf("the herdr server was touched: %q", call)
		}
	}
}

func TestHerdrIsUpdatedAsThoughFromOutsideHerdr(t *testing.T) {
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	t.Setenv("HACHIKO_KEPT", "yes")

	b := newBox(t)
	b.current()
	b.update(false)

	var found bool
	for _, cmd := range b.run {
		if cmd.line() != "herdr update" {
			continue
		}
		found = true
		env := strings.Join(cmd.Env, "\n")
		if strings.Contains(env, "HERDR_") {
			t.Errorf("herdr update was handed herdr's own environment: %q", env)
		}
		harness.Wants(t, env, "HACHIKO_KEPT=yes")
	}
	if !found {
		t.Fatal("herdr update was never run")
	}
}

func TestAMacOSUpdateIsSaidAndNotInstalled(t *testing.T) {
	b := newBox(t)
	b.current()
	b.says("softwareupdate --list", `Software Update Tool

Finding available software
Software Update found the following new or updated software:
* Label: macOS Tahoe 27.1-27B55
	Title: macOS Tahoe 27.1, Version: 27.1, Size: 7101234KiB, Recommended: YES, Action: restart,
`)

	b.update(false)

	harness.Wants(t, b.summary(), "macOS has 1 update waiting")
	harness.Wants(t, b.summary(), "macOS Tahoe 27.1")

	for _, call := range b.calls {
		if strings.Contains(call, "--install") {
			t.Errorf("it tried to install a macOS update: %q", call)
		}
	}
}

// A step that keeps failing is one message for as long as it does, and the line saying it
// works again goes under that one.
func TestAFailingStepIsSaidOnceAndThenCleared(t *testing.T) {
	b := newBox(t)
	b.current()
	b.fails("herdr update", "herdr: the release could not be downloaded")

	b.update(false)
	harness.Equal(t, len(b.incidents()), 1, "incidents on the first night")
	harness.Wants(t, b.incidents()[0].OpenThread, stepHerdr)

	b.sent = nil
	b.now = b.now.Add(24 * time.Hour)
	b.update(false)
	harness.Equal(t, len(b.incidents()), 0, "incidents on the second night")

	b.sent = nil
	b.now = b.now.Add(24 * time.Hour)
	delete(b.answers, "herdr update")
	b.update(false)

	cleared := b.incidents()
	harness.Equal(t, len(cleared), 1, "messages on the night it worked")
	harness.Wants(t, cleared[0].Text, "🟢 The nightly update can update herdr again")
	harness.Equal(t, cleared[0].Thread, "thread-"+stepHerdr, "the thread it cleared into")
}

// A failure nobody has heard about is not a failure already reported.
func TestAFailureThatCouldNotBeSentIsSaidAgain(t *testing.T) {
	b := newBox(t)
	b.current()
	b.fails("herdr update", "herdr: the release could not be downloaded")
	b.sendErr = errors.New("Discord answered 500")

	b.update(false)
	harness.Wants(t, b.log.String(), "did not send and is left to the next update")

	b.sent, b.sendErr = nil, nil
	b.now = b.now.Add(24 * time.Hour)
	b.update(false)

	harness.Equal(t, len(b.incidents()), 1, "incidents on the second night")
}
