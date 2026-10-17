package update

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/statedir"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// Each step's own deadline, because this runs with nobody at it: a download from a mirror
// that has stopped answering, or a tool that decided to ask something, would otherwise hold
// the lock until the Mac restarts. Each is many times the longest real run of it — a full
// Homebrew upgrade with a cask or two is minutes rather than tens of them, and the readings
// either side of it are one process each.
const (
	quick         = 2 * time.Minute
	selfUpdate    = 10 * time.Minute
	toolUpgrade   = 30 * time.Minute
	brewUpgrade   = 60 * time.Minute
	brewHousework = 20 * time.Minute
	gitTimeout    = 5 * time.Minute
	appleList     = 15 * time.Minute
)

// The steps, by the name a message calls each of them and the state remembers it by.
const (
	stepMiseSelf   = "mise self-update"
	stepMiseTools  = "mise up"
	stepLockfiles  = "lockfile push"
	stepPortless   = "portless reinstall"
	stepBrewUpdate = "brew update"
	stepBrewUp     = "brew upgrade"
	stepBrewClean  = "brew cleanup"
	stepTailscale  = "tailscale restart"
	stepHerdr      = "herdr update"
	stepOhMyZsh    = "oh-my-zsh pull"
	stepApple      = "softwareupdate list"
)

// mise itself, from its own installer the way its install page asks for — which is also why
// nothing else here can move it: Homebrew's formula is not what put it on this Mac.
func (u *updater) miseSelf() {
	before := u.miseVersion()

	if u.dry {
		u.say("mise is %s; would run mise self-update", before)
		return
	}

	if out := u.deps.Run(Command{
		Name: "mise", Args: []string{"self-update", "--yes"}, Dir: u.cfg.Home, Timeout: selfUpdate,
	}); !out.OK {
		u.failed(Failure{Step: stepMiseSelf, Detail: out.Text})
		return
	}

	u.move("mise", before, u.miseVersion())
}

func (u *updater) miseVersion() string {
	out := u.deps.Run(Command{Name: "mise", Args: []string{"--version"}, Dir: u.cfg.Home, Timeout: quick})
	if !out.OK {
		return ""
	}
	return field(firstLine(out.Stdout), 0)
}

// The tools mise manages, in the two configs this Mac has: the global one every project
// reads, and this repository's own, which declares the go its compiled tools are built with.
// Each call names its directory, since mise decides what is active from the directory it is
// asked about and a LaunchAgent starts in `/` — and `--local` is what keeps the second call
// to the repository's own tools rather than upgrading the global ones a second time.
//
// What comes back is the names that moved, which is what decides whether the proxy daemon
// has to be put back on a node that is no longer there.
func (u *updater) miseTools() []string {
	before := u.outdated()

	if u.dry {
		for _, name := range statedir.SortedKeys(before) {
			u.say("would upgrade %s from %s to %s", name, before[name].current, before[name].latest)
		}
		if len(before) == 0 {
			u.say("every tool mise manages is current")
		}
		// The names it would move, so the steps that depend on which of them did are a dry
		// run too rather than silence.
		return statedir.SortedKeys(before)
	}

	for _, run := range []Command{
		{Name: "mise", Args: []string{"up", "-C", u.cfg.Home, "--yes"}, Dir: u.cfg.Home, Timeout: toolUpgrade},
		{Name: "mise", Args: []string{"up", "-C", u.cfg.MachineDir, "--local", "--yes"},
			Dir: u.cfg.MachineDir, Timeout: toolUpgrade},
	} {
		if out := u.deps.Run(run); !out.OK {
			u.failed(Failure{Step: stepMiseTools, Detail: out.Text})
		}
	}

	// What is outdated now, which is the honest reading of what moved: a tool that is no
	// longer listed is at the version the first reading called the latest, and one still
	// listed at the same version did not move at all.
	after := u.outdated()

	var moved []string
	for _, name := range statedir.SortedKeys(before) {
		was := before[name]
		if still, ok := after[name]; ok && still.current == was.current {
			continue
		}
		moved = append(moved, name)
		u.move(name, was.current, was.latest)
	}
	return moved
}

type outdatedTool struct{ current, latest string }

