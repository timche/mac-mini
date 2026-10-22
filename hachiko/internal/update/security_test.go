package update

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/harness"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
)

// `hachiko update --security <target>…`, against the same table of command lines and answers
// the nightly run is driven by.
func (b *box) security(dry bool, targets ...string) *updater {
	b.t.Helper()

	u := &updater{cfg: b.cfg, deps: b.deps(), store: Store{Dir: b.cfg.UpdateStateDir},
		hand: b.hand(), dry: dry, targets: targets}
	if err := u.runSecurity(); err != nil {
		b.t.Fatalf("the security upgrade failed: %v", err)
	}
	return u
}

const (
	miseLsGlobal = "mise ls -C " + home + " --json"
	miseUpNode   = "mise up -C " + home + " --yes --minimum-release-age " + noHold + " node"
	miseUpNodeIn = "mise up -C " + repo + " --local --yes --minimum-release-age " + noHold + " node"
)

// What `mise ls --json` says one tool is at, which is what moving is read off either side of
// the upgrade: `mise up` exits zero for a tool it has nothing newer of and for one it has
// never heard of.
func (b *box) miseHas(tool, version string) {
	b.says(miseLsGlobal, fmt.Sprintf(`{%q:[{"version":%q,"active":true}]}`, tool, version))
}

func (b *box) miseThenHas(tool, version string) {
	b.saysNext(miseLsGlobal, fmt.Sprintf(`{%q:[{"version":%q,"active":true}]}`, tool, version))
}

// One of mise's pythons with the install directory mise put it in, which is where the
// interpreter a pip upgrade has to run is.
func (b *box) misePython(version, path string) {
	b.says(miseLsGlobal,
		fmt.Sprintf(`{"python":[{"version":%q,"install_path":%q,"active":true}]}`, version, path))
}

// The git answers a tree with the lockfiles rewritten and nothing of anybody's under way
// gives, which is what lets the commit run.
func (b *box) lockfilesMoved() {
	b.says(gitStatus, " M mise.lock\n")
	b.says(gitBranch, "main\n")
	b.says(gitDefault, "origin/main\n")
	for _, ref := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD"} {
		b.fails("git -C "+repo+" rev-parse -q --verify "+ref, "")
	}
}

// Every one of these names came from an advisory feed — a formula name Homebrew printed, a
// package name OSV did — and ends up in a command line, so this is the guard that matters.
func TestOnlyTheFiveShapesOfTargetAreTaken(t *testing.T) {
	for _, target := range []string{
		"mise:node",
		// mise's npm backend calls its tools `npm:<package>`, which is the name mise prints and
		// so the name the check builds a target out of: the one mise tool an advisory feed can
		// be asked about by name carries a second colon.
		"mise:npm:portless",
		"brew:aria2",
		"pip:3.15.0:certifi",
		MiseSelfTarget,
		HerdrTarget,
	} {
		harness.Equal(t, ValidTarget(target), true, "whether "+target+" is a target")
	}

	for _, target := range []string{
		"",
		"brew:",
		"brew:-rf",
		"brew:aria 2",
		"brew:aria2;rm -rf /",
		"mise:$(whoami)",
		"pip:3.15.0",
		"npm:left-pad",
		"softwareupdate",
	} {
		harness.Equal(t, ValidTarget(target), false, "whether "+target+" is a target")
	}
}

// A process of its own, which is what anything inside hachiko that asks for one needs: moving
// a version takes minutes, and the five-minute check it would be asked from has five.
func TestASecurityUpgradeIsStartedAsAProcessOfItsOwn(t *testing.T) {
	var started [][]string
	start := func(args ...string) error {
		started = append(started, args)
		return nil
	}

	if err := security(start, []string{"brew:aria2", "mise:node"}); err != nil {
		t.Fatal(err)
	}

	harness.Equal(t, len(started), 1, "upgrades started")
	harness.Equal(t, strings.Join(started[0], " "), "update --security brew:aria2 mise:node", "what it started")
}

// Refused where it is built as well as where it runs, so the check that built it finds out
// rather than a tool being handed something a source chose.
func TestATargetThatWouldNotRunIsRefusedBeforeAnythingStarts(t *testing.T) {
	var started [][]string
	start := func(args ...string) error {
		started = append(started, args)
		return nil
	}

	err := security(start, []string{"brew:aria2", "brew:--force"})

	if err == nil {
		t.Fatal("an upgrade of something that is not a target was started")
	}
	harness.Wants(t, err.Error(), `"brew:--force" is not a target hachiko update takes`)
	harness.Equal(t, len(started), 0, "upgrades started")
}

