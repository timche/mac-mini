package vuln

import (
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// mise keeps the install it upgraded away from until `upgrade.prune_after` takes it, so a Mac
// with a fault fixed an hour ago still has the faulty version on disk.
func TestOnlyTheVersionMiseCallsActiveCounts(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.says(miseLsHome, `{"node":[`+
		miseEntry("24.21.0", "/installs/node/24.21.0", false)+","+
		miseEntry("24.22.0", "/installs/node/24.22.0", true)+`]}`)

	inv := b.inventory()

	harness.Equal(t, inv.version(fromMise, "node"), "24.22.0", "the node that runs")
	harness.Equal(t, len(inv.from(fromMise)), 1, "mise tools in the inventory")

	// `go` is active in this repository's own config and nowhere else, so the global listing
	// alone would never mention the toolchain these tools are built with — and a tool active
	// in both listings is one tool.
	both := newBox(t)
	both.clean()
	both.says(miseLsHome, `{"node":[`+miseEntry("24.22.0", "/installs/node/24.22.0", true)+`]}`)
	both.says(miseLsRepo, `{"go":[`+miseEntry("1.27.3", "/installs/go/1.27.3", true)+`],`+
		`"node":[`+miseEntry("24.22.0", "/installs/node/24.22.0", true)+`]}`)

	listed := both.inventory()

	harness.Equal(t, listed.version(fromMise, "go"), "1.27.3", "the toolchain")
	harness.Equal(t, len(listed.from(fromMise)), 2, "mise tools across both listings")
}

// mise's npm backend installs real npm packages, so those are the one kind of mise tool OSV
// can be asked about by name; the rest are runtimes with no package registry behind them.
func TestAnNPMBackendToolIsAskedAboutUnderItsPackageName(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.says(miseLsHome, `{"npm:portless":[`+miseEntry("1.4.0", "/installs/npm-portless/1.4.0", true)+`],`+
		`"bun":[`+miseEntry("1.4.3", "/installs/bun/1.4.3", true)+`]}`)

	inv := b.inventory()
	in, ok := inv.named(fromMise, "portless")
	bun, _ := inv.named(fromMise, "bun")

	harness.Equal(t, ok, true, "portless is in the inventory")
	harness.Equal(t, in.Ecosystem, ecosystemNPM, "the ecosystem OSV knows it by")
	harness.Equal(t, in.Target, "mise:npm:portless", "the target mise up takes back")
	harness.Equal(t, bun.Ecosystem, "", "the ecosystem of a runtime OSV has no record of")
	harness.Equal(t, bun.Target, "mise:bun", "what moves bun")
}

// There is one of these per python and an upgrade has to reach the right one, so the python is
// named by its own install directory rather than by what is on PATH.
func TestPipIsAskedOfTheActivePythonsOwnInterpreter(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.says(miseLsHome, `{"python":[`+miseEntry("3.15.0", "/installs/python/3.15.0", true)+`]}`)
	b.says("/installs/python/3.15.0/bin/python3 -m pip list --format=json",
		`[{"name": "pip", "version": "26.2.2"}, {"name": "certifi", "version": "2026.9.1"}]`)

	inv := b.inventory()
	in, ok := inv.named(fromPip, "certifi")

	harness.Equal(t, ok, true, "certifi is in the inventory")
	harness.Equal(t, in.Version, "2026.9.1", "the version")
	harness.Equal(t, in.Ecosystem, ecosystemPyPI, "the ecosystem")
	harness.Equal(t, in.Target, "pip:3.15.0:certifi", "the target, which says which python")
	harness.Equal(t, len(inv.from(fromPip)), 2, "packages read out of that python")
}

func TestARequireBlockAndASingleRequireAreBothRead(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.file(repo+"/hachiko/go.mod", `module github.com/timche/mac-mini/hachiko

go 1.27

require github.com/bwmarrin/discordgo v0.29.0

require (
	// the gateway client, which this does not hand-roll
	golang.org/x/crypto v0.0.0-20220314234659-1baeb1ce4c0b // indirect
	golang.org/x/sys v0.38.0
)
`)
	b.file(repo+"/mochi/go.mod", "module mochi\n")

	inv := b.inventory()
	discord, _ := inv.named(fromGo, "github.com/bwmarrin/discordgo")
	crypto, _ := inv.named(fromGo, "golang.org/x/crypto")
	sys, _ := inv.named(fromGo, "golang.org/x/sys")

	harness.Equal(t, discord.Version, "v0.29.0", "a single-line require")
	harness.Equal(t, sys.Version, "v0.38.0", "a require inside the block")
	// OSV takes a pseudo-version as it is written, and rewriting one would be this check
	// deciding what a module is.
	harness.Equal(t, crypto.Version, "v0.0.0-20220314234659-1baeb1ce4c0b", "a pseudo-version")
	harness.Equal(t, discord.Ecosystem, ecosystemGo, "the ecosystem")
	harness.Equal(t, discord.Target, "", "a module bump is a commit somebody means to make")
}

// Asked at the `go` directive, OSV answers about every 1.27.x fault including the ones this Mac
// already has the fix for, so a crypto/x509 distrust landing in 1.27.3 would read as unfixed on
// a Mac running 1.27.3.
func TestTheStandardLibraryIsAskedAboutAtTheToolchainMiseHas(t *testing.T) {
	t.Run("mise answered", func(t *testing.T) {
		b := newBox(t)
		b.clean()
		b.says(miseLsRepo, `{"go":[`+miseEntry("1.27.3", "/installs/go/1.27.3", true)+`]}`)
		b.file(repo+"/hachiko/go.mod", "module hachiko\n\ngo 1.27\n")

		in, ok := b.inventory().named(fromGo, "stdlib")

		harness.Equal(t, ok, true, "the standard library is in the inventory")
		harness.Equal(t, in.Version, "1.27.3", "the toolchain rather than the language version")
		harness.Equal(t, in.Ecosystem, ecosystemGo, "the ecosystem")
	})

	t.Run("mise could not say", func(t *testing.T) {
		b := newBox(t)
		b.clean()
		b.file(repo+"/hachiko/go.mod", "module hachiko\n\ngo 1.27\n")

		in, _ := b.inventory().named(fromGo, "stdlib")

		harness.Equal(t, in.Version, "1.27", "the declared version, which is the fallback")
	})
}

// A nested dependency's key carries the whole path it is installed at, and the root entry is
// the project itself and has no version.
func TestALockfileEntryIsNamedByItsInnermostNodeModulesSegment(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.file(repo+"/shibuya/package-lock.json", `{
  "name": "shibuya",
  "lockfileVersion": 3,
  "packages": {
    "": {"name": "shibuya", "devDependencies": {"wrangler": "4.147.0"}},
    "node_modules/@cloudflare/kv-asset-handler": {"version": "0.5.0"},
    "node_modules/wrangler/node_modules/esbuild": {"version": "0.25.4"}
  }
}`)

	inv := b.inventory()
	nested, ok := inv.named(fromNPM, "esbuild")

	harness.Equal(t, ok, true, "the nested package is named by its own name")
	harness.Equal(t, nested.Version, "0.25.4", "its version")
	harness.Equal(t, len(inv.from(fromNPM)), 2, "packages read, the root entry being the project itself")

	scoped, _ := inv.named(fromNPM, "@cloudflare/kv-asset-handler")
	harness.Equal(t, scoped.Ecosystem, ecosystemNPM, "the ecosystem")
	harness.Equal(t, scoped.Target, "", "shibuya's packages move with a deploy to Cloudflare")
}

// Nothing between these three and this check knows their versions, so each is read by asking
// it — and the version is in a different column of each one's answer.
func TestTheThreeInstallerShippedToolsAreReadByAskingThem(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.says("mise --version", "2026.10.11 macos-arm64 (2026-10-15)")
	b.says("herdr --version", "herdr 0.9.5")
	b.says("claude --version", "2.1.40 (Claude Code)")

	inv := b.inventory()
	mise, _ := inv.named(fromTool, "mise")
	herdr, _ := inv.named(fromTool, "herdr")
	claude, _ := inv.named(fromTool, "claude-code")

	harness.Equal(t, mise.Version, "2026.10.11", "mise")
	harness.Equal(t, herdr.Version, "0.9.5", "herdr, whose version is the second field")
	harness.Equal(t, claude.Version, "2.1.40", "Claude Code")

	harness.Equal(t, mise.Target, "mise-self", "what moves mise")
	harness.Equal(t, herdr.Target, "herdr", "what moves herdr")
	// Pinning Claude Code would mean turning its updater off and then owning what the updater
	// was doing.
	harness.Equal(t, claude.Target, "", "what moves Claude Code")
}

// Homebrew's names are what the feed matches by, where a CVE about curl or openssh is about
// this Mac because Homebrew put one here — and macOS is in the list for the same reason, being
// the one thing with no target at all: an install wants a volume owner's password.
func TestHomebrewsFormulaeAndCasksAndMacOSAreAllInTheInventory(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.says("brew list --versions", "curl 8.18.0\nlibssh2 1.11.1\n")
	b.says("brew list --cask --versions", "orbstack 2.1.0\n")
	b.says("sw_vers --productVersion", "27.1\n")

	inv := b.inventory()
	curl, _ := inv.named(fromBrew, "curl")
	orb, _ := inv.named(fromBrew, "orbstack")
	mac, ok := inv.named(fromMacOS, "macos")

	harness.Equal(t, curl.Target, "brew:curl", "what moves a formula")
	harness.Equal(t, orb.Version, "2.1.0", "a cask's version")
	harness.Equal(t, len(inv.from(fromBrew)), 3, "formulae and casks")

	harness.Equal(t, inv.MacOS, "27.1", "the version the feed matcher names")
	harness.Equal(t, ok, true, "macOS is in the inventory")
	harness.Equal(t, mac.Target, "", "what moves macOS")
}

// A source that did not answer is not a source that found nothing, and the difference goes in
// the message.
func TestAReadingThatFailedIsSaidRatherThanTakenAsNothingInstalled(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.fails("brew list --versions", "brew: command not found")
	b.says(miseLsHome, "not json at all")
	b.fails(miseLsRepo, "mise: no such config")
	b.fails("sw_vers --productVersion", "killed")
	delete(b.files, repo+"/mochi/go.mod")
	delete(b.files, repo+"/shibuya/package-lock.json")

	troubles := b.inventory().Troubles

	text := Found{Troubles: troubles}.troubles()
	harness.Wants(t, text, "brew could not list what is installed: brew: command not found")
	harness.Wants(t, text, "mise answered something that is not JSON about the tools in "+home)
	harness.Wants(t, text, "mise could not say which tools are active in "+repo)
	harness.Wants(t, text, "the go.mod of mochi could not be read")
	harness.Wants(t, text, "shibuya's lockfile could not be read")
	harness.Wants(t, text, "macOS could not say what version it is: killed")

	// There is one pip reading per python, so the line says which.
	pip := newBox(t)
	pip.clean()
	pip.says(miseLsHome, `{"python":[`+miseEntry("3.15.0", "/installs/python/3.15.0", true)+`]}`)
	pip.fails("/installs/python/3.15.0/bin/python3 -m pip list --format=json", "No module named pip")

	harness.Wants(t, Found{Troubles: pip.inventory().Troubles}.troubles(),
		"pip could not list what is installed in python 3.15.0: No module named pip")
}
