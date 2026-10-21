package listen

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/timche/mac-mini/mochi/internal/chat"
	"github.com/timche/mac-mini/mochi/internal/event"
	"github.com/timche/mac-mini/mochi/internal/gh"
	"github.com/timche/mac-mini/mochi/internal/herdr"
	"github.com/timche/mac-mini/mochi/internal/mark"
	"github.com/timche/mac-mini/mochi/internal/origin"
	posts "github.com/timche/mac-mini/mochi/internal/post"
	"github.com/timche/mac-mini/mochi/internal/state"
)

// A pull request a session posted with --pr is followed from then on, when it is in the
// repository its server maps to: the event is the session's word, checked again here.
func (h *Handler) watch(ev event.Event, watches map[string]state.Watch) {
	repo, number, ok := posts.ParsePR(ev.PR)
	if !ok {
		return
	}
	rec, ok, _ := h.Store.Get(ev.Thread)
	if !ok {
		return
	}
	server, ok := h.Cfg.Server(chat.GuildOf(h.API, rec))
	if !ok {
		return
	}
	read := h.Origin
	if read == nil {
		read = origin.Of
	}
	if want, err := read(server.Repo); err != nil || !strings.EqualFold(want, repo) {
		h.Log.Warn("a posted pull request is not in the session's repository, so it is not watched", "thread", ev.Thread, "repo", repo, "want", want, "error", err)
		return
	}
	w := state.Watch{Thread: ev.Thread, Repo: repo, Number: number}
	for _, known := range watches {
		if known.Same(w) {
			return
		}
	}
	watches[fmt.Sprintf("%s %s#%d", ev.Thread, strings.ToLower(repo), number)] = w
	h.Log.Info("watching a pull request a session posted", "thread", ev.Thread, "repo", repo, "number", number)
}

