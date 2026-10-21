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

	gw "github.com/timche/mac-mini/discord/gateway"
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
			&gw.CloseError{Code: gw.CloseNormal},
			errors.New("dial tcp: i/o timeout"),
			errors.New("dial tcp: i/o timeout"),
		},
		clock: time.Unix(1792500000, 0),
	}
	if err := f.gateway(filepath.Join(t.TempDir(), "identify")).connect(); err != nil {
		t.Fatal(err)
	}

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
	if err := f.gateway(stamp).connect(); err != nil {
		t.Fatal(err)
	}

	if len(f.slept) != 1 || f.slept[0] != 3*time.Second {
		t.Errorf("slept %v, want 3s", f.slept)
	}
	g := f.gateway(stamp)
	if last, ok := g.lastIdentify(); !ok || !last.Equal(now.Add(3*time.Second)) {
		t.Errorf("identify stamped at %v", last)
	}
}

func failing(n int, msg string) []error {
	fails := make([]error, n)
	for i := range fails {
		fails[i] = errors.New(msg)
	}
	return fails
}

// The backoff stops growing at five minutes.
func TestTheBackoffStopsAtFiveMinutes(t *testing.T) {
	f := &fakeGateway{fails: failing(connectAttempts-1, "503"), clock: time.Unix(1792500000, 0)}
	if err := f.gateway(filepath.Join(t.TempDir(), "identify")).connect(); err != nil {
		t.Fatal(err)
	}

	last := f.slept[len(f.slept)-1]
	if last < 4*time.Minute || last > 6*time.Minute {
		t.Errorf("last wait %s", last)
	}
}

// A token Discord turns away is not tried again: the process exits for a fresh `op run`.
func TestARefusedTokenEndsTheConnect(t *testing.T) {
	f := &fakeGateway{fails: []error{&gw.CloseError{Code: gw.CloseAuthenticationFailed, Reason: "Authentication failed."}}, clock: time.Unix(1792500000, 0)}
	err := f.gateway(filepath.Join(t.TempDir(), "identify")).connect()
	if !errors.Is(err, ErrRefused) || f.opens != 1 || len(f.slept) != 0 {
		t.Errorf("err = %v after %d opens and %v asleep", err, f.opens, f.slept)
	}
}

// The intents, the shard and the API version are as fatal as the token: nothing tries again.
func TestEveryFatalCloseEndsTheConnect(t *testing.T) {
	for _, code := range []gw.CloseCode{gw.CloseInvalidShard, gw.CloseShardingRequired, gw.CloseInvalidAPIVersion, gw.CloseInvalidIntents, gw.CloseDisallowedIntents} {
		f := &fakeGateway{fails: []error{&gw.CloseError{Code: code}}, clock: time.Unix(1792500000, 0)}
		err := f.gateway(filepath.Join(t.TempDir(), "identify")).connect()
		if !errors.Is(err, ErrRefused) || f.opens != 1 || len(f.slept) != 0 {
			t.Errorf("%d: err = %v after %d opens and %v asleep", code, err, f.opens, f.slept)
		}
	}
}

// A gateway that keeps failing is given up on after a bounded run, well inside half an hour.
func TestARunOfFailuresEndsTheConnect(t *testing.T) {
	start := time.Unix(1792500000, 0)
	f := &fakeGateway{fails: failing(50, "dial tcp: i/o timeout"), clock: start}
	err := f.gateway(filepath.Join(t.TempDir(), "identify")).connect()
	if err == nil || errors.Is(err, ErrRefused) {
		t.Fatalf("err = %v", err)
	}
	if f.opens != connectAttempts {
		t.Errorf("opens = %d", f.opens)
	}
	if took := f.clock.Sub(start); took > connectWindow {
		t.Errorf("gave up after %s", took)
	}
}

// Opens that hang count against the window as well as the attempts.
func TestSlowFailuresEndTheConnectAtTheWindow(t *testing.T) {
	start := time.Unix(1792500000, 0)
	f := &fakeGateway{clock: start}
	g := f.gateway(filepath.Join(t.TempDir(), "identify"))
	g.open = func() error {
		f.opens++
		f.clock = f.clock.Add(10 * time.Minute)
		return errors.New("dial tcp: i/o timeout")
	}
	if err := g.connect(); err == nil {
		t.Fatal("connected")
	}
	if f.opens >= connectAttempts || f.clock.Sub(start) > connectWindow+connectBackoffMx+connectBackoffMx/5+10*time.Minute {
		t.Errorf("opens = %d over %s", f.opens, f.clock.Sub(start))
	}
}

// A connection that dropped and stays down past the window ends the process; one that comes
// back in time starts the count again.
func TestAConnectionThatStaysDownIsGivenUpOn(t *testing.T) {
	var d down
	at := time.Unix(1792500000, 0)
	if d.observe(false, at) != nil || d.observe(false, at.Add(connectWindow-time.Second)) != nil {
		t.Fatal("gave up inside the window")
	}
	if d.observe(true, at.Add(connectWindow-time.Second)) != nil || d.observe(false, at.Add(connectWindow)) != nil {
		t.Fatal("did not start again after coming back")
	}
	if d.observe(false, at.Add(2*connectWindow)) == nil {
		t.Error("a connection down for the window was not given up on")
	}
}
