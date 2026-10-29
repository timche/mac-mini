// Package usage is how much of the account's Claude usage limits is left, which is the one
// thing hachiko watches that is not a reading of the machine at all: every Claude Code
// session on this Mac spends the same account's 5-hour and 7-day limits, so running out of
// them is what stops work here long before the CPU or the disk does.
//
// Nothing on this Mac may ask for the number. The only place Claude Code says it is the
// event it hands the status line command, so `claude-hooks statusline` keeps that event's
// `rate_limits` object in a file as it arrived, and this reads it back. Which is why the
// shape below is Claude Code's spelling rather than hachiko's: the file is the documented
// object plus the time it was read, and the one thing described on both sides is its path.
package usage

import (
	"encoding/json"
	"os"
	"time"
)

// The two windows one account has, by the names Claude Code gives them. Its third,
// `spend_limit`, is for a gateway that bills a team and belongs to no subscription here.
const (
	FiveHour = "five_hour"
	SevenDay = "seven_day"
)

// Window is one limit: how much of it has gone, from 0 to 100, and when it starts again.
type Window struct {
	Used   float64
	Resets time.Time
}

// Spent reports whether a window is over the mark and worth saying something about. A window
// whose reset time has passed is a reading about a window that is over, which says nothing
// about the one running now: Claude Code drops a window from the event once it resets, so
// what is in the file then is simply the last thing anybody read.
func (w Window) Spent(percent float64, now time.Time) bool {
	return w.Live(now) && w.Used >= percent
}

func (w Window) Live(now time.Time) bool {
	return !w.Resets.IsZero() && w.Resets.After(now)
}

// Reading is one such file: when the status line was last drawn with usage in its event, and
// the windows that event carried.
type Reading struct {
	ReadAt  time.Time
	Windows map[string]Window
}

func (r Reading) Window(key string) (Window, bool) {
	window, ok := r.Windows[key]
	return window, ok
}

// What the file holds, which is `rate_limits` verbatim and the time it was read.
type file struct {
	ReadAt     int64 `json:"read_at"`
	RateLimits map[string]struct {
		Used   float64 `json:"used_percentage"`
		Resets int64   `json:"resets_at"`
	} `json:"rate_limits"`
}

// Read answers with the reading and whether there is one at all. No file is the ordinary
// state of a Mac where no session has drawn a status line since hachiko's state was made,
// and a file that will not parse is one a session is rewriting several times a minute — so
// neither is a fault to report, and both are the same answer: nothing is known.
func Read(path string) (Reading, bool) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Reading{}, false
	}

	var read file
	if err := json.Unmarshal(body, &read); err != nil || read.ReadAt <= 0 {
		return Reading{}, false
	}

	out := Reading{ReadAt: time.Unix(read.ReadAt, 0), Windows: map[string]Window{}}
	for key, window := range read.RateLimits {
		// A reset time of nought is a window the event did not say when it starts again,
		// which is no reset time rather than one in 1970.
		one := Window{Used: window.Used}
		if window.Resets > 0 {
			one.Resets = time.Unix(window.Resets, 0)
		}
		out.Windows[key] = one
	}
	return out, true
}
