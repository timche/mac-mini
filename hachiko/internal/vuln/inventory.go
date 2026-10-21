package vuln

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/update"
)

// How long one reading of what is installed may take. Every one of them is a tool printing
// a list it already holds, bar `brew vulns`, which fetches Homebrew's own advisory data.
const (
	listing  = 30 * time.Second
	brewScan = 90 * time.Second
)

// Where a thing came from, which is the whole of what decides who can move it: a tool mise
// manages is one `mise up` away, a Go module in this checkout is a commit somebody means to
// make, and macOS is Tim's password.
const (
	fromBrew  = "brew"
	fromMise  = "mise"
	fromPip   = "pip"
	fromGo    = "go"
	fromNPM   = "npm"
	fromTool  = "tool"
	fromMacOS = "macos"
)

// The ecosystems OSV is asked about, spelled the way OSV spells them and nowhere else: a
// batch carrying one ecosystem OSV does not know is refused whole with a 400, so every other
// query in it is lost too. GitHub's advisory API calls the first of these `pip`, which is why
// neither name is written twice.
const (
	ecosystemGo   = "Go"
	ecosystemPyPI = "PyPI"
	ecosystemNPM  = "npm"
)

// Installed is one thing on this Mac with a version a published advisory could be about.
type Installed struct {
	From    string
	Name    string
	Version string

	// The ecosystem OSV knows it by, empty for anything OSV is not asked about — Homebrew's
	// formulae among them, since `brew vulns` does that matching itself and a formula has no
	// ecosystem of its own in OSV.
	Ecosystem string

	// What `hachiko update --security` would be given to move it, empty where nothing on this
	// Mac moves it at all. Built here rather than where a finding is written, because this is
	// the one place that knows how the thing was installed.
	Target string

	// The directory a reading came from, which two of them need: the python a pip package is
	// installed into, and the module directory a Go requirement belongs to.
	Where string
}

// Inventory is everything with a version, and the lines about what could not be read. A flat
// list rather than a field per source: every matcher wants a different slice of it, and the
// feed matcher wants the names of all of them at once.
type Inventory struct {
	Installed []Installed

	// macOS's own version, which is in the list as well and is kept here because the feed
	// matcher names it and nothing else does.
	MacOS string

	// What a reading could not say, one line each. A source that did not answer is not a
	// source that found nothing, and the difference goes in the message.
	Troubles []string
}

func (inv *Inventory) add(in Installed) {
	if in.Name == "" || in.Version == "" {
		return
	}
	inv.Installed = append(inv.Installed, in)
}

func (inv *Inventory) trouble(line string) { inv.Troubles = append(inv.Troubles, line) }

// Everything OSV is asked about in one ecosystem, which is how one batch is built per
// ecosystem rather than one query per package.
func (inv Inventory) inEcosystem(ecosystem string) []Installed {
	var found []Installed
	for _, in := range inv.Installed {
		if in.Ecosystem == ecosystem {
			found = append(found, in)
		}
	}
	return found
}

func (inv Inventory) from(source string) []Installed {
	var found []Installed
	for _, in := range inv.Installed {
		if in.From == source {
			found = append(found, in)
		}
	}
	return found
}

// What one installed thing is at, by the name the reading gave it. The first match, since a
// tool installed twice — a python in two configs — is the same tool at the same version.
func (inv Inventory) version(from, name string) string {
	for _, in := range inv.Installed {
		if in.From == from && in.Name == name {
			return in.Version
		}
	}
	return ""
}

// The whole inventory, and no network in any of it: every one of these is a tool on this Mac
// printing what it has installed, or a file in this checkout. So a test drives the matchers
// against a real inventory with nothing but a table of command lines and a temporary
// directory, and a pass that cannot reach a single source still knows what is installed.
func (c checker) inventory() Inventory {
	inv := Inventory{}

	c.brewList(&inv)
	c.miseList(&inv)
	c.pip(&inv)
	c.goModules(&inv)
	c.npmLock(&inv)
	c.tools(&inv)
	c.macOS(&inv)

	return inv
}

