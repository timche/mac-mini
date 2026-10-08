package status

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
	"github.com/timche/mac-mini/hachiko/internal/watch"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

type screen struct {
	cfg  config.Config
	deps Deps
}

// The whole report is built and then written once, so a reader never sees half a screen
// because something further down could not be read.
func (s screen) write() error {
	_, err := io.WriteString(s.deps.Out, s.report())
	return err
}

func (s screen) report() string {
	now := s.deps.Now()
	state, stateErr := s.deps.State()

	var out strings.Builder
	fmt.Fprintf(&out, "hachiko on %s, %s\n", s.cfg.Host, now.Format("Mon 2 Jan 15:04"))

	s.section(&out, "Agents", s.agents(now))
	s.section(&out, "Disk", s.disk())
	s.section(&out, "Incidents", s.incidents(state, stateErr, now))
	s.section(&out, "Sync", s.repos(now))
	s.section(&out, "Logs", s.logs())

	return out.String()
}

func (s screen) section(out *strings.Builder, name string, rows *table) {
	out.WriteString("\n" + name + "\n")
	out.WriteString(rows.String(painter(s.deps.Colour)))
}

// Every agent a Mac with nobody at it is meant to have running, the watch itself first: what
// launchd has, and how long ago each of them last said anything. Two readings rather than
// one, because they answer different questions — launchd says whether the agent is there, and
// the stamp says whether it is doing its job.
func (s screen) agents(now time.Time) *table {
	rows := &table{}

	for _, agent := range append([]watch.Agent{watch.WatchAgent(s.cfg)}, watch.Agents(s.cfg)...) {
		last, stale := s.lastRun(agent, now)
		rows.add(plain(agent.Subject), loaded(s.deps.Job(agent.Label)), last, stale)
	}
	return rows
}

func loaded(job Job) cell {
	switch {
	case !job.Loaded:
		return wrong("not loaded")
	case job.PID == 0:
		// An agent on an interval, between runs. Loaded and running nothing is what it looks
		// like for all but a second of every ten minutes.
		return plain("loaded")
	default:
		return plain(fmt.Sprintf("loaded, pid %d", job.PID))
	}
}

// The age of the stamp and the threshold it is measured against, which only means anything
// where there is a stamp: a listener nobody configured has none and no threshold to go by, and
// printing one against it would read as a deadline on something that is switched off.
func (s screen) lastRun(agent watch.Agent, now time.Time) (cell, cell) {
	since, stamped := s.deps.Stamp(agent)
	if !stamped {
		return plain(agent.Unstamped), plain("")
	}

	age := now.Sub(since)
	last := strings.ToLower(agent.LastLabel) + " " + wording.DurationPhrase(age) + " ago"

	// The watch's own, which is the one agent with no threshold here because nothing on this
	// Mac can tell whether it is running. Said rather than left blank, since a column that is
	// empty for one row reads as a threshold somebody forgot.
	if agent.Stale == 0 {
		return plain(last), plain("watched by shibuya, off this Mac")
	}
	return maybe(last, age > agent.Stale),
		plain("stale after " + wording.DurationPhrase(agent.Stale))
}

// Free space and the two marks it is read against, which is the whole of what the watch
// decides a disk by.
func (s screen) disk() *table {
	rows := &table{}

	free, err := s.deps.FreeKB()
	if err != nil {
		rows.add(plain("free"), wrong("could not be read: "+err.Error()))
		return rows
	}

	rows.add(plain("free"), maybe(wording.GBUnit(free), free < s.cfg.LowKB))
	rows.add(plain("low below"), plain(wording.GBUnit(s.cfg.LowKB)))
	rows.add(plain("critical below"), plain(wording.GBUnit(s.cfg.CriticalKB)))
	return rows
}

// What the watch has open: an incident somebody is working, and the three things it says once
// and remembers having said — an agent that has stopped, a repository sync is getting nowhere
// with, and a log it could not cap. All of it out of the state file, because an incident is
// not a thing that can be measured a second time: it is what the watch decided.
func (s screen) incidents(state *statedir.State, stateErr error, now time.Time) *table {
	rows := &table{}

	switch {
	case errors.Is(stateErr, statedir.ErrCorrupt):
		rows.add(plain("the watch's state"), wrong("could not be read, so nothing below is what it knows"))
	case stateErr != nil:
		rows.add(plain("the watch's state"), wrong("could not be read: "+stateErr.Error()))
		return rows
	}

	for _, id := range openIncidents(state) {
		rows.add(plain(id), plain(s.incident(state, id, now)))
	}

	for _, key := range statedir.SortedKeys(state.Agents) {
		rows.add(plain(key), wrong("reported as not running "+ago(state.Agents[key].Stale, now)))
	}

	for _, key := range statedir.SortedKeys(state.SyncBehind) {
		behind := state.SyncBehind[key]
		if !behind.Said {
			continue
		}
		kind, path, _ := strings.Cut(key, " ")
		rows.add(plain(s.tilde(path)), wrong("reported as "+kind+" "+ago(behind.Since, now)))
	}

	for _, path := range state.CapTrouble {
		rows.add(plain(s.tilde(path)), wrong("reported as a log that could not be capped"))
	}

	if rows.empty() {
		rows.add(plain("nothing open"))
	}
	return rows
}

