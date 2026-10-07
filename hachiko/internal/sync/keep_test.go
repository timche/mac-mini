package sync

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A real sleep rather than the fixture's clock, because this loop is the one that is meant
// to go round for ever: a Sleep that returned at once would spin a core in a test.
func briefly(d *daemon) {
	d.deps.Sleep = func(time.Duration) { time.Sleep(time.Millisecond) }
}

func blind(d *daemon) {
	d.deps.Self = func() (fileID, bool) { return fileID{}, false }
}

// The daemon has no interval of its own, so nothing would otherwise run a binary the wrapper
// built until the Mac restarted. The exit has to be a failure, because the agent restarts on
// one alone: a daemon that exited cleanly would stay exited.
func TestTheDaemonExitsWhenItsBinaryHasBeenReplaced(t *testing.T) {
	d := newFixture(t).daemon()
	briefly(d)

	looks := 0
	d.deps.Self = func() (fileID, bool) {
		if looks++; looks > 2 {
			return fileID{Inode: 2}, true
		}
		return fileID{Inode: 1}, true
	}

	err := d.keep()
	if err == nil {
		t.Fatal("it kept running on a binary that had been replaced")
	}
	if !strings.Contains(err.Error(), "has been replaced") {
		t.Errorf("err: %v", err)
	}
}

// A binary nothing can stat is a check that says nothing rather than a daemon that restarts
// every half minute for ever.
func TestADaemonThatCannotSeeItsOwnBinaryKeepsRunning(t *testing.T) {
	d := newFixture(t).daemon()
	briefly(d)
	blind(d)

	done := make(chan error, 1)
	go func() { done <- d.keep() }()

	select {
	case err := <-done:
		t.Fatalf("it exited: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
}

// The heartbeat is the oldest of the repositories' own ticks, so a loop wedged on something
// no timeout caught stops it advancing and the watch says sync has stopped.
func TestTheHeartbeatIsTheOldestRepositorysTick(t *testing.T) {
	d := newFixture(t).daemon()
	d.beats = make([]atomic.Int64, 2)

	d.beats[0].Store(base.Add(time.Hour).Unix())
	d.beats[1].Store(base.Unix())

	if got := d.oldestBeat(); !got.Equal(base) {
		t.Errorf("got %s, want %s", got, base)
	}
}

// Where the five-minute check reads it, and with a time in it rather than a half-written
// file: a check that reads nothing raises an incident.
func TestTheHeartbeatIsWrittenWholeWhereTheWatchReadsIt(t *testing.T) {
	d := newFixture(t).daemon()

	if err := d.store.Beat(base); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(filepath.Join(d.store.Dir, "heartbeat"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(body)) != "1700000000" {
		t.Errorf("heartbeat: %q", body)
	}
}

// A dry run writes no state at all, the heartbeat included: the check would otherwise read a
// sync that is only reporting as one that is syncing.
func TestADryRunWritesNoHeartbeat(t *testing.T) {
	f := newFixture(t)
	f.dry = true

	d := f.daemon()
	briefly(d)
	blind(d)

	go d.keep()
	time.Sleep(20 * time.Millisecond)

	if _, err := os.Stat(filepath.Join(d.store.Dir, "heartbeat")); err == nil {
		t.Error("a dry run wrote a heartbeat")
	}
}