// Only what the advisory named. A night's upgrading is a decision about keeping this Mac
// current; this is a decision about one fault, and a run that took everything with it could
// break something unrelated at an hour nobody chose.
func TestABrewTargetUpgradesThatFormulaAndNothingElse(t *testing.T) {
	b := newBox(t)
	b.moveBrew("aria2", "1.37.0", "1.38.0")

	b.security(false, "brew:aria2")

	harness.Equal(t, b.ran("brew upgrade aria2"), true, "the formula upgraded")
	// Whatever asked for this already knew there is a newer version to reach, so what is
	// missing is the upgrade and not the index.
	harness.Equal(t, b.ran("brew update"), false, "Homebrew's formulae fetched")
	harness.Equal(t, b.ran("brew upgrade"), false, "every other package upgraded")
	harness.Equal(t, len(b.matching("mise up")), 0, "mise calls")
	harness.Wants(t, b.summary(), "`aria2` 1.37.0 → 1.38.0")
}

// The target says which tool moves it and not which config declares it: `node` and `bun` are
// the global config's and `go` is this repository's own, and mise answers for the directory it
// is asked about.
func TestAMiseTargetIsTriedInBothConfigs(t *testing.T) {
	b := newBox(t)
	b.miseHas("node", "24.21.0")
	b.miseThenHas("node", "24.22.1")

	b.security(false, "mise:node")

	harness.Equal(t, b.ran(miseUpNode), true, "the global config")
	harness.Equal(t, b.ran(miseUpNodeIn), true, "this repository's own")
	// The nightly form, which moves every tool mise manages rather than the one named.
	harness.Equal(t, b.ran(miseUpGlobal), false, "the night's own upgrade")
	harness.Wants(t, b.summary(), "`node` 24.21.0 → 24.22.1")
	harness.Equal(t, len(b.failing()), 0, "steps left for the check")
}

// A tool the config does not declare is a `mise up` that moves nothing and says so, so the
// failure is only the one where neither config moved the version.
func TestAMiseTargetOneConfigRefusedIsNoFailureWhenTheVersionMoved(t *testing.T) {
	b := newBox(t)
	b.miseHas("node", "24.21.0")
	b.miseThenHas("node", "24.22.1")
	b.fails(miseUpNodeIn, "mise ERR no tool node in this config")

	b.security(false, "mise:node")

	harness.Equal(t, len(b.failing()), 0, "steps left for the check")
	harness.Wants(t, b.summary(), "Security update: 1 version moved")
}

func TestAMiseTargetThatMovedNowhereIsLeftForTheCheck(t *testing.T) {
	b := newBox(t)
	b.miseHas("node", "24.21.0")

	b.security(false, "mise:node")

	failing := b.failing()
	harness.Equal(t, len(failing), 1, "steps left for the check")
	harness.Equal(t, failing[0].Step, stepSecurity, "the step")
	harness.Equal(t, failing[0].Phrase, "upgrade what a published advisory named", "the phrase")
	harness.Wants(t, failing[0].Detail, "node is still 24.21.0")
	harness.Wants(t, failing[0].Action, "mise up -C ~ --minimum-release-age 0d node")
	harness.Wants(t, b.summary(), "Security update: nothing moved, 1 target failed")
}

// The same commit as a night's, because it is the same files: a Mac rebuilt from these
// lockfiles would otherwise install the version the advisory was about.
func TestTheLockfilesASecurityUpgradeRewroteAreCommitted(t *testing.T) {
	b := newBox(t)
	b.miseHas("node", "24.21.0")
	b.miseThenHas("node", "24.22.1")
	b.lockfilesMoved()

	b.security(false, "mise:node")

	harness.Equal(t, b.ran("git -C "+repo+" commit -m mise up moves node -- "+lockPaths), true, "the commit")
	harness.Equal(t, b.ran(gitPush), true, "the push")
}

// mise's own hold is a different setting and a different flag from its tools', because a
// self-update is a GitHub release download rather than a tool install.
func TestMiseItselfSelfUpdatesPastItsOwnHold(t *testing.T) {
	b := newBox(t)
	b.says("mise --version", "2026.10.11 macos-arm64 (2026-10-15)")
	b.saysNext("mise --version", "2026.10.20 macos-arm64 (2026-10-19)")

	b.security(false, MiseSelfTarget)

	harness.Equal(t, b.ran("mise self-update --yes --minimum-release-age "+noHold), true, "the self-update")
	harness.Wants(t, b.summary(), "`mise` 2026.10.11 → 2026.10.20")
}

