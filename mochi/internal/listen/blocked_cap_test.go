package listen

import (
	"os"
	"testing"
	"time"
)

// Once the wait has been counted and pinged, more polls of the same wait write nothing.
func TestALongWaitIsNotRewrittenEveryPoll(t *testing.T) {
	f, thread := startedSession(t)
	f.herdr.agents["mochi-"+thread] = "blocked"
	f.h.WatchBlocked()
	f.h.WatchBlocked()

	old := time.Now().Add(-time.Hour)
	os.Chtimes(f.h.Blocked.Path, old, old)
	for i := 0; i < 5; i++ {
		f.h.WatchBlocked()
	}

	info, _ := os.Stat(f.h.Blocked.Path)
	if !info.ModTime().Equal(old) {
		t.Error("blocked.json was rewritten while nothing changed")
	}
	if all, _ := f.h.Blocked.Load(); all[thread].Polls != blockedPolls {
		t.Errorf("polls = %d", all[thread].Polls)
	}
}