// Homebrew's formulae and casks by name, which is every other tool on this Mac. Their
// versions are read too, although nothing matches against them: `brew vulns` does that
// against Homebrew's own advisory data, and what these names are for is the feed, where a
// CVE about curl or openssh is about this Mac because Homebrew put one here.
func (c checker) brewList(inv *Inventory) {
	for _, args := range [][]string{
		{"list", "--versions"},
		{"list", "--cask", "--versions"},
	} {
		out := c.deps.Run(Command{Name: "brew", Args: args, Timeout: listing})
		if !out.OK || out.Code != 0 {
			inv.trouble("brew could not list what is installed: " + oneLine(out.Text))
			continue
		}

		for _, line := range strings.Split(out.Stdout, "\n") {
			columns := strings.Fields(line)
			if len(columns) < 2 {
				continue
			}
			inv.add(Installed{From: fromBrew, Name: columns[0], Version: columns[1],
				Target: update.BrewTarget(columns[0])})
		}
	}
}

// The tools mise manages, in both of this Mac's configs: the global one every project reads
// and this repository's own, which declares the go its compiled tools are built with. Each
// call names its directory, since mise decides which tools are active from the directory it
// is asked about and a LaunchAgent starts in `/`.
//
// mise's own names, backend prefix and all — `node`, `npm:portless` — because that is what
// `mise up` takes back. The version is mise's rather than the tool's own `--version`: the
// bare `node` on PATH answered 24.22.0 on a Mac where mise's active node was 24.22.1, a
// shim resolving to an install mise had already moved past.
//
// The active versions alone, which is what runs: mise keeps the install it upgraded away
// from until `upgrade.prune_after` takes it, so a Mac with a fault fixed an hour ago still
// has the faulty version on disk and reporting it would be an incident about nothing. Both
// directories and not `--current`, because `--current` answers for the directory it is asked
// about — and `go` is active in this repository's config and nowhere else, so the global
// listing alone would never mention the toolchain these tools are built with.
func (c checker) miseList(inv *Inventory) {
	seen := map[string]bool{}

	for _, dir := range []string{c.cfg.Home, c.cfg.MachineDir} {
		out := c.deps.Run(Command{
			Name: "mise", Args: []string{"ls", "-C", dir, "--json"},
			Dir: dir, Timeout: listing,
		})
		if !out.OK || out.Code != 0 {
			inv.trouble("mise could not say which tools are active in " + dir + ": " + oneLine(out.Text))
			continue
		}

		var listed map[string][]struct {
			Version     string `json:"version"`
			InstallPath string `json:"install_path"`
			Active      bool   `json:"active"`
		}
		if json.Unmarshal([]byte(out.Stdout), &listed) != nil {
			inv.trouble("mise answered something that is not JSON about the tools in " + dir)
			continue
		}

		for name, versions := range listed {
			for _, v := range versions {
				if !v.Active || seen[name+"@"+v.Version] {
					continue
				}
				seen[name+"@"+v.Version] = true

				in := Installed{From: fromMise, Name: name, Version: v.Version,
					Where: v.InstallPath, Target: update.MiseTarget(name)}

				// mise's npm backend installs real npm packages, so those are the one kind of
				// mise tool OSV can be asked about by name. The rest — node, bun, python, go —
				// are runtimes with no package registry behind them, and each has a source of
				// its own below.
				if pkg, ok := strings.CutPrefix(name, "npm:"); ok {
					in.Ecosystem, in.Name = ecosystemNPM, pkg
				}
				inv.add(in)
			}
		}
	}
}

