package listen

import (
	"fmt"
	"strings"

	"github.com/timche/mac-mini/mochi/internal/event"
	"github.com/timche/mac-mini/mochi/internal/gh"
	"github.com/timche/mac-mini/mochi/internal/herdr"
	"github.com/timche/mac-mini/mochi/internal/mark"
	posts "github.com/timche/mac-mini/mochi/internal/post"
	"github.com/timche/mac-mini/mochi/internal/state"
)

// A pull request a session posted with --pr is followed from then on.
func (h *Handler) watch(ev event.Event, watches map[string]state.Watch) {
	repo, number, ok := posts.ParsePR(ev.PR)
	if !ok {
		return
	}
	if _, known := watches[ev.PR]; !known {
		watches[ev.PR] = state.Watch{Thread: ev.Thread, Repo: repo, Number: number}
		h.Log.Info("watching a pull request a session posted", "thread", ev.Thread, "repo", repo, "number", number)
	}
}

// WatchPRs runs every two minutes and asks GitHub nothing while no pull request is watched. A
// merged one is the session done, a closed one is said in its thread, and a push whose checks
// fail is relayed to the session once, as mochi's own prompt, with nothing of it in Discord.
func (h *Handler) WatchPRs() {
	h.mu.Lock()
	defer h.mu.Unlock()

	watches, err := h.Watches.Load()
	if err != nil {
		h.Log.Error("the watched pull requests could not be read", "error", err)
		return
	}
	if len(watches) == 0 || h.GH == nil {
		return
	}
	changed := false
	for url, w := range watches {
		rec, ok, _ := h.Store.Get(w.Thread)
		if !ok {
			delete(watches, url)
			changed = true
			continue
		}
		pr, err := gh.Pull(h.GH, w.Repo, w.Number)
		if err != nil {
			h.Log.Warn("a watched pull request could not be read", "repo", w.Repo, "number", w.Number, "error", err)
			continue
		}
		switch {
		case pr.Merged:
			h.Marks.Settled(w.Thread, mark.Finished)
			h.say(w.Thread, "Merged.")
			delete(watches, url)
			changed = true
		case !pr.Open:
			h.say(w.Thread, "Closed without merging.")
			delete(watches, url)
			changed = true
		case pr.Head != "" && pr.Head != w.Told:
			if h.failedChecks(rec, w, pr.Head) {
				w.Told = pr.Head
				watches[url] = w
				changed = true
			}
		}
	}
	if changed {
		if err := h.Watches.Save(watches); err != nil {
			h.Log.Error("the watched pull requests could not be kept", "error", err)
		}
	}
}

// Whether the head's checks were dealt with: failed and told to the session, or failed with no
// session left to tell. Checks that have not failed, or a session on a question, are tried
// again at the next look.
func (h *Handler) failedChecks(rec state.Session, w state.Watch, head string) bool {
	failed, err := gh.Failed(h.GH, w.Repo, head)
	if err != nil {
		h.Log.Warn("a watched pull request's checks could not be read", "repo", w.Repo, "number", w.Number, "error", err)
		return false
	}
	if len(failed) == 0 {
		return false
	}
	status, err := herdr.Status(h.Herdr, rec.Agent)
	switch {
	case err != nil:
		h.Log.Warn("checks failed on a pull request whose session cannot be reached", "repo", w.Repo, "number", w.Number, "error", h.redact(err))
		return true
	case status == herdr.Blocked:
		return false
	}
	text := fmt.Sprintf("%s%d: %s. Fix it and push.", event.CIPrefix, w.Number, strings.Join(failed, ", "))
	if err := herdr.Prompt(h.Herdr, rec.Agent, text); err != nil {
		h.Log.Warn("checks failed on a pull request and the session could not be told", "repo", w.Repo, "number", w.Number, "error", h.redact(err))
		return false
	}
	h.Log.Info("told a session its pull request's checks failed", "workspace", rec.Workspace, "tab", rec.Tab, "number", w.Number)
	h.Marks.Started(rec.Thread)
	return true
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
