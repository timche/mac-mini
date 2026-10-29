package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// An out-of-cycle run that moves only what a published advisory names. The upgrade is here and
// not beside whatever named the advisory because of the rule this repository holds to:
// `hachiko update` is the one thing on this Mac that moves a version, and a second place that
// moved one would be a second one. So a caller names the targets and this moves them, through
// the same readings, the same lockfile commit, the same summary and the same hand-off to the
// five-minute check that a night's run uses.
//
// Only the targets, and nothing else that happens to be outdated. A night's upgrading is a
// decision about keeping this Mac current; this is a decision about one fault, and a run that
// took everything with it could break something unrelated at an hour nobody chose.
const stepSecurity = "security upgrade"

// What the hold is set to for one of these. The release age exists so that a hijacked or
// broken release has a day to be pulled before this Mac takes it, and that trade is the wrong
// way round for a version that fixes something published: waiting a day is the exposure.
//
// `0d` rather than `0`, because the two commands that take it disagree about the bare number —
// `mise up` accepts it and `mise self-update` parses it as a duration and refuses it — and a
// duration is accepted by both.
const noHold = "0d"

// The shape a target may have, and the whole of what makes one safe to hand a tool: every one
// of these names came from an advisory feed, by way of a formula name Homebrew printed or a
// package name OSV did, and it ends up in a command line. Letters, digits and the punctuation
// a package name really uses, and it may not start with one of them — so nothing a source
// chose can arrive as a flag, a path or a second command.
//
// The colon is in that set because mise's own names carry one: the tools it installs through
// its npm backend are `npm:portless` and `npm:ccstatusline`, which makes `mise:npm:portless`
// the target for one of them. A set without it refused that outright, and since one bad target
// refuses the whole call, a portless advisory started no upgrade at all.
var targetName = regexp.MustCompile(`\A[A-Za-z0-9][A-Za-z0-9._@/:+-]*\z`)

// The three kinds of target that name something, by the prefix that says which tool moves it.
// A prefix rather than working it out from the name, because `node` is a tool mise manages and
// is also a plausible Homebrew formula, and an upgrade aimed at the wrong one does nothing and
// reports that it worked.
const (
	targetMise = "mise:"
	targetBrew = "brew:"
	targetPip  = "pip:"

	// And the two that are one thing each, so there is no name after them.
	MiseSelfTarget = "mise-self"
	HerdrTarget    = "herdr"
)

// The spelling of each kind, so whatever names a target and this, which reads it back,
// cannot be two spellings.
func MiseTarget(tool string) string    { return targetMise + tool }
func BrewTarget(formula string) string { return targetBrew + formula }

// A pip package is installed into one python rather than onto the machine, so the target says
// which: mise keeps a python per version, and `pip install` reaches whichever interpreter runs
// it.
func PipTarget(python, pkg string) string { return targetPip + python + ":" + pkg }

// Security starts the run as a process of its own, which is what anything inside hachiko that
// asks for one needs: moving a version takes minutes, and the five-minute check it would be
// asked from has five. A target this would not run is refused here rather than passed on, so
// whatever built it finds out.
func Security(cfg config.Config, targets []string) error {
	return security(realDeps(cfg).Spawn, targets)
}

func security(start func(args ...string) error, targets []string) error {
	if len(targets) == 0 {
		return nil
	}

	for _, target := range targets {
		if !ValidTarget(target) {
			return fmt.Errorf("%q is not a target hachiko update takes",
				wording.Safe(target, wording.NameLimit))
		}
	}
	return start(append([]string{"update", "--security"}, targets...)...)
}

// Whether a target is one of the five shapes, with a name this would pass to a tool. Checked
// where it is built and again where it runs, because the second of those is a command a person
// or a session can type: `hachiko update --security` takes this and nothing else.
func ValidTarget(target string) bool {
	switch {
	case target == MiseSelfTarget || target == HerdrTarget:
		return true
	case strings.HasPrefix(target, targetMise):
		return targetName.MatchString(strings.TrimPrefix(target, targetMise))
	case strings.HasPrefix(target, targetBrew):
		return targetName.MatchString(strings.TrimPrefix(target, targetBrew))
	case strings.HasPrefix(target, targetPip):
		python, pkg, named := strings.Cut(strings.TrimPrefix(target, targetPip), ":")
		return named && targetName.MatchString(python) && targetName.MatchString(pkg)
	default:
		return false
	}
}

