package vuln

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
)

// A whole pass against a table of command lines, files and recorded answers, which is how
// every rule here is checked without a single reading of this Mac or a single request off it:
// what is installed, which source says what about it, who moves it, and how far into each feed
// the pass got.
type box struct {
	t   *testing.T
	cfg config.Config
	now time.Time

	cursors map[string]int64

	// Keyed by the whole command line, which is what makes a test fail when a reading starts
	// running something other than what it was written for. A command with no answer ran and
	// printed nothing, which is a tool with nothing installed rather than one that broke.
	answers map[string]Output
	calls   []string
	run     []Command

	// What this checkout's files say. A path with no entry is a file that is not there.
	files map[string]string

	// Keyed by the longest prefix of the URL that matches, because two of the sources put the
	// window and the page in the query string and what a test is about is the page rather than
	// the encoding. A queue, so NVD's four pages and OSV's three batches are one key each and
	// the last answer stands for every call after it. A URL nothing answers is a source this
	// Mac could not reach.
	responses map[string][]response
	gets      []string
	posts     []sent

	slept []time.Duration
	log   bytes.Buffer
}

type response struct {
	body string
	err  error
}

type sent struct {
	url  string
	body string
}

const (
	home = "/Users/account"
	repo = "/Users/account/.mac-mini"
)

// Noon, so a cursor three hours back and the first-look window a day back are both the same
// day in UTC: the GitHub feed and CISA's catalogue filter by date rather than by instant.
var base = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

func newBox(t *testing.T) *box {
	t.Helper()

	return &box{
		t:   t,
		now: base,
		cfg: config.Config{
			Home:       home,
			MachineDir: repo,
			Host:       "mac-mini",
		},
		answers:   map[string]Output{},
		files:     map[string]string{},
		responses: map[string][]response{},
	}
}

// A command that ran and printed this, which is what every reading of this Mac is.
func (b *box) says(line, stdout string) {
	b.answers[line] = Output{OK: true, Stdout: stdout, Text: stdout}
}

// A command that ran, printed this and exited non-zero, which is `brew vulns` with something
// to report.
func (b *box) exits(line string, code int, stdout string) {
	b.answers[line] = Output{OK: true, Code: code, Stdout: stdout, Text: stdout}
}

// A command that could not be started at all, so there is no reading rather than a reading of
// nothing.
func (b *box) fails(line, text string) { b.answers[line] = Output{Text: text} }

func (b *box) file(path, body string) { b.files[path] = body }

func (b *box) serves(url, body string) { b.responses[url] = []response{{body: body}} }

// What the same URL answers from its next call onward, which is how NVD's second page and
// OSV's second ecosystem are written down.
func (b *box) thenServes(url, body string) {
	b.responses[url] = append(b.responses[url], response{body: body})
}

func (b *box) refuses(url, text string) {
	b.responses[url] = []response{{err: errors.New(text)}}
}

// The answers a Mac where no source has anything to say gives, so a test says only what it is
// about. Homebrew's scanner and mise both have to answer something that parses: empty output
// from one of those is a source that could not be read rather than one that found nothing.
func (b *box) clean() {
	b.says("brew vulns --json", `{"findings":[],"skipped_formulae":[],"checked":38,"complete":true}`)
	b.says(miseLsHome, "{}")
	b.says(miseLsRepo, "{}")
	b.file(repo+"/hachiko/go.mod", "module hachiko\n")
	b.file(repo+"/mochi/go.mod", "module mochi\n")
	b.file(repo+"/shibuya/package-lock.json", `{"packages":{}}`)
	b.quiet()
}

// The four feeds and node's index answering with nothing published, each in its own shape:
// there is no one body that parses as empty for all of them.
func (b *box) quiet() {
	b.serves(nvdURL, `{"totalResults":0,"vulnerabilities":[]}`)
	b.serves(ghFeedURL, `[]`)
	b.serves(kevURL, `{"vulnerabilities":[]}`)
	b.serves(bunURL, `[]`)
	b.serves(nodeIndexURL, `[]`)
}