// Nothing asks for an out-of-cycle upgrade a second time, so the line says what does come
// round: the night's own run, once the hold this one was bypassing has passed.
func TestAFailedSecurityStepWithNothingToSuggestSaysNothingAsksAgain(t *testing.T) {
	b := newBox(t)
	b.says("mise --version", "2026.10.11 macos-arm64 (2026-10-15)")
	b.fails("mise self-update --yes --minimum-release-age "+noHold, "mise ERR the release could not be downloaded")

	b.security(false, MiseSelfTarget)

	failing := b.failing()
	harness.Equal(t, len(failing), 1, "steps left for the check")
	harness.Wants(t, failing[0].Action, "nothing asks for this upgrade again")
	harness.Wants(t, failing[0].Action, "tomorrow night's run moves the version once the release-age hold has passed")
	harness.Wants(t, failing[0].Action, "`hachiko update --security`")
}

// herdr refuses to update from inside one of its own panes, and the server is left alone
// either way: restarting it would end every session on this Mac.
func TestHerdrIsUpdatedAsThoughFromOutsideIt(t *testing.T) {
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	t.Setenv("HACHIKO_KEPT", "yes")

	b := newBox(t)
	b.moveHerdr("0.9.5", "0.9.6")

	b.security(false, HerdrTarget)

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
	harness.Wants(t, b.summary(), "takes the new binary at its next restart")
}

// There is a python per version here and `pip install` upgrades whichever interpreter ran it,
// so the wrong one would report success and leave the fault where it was.
func TestAPipTargetIsUpgradedInThePythonTheTargetNames(t *testing.T) {
	installed := home + "/.local/share/mise/installs/python/3.15.0"

	b := newBox(t)
	b.misePython("3.15.0", installed)

	b.security(false, PipTarget("3.15.0", "certifi"))

	harness.Equal(t, b.ran(installed+"/bin/python3 -m pip install --upgrade certifi"), true, "the upgrade")
	harness.Wants(t, b.summary(), "certifi was upgraded in python 3.15.0")
}

func TestAPipTargetForAPythonMiseNoLongerHasIsSaid(t *testing.T) {
	b := newBox(t)
	b.misePython("3.14.7", home+"/.local/share/mise/installs/python/3.14.7")

	b.security(false, PipTarget("3.15.0", "certifi"))

	failing := b.failing()
	harness.Equal(t, len(failing), 1, "steps left for the check")
	harness.Wants(t, failing[0].Detail, "mise has no python 3.15.0 installed any more")
	harness.Equal(t, len(b.matching("pip install")), 0, "pip calls")
}

// A `mise up` of everything and a `mise up` of one tool are two processes writing one
// lockfile, and the night's run is moving the same tool forward anyway.
func TestASecurityUpgradeFindingTheNightlyRunGoingDoesNothing(t *testing.T) {
	b := newBox(t)

	lock, _, err := statedir.LockDir(b.cfg.UpdateLock, lockStale, b.now, "nobody")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	b.security(false, "brew:aria2")

	harness.Equal(t, len(b.calls), 0, "commands run")
	harness.Equal(t, len(b.sent), 0, "messages sent")
	harness.Wants(t, b.log.String(), "an update is already running, so this security upgrade does nothing")
}

// The stamp is what says a night's run happened, and a security upgrade at noon that wrote
// one would mean no nightly run that night.
func TestASecurityUpgradeWritesNoLastRunStamp(t *testing.T) {
	b := newBox(t)
	b.moveBrew("aria2", "1.37.0", "1.38.0")

	b.security(false, "brew:aria2")

	if _, err := os.Stat(b.cfg.UpdateStamp()); err == nil {
		t.Error("a security upgrade wrote the stamp")
	}

	// And one run after a night's leaves that night's where it was, rather than putting the
	// next one off by a day.
	lastNight := b.now.Add(-8 * time.Hour)
	if err := (Store{Dir: b.cfg.UpdateStateDir}).Stamp(lastNight); err != nil {
		t.Fatal(err)
	}

	b.security(false, "brew:aria2")

	harness.Equal(t, LastRun(b.cfg.UpdateStamp()).Unix(), lastNight.Unix(), "when an update last ran")
}

// The hand-off is rewritten whole by whichever run wrote it last, so a security upgrade that
// attempted neither has to leave the night's own account of them alone: its silence about
// `brew update` is not evidence that `brew update` works.
func TestASecurityUpgradeSaysNothingAboutWhatTheNightlyRunIsFailingOn(t *testing.T) {
	b := newBox(t)
	b.current()
	b.fails("brew update", "fatal: unable to access https://github.com/Homebrew/brew")

	b.update(false)
	was := b.failing()
	harness.Equal(t, len(was), 1, "steps left for the check")
	harness.Equal(t, was[0].Step, stepBrewUpdate, "the step")

	b.raised(stepBrewUpdate, "update-1", "thread-1")
	b.sent = nil
	b.now = b.now.Add(8 * time.Hour)
	b.moveBrew("aria2", "1.37.0", "1.38.0")

	b.security(false, "brew:aria2")

	still := b.failing()
	harness.Equal(t, len(still), 1, "steps left for the check")
	harness.Equal(t, still[0].Step, stepBrewUpdate, "the step")
	harness.Equal(t, still[0].Detail, was[0].Detail, "what the night's run said about it")
	harness.Equal(t, still[0].Since, was[0].Since, "when it started failing")
	harness.Equal(t, len(b.incidents()), 0, "messages saying a step works again")
}

// Its own step is one it is answerable for, so it is raised like any other and cleared by the
// run that managed it.
func TestASecurityUpgradeThatFailedIsRaisedAndThenCleared(t *testing.T) {
	b := newBox(t)
	b.fails("brew upgrade aria2", "Error: aria2 has no bottle for this macOS")

	b.security(false, "brew:aria2")

	failing := b.failing()
	harness.Equal(t, len(failing), 1, "steps left for the check")
	harness.Equal(t, failing[0].Step, stepSecurity, "the step")
	harness.Wants(t, failing[0].Detail, "no bottle for this macOS")
	harness.Wants(t, failing[0].Action, "brew upgrade aria2")

	b.raised(stepSecurity, "update-1", "thread-1")
	b.sent = nil
	b.now = b.now.Add(3 * time.Hour)
	delete(b.answers, "brew upgrade aria2")

	b.security(false, "brew:aria2")

	cleared := b.incidents()
	harness.Equal(t, len(cleared), 1, "messages on the run that managed it")
	harness.Wants(t, cleared[0].Text, "🟢 A security upgrade can upgrade what a published advisory named again")
	harness.Equal(t, cleared[0].Thread, "thread-1", "the thread it cleared into")
	harness.Equal(t, cleared[0].Outcome, true, "the clear as the incident's outcome")
	harness.Equal(t, len(b.failing()), 0, "steps left for the check")
}

// A night's run is a report on keeping the Mac current and a security upgrade is a report on
// one fault, so an upgrade that moved nothing is the news rather than the absence of it —
// where a quiet night goes unmentioned.
func TestASecurityUpgradeAlwaysSaysSomethingInTheUpdatesChannel(t *testing.T) {
	b := newBox(t)
	b.says("brew list --versions", "aria2 1.37.0\n")

	b.security(false, "brew:aria2")

	harness.Equal(t, len(b.sent), 1, "messages sent")
	harness.Equal(t, b.sent[0].Channel, b.cfg.Discord.UpdatesID, "where the summary went")
	harness.Wants(t, b.summary(), "ℹ️ Security update: nothing moved")
	harness.Lacks(t, b.summary(), "Nightly update")
}

func TestADrySecurityUpgradeChangesNothing(t *testing.T) {
	b := newBox(t)
	b.miseHas("node", "24.21.0")
	b.says("mise --version", "2026.10.11 macos-arm64 (2026-10-15)")
	b.says("herdr --version", "herdr 0.9.5")
	b.says("brew list --versions", "aria2 1.37.0\n")
	b.lockfilesMoved()

	b.security(true, "mise:node", "brew:aria2", MiseSelfTarget, HerdrTarget)

	for _, call := range b.calls {
		for _, forbidden := range []string{"up -C", "self-update", "brew upgrade", "brew cleanup",
			"herdr update", "pip install", "commit", "push"} {
			if strings.Contains(call, forbidden) {
				t.Errorf("a dry run ran %q", call)
			}
		}
	}

	harness.Equal(t, len(b.sent), 0, "messages sent")
	harness.Equal(t, len(b.failing()), 0, "steps left for the check")
	harness.Wants(t, b.log.String(), "would upgrade node to the newest release, with no release-age hold")
	harness.Wants(t, b.log.String(), "would upgrade the Homebrew package aria2, which is at 1.37.0")
	harness.Wants(t, b.log.String(), "would self-update with no release-age hold")

	if _, err := os.Stat(filepath.Join(b.cfg.UpdateStateDir, "state.json")); err == nil {
		t.Error("a dry run wrote the state")
	}
	if _, err := os.Stat(b.cfg.UpdateStamp()); err == nil {
		t.Error("a dry run wrote the stamp")
	}
}