// RunSecurity is `hachiko update --security <target>…`: the steps those targets name and no
// others, the lockfile commit behind them, and then the one summary and the one hand-off every
// run here ends with.
func RunSecurity(cfg config.Config, targets []string, dry bool) error {
	for _, target := range targets {
		if !ValidTarget(target) {
			return fmt.Errorf("%q is not a target hachiko update takes",
				wording.Safe(target, wording.NameLimit))
		}
	}

	return (&updater{
		cfg:     cfg,
		deps:    realDeps(cfg),
		store:   Store{Dir: cfg.UpdateStateDir},
		hand:    Handoff{Dir: cfg.UpdateStateDir, WatchDir: cfg.StateDir},
		dry:     dry,
		targets: targets,
	}).runSecurity()
}

func (u *updater) runSecurity() error {
	now := u.deps.Now()

	// The nightly run's lock, because the two may not move a version at the same time: a
	// `mise up` of everything and a `mise up` of one tool would be two processes writing one
	// lockfile. A security upgrade that finds the night's run going says so and leaves it —
	// the night's run is moving the same tool forward anyway.
	if !u.dry {
		lock, takenFrom, err := statedir.LockDir(u.cfg.UpdateLock, lockStale, now, "an earlier update")

		switch {
		case errors.Is(err, statedir.ErrLockHeld):
			u.say("an update is already running, so this security upgrade does nothing and the version is left to that run")
			return nil
		case err != nil:
			u.say("the lock %s could not be taken, so this security upgrade runs without one: %v", u.cfg.UpdateLock, err)
		}

		if lock != nil {
			defer lock.Release()
		}
		if takenFrom != "" {
			u.say("taking over a lock left behind by %s", takenFrom)
		}
	}

	state, err := u.store.Load()
	if err != nil {
		u.say("%v", err)
	}

	u.say("upgrading what a published advisory names, ahead of the release-age hold: %s",
		strings.Join(u.targets, ", "))

	moved := u.securitySteps()

	// The same commit as a night's, because it is the same files: `mise up` rewrote this
	// checkout's lockfiles and a Mac rebuilt from them would otherwise install the version the
	// advisory was about.
	u.lockfiles(moved)

	if u.dry {
		u.sayWaiting()
		u.say("dry run over")
		return nil
	}

	u.report(state, now)

	if err := u.store.Save(state); err != nil {
		u.say("the state could not be written, so what has been reported is forgotten: %v", err)
	}

	// No stamp. The stamp is what says a night's run happened, and this is not one: a security
	// upgrade at noon that wrote it would mean no nightly run that night, and the nightly run
	// is what moves everything else.
	return nil
}

// Each target in turn, and the names of the mise tools that moved — which is what the lockfile
// commit's subject is written from.
func (u *updater) securitySteps() []string {
	var moved []string

	for _, target := range u.targets {
		switch {
		case target == MiseSelfTarget:
			u.securityMiseSelf()
		case target == HerdrTarget:
			u.herdrServer(u.securityHerdr())
		case strings.HasPrefix(target, targetMise):
			tool := strings.TrimPrefix(target, targetMise)
			if u.securityMiseTool(tool) {
				moved = append(moved, tool)
			}
		case strings.HasPrefix(target, targetBrew):
			u.securityBrew(strings.TrimPrefix(target, targetBrew))
		case strings.HasPrefix(target, targetPip):
			python, pkg, _ := strings.Cut(strings.TrimPrefix(target, targetPip), ":")
			u.securityPip(python, pkg)
		}
	}
	return moved
}