// pip in each python mise manages, and whatever else is installed in one. The python is
// named by its own install directory rather than by what is on PATH, because there is one of
// these per python and an upgrade has to reach the right one.
func (c checker) pip(inv *Inventory) {
	for _, python := range inv.from(fromMise) {
		if python.Name != "python" || python.Where == "" {
			continue
		}

		exe := filepath.Join(python.Where, "bin", "python3")
		out := c.deps.Run(Command{
			Name: exe, Args: []string{"-m", "pip", "list", "--format=json"}, Timeout: listing,
		})
		if !out.OK || out.Code != 0 {
			inv.trouble("pip could not list what is installed in python " + python.Version + ": " + oneLine(out.Text))
			continue
		}

		var listed []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		if json.Unmarshal([]byte(out.Stdout), &listed) != nil {
			inv.trouble("pip answered something that is not JSON about python " + python.Version)
			continue
		}

		for _, pkg := range listed {
			inv.add(Installed{From: fromPip, Name: pkg.Name, Version: pkg.Version,
				Ecosystem: ecosystemPyPI, Where: exe,
				Target: update.PipTarget(python.Version, pkg.Name)})
		}
	}
}

// The Go toolchain and every module the two compiled tools require, read out of their
// `go.mod` files in this checkout. Not `go list -m all`, which resolves the module graph and
// so wants the network and a module cache: what is built here is what the file says, since
// the wrapper builds from this very tree.
//
// Nothing here is a target. A module version is a line in a file somebody means to change,
// with a commit behind it and tests to run, so a bump is an on-call session's or Tim's and
// never something started at four in the morning by a check.
//
// The standard library is asked about at the toolchain mise has rather than at the `go`
// directive, which is a language version — `go 1.27` against a toolchain of 1.27.3. Asked at
// the directive, OSV answers about every 1.27.x fault including the ones this Mac already
// has the fix for, and a crypto/x509 distrust landing in 1.27.3 would read as unfixed on a
// Mac running 1.27.3. The directive is the fallback for a Mac where mise cannot say.
func (c checker) goModules(inv *Inventory) {
	toolchain := inv.version(fromMise, "go")

	for _, tool := range []string{"hachiko", "mochi"} {
		path := filepath.Join(c.cfg.MachineDir, tool, "go.mod")

		file, err := c.deps.ReadFile(path)
		if err != nil {
			inv.trouble("the go.mod of " + tool + " could not be read: " + oneLine(err.Error()))
			continue
		}

		declared, requires := parseGoMod(string(file))
		if toolchain == "" {
			toolchain = declared
		}
		if toolchain != "" {
			// OSV's name for the standard library, which is where a crypto/x509 or a net/http
			// fault lands — and the one that mattered the day this check was written.
			inv.add(Installed{From: fromGo, Name: "stdlib", Version: toolchain,
				Ecosystem: ecosystemGo, Where: tool})
		}
		for name, version := range requires {
			inv.add(Installed{From: fromGo, Name: name, Version: version,
				Ecosystem: ecosystemGo, Where: tool})
		}
	}
}

// The `go` directive and every `require` line, which is the whole of what a go.mod says
// about versions. A pseudo-version is kept as it is written: OSV takes it, and rewriting one
// would be this check deciding what a module is.
//
// A requirement a `replace` points at a directory is dropped, because it is not a published
// module at all: mochi requires `github.com/timche/mac-mini/discord v0.0.0` and replaces it
// with the checkout beside it, so nothing is fetched and OSV has nothing to answer about it.
// Asking anyway is a query whose answer can only ever be "nothing".
func parseGoMod(body string) (declared string, requires map[string]string) {
	requires = map[string]string{}
	local := map[string]bool{}
	block := false

	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if comment := strings.Index(line, "//"); comment >= 0 {
			line = strings.TrimSpace(line[:comment])
		}

		switch {
		case line == "require (":
			block = true
			continue
		case block && line == ")":
			block = false
			continue
		}

		fields := strings.Fields(line)
		switch {
		case len(fields) == 2 && fields[0] == "go":
			declared = fields[1]
		case len(fields) == 3 && fields[0] == "require":
			requires[fields[1]] = fields[2]
		case block && len(fields) == 2:
			requires[fields[0]] = fields[1]
		// A directory rather than another module on the right of the arrow, which go.mod spells
		// as a path: `replace x => ../y` against `replace x => y v1.2.3`.
		case len(fields) == 4 && fields[0] == "replace" && fields[2] == "=>" &&
			(strings.HasPrefix(fields[3], ".") || strings.HasPrefix(fields[3], "/")):
			local[fields[1]] = true
		}
	}

	for name := range local {
		delete(requires, name)
	}
	return declared, requires
}

