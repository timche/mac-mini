package update

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Handoff is the whole of what the nightly update and the five-minute check say to each other
// about a step that failed, and it is two files rather than one so that each of them has a
// single writer: the update writes what it is failing on into its own state directory, and
// the check writes back which incident it raised each of those steps as into the watch's.
// Neither ever touches the other's state.json — they hold a lock each, and the update holds
// its across however long brew takes, which is a dozen checks.
//
// The failing file is a statement of what is wrong rather than a queue of things to post: the
// update rewrites it whole on every run out of what it remembers, and the check raises the
// steps in it that are not in the raised file yet. So a Discord that was down all day loses
// nothing, a step is never raised twice, and a step that stops failing simply leaves the file
// — which is how the check knows there is nobody left to wait for on its incident.
type Handoff struct {
	// The update's own state directory, which it writes and the check reads.
	Dir string

	// The watch's, which the check writes and the update reads for the thread to clear into.
	WatchDir string
}

// Handed is one step the update could not do, as the check reads it. Everything a message
// about one needs is here, because the check is what writes that message and the words for a
// step are the update's own: the phrase and the action are hachiko's own wording either way,
// and Detail is the command's own output and goes in a fenced block like any other.
type Handed struct {
	Step   string `json:"step"`
	Phrase string `json:"phrase"`
	Detail string `json:"detail,omitempty"`
	Since  int64  `json:"since"`
	Action string `json:"action,omitempty"`
}

// Raised is the incident one of those steps was raised as, and the thread of the post it
// opened — which is where the line saying the step works again goes, however many nights
// later that is.
type Raised struct {
	Incident string `json:"incident"`
	Thread   string `json:"thread,omitempty"`
}

func (h Handoff) failingPath() string { return filepath.Join(h.Dir, "failing.json") }

// Named for whose record it is rather than for what is in it, because it sits in a directory
// full of the watch's own files and is the one thing in there the watch writes for somebody
// else to read.
func (h Handoff) raisedPath() string { return filepath.Join(h.WatchDir, "update-raised.json") }

type failingFile struct {
	Steps []Handed `json:"steps"`
}

type raisedFile struct {
	Steps map[string]Raised `json:"steps"`
}

// What the update is failing on, as of its last run. A file that is missing or will not parse
// is read as nothing failing, which costs the check one night of knowing rather than its
// check: the update writes the whole of it again on its next run.
func (h Handoff) Failing() []Handed {
	data, err := os.ReadFile(h.failingPath())
	if err != nil {
		return nil
	}

	file := failingFile{}
	if json.Unmarshal(data, &file) != nil {
		return nil
	}

	var steps []Handed
	for _, step := range file.Steps {
		if step.Step != "" {
			steps = append(steps, step)
		}
	}
	return steps
}

func (h Handoff) SetFailing(steps []Handed) error {
	data, err := json.Marshal(failingFile{Steps: steps})
	if err != nil {
		return err
	}
	return writeWhole(h.failingPath(), "failing-*.json", data)
}

// Which incident each failing step has been raised as, keyed by the step. Read by both: by
// the check, to tell a step it has already posted about from one it has not, and by the
// update, for the thread to say in that the step works again.
func (h Handoff) Raised() map[string]Raised {
	raised := map[string]Raised{}

	data, err := os.ReadFile(h.raisedPath())
	if err != nil {
		return raised
	}

	file := raisedFile{}
	if json.Unmarshal(data, &file) != nil {
		return raised
	}

	for step, was := range file.Steps {
		if was.Incident != "" {
			raised[step] = was
		}
	}
	return raised
}

func (h Handoff) SetRaised(raised map[string]Raised) error {
	data, err := json.Marshal(raisedFile{Steps: raised})
	if err != nil {
		return err
	}
	return writeWhole(h.raisedPath(), "update-raised-*.json", data)
}
