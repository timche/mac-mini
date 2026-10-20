package mark

import (
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
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
	f.k.Started("t1")
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

	f.k.Started("t1")

	if !strings.Contains(logged.String(), "the marks could not be written") {
		t.Errorf("log:\n%s", logged.String())
	}
}

// A session whose record is gone is forgotten, its name and its state; live ones stay.
func TestPruneForgetsWhatNoSessionHolds(t *testing.T) {
	f := newFixture(t)
	f.k.Named("t-gone", "Gone", ThreadName(Running, "Gone"))
	f.k.Settled("t-gone", Finished)
	f.k.Named("t-live", "Live", ThreadName(Running, "Live"))
	f.k.Started("t-live")

	f.k.Prune(func(thread string) bool { return thread == "t-live" })
	m := f.read(t)
	if m.Names["t-gone"] != nil || m.State["t-gone"] != "" || m.Seq["t-gone"] != 0 {
		t.Errorf("the gone session is still remembered: %+v", m)
	}
	if m.Names["t-live"] == nil || m.State["t-live"] != Running {
		t.Errorf("the live session was forgotten: %+v", m)
	}
}
