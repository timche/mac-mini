package listen

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A file that will not parse is said once, moved aside, and the watch carries on from nothing.
func TestACorruptWaitingFileIsMovedAsideOnce(t *testing.T) {
	f, thread := startedSession(t)
	var logged strings.Builder
	f.h.Log = slog.New(slog.NewTextHandler(&logged, nil))
	os.WriteFile(f.h.Blocked.Path, []byte("{not json"), 0o600)

	f.herdr.agents["mochi-"+thread] = "blocked"
	f.h.WatchBlocked()
	f.h.WatchBlocked()

	if n := strings.Count(logged.String(), "was corrupt"); n != 1 {
		t.Errorf("said %d times:\n%s", n, logged.String())
	}
	aside, _ := filepath.Glob(f.h.Blocked.Path + ".corrupt-*")
	if len(aside) != 1 {
		t.Errorf("moved aside: %v", aside)
	}
	if f.pings(thread) != 1 {
		t.Errorf("pings = %d", f.pings(thread))
	}
}
