package update

import (
	"errors"
	"fmt"
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
		ohMyZshPull,
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

// The restart is news about the version that made it necessary, so it is read beside that
// version rather than on a line of its own further down — and beside portless's where both
// moved, a dev URL that is down being about portless far more often than about node.
func TestTheProxyRestartGoesOnTheMoveItFollowedFrom(t *testing.T) {
	t.Run("node and portless both moved", func(t *testing.T) {
		b := newBox(t)
		b.current()
		b.moveTool(outdatedLine("node", "lts", "24.21.0", "24.22.0") +
			outdatedLine("npm:portless", "latest", "0.15.11", "0.15.12"))

		b.update(false)

		harness.Wants(t, b.summary(), "`npm:portless` 0.15.11 → 0.15.12 · dev-URL proxy restarted")
		harness.Wants(t, b.summary(), "`node` 24.21.0 → 24.22.0\n")
	})

	t.Run("only node moved", func(t *testing.T) {
		b := newBox(t)
		b.current()
		b.moveTool(outdatedLine("node", "lts", "24.21.0", "24.22.0"))

		b.update(false)

		harness.Wants(t, b.summary(), "`node` 24.21.0 → 24.22.0 · dev-URL proxy restarted")
	})
}

func TestAProxyReinstallThatFailsIsAnIncident(t *testing.T) {
	b := newBox(t)
	b.current()
	b.moveTool(outdatedLine("node", "lts", "24.21.0", "24.22.0"))
	b.fails("sudo -n /usr/local/libexec/claude-root reinstall-portless", "sudo: a password is required")

	b.update(false)

	failing := b.failing()
	harness.Equal(t, len(failing), 1, "steps left for the check")
	harness.Equal(t, failing[0].Phrase, "put portless's proxy daemon back", "the phrase")
	harness.Wants(t, failing[0].Detail, "sudo: a password is required")
	harness.Wants(t, failing[0].Action, "portless service install")
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
		harness.Equal(t, len(b.failing()), 0, "steps left for the check")
		harness.Wants(t, b.summary(), "· tailscaled restarted, back on the tailnet")
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

		failing := b.failing()
		harness.Equal(t, len(failing), 1, "steps left for the check")
		harness.Equal(t, failing[0].Phrase, "restart tailscaled after its upgrade", "the phrase")
		harness.Wants(t, failing[0].Detail, "rather than Running")
		harness.Wants(t, failing[0].Action, "sudo brew services restart tailscale")
		harness.Wants(t, failing[0].Action, "from the LAN")
	})
}

