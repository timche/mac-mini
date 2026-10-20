package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Blocked is what the listener knows of a session waiting on Tim: how many polls in a row
// have found it so, and whether he has been told for this episode.
type Blocked struct {
	Polls  int  `json:"polls,omitempty"`
	Pinged bool `json:"pinged,omitempty"`

	// The session record's Updated when herdr last said its agent was gone; it is not asked
	// again until the record changes, as it does when a follow-up starts a new session.
	Gone int64 `json:"gone,omitempty"`
}

type BlockedStore struct{ Path string }

// ErrCorrupt is a file that would not parse and has been moved aside: what it held is lost,
// which costs at most one more ping, and the next load starts fresh.
var ErrCorrupt = errors.New("the waiting sessions file did not parse, so it was moved aside and starts fresh")

func (s BlockedStore) Load() (map[string]Blocked, error) {
	body, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Blocked{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]Blocked{}
	if err := json.Unmarshal(body, &out); err != nil {
		os.Rename(s.Path, fmt.Sprintf("%s.corrupt-%d", s.Path, time.Now().Unix()))
		return map[string]Blocked{}, ErrCorrupt
	}
	return out, nil
}

func (s BlockedStore) Save(all map[string]Blocked) error {
	body, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}
