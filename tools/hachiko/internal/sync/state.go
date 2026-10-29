package sync

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// State is the two things a sync has to remember between passes. Its own file rather than
// the five-minute check's, for the reason gc's is its own: what sync knows about a paused
// repository and a failure it has posted is nothing the check reads or writes.
type State struct {
	// What has already been said about a failure, so one that persists is one message
	// rather than one a minute, and one that clears is a line saying so.
	Posted map[string]Posted `json:"posted,omitempty"`

	// The repositories whose rebase conflicted, keyed by path. Written down rather than kept
	// in memory, because a daemon that restarts would otherwise rebase again over the
	// conflict that stopped it.
	Paused map[string]Paused `json:"paused,omitempty"`
}

type Posted struct {
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	Since   int64  `json:"since"`

	// The thread the message opened, so the line that says it cleared lands under the one
	// that said it had not.
	Thread string `json:"thread,omitempty"`

	// Whether the message actually left the machine. A send that failed is recorded all the
	// same — the next pass tries it again rather than treating a failure nobody has heard
	// about as one already reported.
	Sent bool `json:"sent,omitempty"`
}

// The two shas a paused repository is re-armed against. Either one moving means somebody has
// been here: a local HEAD that changed is Tim having resolved the conflict or committed over
// it, and an upstream that changed is the other side having moved on. Nothing else resumes
// it, because rebasing again over the same two sides can only conflict again.
type Paused struct {
	Since    int64  `json:"since"`
	Head     string `json:"head"`
	Upstream string `json:"upstream"`
}

// A state file that cannot be read is a sync that starts over rather than one that stops.
// What it costs is one duplicate message per open failure and a paused repository that tries
// its rebase once more, which is a conflict it aborts again.
var ErrCorrupt = errors.New("the state file could not be read, so a failure already reported may be reported again")

// Every repository runs its own loop, so the load-change-save around the state is one at a
// time. The file is two maps and a few hundred bytes: a mutex costs nothing next to the git
// commands either side of it, and a second writer would lose whichever of them finished
// first.
type Store struct {
	Dir string

	mu sync.Mutex
}

func (st *Store) path() string { return filepath.Join(st.Dir, "state.json") }

func (st *Store) load() (*State, error) {
	state := &State{}

	data, err := os.ReadFile(st.path())
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}

	if err := json.Unmarshal(data, state); err != nil {
		return &State{}, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return state, nil
}

func (st *Store) save(state *State) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return st.write("state-*.json", st.path(), data)
}

// Loads the state, hands it to the caller to change, and writes back whatever it left — all
// under the one lock, so two repositories reporting at once do not each save a copy of the
// state as it was before the other touched it.
func (st *Store) Change(with func(*State)) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	state, err := st.load()
	if err != nil && !errors.Is(err, ErrCorrupt) {
		return err
	}
	loadErr := err

	with(state)

	if err := st.save(state); err != nil {
		return err
	}
	return loadErr
}

// Whether a repository is paused, and against what. Read without changing anything, which
// is what the poll does on every fetch interval.
func (st *Store) PausedAt(repo string) (Paused, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()

	state, err := st.load()
	if err != nil && !errors.Is(err, ErrCorrupt) {
		return Paused{}, false
	}
	at, paused := state.Paused[repo]
	return at, paused
}

// The heartbeat the five-minute check reads: a sync that has stopped is the one thing about
// it nothing on this Mac could otherwise notice. A file of its own rather than a field of
// the state, because the check may not parse a file this writes the shape of, and because
// what it has to know is a modification time.
func (st *Store) Beat(now time.Time) error {
	return st.write("heartbeat-*", filepath.Join(st.Dir, "heartbeat"),
		[]byte(strconv.FormatInt(now.Unix(), 10)+"\n"))
}

// Written whole and moved into place, so a sync killed mid-write leaves the last file that
// was finished rather than half of this one — which for the heartbeat is the difference
// between a check that reads an older time and one that reads nothing and alerts.
func (st *Store) write(pattern, path string, data []byte) error {
	if err := os.MkdirAll(st.Dir, 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(st.Dir, pattern)
	if err != nil {
		return err
	}
	name := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}
