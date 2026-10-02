package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// State is everything a sweep has to remember: the two samples it measures growth
// and CPU share against, what has already been alerted on so a thing that is still
// going is still one incident, and the sessions that owe a report.
type State struct {
	Disk DiskSample `json:"disk"`
	CPU  CPUSample  `json:"cpu"`

	AlertedFiles []string `json:"alerted_files,omitempty"`
	AlertedProcs []string `json:"alerted_procs,omitempty"`

	// The threshold the last alert was about, in GB, so a disk that keeps filling
	// says so once more and says nothing again until it has recovered.
	LowSpaceLevel int64 `json:"low_space_level,omitempty"`

	Pending map[string]Pending `json:"pending,omitempty"`
}

type DiskSample struct {
	At    int64            `json:"at,omitempty"`
	Files map[string]int64 `json:"files,omitempty"`
}

type CPUSample struct {
	At    int64                 `json:"at,omitempty"`
	Procs map[string]ProcSample `json:"procs,omitempty"`
}

// HotSince is the start of the unbroken run of busy intervals and CPUAtHotSince the
// cumulative time at that moment, so the average over the window needs no history
// beyond these three numbers.
type ProcSample struct {
	CPU           float64 `json:"cpu"`
	HotSince      int64   `json:"hot_since,omitempty"`
	CPUAtHotSince float64 `json:"cpu_at_hot_since,omitempty"`
}

// Pending is an incident an on-call session was opened for and has not reported on.
type Pending struct {
	OpenedAt int64  `json:"opened_at"`
	Tab      string `json:"tab"`
	Details  string `json:"details"`
}

func (s *State) hasDiskSample() bool { return s.Disk.At > 0 }
func (s *State) hasCPUSample() bool  { return s.CPU.At > 0 }

func (s *State) alertedFile(path string) bool { return contains(s.AlertedFiles, path) }
func (s *State) alertedProc(key string) bool  { return contains(s.AlertedProcs, key) }

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type Store struct{ dir string }

func (st Store) path() string { return filepath.Join(st.dir, "state.json") }

// A state file that cannot be read is a sweep that starts over rather than one that
// stops: a corrupt sample costs one interval of history, a refusal costs every
// interval after it. It is still said out loud, because a file that goes corrupt
// every run is a sweep that can never measure growth and would otherwise look quiet.
var errStateCorrupt = errors.New("the state file could not be read, so this check measures from nothing")

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
		return &State{}, fmt.Errorf("%w: %v", errStateCorrupt, err)
	}
	return state, nil
}

func (st Store) Save(state *State) error {
	if err := os.MkdirAll(st.dir, 0o755); err != nil {
		return err
	}

	data, err := json.Marshal(state)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(st.dir, "state-*.json")
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
	return os.Rename(name, st.path())
}

// mkdir rather than flock, which macOS does not have. A lock older than the interval
// is taken over, so one left behind by a killed sweep cannot stop every sweep after
// it.
type Lock struct {
	dir string
	pid int
}

// errLockHeld is the one failure that is not a fault: another sweep is running, and
// this one has nothing to say about it.
var errLockHeld = errors.New("another check holds the lock")

// A failure that is not errLockHeld is a fault worth a line, and `mkdir` on a disk
// with nothing left is exactly the fault this watch exists to catch — so the sweep
// goes ahead without a lock rather than going quiet about a full disk.
func (st Store) Acquire(stale time.Duration, now time.Time) (*Lock, string, error) {
	if err := os.MkdirAll(st.dir, 0o755); err != nil {
		return nil, "", err
	}

	dir := filepath.Join(st.dir, "lock")

	lock, err := tryLock(dir, now)
	if err == nil {
		return lock, "", nil
	}
	if !errors.Is(err, os.ErrExist) {
		return nil, "", err
	}

	info, statErr := os.Stat(dir)
	if statErr != nil {
		return nil, "", statErr
	}
	if now.Sub(info.ModTime()) <= stale {
		return nil, "", errLockHeld
	}

	owner := "an earlier check"
	if pid, err := os.ReadFile(filepath.Join(dir, "pid")); err == nil && len(pid) > 0 {
		owner = strings.TrimSpace(string(pid))
	}

	if err := os.RemoveAll(dir); err != nil {
		return nil, "", err
	}

	lock, err = tryLock(dir, now)
	if err != nil {
		return nil, "", err
	}
	return lock, owner, nil
}

func tryLock(dir string, now time.Time) (*Lock, error) {
	if err := os.Mkdir(dir, 0o755); err != nil {
		return nil, err
	}

	pid := os.Getpid()
	os.WriteFile(filepath.Join(dir, "pid"), []byte(strconv.Itoa(pid)), 0o644)
	os.Chtimes(dir, now, now)
	return &Lock{dir: dir, pid: pid}, nil
}

// Only the lock this process actually holds. A sweep slow enough to have its lock
// taken over is a sweep whose Release would otherwise delete the lock of the check
// that took it, and then a third would run beside both.
func (l *Lock) Release() {
	if l == nil || l.pid == 0 {
		return
	}

	pid, err := os.ReadFile(filepath.Join(l.dir, "pid"))
	if err != nil || strings.TrimSpace(string(pid)) != strconv.Itoa(l.pid) {
		return
	}

	os.RemoveAll(l.dir)
	l.pid = 0
}

// An incident id is a filename below, so it may only be what a sweep makes one.
var incidentID = regexp.MustCompile(`\A[a-z]+-[0-9]+\z`)

func (st Store) reportedDir() string { return filepath.Join(st.dir, "reported") }

// How `hachiko notify` tells the next sweep that the session has spoken, without
// touching state.json and so without waiting for a lock a sweep can hold for as long
// as herdr and `op run` take. One file per incident, created and never read back by
// the writer: the sweep is the only thing that edits state, and this is the one fact
// it needs from outside.
func (st Store) MarkReported(incident string) error {
	if !incidentID.MatchString(incident) {
		return fmt.Errorf("%q is not an incident id", incident)
	}
	if err := os.MkdirAll(st.reportedDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(st.reportedDir(), incident), nil, 0o644)
}

func (st Store) Reported(incident string) bool {
	if !incidentID.MatchString(incident) {
		return false
	}
	_, err := os.Stat(filepath.Join(st.reportedDir(), incident))
	return err == nil
}

func (st Store) ClearReported(incident string) {
	if incidentID.MatchString(incident) {
		os.Remove(filepath.Join(st.reportedDir(), incident))
	}
}

func (st Store) ReportedIDs() []string {
	entries, err := os.ReadDir(st.reportedDir())
	if err != nil {
		return nil
	}

	var ids []string
	for _, entry := range entries {
		if incidentID.MatchString(entry.Name()) {
			ids = append(ids, entry.Name())
		}
	}
	sort.Strings(ids)
	return ids
}

// A marker for an incident nothing is waiting on any more, which is what a report for
// an incident that had already been superseded leaves behind.
func (st Store) ForgetReportedExcept(keep map[string]Pending) {
	entries, err := os.ReadDir(st.reportedDir())
	if err != nil {
		return
	}
	for _, entry := range entries {
		if _, ok := keep[entry.Name()]; !ok {
			os.Remove(filepath.Join(st.reportedDir(), entry.Name()))
		}
	}
}