// One tool, in whichever of this Mac's two configs declares it. Both are tried because the
// target does not say which: `node` and `bun` are the global config's and `go` is this
// repository's own, and mise answers for the directory it is asked about. A tool the config
// does not declare is a `mise up` that moves nothing, so the failure is only reported when
// neither config moved it.
func (u *updater) securityMiseTool(tool string) bool {
	before := u.toolVersion(tool)

	if u.dry {
		u.say("would upgrade %s to the newest release, with no release-age hold", tool)
		return true
	}

	var said []string
	for _, where := range []struct {
		dir   string
		local []string
	}{
		{dir: u.cfg.Home},
		{dir: u.cfg.MachineDir, local: []string{"--local"}},
	} {
		args := append([]string{"up", "-C", where.dir}, where.local...)
		args = append(args, "--yes", "--minimum-release-age", noHold, tool)

		out := u.deps.Run(Command{Name: "mise", Args: args, Dir: where.dir, Timeout: toolUpgrade})
		if !out.OK {
			said = append(said, out.Text)
		}
	}

	after := u.toolVersion(tool)
	if after != "" && after != before {
		u.move(tool, before, after)
		return true
	}

	u.failed(Failure{
		Step:   stepSecurity,
		Detail: strings.Join(append(said, fmt.Sprintf("%s is still %s", tool, before)), "\n"),
		Action: "**Needs you:** " + wording.CodeSpan("mise up -C ~ --minimum-release-age 0d "+tool) +
			" — a published advisory names the version this Mac is on and mise did not move it.",
	})
	return false
}

// What mise says the tool is at, in whichever config it is active in. Read either side of the
// upgrade, because that difference is the only honest answer to whether it moved — `mise up`
// exits zero for a tool it has nothing newer of and for a tool it does not know.
func (u *updater) toolVersion(tool string) string {
	for _, dir := range []string{u.cfg.Home, u.cfg.MachineDir} {
		out := u.deps.Run(Command{
			Name: "mise", Args: []string{"ls", "-C", dir, "--json"}, Dir: dir, Timeout: quick,
		})
		if !out.OK {
			continue
		}

		var listed map[string][]struct {
			Version string `json:"version"`
			Active  bool   `json:"active"`
		}
		if json.Unmarshal([]byte(out.Stdout), &listed) != nil {
			continue
		}
		for _, v := range listed[tool] {
			if v.Active {
				return v.Version
			}
		}
	}
	return ""
}

// mise itself, past its own hold for the same reason the tools are past theirs. Its hold is a
// different setting from theirs and a different flag, because a self-update is a GitHub
// release download rather than a tool install.
func (u *updater) securityMiseSelf() {
	before := u.miseVersion()

	if u.dry {
		u.say("mise is %s; would self-update with no release-age hold", before)
		return
	}

	if out := u.deps.Run(Command{
		Name: "mise", Args: []string{"self-update", "--yes", "--minimum-release-age", noHold},
		Dir: u.cfg.Home, Timeout: selfUpdate,
	}); !out.OK {
		u.failed(Failure{Step: stepSecurity, Detail: out.Text})
		return
	}
	u.move("mise", before, u.miseVersion())
}

// One Homebrew formula or cask, and never a `brew update` before it: fetching Homebrew's
// formulae is a step of the night's run, and whatever asked for this already knew there is a
// newer version to reach — so what is missing here is the upgrade and not the index.
func (u *updater) securityBrew(formula string) {
	before := u.brewVersions()

	if u.dry {
		u.say("would upgrade the Homebrew package %s, which is at %s", formula, before[formula].String())
		return
	}

	if out := u.deps.Run(Command{
		Name: "brew", Args: []string{"upgrade", formula}, Timeout: brewUpgrade,
	}); !out.OK {
		u.failed(Failure{
			Step:   stepSecurity,
			Detail: out.Text,
			Action: "**Needs you:** " + wording.CodeSpan("brew upgrade "+formula) +
				" — a published advisory names the version installed here.",
		})
		return
	}

	after := u.brewVersions()
	if from, to, ok := upgraded(before[formula], after[formula]); ok {
		u.move(formula, from, to)
	}

	// What the upgrade replaced, the same way a night's run clears it: a keg left behind is
	// disk, and the one that will not go is root's, which the night's own cleanup explains.
	u.cleanup(before, after)
}

