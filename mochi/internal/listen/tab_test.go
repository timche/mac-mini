package listen

import (
	"strings"
	"testing"

	"github.com/timche/mac-mini/mochi/internal/split"
)

func TestATabIsNamedForItsTitleAndNeverRepeatsOne(t *testing.T) {
	f := newFixture(t)
	f.herdr.tabs = []string{"faceit-overlay-hover"}
	f.h.Split = splitsInto([]split.Task{{Title: "FACEIT overlay hover", Instruction: "have a look at this"}}, nil, nil)

	m := f.mention(channel, 1, tim, "have a look at this", nil)
	f.h.Handle(m)

	tabs := f.herdr.called("tab create")
	if len(tabs) != 1 || tabs[0][5] != "faceit-overlay-hover-2" {
		t.Fatalf("tabs = %v", tabs)
	}
	// The tab carries the thread under exactly the name sessions and the lead skill read.
	if strings.Join(tabs[0][9:], " ") != "--env MOCHI_THREAD="+m.ID {
		t.Errorf("env = %v", tabs[0][9:])
	}
	// The label is for Tim in the terminal; Discord is told only that a session started.
	if posts := f.sentTo(m.ID); len(posts) != 1 || head(posts[0]) != startedPost(m.ID) {
		t.Errorf("the ack is %q", posts)
	}
	if !strings.Contains(shipped(t, "briefing.md"), "This tab is already named for the thread; don't rename it.") {
		t.Error("the briefing does not say to keep the tab's name")
	}
}

func TestTabLabels(t *testing.T) {
	for _, c := range []struct {
		name  string
		taken []string
		want  string
	}{
		{"FACEIT overlay blocks hover", nil, "faceit-overlay-blocks-hover"},
		{"Research average elo per team and per season", nil, "research-average-elo-per-team"},
		{"Research average elo per team and per season", []string{"research-average-elo-per-team"}, "research-average-elo-per-tea-2"},
		{"x", []string{"x", "x-2"}, "x-3"},
		{"!!!", nil, "discord"},
	} {
		got := tabLabel(c.name, c.taken)
		if got != c.want || len(got) > 30 {
			t.Errorf("tabLabel(%q, %v) = %q, want %q", c.name, c.taken, got, c.want)
		}
	}
}