// The npm packages the Worker is built from, out of its lockfile rather than out of
// `node_modules`, which is not installed on this Mac between deploys: the lockfile is what
// `npm ci` installs and so what a deploy ships.
//
// Nothing here is a target either. Moving one is a lockfile change and then a deploy to
// Cloudflare, and a deploy is nothing a five-minute check starts.
func (c checker) npmLock(inv *Inventory) {
	path := filepath.Join(c.cfg.MachineDir, "shibuya", "package-lock.json")

	file, err := c.deps.ReadFile(path)
	if err != nil {
		inv.trouble("shibuya's lockfile could not be read: " + oneLine(err.Error()))
		return
	}

	var lock struct {
		Packages map[string]struct {
			Version string `json:"version"`
		} `json:"packages"`
	}
	if json.Unmarshal(file, &lock) != nil {
		inv.trouble("shibuya's lockfile is not JSON this could read")
		return
	}

	for path, pkg := range lock.Packages {
		// The root entry, which is the project itself and has no version; and the package name
		// is the last `node_modules/` segment, since a nested dependency's key carries the
		// whole path it is installed at.
		_, name, nested := strings.Cut(path, "node_modules/")
		if !nested {
			continue
		}
		if cut := strings.LastIndex(name, "node_modules/"); cut >= 0 {
			name = name[cut+len("node_modules/"):]
		}
		inv.add(Installed{From: fromNPM, Name: name, Version: pkg.Version,
			Ecosystem: ecosystemNPM, Where: "shibuya"})
	}
}

// The three tools that come from their authors' own installers rather than from mise or
// Homebrew, which is why each is read by asking it. Claude Code updates itself on the default
// channel and so has no target: pinning it would mean turning its updater off and then owning
// what the updater was doing.
func (c checker) tools(inv *Inventory) {
	for _, tool := range []struct {
		name, command, target string
		field                 int
	}{
		{name: "mise", command: "mise", target: update.MiseSelfTarget, field: 0},
		{name: "herdr", command: "herdr", target: update.HerdrTarget, field: 1},
		{name: "claude-code", command: "claude", field: 0},
	} {
		out := c.deps.Run(Command{Name: tool.command, Args: []string{"--version"}, Timeout: listing})
		if !out.OK || out.Code != 0 {
			inv.trouble(tool.name + " could not say what version it is: " + oneLine(out.Text))
			continue
		}
		inv.add(Installed{From: fromTool, Name: tool.name,
			Version: field(firstLine(out.Stdout), tool.field), Target: tool.target})
	}
}

// macOS, which is in the list for the feed's sake and is the one thing here with no target at
// all: an install wants a volume owner's password, which nothing on this Mac may hold. What
// Apple has waiting is already said by the nightly update's own `softwareupdate --list` step,
// so this reads the version and nothing more.
func (c checker) macOS(inv *Inventory) {
	out := c.deps.Run(Command{Name: "sw_vers", Args: []string{"--productVersion"}, Timeout: listing})
	if !out.OK || out.Code != 0 {
		inv.trouble("macOS could not say what version it is: " + oneLine(out.Text))
		return
	}

	inv.MacOS = strings.TrimSpace(out.Stdout)
	inv.add(Installed{From: fromMacOS, Name: "macos", Version: inv.MacOS})
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

// A command's or a source's own words on one line, for a log that is one line per thing that
// happened.
func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }
