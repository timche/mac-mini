package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/timche/mac-mini/mochi/internal/split"
)

// Pending is a question mochi asked Tim — one session or several — with everything needed to
// start them once he answers: where his mention is, his words, and what the classifier saw.
// The conversation itself is read again then, so it is as fresh as his answer.
type Pending struct {
	Nonce       string       `json:"nonce"`
	Guild       string       `json:"guild"`
	Channel     string       `json:"channel"`
	Mention     string       `json:"mention"`
	Question    string       `json:"question"`
	Instruction string       `json:"instruction"`
	Answer      split.Answer `json:"answer"`
	Created     int64        `json:"created"`
}

type PendingStore struct{ Path string }

func (s PendingStore) Load() (map[string]Pending, error) {
	body, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Pending{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]Pending{}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s PendingStore) Get(nonce string) (Pending, bool, error) {
	all, err := s.Load()
	if err != nil {
		return Pending{}, false, err
	}
	p, ok := all[nonce]
	return p, ok, nil
}

// The question a message of Tim's replies to, when he answers in words rather than a button.
func (s PendingStore) ByQuestion(message string) (Pending, bool, error) {
	all, err := s.Load()
	if err != nil {
		return Pending{}, false, err
	}
	for _, p := range all {
		if p.Question == message {
			return p, true, nil
		}
	}
	return Pending{}, false, nil
}

func (s PendingStore) Put(p Pending) error {
	all, err := s.Load()
	if err != nil {
		return err
	}
	all[p.Nonce] = p
	return s.save(all)
}

func (s PendingStore) Remove(nonce string) error {
	all, err := s.Load()
	if err != nil {
		return err
	}
	delete(all, nonce)
	return s.save(all)
}

func (s PendingStore) save(all map[string]Pending) error {
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
