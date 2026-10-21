package vuln

import (
	"fmt"
	"sort"
	"strings"

	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// The labels a finding goes under. This command's own words, the way the update's are its
// own: what the check knows about an advisory is what the source told it, and only this
// package knows which source that was.
const (
	LabelAffected = "Affected"
	LabelWatch    = "Worth reading"
	LabelFixing   = "Being fixed now"
	LabelUnread   = "Not read this time"
)

// Exposure is one installed version a source says outright is affected — a formula Homebrew
// has advisory data for, a node release line with a security release above it, a package OSV
// or a repository's own advisories name. It is the half of this check that needs no judgement:
// the source compared the version, so the only question left is who moves it.
type Exposure struct {
	// The advisory as the source that found it spells it, and every other spelling of the same
	// issue. Both, because one fault reaches this Mac through several sources under several
	// ids and it is one incident however many of them saw it.
	ID      string
	Aliases []string

	Source   string
	Severity string
	Summary  string
	URL      string

	// What is installed, as the reading that found it named it, and what the source says is
	// not affected. Fixed is empty where the advisory names no released version, which is the
	// difference between something to upgrade and something to work around.
	Name    string
	Version string
	Fixed   string

	// What `hachiko update --security` is given to move it, and the line for Tim where
	// nothing on this Mac moves it at all. Exactly one of the two.
	Target string
	Needs  string
}

// Lead is a published advisory that mentions something on this Mac, or carries the wording of
// a trust-store fault, or is critical — and that nothing here has compared a version against.
// It is the half that is a judgement, which is why it goes to an agent rather than to an
// upgrade: an advisory about "curl" is about this Mac's curl or about a library that happens
// to share the name, and only reading it says which.
type Lead struct {
	ID      string
	Aliases []string

	Source   string
	Title    string
	Severity string
	URL      string

	// Which rule kept it, in hachiko's own words, so the agent reads why it is being asked
	// about this one rather than guessing at it.
	Why string
}

// Found is one pass of the check.
type Found struct {
	Exposures []Exposure
	Leads     []Lead

	// What a source could not say, one line each. A source that did not answer is not a source
	// that found nothing: the message says which were not read, and the cursor of one that was
	// not read is left where it was so that nothing published meanwhile is skipped.
	Troubles []string

	// How far into each feed this pass actually read, by the source's own name and only for
	// the sources it reached. Written back to the state as each source's cursor.
	Cursors map[string]int64

	// The targets the caller actually started an upgrade with, which is what the message names.
	// Set after the fact rather than worked out here, so a message cannot say an upgrade is
	// running that never started.
	Started []string

	// The Homebrew formulae its own scanner could not read, which are the one part of the
	// inventory the feed matches by name: see terms in match.go. Carried here rather than on
	// the inventory because it is what one source said about itself rather than something that
	// is installed.
	unscanned []string

	// The exact sources that answered in full on this pass, by the name their findings carry.
	// A reading that is missing is not a reading that is clear: without this, a pass where
	// `brew vulns` would not run would find no formula affected and post that this Mac is no
	// longer affected by every formula advisory open against it.
	//
	// Exported, and a list rather than a set, because the check that decides what has cleared
	// is in another package and its tests have to be able to say which sources a pass heard.
	Sources []string
}

// Said by an exact source that reached the end of what it had to say. A source that managed
// part of it — OSV with records it did not read, Homebrew with formulae it could not scan —
// deliberately does not say this, because the part it missed is the part a finding would have
// been in.
func (f *Found) read(source string) {
	if !contains(f.Sources, source) {
		f.Sources = append(f.Sources, source)
	}
}

// Whether this pass heard the whole of what one source had to say, which is what a record
// already raised is measured against before it is called clear.
func (f Found) Read(source string) bool { return contains(f.Sources, source) }

// Everything worth raising, which is what decides whether there is an incident at all.
func (f Found) Empty() bool { return len(f.Exposures) == 0 && len(f.Leads) == 0 }

// Whether a source still says this advisory is about a version installed here, which is what
// a later pass answers for an advisory already raised: an exact match that has stopped
// matching is a Mac that has the fix, and the only kind of finding here that clears by itself.
func (f Found) Matches(key string) bool {
	for _, e := range f.Exposures {
		if e.Key() == key {
			return true
		}
	}
	return false
}

// The key that makes two spellings of one fault one record: the lowest of the advisory's own
// id and every alias it carries. Lowest rather than first, because which spelling a source
// leads with is the source's choice — certifi's fault is GHSA-6q48-6c7w-9gw2 to GitHub and
// PYSEC-2026-4320 to OSV, and the Go one is CVE-2026-111943 to NVD and GO-2026-6809 to OSV.
// Taking the lowest of the whole set gives both sources the same answer whichever of them
// saw it first.
func key(id string, aliases []string) string {
	lowest := strings.ToUpper(strings.TrimSpace(id))

	for _, alias := range aliases {
		alias = strings.ToUpper(strings.TrimSpace(alias))
		if alias != "" && (lowest == "" || alias < lowest) {
			lowest = alias
		}
	}
	return lowest
}

func (e Exposure) Key() string { return key(e.ID, e.Aliases) }
func (l Lead) Key() string     { return key(l.ID, l.Aliases) }

// How bad the source called it, as a number to sort by. Unknown sorts last rather than
// first: a source with no severity has said nothing about how bad it is, and a message puts
// what is known above what is not.
func rank(severity string) int {
	switch strings.ToLower(severity) {
	case "critical":
		return 0
	case "high":
		return 1
	case "moderate", "medium":
		return 2
	case "low":
		return 3
	default:
		return 4
	}
}

// The order a message lists them in, and so the order they are read in. What has no released
// fix goes first, whatever its severity: nothing is happening about one of those and nothing
// is going to, where everything below it is a version somebody or something can reach. Then
// worst first, and within one severity by what is installed, so two findings about one formula
// sit together.
func (f *Found) sortFindings() {
	sort.SliceStable(f.Exposures, func(i, j int) bool {
		a, b := f.Exposures[i], f.Exposures[j]
		if (a.Fixed == "") != (b.Fixed == "") {
			return a.Fixed == ""
		}
		if rank(a.Severity) != rank(b.Severity) {
			return rank(a.Severity) < rank(b.Severity)
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	})

	sort.SliceStable(f.Leads, func(i, j int) bool {
		a, b := f.Leads[i], f.Leads[j]
		if rank(a.Severity) != rank(b.Severity) {
			return rank(a.Severity) < rank(b.Severity)
		}
		return a.ID < b.ID
	})
}

// What a post about these opens with, which is also its title in the forum. One exposure by
// what it is about, since that is the thing Tim would recognise, and several by how many
// things are affected rather than how many advisories there are: eighteen advisories against
// one module that has not moved in four years is one thing to deal with, and a title saying
// eighteen reads as eighteen. An exposure leads over a lead, because a version this Mac is
// running that a source names is not the same news as an advisory somebody has to read.
func Headline(f Found) string {
	affected := f.affected()

	switch {
	case len(affected) == 1:
		e := f.Exposures[0]
		return "Vulnerable: " + wording.PlainWords(wording.Safe(e.Name, wording.NameLimit)) +
			" " + wording.PlainWords(wording.Safe(e.Version, wording.NameLimit))
	case len(affected) > 1:
		return "Vulnerable: " + wording.CountOf(int64(len(affected)), "thing", "things") +
			" a published advisory names"
	case len(f.Leads) == 1:
		return "One published advisory may be about this Mac"
	default:
		return wording.CountOf(int64(len(f.Leads)), "published advisory", "published advisories") +
			" may be about this Mac"
	}
}

// What is affected, each thing once, in the order the findings are listed.
func (f Found) affected() []string {
	var names []string
	for _, e := range f.Exposures {
		if !contains(names, e.Name) {
			names = append(names, e.Name)
		}
	}
	return names
}

// What goes under it for Tim: one bullet per thing that is affected rather than per advisory,
// since what he does about a module with eighteen advisories against it is one decision.
// Discord caps a message at two thousand characters, and the full form below runs to dozens of
// bullets on a Mac whose dependencies have drifted — a cap that cut it would cut the half
// saying what is being done about it.
func Summary(f Found) string {
	var bullets []string
	for _, name := range f.affected() {
		bullets = append(bullets, f.thing(name))
	}

	return join(wording.Section(LabelAffected, bullets),
		wording.Section(LabelWatch, leadBullets(f.Leads, leadsShown)),
		f.fixing(), f.unread())
}

// One thing, with how many advisories are against it, the worst of them, and what is to
// happen about it — which is the same for all of them, being a property of how the thing was
// installed rather than of the advisory.
func (f Found) thing(name string) string {
	var against []string
	worst, fixed, needs, unfixed := "", "", "", ""

	for _, e := range f.Exposures {
		if e.Name != name {
			continue
		}
		against = append(against, e.ID)
		if worst == "" || rank(e.Severity) < rank(worst) {
			worst = e.Severity
		}

		// The highest fixed version any of them names, which is the one an upgrade has to
		// reach to be clear of all of them — and the line about what to do next taken from an
		// advisory that agrees with it. Taken from whichever came first, a group whose first
		// advisory has no fix read as "fixed by 0.56.0 — nothing is released that fixes it",
		// which is two sentences contradicting each other about the same thing.
		switch {
		case e.Fixed == "":
			if unfixed == "" {
				unfixed = e.Needs
			}
		default:
			if fixed == "" || compare(e.Fixed, fixed) > 0 {
				fixed, needs = e.Fixed, e.Needs
			}
		}
	}
	if fixed == "" {
		needs = unfixed
	}

	bullet := wording.CodeSpan(wording.Safe(name, wording.NameLimit)) + " — " +
		wording.CountOf(int64(len(against)), "advisory", "advisories")
	if worst != "" {
		bullet += ", worst " + wording.PlainWords(strings.ToLower(wording.Safe(worst, wording.NameLimit)))
	}
	bullet += ": " + wording.PlainWords(wording.Safe(strings.Join(idsShown(against), ", "), wording.PathLimit))

	switch {
	case fixed != "":
		bullet += "\n  fixed by " + wording.PlainWords(wording.Safe(fixed, wording.NameLimit))
	default:
		bullet += "\n  no released fix"
	}
	if needs != "" {
		bullet += " — " + needs
	}
	return bullet
}

// How many advisory ids one thing's bullet names before it stops counting them out, and how
// many leads the short form lists. Both are about one screen on a phone: the full form below
// has every one of them, and the session reads that.
const (
	idsShownLimit = 3
	leadsShown    = 3
)

func idsShown(ids []string) []string {
	if len(ids) <= idsShownLimit {
		return ids
	}
	return append(ids[:idsShownLimit:idsShownLimit],
		fmt.Sprintf("and %d more", len(ids)-idsShownLimit))
}

// What goes under it for the session, which needs every one of them: one bullet per advisory,
// each saying what is installed, what the advisory is, and either the upgrade already running
// or the one thing Tim can do; then the leads, each saying why it was kept, since that is what
// an agent triaging one needs before anything else.
func Details(f Found) string {
	var bullets []string
	for _, e := range f.Exposures {
		bullets = append(bullets, e.bullet())
	}

	return join(wording.Section(LabelAffected, bullets),
		wording.Section(LabelWatch, leadBullets(f.Leads, 0)), f.fixing(), f.unread())
}

// The leads as bullets, all of them or the first few with a line saying how many were left.
func leadBullets(leads []Lead, limit int) []string {
	var bullets []string
	for i, l := range leads {
		if limit > 0 && i == limit {
			bullets = append(bullets,
				wording.CountOf(int64(len(leads)-limit), "more advisory", "more advisories")+
					" in the brief the agent has")
			break
		}
		bullets = append(bullets, l.bullet())
	}
	return bullets
}

func (f Found) fixing() string {
	if len(f.Started) == 0 {
		return ""
	}
	return "**" + LabelFixing + ":** " +
		wording.CodeSpan("hachiko update --security "+strings.Join(f.Started, " ")) +
		" is running; the next check says whether it worked."
}

func (f Found) unread() string {
	return wording.Section(LabelUnread, plainly(f.Troubles))
}

func (e Exposure) bullet() string {
	name := wording.CodeSpan(wording.Safe(e.Name, wording.NameLimit))
	bullet := name + " " + wording.PlainWords(wording.Safe(e.Version, wording.NameLimit)) +
		" — " + wording.CodeSpan(wording.Safe(e.ID, wording.NameLimit))

	if e.Severity != "" {
		bullet += ", " + wording.PlainWords(strings.ToLower(wording.Safe(e.Severity, wording.NameLimit)))
	}
	if e.Summary != "" {
		bullet += ", " + wording.PlainWords(wording.Safe(e.Summary, summaryLimit))
	}

	switch {
	case e.Fixed != "":
		bullet += "\n  fixed in " + wording.PlainWords(wording.Safe(e.Fixed, wording.NameLimit))
	default:
		bullet += "\n  no released fix"
	}
	if e.Needs != "" {
		bullet += " — " + e.Needs
	}
	return bullet
}

func (l Lead) bullet() string {
	bullet := wording.CodeSpan(wording.Safe(l.ID, wording.NameLimit))
	if l.Severity != "" {
		bullet += " " + wording.PlainWords(strings.ToLower(wording.Safe(l.Severity, wording.NameLimit)))
	}
	bullet += " — " + wording.PlainWords(wording.Safe(l.Title, summaryLimit))

	return bullet + "\n  " + wording.PlainWords(wording.Safe(l.Why, wording.PathLimit))
}

// An advisory's own summary, which is the source's text and so the longest thing in one of
// these bullets. Short enough that a message with several findings in it is still one screen.
const summaryLimit = 160

func plainly(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, wording.PlainWords(wording.Safe(line, wording.PathLimit)))
	}
	return out
}

func join(blocks ...string) string {
	var kept []string
	for _, block := range blocks {
		if block != "" {
			kept = append(kept, block)
		}
	}
	return strings.Join(kept, "\n\n")
}

func contains(list []string, want string) bool {
	for _, got := range list {
		if got == want {
			return true
		}
	}
	return false
}