// The keg an upgrade replaced stays on disk until `brew cleanup` clears it, and brew lists a
// package's versions in directory order — so the version that moved is the one that is new,
// wherever brew happens to print it.
func TestABrewUpgradeIsSeenBesideTheKegItReplaced(t *testing.T) {
	restart := "sudo -n /usr/local/libexec/claude-root restart-daemon tailscale"

	for name, after := range map[string]string{
		"listed after the old keg":  "tailscale 1.104.1 1.104.2\n",
		"listed before the old keg": "tailscale 1.104.2 1.104.1\n",
	} {
		t.Run(name, func(t *testing.T) {
			b := newBox(t)
			b.current()
			b.says("brew list --versions", "tailscale 1.104.1\n")
			b.saysNext("brew list --versions", after)
			b.says("brew list --cask --versions", "")
			b.says("tailscale status --json", `{"BackendState":"Running"}`)

			b.update(false)

			harness.Equal(t, b.ran(restart), true, "restarted")
			harness.Wants(t, b.summary(), "· tailscaled restarted, back on the tailnet")
		})
	}

	t.Run("a night after one that left the old keg", func(t *testing.T) {
		b := newBox(t)
		b.current()
		b.says("brew list --versions", "tailscale 1.104.1 1.104.2\n")
		b.says("brew list --cask --versions", "")

		b.update(false)
		harness.Equal(t, b.ran(restart), false, "restarted")
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

	failing := b.failing()
	harness.Equal(t, len(failing), 1, "steps left for the check")
	harness.Equal(t, failing[0].Phrase, "clear what Homebrew replaced", "the phrase")
	harness.Wants(t, failing[0].Action, "sudo rm -rf /opt/homebrew/Cellar/tailscale/1.104.1")
	harness.Wants(t, b.summary(), "- `brew cleanup`: could not clear what Homebrew replaced\n```\nPermission denied @ apply2files")
}

func TestHerdrIsUpdatedWithoutRestartingTheServer(t *testing.T) {
	b := newBox(t)
	b.current()
	b.moveHerdr("0.9.5", "0.9.6")
	b.herdrServerOn("0.9.5")

	b.update(false)

	harness.Wants(t, b.summary(), "`herdr` 0.9.5 → 0.9.6")
	harness.Wants(t, b.summary(), "the herdr server is running `0.9.5` and the binary is `0.9.6`")

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

// oh-my-zsh is a clone rather than a release, so what it moved between is the two commits
// either side of the pull — read back the way every other step's versions are, rather than
// taken from git having printed something other than "Already up to date".
func TestOhMyZshMovesBetweenTwoCommits(t *testing.T) {
	t.Run("it moved", func(t *testing.T) {
		b := newBox(t)
		b.current()
		b.moveOhMyZsh("a1b2c3d", "e4f5a6b")

		b.update(false)

		harness.Wants(t, b.summary(), "`oh-my-zsh` a1b2c3d → e4f5a6b")
	})

	t.Run("it was already current", func(t *testing.T) {
		b := newBox(t)
		b.current()
		b.says(ohMyZshHead, "a1b2c3d\n")

		b.update(false)

		harness.Equal(t, len(b.sent), 0, "messages sent")
	})
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

	// And a night where nothing moved still posts, because what is waiting is a reading of the
	// Mac rather than news of tonight: it is waiting again tomorrow until it is installed.
	harness.Wants(t, b.summary(), "⚠️ Nightly update: nothing moved, 1 needs you")
	harness.Wants(t, b.summary(), "**Needs you:**\n- `macOS Tahoe 27.1` is waiting: macOS installs it "+
		"by itself and restarts the Mac at a time it picks, and you can install it sooner in "+
		"System Settings › General › Software Update")

	for _, call := range b.calls {
		if strings.Contains(call, "--install") {
			t.Errorf("it tried to install a macOS update: %q", call)
		}
	}
}

// The server stays on the binary it started from until something restarts it, and only Tim
// chooses when that is — so the line about it is in every summary until he has, and not in the
// one on the night the binary moved.
func TestTheHerdrServerIsSaidOnEveryNightUntilItIsRestarted(t *testing.T) {
	b := newBox(t)
	b.current()
	b.says("herdr --version", "herdr 0.9.6")
	b.herdrServerOn("0.9.5")

	b.update(false)

	harness.Wants(t, b.summary(), "⚠️ Nightly update: nothing moved, 1 needs you")
	harness.Wants(t, b.summary(), "**Needs you:**\n- the herdr server is running `0.9.5` and the binary "+
		"is `0.9.6`: the server takes the new one at its next restart, which ends every session on this Mac")
}

func TestAServerOnTheVersionTheBinaryIsOnIsNothingWaiting(t *testing.T) {
	b := newBox(t)
	b.current()
	b.herdrServerOn("0.9.5")

	b.update(false)

	harness.Equal(t, len(b.sent), 0, "messages sent")
	harness.Wants(t, b.log.String(), "nothing moved and nothing is waiting")
}

// Two of them is "need you" rather than "needs you", and the count in the lead is what Tim
// reads off a locked phone.
func TestTheLeadCountsWhatIsWaitingOnHim(t *testing.T) {
	b := newBox(t)
	b.current()
	b.moveTool(outdatedLine("bun", "latest", "1.4.2", "1.4.3"))
	b.says("herdr --version", "herdr 0.9.6")
	b.herdrServerOn("0.9.5")
	b.says("softwareupdate --list",
		"	Title: macOS 27.2, Version: 27.2, Size: 7101234KiB, Recommended: YES, Action: restart,\n")

	b.update(false)

	harness.Wants(t, b.summary(), "⚠️ Nightly update: 1 version moved, 2 need you")
}

// Apple prints the version of a macOS release in its title as well as in its own column, and
// the title is what Tim reads: "macOS 27.2 27.2" is the same number twice. Whether the install
// restarts the Mac is the rest of that line's to say.
func TestAnAppleTitleCarriesItsVersionOnlyOnce(t *testing.T) {
	waiting := appleWaiting(`* Label: macOS Sequoia 27.2-27C50
	Title: macOS 27.2, Version: 27.2, Size: 7101234KiB, Recommended: YES, Action: restart,
* Label: Safari27.2TahoeAuto-27.2
	Title: Safari, Version: 27.2, Size: 136752KiB, Recommended: YES,
* Label: Command Line Tools for Xcode-27.2
	Title: Command Line Tools for Xcode, Version: 27.2, Size: 756121KiB, Recommended: NO,
`)

	var read []string
	for _, update := range waiting {
		read = append(read, fmt.Sprintf("%s (restart: %t)", update.title, update.restart))
	}

	harness.Equal(t, strings.Join(read, "; "), "macOS 27.2 (restart: true); Safari 27.2 (restart: false); "+
		"Command Line Tools for Xcode 27.2 (restart: false)", "what is waiting")
}

// The hand-off carries the whole of what a message about a step needs, the one line for Tim
// included — the check knows a step by its name and nothing else.
func TestAHandoffCarriesTheLineForTimEvenWithNothingToSuggest(t *testing.T) {
	b := newBox(t)
	b.current()
	b.fails(miseUpGlobal, "mise ERR failed to install node@24.22.0")

	b.update(false)

	failing := b.failing()
	harness.Equal(t, len(failing), 1, "steps left for the check")
	harness.Equal(t, failing[0].Step, stepMiseTools, "the step")
	harness.Equal(t, failing[0].Phrase, "upgrade the tools mise manages", "the phrase")
	harness.Equal(t, failing[0].Since, b.now.Unix(), "when it started failing")
	harness.Wants(t, failing[0].Detail, "mise ERR failed to install node@24.22.0")
	harness.Wants(t, failing[0].Action, "**Needs you:** nothing here tries it again before tomorrow night.")
	harness.Wants(t, failing[0].Action, "The step is `mise up` in `hachiko update`.")
}

// A step that keeps failing stays one entry in the hand-off, and the line saying it works
// again goes into the thread of the post the check opened about it.
func TestAFailingStepIsHandedOnOnceAndThenCleared(t *testing.T) {
	b := newBox(t)
	b.current()
	b.fails("herdr update", "herdr: the release could not be downloaded")

	b.update(false)
	harness.Wants(t, b.log.String(), "left 1 step for the five-minute check to raise as an incident: "+stepHerdr)
	harness.Equal(t, len(b.incidents()), 0, "messages from the update itself")

	since := b.failing()[0].Since
	b.raised(stepHerdr, "update-1", "thread-1")

	// Still failing a night later, and still one entry with the clock running from the first
	// night rather than this one — so the check has nothing new to raise.
	b.now = b.now.Add(24 * time.Hour)
	b.update(false)
	harness.Equal(t, len(b.failing()), 1, "steps left for the check")
	harness.Equal(t, b.failing()[0].Since, since, "when it started failing")
	harness.Equal(t, len(b.incidents()), 0, "messages from the update itself")

	b.now = b.now.Add(24 * time.Hour)
	delete(b.answers, "herdr update")
	b.update(false)

	cleared := b.incidents()
	harness.Equal(t, len(cleared), 1, "messages on the night it worked")
	harness.Wants(t, cleared[0].Text, "🟢 The nightly update can update herdr again")
	harness.Equal(t, cleared[0].Thread, "thread-1", "the thread it cleared into")
	// Which is what closes the post and gets it deleted three days later, rather than leaving
	// a step that came back open in the forum for ever.
	harness.Equal(t, cleared[0].Outcome, true, "the clear as the incident's outcome")

	// And gone from the hand-off, which is how the check knows there is nobody left to wait
	// for on that incident.
	harness.Equal(t, len(b.failing()), 0, "steps left for the check")
}

// Two steps raised as one incident share its post, so the first to work again says so in the
// thread and leaves it open, and the last one closes it.
func TestOnlyTheLastStepUnderAnIncidentClosesItsPost(t *testing.T) {
	b := newBox(t)
	b.current()
	b.fails("herdr update", "herdr: the release could not be downloaded")
	b.fails(ohMyZshPull, "fatal: not possible to fast-forward")

	b.update(false)
	b.raised(stepHerdr, "update-1", "thread-1")
	b.raised(stepOhMyZsh, "update-1", "thread-1")

	b.now = b.now.Add(24 * time.Hour)
	delete(b.answers, "herdr update")
	b.update(false)

	first := b.incidents()
	harness.Equal(t, len(first), 1, "messages on the night one worked")
	harness.Equal(t, first[0].Outcome, false, "a clear with a sibling still failing as the outcome")

	b.sent = nil
	b.now = b.now.Add(24 * time.Hour)
	b.says(ohMyZshPull, "Already up to date.")
	b.update(false)

	last := b.incidents()
	harness.Equal(t, len(last), 1, "messages on the night the other worked")
	harness.Equal(t, last[0].Outcome, true, "the last clear as the outcome")
}

// A clear that did not send is a step nobody has heard is over, so it stays on the hand-off
// and the next night says it again.
func TestAClearThatCouldNotBeSentIsSaidAgain(t *testing.T) {
	b := newBox(t)
	b.current()
	b.fails("herdr update", "herdr: the release could not be downloaded")

	b.update(false)
	b.raised(stepHerdr, "update-1", "thread-1")

	b.now = b.now.Add(24 * time.Hour)
	delete(b.answers, "herdr update")
	b.sendErr = errors.New("Discord answered 500")
	b.update(false)

	harness.Wants(t, b.log.String(), "did not send and is left to the next update")
	harness.Equal(t, len(b.failing()), 1, "steps left for the check")

	b.sent, b.sendErr = nil, nil
	b.now = b.now.Add(24 * time.Hour)
	b.update(false)

	harness.Equal(t, len(b.incidents()), 1, "messages on the night after")
	harness.Equal(t, len(b.failing()), 0, "steps left for the check")
}

// A step that failed and worked again before any check had raised it is one nobody was ever
// told about, so there is nothing to tell them is over.
func TestAStepNobodyHeardAboutClearsInSilence(t *testing.T) {
	b := newBox(t)
	b.current()
	b.fails("herdr update", "herdr: the release could not be downloaded")
	b.update(false)

	b.now = b.now.Add(24 * time.Hour)
	delete(b.answers, "herdr update")
	b.update(false)

	harness.Equal(t, len(b.incidents()), 0, "messages sent")
	harness.Equal(t, len(b.failing()), 0, "steps left for the check")
}

// A hand-off that could not be written is a failure nothing will ever raise, so the next
// night writes the whole of what is failing again rather than assuming it arrived.
func TestAHandoffThatCouldNotBeWrittenIsWrittenAgain(t *testing.T) {
	b := newBox(t)
	b.current()
	b.fails("herdr update", "herdr: the release could not be downloaded")

	// A file where the directory has to go, which is what a write that cannot happen looks
	// like from here.
	harness.WriteFile(t, b.cfg.UpdateStateDir, "")

	b.update(false)
	harness.Wants(t, b.log.String(), "could not be left for the five-minute check")

	if err := os.Remove(b.cfg.UpdateStateDir); err != nil {
		t.Fatal(err)
	}
	b.now = b.now.Add(24 * time.Hour)
	b.update(false)

	harness.Equal(t, len(b.failing()), 1, "steps left for the check")
}

func TestACommandsOutputHasBothStreamsInIt(t *testing.T) {
	for range 20 {
		out := run(Command{Name: "sh", Args: []string{"-c", "echo out; echo err >&2"}, Timeout: quick})
		harness.Wants(t, out.Text, "out")
		harness.Wants(t, out.Text, "err")
		harness.Equal(t, out.Stdout, "out\n", "stdout")
	}
}
