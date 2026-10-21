// Package vuln is hachiko's answer to a published vulnerability nobody told this Mac about.
// On 2026-10-20 eMudhra's intermediate CA keys turned out to be recoverable, and this Mac was
// exposed through `/usr/bin/curl` and the system python by way of `/etc/ssl/cert.pem`, through
// node, through pip and through bun — and nothing on it said a word. Every tool here moves
// itself or is moved by `hachiko update`, and none of them knows why a version matters.
//
// So every three hours this reads what is installed and asks whether anybody has published
// something about it, in two halves that are nothing alike. The first is exact: Homebrew's own
// scanner, node's release index, OSV asked about a version, and the two repositories that
// publish advisories about tools installed from their authors' own installers. Those sources
// compare the version themselves, so the only question left is who moves it — and where the
// answer is `hachiko update`, this starts an out-of-cycle run of it rather than waiting for
// four in the morning.
//
// The second half is a judgement. NVD, GitHub's unreviewed advisories, CISA's catalogue of
// what is being exploited and bun's own release notes are read for anything that names
// something installed here, says something about what this Mac trusts, or is critical — and
// those go to an on-call agent, because whether an advisory about "curl" is about this Mac's
// curl is a question only reading it answers. The fault that started this is the case in
// point: Go's own note about it never says eMudhra, emSign or certificate authority, and the
// word that catches it is `crypto/x509`.
//
// Nothing here sends anything, opens a session or writes any state. It takes the readings and
// answers with what it found, and `internal/watch` is what raises an incident about it.
package vuln

import (
	"fmt"
	"strings"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/logs"
)

const logName = "hachiko vuln"

type checker struct {
	cfg  config.Config
	deps Deps

	// How far into each feed the state says this Mac has read, which is the whole of what
	// keeps an advisory published between two passes from being skipped.
	cursors map[string]int64

	// The readings, taken once and read by every matcher: four of the sources need to know
	// what is installed before they can say anything about it.
	inv Inventory
}

func (c checker) say(format string, args ...any) {
	logs.Logger{Out: c.deps.Log, Now: c.deps.Now, Name: logName}.Say(format, args...)
}

// Look is one pass: the inventory, the four exact sources against it, and the four feeds from
// each one's own cursor. It changes nothing on this Mac and nothing off it — the upgrade is
// started by the caller, out of Targets below, so that what starts one is the same code that
// writes down having started it.
func Look(cfg config.Config, cursors map[string]int64) Found {
	return look(cfg, realDeps(cfg), cursors)
}

// Check is `hachiko --vuln-check`: one pass printed and nothing else. It is the explicit
// "look now" beside the five-minute check's own three-hour schedule, and the one way to see
// what the sources really say without waiting for a pass or sending anything.
//
// Every feed is read from its own first-look window rather than from the state's cursors,
// which is what somebody running this wants to see, and no cursor is written back: a look that
// moved them would have the next real pass skip what it had just printed.
// Both lengths of the message, because the point of running this by hand is to see what would
// go out: the short form is what Tim would read and the full one is what the on-call session
// would be briefed with, and a finding that reads well in one of them can read badly in the
// other.
func Check(cfg config.Config) error {
	found := Look(cfg, nil)

	out := Headline(found)
	if short := Summary(found); short != "" {
		out += "\n\n" + short
	}
	if full := Details(found); full != "" {
		out += "\n\n--- and the brief the on-call session would get ---\n\n" + full
	}
	fmt.Println(out)
	return nil
}

func look(cfg config.Config, deps Deps, cursors map[string]int64) Found {
	inv := checker{cfg: cfg, deps: deps}.inventory()
	c := checker{cfg: cfg, deps: deps, cursors: cursors, inv: inv}

	found := Found{Troubles: inv.Troubles}

	// Cheapest first, and OSV last, because the whole pass is under one deadline and OSV is the
	// one source that can spend the lot: querybatch answers with ids alone, so every record is
	// a request of its own and a module that has not moved in four years has twenty-six of
	// them. Read before the feeds, it took the budget and NVD was reported unreachable on a
	// pass where it had answered fine.
	//
	// The order says nothing about which finding wins, because nothing is decided until
	// dedupe below: an exact match beats a lead about the same advisory whichever was read
	// first. What does depend on the order is that Homebrew's scanner runs before the feeds,
	// since the formulae it could not read are the ones they match by name.
	c.brew(&found)
	c.node(inv, &found)
	c.repoAdvisories(inv, &found)
	c.feed(inv, &found)
	c.osv(inv, &found)

	found.dedupe()
	found.sortFindings()

	c.say("%s", found.line())
	return found
}

// One line for the log, on every pass and whether or not anything was found: this runs eight
// times a day for ever, and what it says is how much of the Mac it managed to look at.
func (f Found) line() string {
	line := fmt.Sprintf("looked for published vulnerabilities: %d installed version(s) a source names, %d advisor(ies) worth reading",
		len(f.Exposures), len(f.Leads))
	if len(f.Troubles) > 0 {
		line += "; not read: " + strings.Join(f.Troubles, "; ")
	}
	return line
}

// One record per advisory, whichever source saw it and however it was found. An exact match
// replaces a lead about the same advisory, because a version this Mac is running that a source
// names is the whole of what a lead was asking somebody to find out; and two exact matches on
// one advisory stay as two where they are about different things, since a CVE against two
// formulae is two upgrades.
func (f *Found) dedupe() {
	exact := map[string]bool{}

	kept := make([]Exposure, 0, len(f.Exposures))
	seen := map[string]bool{}
	for _, e := range f.Exposures {
		exact[e.Key()] = true
		if at := e.Key() + "@" + e.Name; !seen[at] {
			seen[at] = true
			kept = append(kept, e)
		}
	}
	f.Exposures = kept

	leads := make([]Lead, 0, len(f.Leads))
	seen = map[string]bool{}
	for _, l := range f.Leads {
		if exact[l.Key()] || seen[l.Key()] {
			continue
		}
		seen[l.Key()] = true
		leads = append(leads, l)
	}
	f.Leads = leads
}

// The targets an out-of-cycle security upgrade would be started with, skipping the ones
// already started for an advisory this Mac has been told about: an upgrade that did not fix
// the fault would otherwise be started again every three hours for ever.
//
// Only where the source named a released fixed version and the thing is one `hachiko update`
// already moves. Everything else — a Go module in this checkout, shibuya's npm packages, a
// macOS update, anything needing sudo, anything with no fix released — is the on-call agent's,
// which is where the fix is a decision rather than a version.
func (f Found) Targets(started map[string]bool) []string {
	var targets []string

	for _, e := range f.Exposures {
		if e.Target == "" || started[e.Key()] {
			continue
		}
		if !contains(targets, e.Target) {
			targets = append(targets, e.Target)
		}
	}
	return targets
}

// Which advisories a set of targets covers, which is what gets written down as having had an
// upgrade started for it.
func (f Found) Covered(targets []string) []string {
	var keys []string

	for _, e := range f.Exposures {
		if e.Target != "" && contains(targets, e.Target) && !contains(keys, e.Key()) {
			keys = append(keys, e.Key())
		}
	}
	return keys
}