// One pip package in one of mise's pythons, reached through that python rather than through a
// `pip` on PATH: there is a python per version here and `pip install` upgrades whichever
// interpreter ran it, so the wrong one would report success and leave the fault where it was.
func (u *updater) securityPip(python, pkg string) {
	exe := u.pythonAt(python)
	if exe == "" {
		u.failed(Failure{
			Step:   stepSecurity,
			Detail: fmt.Sprintf("mise has no python %s installed any more, so %s could not be upgraded in it", python, pkg),
		})
		return
	}

	if u.dry {
		u.say("would upgrade the pip package %s in python %s", pkg, python)
		return
	}

	before := u.pipVersion(exe, pkg)

	if out := u.deps.Run(Command{
		Name: exe, Args: []string{"-m", "pip", "install", "--upgrade", pkg}, Timeout: toolUpgrade,
	}); !out.OK {
		u.failed(Failure{
			Step:   stepSecurity,
			Detail: out.Text,
			Action: "**Needs you:** " + wording.CodeSpan(exe+" -m pip install --upgrade "+pkg) +
				" — a published advisory names the version in that python.",
		})
		return
	}

	// Read back rather than taken from the install having exited zero, which it does for a
	// package it had nothing newer of: this ran because an advisory named the version in that
	// python, so the version not having moved is the fault still being there.
	after := u.pipVersion(exe, pkg)
	if after == "" || after == before {
		u.failed(Failure{
			Step: stepSecurity,
			Detail: fmt.Sprintf("pip installed %s in python %s and it is %s", pkg, python,
				pipStillAt(before, after)),
			Action: "**Needs you:** " + wording.CodeSpan(exe+" -m pip install --upgrade "+pkg) +
				" — a published advisory names the version in that python and pip did not move it.",
		})
		return
	}

	u.move(pkg, before, after)
	u.qualify("in python "+wording.CodeSpan(wording.Safe(python, wording.NameLimit))+
		", which a published advisory named", pkg)
}

// Which way the reading failed, since the two are different faults: a version that did not
// move is pip having nothing newer, and one that cannot be read is a package pip no longer
// lists at all.
func pipStillAt(before, after string) string {
	if after == "" {
		return "no longer a package pip can name a version for"
	}
	return "still " + after
}

// What pip says is installed, out of `pip show`'s own `Version:` line.
func (u *updater) pipVersion(exe, pkg string) string {
	out := u.deps.Run(Command{Name: exe, Args: []string{"-m", "pip", "show", pkg}, Timeout: quick})
	if !out.OK {
		return ""
	}
	for _, line := range strings.Split(out.Stdout, "\n") {
		if label, version, found := strings.Cut(line, ":"); found && strings.TrimSpace(label) == "Version" {
			return strings.TrimSpace(version)
		}
	}
	return ""
}

// The interpreter of one of mise's pythons, by the version the target named. Asked of mise
// rather than built from a path, because where mise puts an install is mise's business and a
// path written here would be whichever layout was current the day it was written.
func (u *updater) pythonAt(version string) string {
	for _, dir := range []string{u.cfg.Home, u.cfg.MachineDir} {
		out := u.deps.Run(Command{
			Name: "mise", Args: []string{"ls", "-C", dir, "--json"}, Dir: dir, Timeout: quick,
		})
		if !out.OK {
			continue
		}

		var listed map[string][]struct {
			Version     string `json:"version"`
			InstallPath string `json:"install_path"`
		}
		if json.Unmarshal([]byte(out.Stdout), &listed) != nil {
			continue
		}
		for _, v := range listed["python"] {
			if v.Version == version && v.InstallPath != "" {
				return filepath.Join(v.InstallPath, "bin", "python3")
			}
		}
	}
	return ""
}

// herdr, which has no hold to bypass: it is its own installer and takes whatever its latest
// release is. The server is left alone, as it is on a night's run — restarting it would end
// every session on this Mac — so it picks the new binary up at its next start, and what comes
// back is the version it would pick up.
func (u *updater) securityHerdr() string {
	before := u.herdrVersion()

	if u.dry {
		u.say("herdr is %s; would run herdr update", before)
		return before
	}

	if out := u.deps.Run(Command{
		Name: "herdr", Args: []string{"update"}, Timeout: selfUpdate, Env: outsideHerdr(os.Environ()),
	}); !out.OK {
		u.failed(Failure{Step: stepSecurity, Detail: out.Text})
		return before
	}

	after := u.herdrVersion()
	u.move("herdr", before, after)
	return after
}

// Which steps this run is answerable for. A night's run attempts every one of them, so a step
// missing from what it found failing is a step that works again; a security upgrade attempts
// two, and must say nothing at all about the rest — its own silence about `brew update` is not
// evidence that brew update works.
func (u *updater) answerable(step string) bool {
	if len(u.targets) == 0 {
		return true
	}
	return step == stepSecurity || step == stepLockfiles
}