func (u *updater) outdated() map[string]outdatedTool {
	tools := map[string]outdatedTool{}

	for _, cmd := range []Command{
		{Name: "mise", Args: []string{"outdated", "-C", u.cfg.Home, "--no-header"},
			Dir: u.cfg.Home, Timeout: quick},
		{Name: "mise", Args: []string{"outdated", "-C", u.cfg.MachineDir, "--local", "--no-header"},
			Dir: u.cfg.MachineDir, Timeout: quick},
	} {
		out := u.deps.Run(cmd)
		if !out.OK {
			u.say("mise could not say what is outdated in %s, so nothing it manages is reported as moved: %s",
				cmd.Dir, oneLine(out.Text))
			continue
		}

		// `<tool> <requested> <current> <latest> <config>`, which is mise's own table with the
		// header turned off. Anything with fewer columns is not a tool.
		for _, line := range strings.Split(out.Stdout, "\n") {
			columns := strings.Fields(line)
			if len(columns) < 4 {
				continue
			}
			tools[columns[0]] = outdatedTool{current: columns[2], latest: columns[3]}
		}
	}
	return tools
}

// portless's proxy daemon runs the account's own node and the account's own portless, both
// at paths that carry a version, and the launchd job reads its plist once — so a `mise up`
// that moved either leaves root running a file that is gone or about to be pruned, and with
// it every dev URL on this Mac. The root helper is what puts it back, which is the one
// action of its own that reaches a path the account can write.
func (u *updater) portless(moved []string) {
	if !movedAny(moved, "node", "npm:portless") {
		return
	}

	if u.dry {
		u.say("node or portless would have moved, so the proxy daemon would be installed again")
		return
	}

	out := u.deps.Run(Command{
		Name: "sudo", Args: []string{"-n", u.cfg.RootHelper, "reinstall-portless"}, Timeout: quick,
	})
	if !out.OK {
		u.failed(Failure{
			Step:   stepPortless,
			Detail: out.Text,
			Action: "**Needs you:** " + wording.CodeSpan("sudo portless service install --tld localhost --tld timche.dev") +
				" — until it runs, every `.localhost` and `.timche.dev` dev URL on this Mac is down.",
		})
		return
	}
	u.note("portless's proxy daemon was installed again, on the node and portless mise has now")
}

// Homebrew's formulae and casks, which is every other tool on this Mac. What comes back is
// the names that moved — or, in a dry run, the names that would — which is what says
// whether tailscaled has to be restarted.
func (u *updater) brew() []string {
	if u.dry {
		var waiting []string
		out := u.deps.Run(Command{Name: "brew", Args: []string{"outdated"}, Timeout: brewHousework})
		for _, line := range strings.Split(strings.TrimSpace(out.Stdout), "\n") {
			if name := field(line, 0); name != "" {
				u.say("would upgrade %s", name)
				waiting = append(waiting, name)
			}
		}
		return waiting
	}

	if out := u.deps.Run(Command{Name: "brew", Args: []string{"update"}, Timeout: brewHousework}); !out.OK {
		// Reported and not fatal: an upgrade against formulae that were not fetched is an
		// upgrade that finds nothing, which is a quiet night rather than a broken Mac.
		u.failed(Failure{Step: stepBrewUpdate, Detail: out.Text})
	}

	before := u.brewVersions()

	if out := u.deps.Run(Command{Name: "brew", Args: []string{"upgrade"}, Timeout: brewUpgrade}); !out.OK {
		// A cask that will not upgrade — root-owned files, an app that is running — stops
		// `brew upgrade` partway, so what moved is read off the versions either way.
		u.failed(Failure{Step: stepBrewUp, Detail: out.Text})
	}

	after := u.brewVersions()

	var moved []string
	for _, name := range statedir.SortedKeys(before) {
		if after[name] != "" && after[name] != before[name] {
			moved = append(moved, name)
			u.move(name, before[name], after[name])
		}
	}

	u.cleanup(before, after)
	return moved
}

// `brew list --versions` for both kinds, as one map. The cask list is read separately
// because `brew list` answers for formulae alone unless it is asked.
func (u *updater) brewVersions() map[string]string {
	versions := map[string]string{}

	for _, args := range [][]string{
		{"list", "--versions"},
		{"list", "--cask", "--versions"},
	} {
		out := u.deps.Run(Command{Name: "brew", Args: args, Timeout: brewHousework})
		if !out.OK {
			u.say("brew could not list what is installed, so what moved may be missing from the summary: %s",
				oneLine(out.Text))
			continue
		}

		for _, line := range strings.Split(out.Stdout, "\n") {
			columns := strings.Fields(line)
			if len(columns) < 2 {
				continue
			}
			versions[columns[0]] = columns[1]
		}
	}
	return versions
}

