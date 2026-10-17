package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// State is what the update remembers between nights: which steps it has already said are
// failing, so one that keeps failing is one message rather than one a night, and the line
// saying it works again lands under it.
type State struct {
	Posted map[string]Posted `json:"posted,omitempty"`
}

type Posted struct {
	Step  string `json:"step"`
	Since int64  `json:"since"`

	// The thread the message opened, so the line saying the step works again goes under the
	// one that said it did not.
	Thread string `json:"thread,omitempty"`

	// What the command said. Kept because the message may have to be sent again on a later
	// night, and a failure with nothing of the command's words in it is a failure nobody can
	// act on.
	Detail string `json:"detail,omitempty"`

	// Whether the message left the machine. A send that failed is recorded all the same, and
	// the next night says it again rather than treating a failure nobody has heard about as
	// one already reported.
	Sent bool `json:"sent,omitempty"`
}

type Store struct{ Dir string }

func (st Store) path() string { return filepath.Join(st.Dir, "state.json") }

// A state file that cannot be read costs the update what it has already said, so a failure
// that is still failing is reported once more rather than the run stopping.
var ErrCorrupt = errors.New("the state file could not be read, so what has already been reported is forgotten")

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

// When an update last ran to the end, which is the whole of what the schedule decides by. A
// file of its own rather than a field of the state, because what is read is a modification
// time and nothing has to parse it.
func (st Store) Stamp(now time.Time) error {
	return st.write("last-run-*", filepath.Join(st.Dir, "last-run"),
		[]byte(strconv.FormatInt(now.Unix(), 10)+"\n"))
}

// When that was, and the zero time where nothing has ever finished — which is a Mac that
// updates on its next check rather than one that never does.
//
// The time inside the file rather than the file's own, so that a run under a simulated clock
// is read back as the time it was told it was.
func LastRun(path string) time.Time {
	file, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}
	}

	seconds, err := strconv.ParseInt(strings.TrimSpace(string(file)), 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(seconds, 0)
}

// Written whole and moved into place, so a run killed mid-write leaves the last finished
// file rather than half of this one.
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
