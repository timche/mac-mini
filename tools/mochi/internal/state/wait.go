package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// Wait is what the listener knows of a session that stopped on Tim: whether he has been
// pinged since he last sent it anything, and how a ping that did not land is being retried.
type Wait struct {
	Pinged  bool `json:"pinged,omitempty"`
	Pending bool `json:"pending,omitempty"`

	// A wait shows ❓ and pings only once Due has passed, in nanoseconds, so a follow-up that
	// was queued behind the turn and starts the moment it ends cancels it first; Asked once ❓
	// is showing.
	Due   int64 `json:"due,omitempty"`
	Asked bool  `json:"asked,omitempty"`

	// A ping that did not land is tried again at RetryAt, after a wait that doubles; one
	// Discord says has no thread to land in is never tried again.
	Backoff int64 `json:"backoff,omitempty"`
	RetryAt int64 `json:"retry_at,omitempty"`
	Dead    bool  `json:"dead,omitempty"`

	// How many states the session had been through when the ping set ❓, so Tim's answer
	// puts it back to ⏳ only if nothing else — a --review, a --done — has been set since.
	Seq int `json:"seq,omitempty"`

	// When Tim last reached the session, in nanoseconds: anything it reported from before
	// then is out of date and pings nobody.
	Heard int64 `json:"heard,omitempty"`

	// Whether that last word came through Discord rather than typed in the session, and when
	// a session that posted --done --close has its tab closed, in nanoseconds; only one Tim
	// last reached from Discord is closed.
	Discord bool  `json:"discord,omitempty"`
	CloseAt int64 `json:"close_at,omitempty"`
}

// Lasting is what outlives one wait: when and how Tim last reached the session, a close that is
// coming, and a thread that is gone.
func (w Wait) Lasting() Wait {
	return Wait{Dead: w.Dead, Heard: w.Heard, Discord: w.Discord, CloseAt: w.CloseAt}
}

type WaitStore struct{ Path string }

// ErrCorrupt is a file that would not parse and has been moved aside: what it held is lost,
// which costs at most one more ping, and the next load starts fresh.
var ErrCorrupt = errors.New("the waiting sessions file did not parse, so it was moved aside and starts fresh")

func (s WaitStore) Load() (map[string]Wait, error) {
	body, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Wait{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]Wait{}
	if err := json.Unmarshal(body, &out); err != nil {
		os.Rename(s.Path, fmt.Sprintf("%s.corrupt-%d", s.Path, time.Now().Unix()))
		return map[string]Wait{}, ErrCorrupt
	}
	return out, nil
}

func (s WaitStore) Save(all map[string]Wait) error {
	return write(s.Path, all)
}