// The keg or cask a path in brew's own output names, cut to the version directory rather
// than the file inside it that brew happened to stop on: what is left behind is the whole
// keg, and what clears it is one `rm -rf` of that directory.
var cellarPath = regexp.MustCompile(`/[^\s'"]*/(?:Cellar|Caskroom)/[^/\s'"]+/[^/\s'"]+`)

// What Homebrew replaced, which it leaves on disk until it is told to clear it. The one
// failure worth expecting: `sudo brew services start` takes root ownership of the keg it
// runs tailscaled out of, so the first cleanup after a tailscale upgrade meets a root-owned
// directory and stops on it.
func (u *updater) cleanup(before, after map[string]string) {
	out := u.deps.Run(Command{Name: "brew", Args: []string{"cleanup"}, Timeout: brewHousework})
	if out.OK {
		return
	}

	action := "**Needs you:** " + wording.CodeSpan("brew cleanup") +
		" left something it could not remove. A keg `sudo brew services` ran out of is root's, " +
		"so clearing it is a `sudo rm -rf` of the path above."

	// The path brew named, or the old keg of anything that moved, which for tailscale is the
	// one this is nearly always about.
	if path := cellarPath.FindString(out.Text); path != "" {
		action = "**You can run:** " + wording.CodeSpan("sudo rm -rf "+path) +
			" — it is root's because `sudo brew services` ran out of it."
	} else if was, ok := before["tailscale"]; ok && after["tailscale"] != was {
		action = "**You can run:** " + wording.CodeSpan("sudo rm -rf /opt/homebrew/Cellar/tailscale/"+was) +
			" — the keg tailscaled was running out of is root's, since `sudo brew services` started it there."
	}

	u.failed(Failure{Step: stepBrewClean, Detail: out.Text, Action: action})
}

// launchd reads a plist once, and tailscale's names the `opt` symlink rather than a version
// — so the formula moving under it leaves root running the binary it already exec'd. The
// restart is the root helper's, since it is one of the daemons on its list.
func (u *updater) tailscale(moved []string) {
	if !movedAny(moved, "tailscale") {
		return
	}

	if u.dry {
		u.say("tailscale would move, so tailscaled would be restarted and read back")
		return
	}

	restart := "**Needs you:** " + wording.CodeSpan("sudo brew services restart tailscale") +
		" — run it from the LAN rather than over the tailnet, which the restart drops."

	if out := u.deps.Run(Command{
		Name: "sudo", Args: []string{"-n", u.cfg.RootHelper, "restart-daemon", "tailscale"}, Timeout: quick,
	}); !out.OK {
		u.failed(Failure{Step: stepTailscale, Detail: out.Text, Action: restart})
		return
	}

	if state := u.tailscaleBackend(); state != "Running" {
		u.failed(Failure{
			Step:   stepTailscale,
			Detail: fmt.Sprintf("tailscaled came back and its backend is %q rather than Running", state),
			Action: restart,
		})
		return
	}
	u.note("tailscaled was restarted after the upgrade and is back on the tailnet")
}

// How long tailscaled is given to come back, and how often it is asked. It is a daemon
// reconnecting to a coordination server, so seconds; a minute without `Running` is the
// tailnet being down rather than a restart still in progress — and this Mac is reached over
// it, so nothing but an alert is any use.
const (
	backendWait  = time.Minute
	backendEvery = 5 * time.Second
)

func (u *updater) tailscaleBackend() string {
	state := ""

	for attempt := 0; attempt <= int(backendWait/backendEvery); attempt++ {
		if attempt > 0 {
			u.deps.Sleep(backendEvery)
		}

		out := u.deps.Run(Command{Name: "tailscale", Args: []string{"status", "--json"}, Timeout: quick})
		if out.OK {
			var status struct {
				BackendState string `json:"BackendState"`
			}
			if json.Unmarshal([]byte(out.Stdout), &status) == nil {
				state = status.BackendState
			}
		}
		if state == "Running" {
			return state
		}
	}
	return state
}

