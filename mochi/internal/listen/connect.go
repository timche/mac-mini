package listen

import (
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Discord allows one identify every five seconds, and a listener that exec'd its new build
// identifies again moments after the old one did: so every connect waits that long after the
// one before, whichever process made it.
const (
	identifyGap      = 5 * time.Second
	connectBackoff   = 5 * time.Second
	connectBackoffMx = 5 * time.Minute

	// Past either, the process exits and launchd starts it again under a fresh `op run`, which
	// is what picks up a token rotated in 1Password: nothing inside the process ever can.
	connectAttempts = 10
	connectWindow   = 30 * time.Minute
)

// ErrRefused is Discord turning the token away, close code 4004: no retry with the same token
// will get in.
var ErrRefused = errors.New("Discord refused the bot token")

// gateway is the connect step, so a test can fail it.
type gateway struct {
	open  func() error
	sleep func(time.Duration)
	now   func() time.Time
	stamp string // when the last identify was sent, by any mochi process
	log   *slog.Logger
	scrub func(error) string
}

// connect tries until the gateway is up: a gateway that will not connect is tried again in
// seconds, backing off to five minutes with jitter, and a normal close straight after an
// identify is tried again at once. A refused token, or a run of failures as long as
// connectAttempts or connectWindow, is an error for the caller to exit on.
func (g gateway) connect() error {
	backoff := connectBackoff
	first := g.now()
	for attempt := 0; ; attempt++ {
		if last, ok := g.lastIdentify(); ok {
			if wait := identifyGap - g.now().Sub(last); wait > 0 {
				g.sleep(wait)
			}
		}
		g.markIdentify()

		err := g.open()
		if err == nil {
			return nil
		}
		if refused(err) {
			return fmt.Errorf("%w: %s", ErrRefused, g.scrub(err))
		}
		if attempt == 0 && normalClose(err) {
			g.log.Warn("Discord closed the connection as it opened, so it is tried again at once", "error", g.scrub(err))
			continue
		}
		if attempt+1 >= connectAttempts || g.now().Sub(first) >= connectWindow {
			return fmt.Errorf("could not connect to Discord in %d attempts over %s: %s", attempt+1, g.now().Sub(first).Round(time.Second), g.scrub(err))
		}
		wait := jitter(backoff)
		g.log.Error("could not connect to Discord", "error", g.scrub(err), "retry", wait.Round(time.Second).String())
		g.sleep(wait)
		backoff = min(backoff*2, connectBackoffMx)
	}
}

// The websocket library says it as "websocket: close 1000 (normal)"; read off the text so its
// error type is not one more dependency.
func normalClose(err error) bool { return strings.Contains(err.Error(), "close 1000") }

func refused(err error) bool { return strings.Contains(err.Error(), "close 4004") }

// down tells a connection that dropped and stayed down, which discordgo would retry for ever
// with the token it has, from one that is being re-established.
type down struct{ since time.Time }

func (d *down) observe(up bool, now time.Time) error {
	switch {
	case up:
		d.since = time.Time{}
	case d.since.IsZero():
		d.since = now
	case now.Sub(d.since) >= connectWindow:
		return fmt.Errorf("Discord has not answered for %s", now.Sub(d.since).Round(time.Second))
	}
	return nil
}

// Up to a fifth either way, so several things waiting on Discord do not all come back at once.
func jitter(d time.Duration) time.Duration {
	return d + time.Duration((rand.Float64()*0.4-0.2)*float64(d))
}

func (g gateway) lastIdentify() (time.Time, bool) {
	body, err := os.ReadFile(g.stamp)
	if err != nil {
		return time.Time{}, false
	}
	nanos, err := strconv.ParseInt(strings.TrimSpace(string(body)), 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, nanos), true
}

func (g gateway) markIdentify() {
	if os.MkdirAll(filepath.Dir(g.stamp), 0o700) == nil {
		os.WriteFile(g.stamp, []byte(strconv.FormatInt(g.now().UnixNano(), 10)+"\n"), 0o600)
	}
}