func (b *box) deps() Deps {
	return Deps{
		Now:   func() time.Time { return b.now },
		Log:   &b.log,
		Sleep: func(d time.Duration) { b.slept = append(b.slept, d) },

		Run: func(cmd Command) Output {
			b.calls = append(b.calls, cmd.line())
			b.run = append(b.run, cmd)

			if out, ok := b.answers[cmd.line()]; ok {
				return out
			}
			return Output{OK: true}
		},

		ReadFile: func(path string) ([]byte, error) {
			if body, ok := b.files[path]; ok {
				return []byte(body), nil
			}
			return nil, errors.New("open " + path + ": no such file or directory")
		},

		Get: func(url string) ([]byte, error) {
			b.gets = append(b.gets, url)
			return b.answer(url)
		},
		Post: func(url string, body []byte) ([]byte, error) {
			b.posts = append(b.posts, sent{url: url, body: string(body)})
			return b.answer(url)
		},
	}
}

func (b *box) answer(url string) ([]byte, error) {
	key, found := "", false
	for prefix := range b.responses {
		if strings.HasPrefix(url, prefix) && (!found || len(prefix) > len(key)) {
			key, found = prefix, true
		}
	}
	if !found || len(b.responses[key]) == 0 {
		return nil, errors.New("this Mac could not reach it")
	}

	queue := b.responses[key]
	if len(queue) > 1 {
		b.responses[key] = queue[1:]
	}
	return []byte(queue[0].body), queue[0].err
}

func (b *box) look() Found { return look(b.cfg, b.deps(), b.cursors) }

func (b *box) inventory() Inventory {
	return checker{cfg: b.cfg, deps: b.deps()}.inventory()
}

func (b *box) ran(line string) bool {
	for _, call := range b.calls {
		if call == line {
			return true
		}
	}
	return false
}

// Every URL asked for whose text holds this, which is how NVD's pages and one repository's
// advisories are read back without writing the whole query string out.
func (b *box) asked(part string) []string {
	var found []string
	for _, url := range b.gets {
		if strings.Contains(url, part) {
			found = append(found, url)
		}
	}
	return found
}

// What a source could not say, as one string, since what a test about a trouble line is about
// is the words rather than which of them it was.
func (f Found) troubles() string { return strings.Join(f.Troubles, "\n") }

func (f Found) exposure(t *testing.T, id string) Exposure {
	t.Helper()

	for _, e := range f.Exposures {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("no exposure for %s; found %v", id, f.ids())
	return Exposure{}
}

func (f Found) lead(t *testing.T, id string) Lead {
	t.Helper()

	for _, l := range f.Leads {
		if l.ID == id {
			return l
		}
	}
	t.Fatalf("no lead for %s; found %v", id, f.leadIDs())
	return Lead{}
}

func (f Found) ids() []string {
	var ids []string
	for _, e := range f.Exposures {
		ids = append(ids, e.ID)
	}
	return ids
}

func (f Found) leadIDs() []string {
	var ids []string
	for _, l := range f.Leads {
		ids = append(ids, l.ID)
	}
	return ids
}

func (inv Inventory) named(from, name string) (Installed, bool) {
	for _, in := range inv.Installed {
		if in.From == from && in.Name == name {
			return in, true
		}
	}
	return Installed{}, false
}

const (
	miseLsHome = "mise ls -C " + home + " --json"
	miseLsRepo = "mise ls -C " + repo + " --json"

	brewVulns    = "brew vulns --json"
	brewOutdated = "brew outdated --json=v2"

	miseAdvisories   = "https://api.github.com/repos/jdx/mise/security-advisories"
	claudeAdvisories = "https://api.github.com/repos/anthropics/claude-code/security-advisories"
)

// mise's own listing of one tool at one version, which is the shape every reading of what is
// active comes in: the install path is what the pip reading is aimed at, and `active` is the
// only field that decides whether a version counts.
func miseEntry(version, path string, active bool) string {
	return `{"version":"` + version + `","requested_version":"lts","install_path":"` + path +
		`","source":{"type":"mise.toml","path":"` + home + `/.config/mise/config.toml"},` +
		`"installed":true,"active":` + boolOf(active) + `}`
}

func boolOf(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
