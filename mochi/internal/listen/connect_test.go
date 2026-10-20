package listen

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

type fakeGateway struct {
	fails  []error
	opens  int
	opened []time.Time
	slept  []time.Duration
	clock  time.Time
}

func (f *fakeGateway) gateway(stamp string) gateway {
	return gateway{
		open: func() error {
			f.opens++
			f.opened = append(f.opened, f.clock)
			if f.opens <= len(f.fails) {
				return f.fails[f.opens-1]
			}
			return nil
		},
		sleep: func(d time.Duration) { f.slept = append(f.slept, d); f.clock = f.clock.Add(d) },
		now:   func() time.Time { return f.clock },
		stamp: stamp,
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		scrub: func(err error) string { return err.Error() },
	}
}

// The live outage: the first connect after an exec was closed with 1000 and mochi waited half
// an hour. A gateway that refuses is tried again in seconds, at once for a normal close.
func TestAFailedConnectIsRetriedInSeconds(t *testing.T) {
	f := &fakeGateway{
		fails: []error{
			errors.New("websocket: close 1000 (normal)"),
			errors.New("dial tcp: i/o timeout"),
			errors.New("dial tcp: i/o timeout"),
		},
		clock: time.Unix(1792500000, 0),
	}
	f.gateway(filepath.Join(t.TempDir(), "identify")).connect()

	if f.opens != 4 {
		t.Fatalf("opens = %d", f.opens)
	}
	// Between identifies: the gap alone after the close; then 5 s and 10 s, give or take a
	// fifth, and never under the gap.
	gaps := []time.Duration{}
	for i := 1; i < len(f.opened); i++ {
		gaps = append(gaps, f.opened[i].Sub(f.opened[i-1]))
	}
	if gaps[0] != identifyGap || gaps[1] < identifyGap || gaps[1] > 6*time.Second || gaps[2] < 8*time.Second || gaps[2] > 12*time.Second {
		t.Errorf("gaps between identifies = %v", gaps)
	}
	for _, d := range f.slept {
		if d > connectBackoffMx+connectBackoffMx/5 {
			t.Errorf("slept %s", d)
		}
	}
}

// After an exec the new process waits out Discord's identify gap since the old one's.
func TestAConnectWaitsForTheIdentifyGap(t *testing.T) {
	stamp := filepath.Join(t.TempDir(), "identify")
	now := time.Unix(1792500000, 0)
	os.WriteFile(stamp, []byte(strconv.FormatInt(now.Add(-2*time.Second).UnixNano(), 10)), 0o600)

	f := &fakeGateway{clock: now}
	f.gateway(stamp).connect()

	if len(f.slept) != 1 || f.slept[0] != 3*time.Second {
		t.Errorf("slept %v, want 3s", f.slept)
	}
	g := f.gateway(stamp)
	if last, ok := g.lastIdentify(); !ok || !last.Equal(now.Add(3*time.Second)) {
		t.Errorf("identify stamped at %v", last)
	}
}

// The backoff stops growing at five minutes.
func TestTheBackoffStopsAtFiveMinutes(t *testing.T) {
	fails := make([]error, 12)
	for i := range fails {
		fails[i] = errors.New("503")
	}
	f := &fakeGateway{fails: fails, clock: time.Unix(1792500000, 0)}
	f.gateway(filepath.Join(t.TempDir(), "identify")).connect()

	last := f.slept[len(f.slept)-1]
	if last < 4*time.Minute || last > 6*time.Minute {
		t.Errorf("last wait %s", last)
	}
}
