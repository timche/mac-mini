package gc

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// State is the two things a sweep has to remember between runs. Its own file rather than
// the watch's, for the reason the lock is its own: the watch holds that one across a herdr
// call and an `op run`, and a sweep with ten minutes of containers to stop may not wait for
// it.
type State struct {
	// Which compose projects have been seen running inside a worktree, and which worktree.
	// A volume carries `com.docker.compose.project` and nothing about the folder, so this
	// record is the whole of what makes a volume a removed worktree's rather than a
	// checkout's — `repeek-main_postgres-data` is the main checkout's database with no
	// container in front of it, and it is never in here.
	Projects map[string]Project `json:"projects,omitempty"`

	// What has already been said about a failure, so one that persists is one message
	// rather than six an hour, and one that clears is a line saying so.
	Posted map[string]Posted `json:"posted,omitempty"`
}

type Project struct {
	Worktree string `json:"worktree"`
	SeenAt   int64  `json:"seen_at"`
}

type Posted struct {
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	Since   int64  `json:"since"`

	// The thread the message opened, so the line that says it cleared lands under the one
	// that said it had not.
	Thread string `json:"thread,omitempty"`

	// Whether the message actually left the machine. A send that failed is recorded all the
	// same — the next sweep tries it again rather than treating a failure nobody has heard
	// about as one already reported.
	Sent bool `json:"sent,omitempty"`
}

func (s *State) see(project, worktree string, now time.Time) {
	if s.Projects == nil {
		s.Projects = map[string]Project{}
	}
	s.Projects[project] = Project{Worktree: worktree, SeenAt: now.Unix()}
}

type Store struct{ Dir string }

func (st Store) path() string { return filepath.Join(st.Dir, "state.json") }

// A state file that cannot be read is a sweep that starts over rather than one that stops:
// what it costs is the record of which projects ran in a worktree, and so a run that
// removes no volume. Said out loud, because a file that goes corrupt every run is a sweep
// that can never remove one.
var ErrCorrupt = errors.New("the state file could not be read, so this sweep removes no volume and records the projects it sees again")

func (st Store) Load() (*State, error) {
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

func (st Store) Save(state *State) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return st.write("state-*.json", st.path(), data)
}

// Written whole and moved into place, so a sweep killed mid-write leaves the last file that
// was finished rather than half of this one.
func (st Store) write(pattern, path string, data []byte) error {
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