// WatchPRs runs every two minutes and asks GitHub nothing while no pull request is watched. A
// merged one is the session done, either end is said in its thread when the watch saw it open
// first, and a push whose checks
// fail is relayed to the session once, as mochi's own prompt, with nothing of it in Discord.
//
// What it watches is read under the handler's lock and what came of it applied under it, but
// GitHub and herdr are asked with the lock free, so a mention is never held behind them.
func (h *Handler) WatchPRs() {
	h.mu.Lock()
	watches, sessions, ok := h.watched()
	h.mu.Unlock()
	if !ok {
		return
	}

	looks := make(map[string]look, len(watches))
	for url, w := range watches {
		if rec, ok := sessions[w.Thread]; ok && !w.Done {
			looks[url] = h.lookAt(rec, w)
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.apply(looks)
}

// What one look at a watched pull request found.
type look struct {
	gone, open, merged, closed bool

	// The head whose failed checks were dealt with, and whether the session was told of them.
	told    string
	relayed bool
}

func (h *Handler) watched() (map[string]state.Watch, map[string]state.Session, bool) {
	watches, err := h.Watches.Load()
	if err != nil {
		h.Log.Error("the watched pull requests could not be read", "error", err)
		return nil, nil, false
	}
	active := false
	for _, w := range watches {
		active = active || !w.Done
	}
	if !active || h.GH == nil {
		return nil, nil, false
	}
	sessions, err := h.Store.Load()
	if err != nil {
		h.Log.Error("the state file could not be read to follow pull requests", "error", err)
		return nil, nil, false
	}
	return watches, sessions, true
}

func (h *Handler) lookAt(rec state.Session, w state.Watch) look {
	pr, err := gh.Pull(h.GH, w.Repo, w.Number)
	if err != nil {
		h.Log.Warn("a watched pull request could not be read", "repo", w.Repo, "number", w.Number, "error", err)
		return look{}
	}
	switch {
	case pr.Merged:
		return look{merged: true}
	case !pr.Open:
		return look{closed: true}
	case pr.Head != "" && pr.Head != w.Told:
		if dealt, relayed := h.failedChecks(rec, w, pr.Head); dealt {
			return look{open: true, told: pr.Head, relayed: relayed}
		}
	}
	return look{open: true}
}

// The watches are read again under the lock, since an event may have added one meanwhile.
func (h *Handler) apply(looks map[string]look) {
	watches, err := h.Watches.Load()
	if err != nil {
		h.Log.Error("the watched pull requests could not be read", "error", err)
		return
	}
	sessions, err := h.Store.Load()
	if err != nil {
		h.Log.Error("the state file could not be read to follow pull requests", "error", err)
		return
	}
	changed := false
	for url, w := range watches {
		l, looked := looks[url]
		if _, ok := sessions[w.Thread]; !ok {
			l.gone, looked = true, true
		}
		if !looked {
			continue
		}
		switch {
		case l.gone:
			delete(watches, url)
		case l.merged:
			h.Marks.Settled(w.Thread, mark.Finished)
			if w.Open {
				h.say(w.Thread, fmt.Sprintf("#%d merged.", w.Number))
			}
			w.Done = true
		case l.closed:
			if w.Open {
				h.say(w.Thread, fmt.Sprintf("#%d closed without merging.", w.Number))
			}
			w.Done = true
		case !l.open || (w.Open && l.told == ""):
			continue
		default:
			w.Open = true
			if l.told != "" {
				w.Told = l.told
				if l.relayed {
					h.Marks.Started(w.Thread)
				}
			}
		}
		if !l.gone {
			watches[url] = w
		}
		changed = true
	}
	if changed {
		if err := h.Watches.Save(watches); err != nil {
			h.Log.Error("the watched pull requests could not be kept", "error", err)
		}
	}
}

// Whether the head's checks were dealt with, and whether the session was told: failed and
// told, or failed with no session left to tell. Checks that have not failed, a session on a
// question, or one Tim stopped, are tried again at the next look.
func (h *Handler) failedChecks(rec state.Session, w state.Watch, head string) (dealt, relayed bool) {
	if shown, _ := h.Marks.Session(rec.Thread); shown == mark.Stopped {
		return false, false
	}
	failed, err := gh.Failed(h.GH, w.Repo, head)
	if err != nil {
		h.Log.Warn("a watched pull request's checks could not be read", "repo", w.Repo, "number", w.Number, "error", err)
		return false, false
	}
	if len(failed) == 0 {
		return false, false
	}
	status, err := herdr.Status(h.Herdr, rec.Agent)
	switch {
	case err != nil:
		h.Log.Warn("checks failed on a pull request whose session cannot be reached", "repo", w.Repo, "number", w.Number, "error", h.redact(err))
		return true, false
	case status == herdr.Blocked:
		return false, false
	}
	text := fmt.Sprintf("%s%d: %s. Fix it and push.", event.CIPrefix, w.Number, checkNames(failed))
	if err := herdr.Prompt(h.Herdr, rec.Agent, text); err != nil {
		h.Log.Warn("checks failed on a pull request and the session could not be told", "repo", w.Repo, "number", w.Number, "error", h.redact(err))
		return false, false
	}
	h.Log.Info("told a session its pull request's checks failed", "workspace", rec.Workspace, "tab", rec.Tab, "number", w.Number)
	return true, true
}

// A session forgotten takes its watched pull requests with it.
func (h *Handler) unwatch(thread string) {
	watches, err := h.Watches.Load()
	if err != nil {
		return
	}
	for url, w := range watches {
		if w.Thread == thread {
			delete(watches, url)
		}
	}
	if err := h.Watches.Save(watches); err != nil {
		h.Log.Error("the watched pull requests could not be kept", "error", err)
	}
}

// A check's name is whatever the repository's workflows call it, and goes into a prompt as
// mochi's own words, so only a short, plain form of a few of them does.
var unsafeInName = regexp.MustCompile(`[^A-Za-z0-9 _./:-]+`)

const (
	maxCheckName = 60
	maxChecks    = 10
)

func checkNames(failed []string) string {
	var names []string
	for _, name := range failed {
		name = strings.Join(strings.Fields(unsafeInName.ReplaceAllString(name, " ")), " ")
		if len(name) > maxCheckName {
			name = strings.TrimSpace(name[:maxCheckName])
		}
		if name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "a check"
	}
	if extra := len(names) - maxChecks; extra > 0 {
		names = append(names[:maxChecks], fmt.Sprintf("+%d more", extra))
	}
	return strings.Join(names, ", ")
}
