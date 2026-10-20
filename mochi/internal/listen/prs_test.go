package listen

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/mochi/internal/event"
	"github.com/timche/mac-mini/mochi/internal/mark"
)

const prURL = "https://github.com/timche/repeek/pull/42"

// GitHub as gh api answers it: one pull request, and the check runs of its head.
type fakeGH struct {
	state  string
	merged bool
	head   string
	checks []map[string]string
	calls  []string
}

func (g *fakeGH) run(args ...string) ([]byte, error) {
	g.calls = append(g.calls, strings.Join(args, " "))
	switch {
	case args[1] == "repos/timche/repeek/pulls/42":
		return json.Marshal(map[string]any{"state": g.state, "merged": g.merged, "head": map[string]string{"sha": g.head}})
	case strings.HasPrefix(args[1], "repos/timche/repeek/commits/"+g.head+"/check-runs"):
		return json.Marshal(map[string]any{"check_runs": g.checks})
	}
	return nil, nil
}

// A session that posted its pull request, as `mochi post --pr` leaves it in the spool.
func watched(t *testing.T) (*fixture, string, *fakeGH) {
	f, thread := startedSession(t)
	g := &fakeGH{state: "open", head: "sha1"}
	f.h.GH = g.run
	at := f.h.Now()
	if err := event.Put(f.cfg.EventsDir(), event.Event{Thread: thread, Kind: event.PR, PR: prURL, At: at.Unix(), Nanos: at.UnixNano()}); err != nil {
		t.Fatal(err)
	}
	f.h.ConsumeEvents()
	return f, thread, g
}

func (f *fixture) watching() int {
	f.t.Helper()
	all, err := f.h.Watches.Load()
	if err != nil {
		f.t.Fatal(err)
	}
	return len(all)
}

// Nothing watched, nothing asked of GitHub.
func TestNoWatchedPullRequestAsksGitHubNothing(t *testing.T) {
	f, _ := startedSession(t)
	g := &fakeGH{}
	f.h.GH = g.run
	f.h.WatchPRs()
	if len(g.calls) != 0 {
		t.Errorf("calls = %v", g.calls)
	}
}

// Merged: the session is done, its thread says so once, and it is watched no more; its tab is
// left as it is.
func TestAMergedPullRequestIsTheSessionDone(t *testing.T) {
	f, thread, g := watched(t)
	if f.watching() != 1 {
		t.Fatal("the posted pull request is not watched")
	}
	f.h.WatchPRs()
	if f.watching() != 1 || f.sentTo(thread)[len(f.sentTo(thread))-1] == "Merged." {
		t.Fatal("an open pull request was taken for merged")
	}

	g.state, g.merged = "closed", true
	f.h.WatchPRs()
	f.h.WatchPRs()
	f.inState(thread, mark.Finished)
	if f.api.Channels[thread].Name != "✅ research" {
		t.Errorf("thread = %q", f.api.Channels[thread].Name)
	}
	merged := 0
	for _, p := range f.sentTo(thread) {
		if p == "Merged." {
			merged++
		}
	}
	if merged != 1 || f.watching() != 0 {
		t.Errorf("merged posts %d, still watching %d", merged, f.watching())
	}
	if len(f.herdr.called("tab close")) != 0 {
		t.Error("a merge closed the tab")
	}
}

// Closed without merging: said once, watched no more, the state left alone.
func TestAClosedPullRequestIsSaid(t *testing.T) {
	f, thread, g := watched(t)
	f.h.Marks.Settled(thread, mark.Asking)
	g.state = "closed"
	f.h.WatchPRs()
	f.h.WatchPRs()
	if posts := f.sentTo(thread); posts[len(posts)-1] != "Closed without merging." || f.watching() != 0 {
		t.Errorf("posts %q, watching %d", posts, f.watching())
	}
	f.inState(thread, mark.Asking)
}

