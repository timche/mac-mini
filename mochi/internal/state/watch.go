package state

import (
	"encoding/json"
	"errors"
	"os"
)

// Watch is a pull request a session posted, which mochi follows until it is merged or closed.
type Watch struct {
	Thread string `json:"thread"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`

	// The head commit whose failed checks the session was last told about, so it is told once
	// per push.
	Told string `json:"told,omitempty"`
}

// WatchStore is the listener's alone, keyed by the pull request's address.
type WatchStore struct{ Path string }

func (s WatchStore) Load() (map[string]Watch, error) {
	body, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Watch{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]Watch{}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s WatchStore) Save(all map[string]Watch) error {
	return write(s.Path, all)
}
