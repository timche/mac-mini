package mark

import (
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/mochi/internal/state"
)

func (f *fixture) read(t *testing.T) marks {
	t.Helper()
	var m marks
	body, err := os.ReadFile(f.k.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// A retry with nothing due writes nothing: it runs every half minute for the life of mochi.
func TestARetryWithNothingDueLeavesTheFileAlone(t *testing.T) {
	f := newFixture(t)
	f.k.Started("t1", mention)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(f.k.Path, old, old)

	f.k.Retry()

	info, _ := os.Stat(f.k.Path)
	if !info.ModTime().Equal(old) {
		t.Error("the file was rewritten with nothing changed")
	}
}

func TestAFailedWriteIsLogged(t *testing.T) {
	f := newFixture(t)
	var logged strings.Builder
	f.k.Log = slog.New(slog.NewTextHandler(&logged, nil))
	os.Mkdir(f.k.Path+".tmp", 0o700) // a write of the next version cannot land

	f.k.Started("t1", mention)

	if !strings.Contains(logged.String(), "the marks could not be written") {
		t.Errorf("log:\n%s", logged.String())
	}
}

// A session whose record is gone is forgotten with every message it was shown on, a group
// with no session left goes with its mention, and a reaction nothing holds goes once it is
// old; live sessions keep theirs.
func TestPruneForgetsWhatNoSessionHolds(t *testing.T) {
	f := newFixture(t)
	gone := state.Ref{Channel: "c", Message: "gone"}
	live := state.Ref{Channel: "c", Message: "live"}
	loose := state.Ref{Channel: "c", Message: "loose"}
	group := state.Ref{Channel: "c", Message: "group"}

	f.k.Started("t-gone", gone)
	f.k.Settled("t-gone", Finished)
	f.k.Started("t-live", live)
	f.k.Started("t-g1", state.Ref{Channel: "t-g1", Message: "s1"})
	f.k.Group(group, []string{"t-g1"})
	f.k.Set(loose, Failed)

	alive := func(thread string) bool { return thread == "t-live" }
	f.k.Prune(alive, 24*time.Hour)
	m := f.read(t)
	for _, k := range []string{"c/gone", "c/group", "t-g1/s1"} {
		if _, ok := m.Shown[k]; ok {
			t.Errorf("%s is still remembered", k)
		}
	}
	if m.Shown["c/live"] != Running || m.Shown["c/loose"] != Failed {
		t.Errorf("shown = %v", m.Shown)
	}
	if len(m.Groups) != 0 || m.Threads["t-gone"] != nil {
		t.Errorf("groups %v, threads %v", m.Groups, m.Threads)
	}

	f.now = f.now.Add(25 * time.Hour)
	f.k.Prune(alive, 24*time.Hour)
	m = f.read(t)
	if _, ok := m.Shown["c/loose"]; ok {
		t.Error("an old reaction nothing holds is still remembered")
	}
	if m.Shown["c/live"] != Running {
		t.Error("a live session's reaction was forgotten")
	}
}
