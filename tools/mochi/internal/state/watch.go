package state

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
)

// Watch is a pull request a session posted, which mochi follows until it is merged or closed.
type Watch struct {
	Thread string `json:"thread"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`

	// The head commit whose failed checks the session was last told about, so it is told once
	// per push.
	Told string `json:"told,omitempty"`

	// Whether a look found it open: a merge or close it never saw open happened before the
	// session posted it, and the session has said so itself.
	Open bool `json:"open,omitempty"`

	// Merged or closed, and kept until the session is forgotten, so the session posting it
	// again starts no second watch and no second notice.
	Done bool `json:"done,omitempty"`
}

// Same is whether two watches are one session's one pull request.
func (w Watch) Same(o Watch) bool {
	return w.Thread == o.Thread && w.Number == o.Number && strings.EqualFold(w.Repo, o.Repo)
}

// WatchStore is the listener's alone; its keys mean nothing beyond keeping entries apart.
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