// Every incident the watch still has something to do about: one whose session owes its first
// report, and one whose question is in front of Tim.
func openIncidents(state *statedir.State) []string {
	open := map[string]bool{}
	for id := range state.Pending {
		open[id] = true
	}
	for _, waiting := range state.Waiting {
		if waiting.Incident != "" {
			open[waiting.Incident] = true
		}
	}
	return statedir.SortedKeys(open)
}

func (s screen) incident(state *statedir.State, id string, now time.Time) string {
	pending, owesReport := state.Pending[id]
	waiting := state.Waiting[statedir.KindOf(id)]
	onIt := waiting.Incident == id

	tab := waiting.Tab
	if owesReport {
		tab = pending.Tab
	}

	said := []string{}
	switch {
	case owesReport:
		said = append(said, "opened "+ago(pending.OpenedAt, now), "no report from the agent yet")
	case onIt && waiting.Since != 0:
		said = append(said, "opened "+ago(waiting.Opened, now),
			"a question has been in front of you for "+wording.DurationPhrase(now.Sub(time.Unix(waiting.Since, 0))))
	case onIt:
		said = append(said, "opened "+ago(waiting.Opened, now), "the agent is still working on it")
	}

	if tab != "" {
		said = append(said, "an on-call session is in "+tab)
	} else {
		said = append(said, "no on-call session is attached")
	}
	return strings.Join(said, ", ")
}

// Every repository sync is meant to be keeping upstream, as git has it this minute. The
// numbers are measured again here rather than read out of anything sync wrote: what sync
// records is what it has said to Tim, and the question this screen answers is what is true
// now.
func (s screen) repos(now time.Time) *table {
	rows := &table{}

	repos, err := s.deps.Repos()
	if err != nil {
		rows.add(plain("the config"), wrong(s.tilde(err.Error())))
		return rows
	}

	for _, repo := range repos {
		state := s.deps.RepoState(repo)
		if !state.Read {
			rows.add(plain(repoName(s, repo)), wrong(state.Trouble))
			continue
		}
		rows.add(plain(repoName(s, repo)), tree(state), s.unpushed(repo, state, now), paused(state, now))
	}

	if rows.empty() {
		rows.add(plain("no repository is configured"))
	}
	return rows
}

// The repository, and the paths inside it sync is limited to where it is limited to any: the
// word every other cell in the row is to be read under, since each of them is measured within
// those paths and nowhere else in the checkout.
func repoName(s screen, repo Repo) string {
	if len(repo.Paths) == 0 {
		return s.tilde(repo.Path)
	}
	return fmt.Sprintf("%s (%s only)", s.tilde(repo.Path), strings.Join(repo.Paths, ", "))
}

// A dirty tree is what every repository here looks like for the seconds between a write and
// the commit, so it is a fact rather than a fault; the watch is what decides one has been
// dirty too long, and that decision is in the section above.
func tree(state RepoState) cell {
	if state.Dirty {
		return plain("dirty")
	}
	return plain("clean")
}

// Late by the watch's own rule — the delay the repository was given plus the slack the watch
// allows on top of it — so the screen and the alert cannot disagree about one commit.
func (s screen) unpushed(repo Repo, state RepoState, now time.Time) cell {
	if state.Unpushed == 0 {
		return plain("nothing unpushed")
	}

	text := wording.CountOf(int64(state.Unpushed), "commit unpushed", "commits unpushed")
	if state.Oldest.IsZero() {
		return plain(text)
	}

	waited := now.Sub(state.Oldest)
	return maybe(text+", oldest "+wording.DurationPhrase(waited)+" old",
		waited > repo.PushDelay+s.cfg.SyncLateAfter)
}

// Sync's own record of a rebase that conflicted, which is the one state a repository stays in
// until somebody moves one side or the other: nothing resumes it by itself, so a screen that
// left it out would be a screen that says a repository is fine while nothing is syncing it.
func paused(state RepoState, now time.Time) cell {
	if state.Paused.IsZero() {
		return plain("")
	}
	return wrong("paused on a conflict for " + wording.DurationPhrase(now.Sub(state.Paused)))
}

func (s screen) logs() *table {
	rows := &table{}

	for _, log := range s.deps.Logs() {
		if !log.Read {
			rows.add(plain(s.tilde(log.Path)), plain("nothing has been written to it"))
			continue
		}
		rows.add(plain(s.tilde(log.Path)),
			maybe(wording.GBUnit(log.KB)+" of "+wording.GBUnit(s.cfg.LogCapKB), log.KB > s.cfg.LogCapKB))
	}
	return rows
}

// As a message spells a path rather than as this process resolved it: `~` is shorter than the
// account name, it is what the rest of hachiko writes, and nothing here is written for one
// account.
func (s screen) tilde(text string) string {
	if s.cfg.Home == "" {
		return text
	}
	return strings.ReplaceAll(text, s.cfg.Home, "~")
}

func ago(at int64, now time.Time) string {
	if at == 0 {
		return "at no time anything recorded"
	}
	return wording.DurationPhrase(now.Sub(time.Unix(at, 0))) + " ago"
}