// Failed checks on the head go to the session once per push, as mochi's own prompt, with ⏳
// and nothing in Discord; a new push that fails is told again.
func TestFailedChecksAreRelayedOncePerPush(t *testing.T) {
	f, thread, g := watched(t)
	agent := "mochi-" + thread
	f.h.Marks.Settled(thread, mark.Asking)
	posted := len(f.sentTo(thread))
	g.checks = []map[string]string{
		{"name": "test", "status": "completed", "conclusion": "failure"},
		{"name": "lint", "status": "completed", "conclusion": "success"},
		{"name": "e2e", "status": "in_progress", "conclusion": ""},
		{"name": "build", "status": "completed", "conclusion": "timed_out"},
	}

	f.h.WatchPRs()
	f.h.WatchPRs()
	prompts := f.herdr.prompts[agent]
	want := "CI failed on #42: build, test. Fix it and push."
	if len(prompts) != 2 || prompts[1] != want || !event.FromMochi(prompts[1]) {
		t.Fatalf("prompts = %q", prompts)
	}
	f.inState(thread, mark.Running)
	if len(f.sentTo(thread)) != posted {
		t.Errorf("the failure reached Discord: %q", f.sentTo(thread)[posted:])
	}
	if f.pings(thread) != 0 {
		t.Error("Tim was pinged for a failed check")
	}

	g.head = "sha2"
	f.h.WatchPRs()
	if len(f.herdr.prompts[agent]) != 3 {
		t.Error("a new push that failed was not told")
	}
}

// Passing checks, or a session on a question, are no relay; the question is tried again later.
func TestPassingChecksOrAQuestionTellNothing(t *testing.T) {
	f, thread, g := watched(t)
	agent := "mochi-" + thread
	g.checks = []map[string]string{{"name": "test", "status": "completed", "conclusion": "success"}}
	f.h.WatchPRs()
	if len(f.herdr.prompts[agent]) != 1 {
		t.Error("passing checks were relayed")
	}

	g.checks = []map[string]string{{"name": "test", "status": "completed", "conclusion": "failure"}}
	f.herdr.agents[agent] = "blocked"
	f.h.WatchPRs()
	if len(f.herdr.prompts[agent]) != 1 {
		t.Error("a failure was pushed over a question")
	}
	f.herdr.agents[agent] = "idle"
	f.h.WatchPRs()
	if len(f.herdr.prompts[agent]) != 2 {
		t.Error("the failure was not told once the question was answered")
	}
}

// Watches outlive a restart, and go with the session they belong to.
func TestWatchesOutliveARestartAndGoWithTheirSession(t *testing.T) {
	f, _, g := watched(t)
	restarted := &Handler{
		Cfg: f.h.Cfg, API: f.api, Herdr: f.herdr.run, Store: f.h.Store, Waits: f.h.Waits, Marks: f.h.Marks,
		Watches: f.h.Watches, GH: g.run, Now: f.h.Now, Log: f.h.Log,
	}
	g.state, g.merged = "closed", true
	restarted.WatchPRs()
	if f.watching() != 0 {
		t.Error("the watch did not outlive a restart")
	}

	h, other, _ := watched(t)
	delete(h.herdr.agents, "mochi-"+other)
	h.at(h.h.Now().Add(8 * 24 * time.Hour))
	h.h.Prune(keepGone)
	if h.watching() != 0 {
		t.Error("a pruned session's watch was kept")
	}
}

// A session Tim stopped is not told its checks failed, and is told once he takes it up again.
func TestAStoppedSessionIsNotToldItsChecksFailed(t *testing.T) {
	f, thread, g := watched(t)
	agent := "mochi-" + thread
	g.checks = []map[string]string{{"name": "test", "status": "completed", "conclusion": "failure"}}
	f.h.Handle(f.mention(thread, 5, tim, "stop", nil))
	f.h.WatchPRs()
	if len(f.herdr.prompts[agent]) != 1 {
		t.Fatal("a stopped session was told")
	}
	f.h.Handle(f.mention(thread, 6, tim, "go on", nil))
	f.h.WatchPRs()
	if prompts := f.herdr.prompts[agent]; len(prompts) != 3 || !strings.HasPrefix(prompts[2], "CI failed on #42") {
		t.Errorf("prompts = %q", prompts)
	}
}