// herdr from its own installer, for the reason mise is: `herdr update` replaces the binary
// it is running from, so nothing else can move it. The server is deliberately left alone —
// restarting it ends every session on this Mac — and it picks the new binary up whenever it
// next starts.
func (u *updater) herdr() {
	before := u.herdrVersion()

	if u.dry {
		u.say("herdr is %s; would run herdr update", before)
		return
	}

	if out := u.deps.Run(Command{Name: "herdr", Args: []string{"update"}, Timeout: selfUpdate}); !out.OK {
		u.failed(Failure{Step: stepHerdr, Detail: out.Text})
		return
	}

	after := u.herdrVersion()
	u.move("herdr", before, after)
	if after != "" && after != before {
		u.note("the herdr server is still on " + wording.CodeSpan(before) +
			" and takes the new binary at its next restart, since restarting it now would end every session")
	}
}

func (u *updater) herdrVersion() string {
	out := u.deps.Run(Command{Name: "herdr", Args: []string{"--version"}, Timeout: quick})
	if !out.OK {
		return ""
	}
	line := strings.Fields(firstLine(out.Stdout))
	if len(line) == 0 {
		return ""
	}
	return line[len(line)-1]
}

// The one thing mise's bootstrap declares as a clone rather than a tool, so the only thing
// that moves it is a pull. Fast-forward only: a clone that cannot fast-forward has commits
// somebody made in it, and merging them is not this Mac's decision to make at four in the
// morning.
func (u *updater) ohMyZsh() {
	if u.dry {
		u.say("would fast-forward %s", u.cfg.OhMyZsh)
		return
	}

	out := u.deps.Run(Command{
		Name: "git", Args: []string{"-C", u.cfg.OhMyZsh, "pull", "--ff-only"}, Timeout: gitTimeout,
	})
	if !out.OK {
		u.failed(Failure{
			Step:   stepOhMyZsh,
			Detail: out.Text,
			Action: "**Needs you:** " + wording.CodeSpan("git -C ~/.oh-my-zsh status") +
				" — a clone that will not fast-forward has something of somebody's in it.",
		})
		return
	}
	if !strings.Contains(out.Text, "Already up to date") {
		u.note("oh-my-zsh was brought forward")
	}
}

// Apple's own, and the one thing here that is a message rather than an action. On Apple
// silicon `softwareupdate --install` of a macOS update wants a volume owner's password —
// `--user` with `--stdinpass`, both documented as Apple-silicon-only — and this Mac has no
// MDM to authorise one in its place. Nothing on it may hold Tim's password, so what is
// waiting is said and he installs it.
func (u *updater) macOS() {
	out := u.deps.Run(Command{Name: "softwareupdate", Args: []string{"--list"}, Timeout: appleList})
	if !out.OK {
		u.failed(Failure{Step: stepApple, Detail: out.Text})
		return
	}

	waiting := appleTitles(out.Text)
	if len(waiting) == 0 {
		return
	}

	if u.dry {
		u.say("macOS updates are waiting: %s", strings.Join(waiting, "; "))
		return
	}

	u.note(fmt.Sprintf("macOS has %s waiting, already downloaded: %s",
		wording.CountOf(int64(len(waiting)), "update", "updates"),
		wording.CodeSpan(wording.Safe(strings.Join(waiting, "; "), wording.PathLimit))))
}

// What `softwareupdate --list` says is there: the `Title:` line under each label, which is
// the one a person recognises. A label is machine detail and the command Tim runs takes none.
var appleTitle = regexp.MustCompile(`Title:\s*([^,]+),\s*Version:\s*([^,]+)`)

func appleTitles(text string) []string {
	var titles []string
	for _, match := range appleTitle.FindAllStringSubmatch(text, -1) {
		titles = append(titles, strings.TrimSpace(match[1])+" "+strings.TrimSpace(match[2]))
	}
	return titles
}

func movedAny(moved []string, names ...string) bool {
	for _, name := range names {
		for _, was := range moved {
			if was == name {
				return true
			}
		}
	}
	return false
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return line
}

func field(line string, n int) string {
	columns := strings.Fields(line)
	if n >= len(columns) {
		return ""
	}
	return columns[n]
}

// A command's own words on one line, for a log that is one line per thing that happened.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
