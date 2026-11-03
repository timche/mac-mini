package listen

import (
	"strings"
	"testing"

	"github.com/timche/mac-mini/mochi/internal/config"
	"github.com/timche/mac-mini/mochi/internal/hc"
)

// The labels are the whole of what puts a session in front of Tim: the Herdr app shows no
// terminal without a space, and files it under the tab.
func TestASessionIsLabelledForTheAppAndRecordedByIt(t *testing.T) {
	f := newFixture(t)
	m := f.mention(channel, 1, tim, "have a look at this", nil)

	f.h.Handle(m)

	labels := f.hc.live["mochi-"+m.ID].labels
	for key, want := range map[string]string{
		"herdr.space":  "sp-repeek",
		"herdr.tab":    "tb-mochi-" + m.ID,
		hc.ThreadLabel: m.ID,
	} {
		if labels[key] != want {
			t.Errorf("%s = %q, want %q", key, labels[key], want)
		}
	}
	if rec := f.record(m.ID); rec.Space != "sp-repeek" || rec.Tab != "tb-mochi-"+m.ID {
		t.Errorf("record = %+v", rec)
	}

	// The labels are for Tim in the app; Discord is told only that a session started.
	if posts := f.sentTo(m.ID); len(posts) != 1 || head(posts[0]) != startedPost(m.ID) {
		t.Errorf("the ack is %q", posts)
	}
	if !strings.Contains(shipped(t, "briefing.md"), "This tab is already named for the thread; don't rename it.") {
		t.Error("the briefing does not say to keep the tab's name")
	}
}

// A server names its project and the space id follows from it; one whose space the app calls
// something else says so.
func TestTheSpaceIsTheWorkspaceUnlessTheServerNamesOne(t *testing.T) {
	for _, c := range []struct {
		server config.Server
		want   string
	}{
		{config.Server{Workspace: "repeek"}, "sp-repeek"},
		{config.Server{Workspace: "repeek", Space: "sp-repeek-2"}, "sp-repeek-2"},
		{config.Server{Space: "sp-only"}, "sp-only"},
	} {
		if got := c.server.SpaceID(); got != c.want {
			t.Errorf("%+v: SpaceID() = %q, want %q", c.server, got, c.want)
		}
	}
}
