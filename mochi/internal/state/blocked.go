package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Blocked is what the listener knows of a session waiting on Tim: how many polls in a row
// have found it so, and whether he has been told for this episode.
type Blocked struct {
	Polls  int  `json:"polls"`
	Pinged bool `json:"pinged"`
}

type BlockedStore struct{ Path string }

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
		return nil, err
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
