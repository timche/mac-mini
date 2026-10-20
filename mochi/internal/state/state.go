// Package state is mochi's one file of its own: which Discord thread belongs to which herdr
// agent. The listener is the only writer; `mochi post` and `mochi history` read it to hold a
// session to its own thread and its own source channel.
package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Session struct {
	// Where the session posts, and the key everything else is found by.
	Thread string `json:"thread"`

	// The channel or thread it was started from, which is all `mochi history` may read.
	Source string `json:"source"`
	Guild  string `json:"guild"`

	Agent     string `json:"agent"`
	Workspace string `json:"workspace"`
	Tab       string `json:"tab"`

	// Tim's last mention in the thread, so a relay carries only what others wrote since.
	LastMention string `json:"last_mention"`
	Updated     int64  `json:"updated"`
}

type Store struct{ Path string }

func (s Store) Load() (map[string]Session, error) {
	body, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Session{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]Session{}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s Store) Get(thread string) (Session, bool, error) {
	all, err := s.Load()
	if err != nil {
		return Session{}, false, err
	}
	rec, ok := all[thread]
	return rec, ok, nil
}

func (s Store) Put(rec Session) error {
	all, err := s.Load()
	if err != nil {
		return err
	}
	all[rec.Thread] = rec
	return s.save(all)
}

func (s Store) Remove(thread string) error {
	all, err := s.Load()
	if err != nil {
		return err
	}
	delete(all, thread)
	return s.save(all)
}

// Renamed into place, so a reader never sees half a file.
func (s Store) save(all map[string]Session) error {
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
