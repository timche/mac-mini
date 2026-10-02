package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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

func (st Store) Load() (*State, error) {
	state := &State{}

	data, err := os.ReadFile(st.path())
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}

	// A state file that cannot be read is a sweep that starts over rather than one
	// that stops: a corrupt sample costs one interval of history, a refusal costs
	// every interval after it.
	if err := json.Unmarshal(data, state); err != nil {
		return &State{}, nil
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

// mkdir rather than flock, which macOS does not have. A lock older than the
// interval is taken over, so one left behind by a killed sweep cannot stop every
// sweep after it.
type Lock struct {
	dir  string
	held bool
}

func (st Store) Acquire(stale time.Duration, now time.Time) (*Lock, bool, string) {
	if err := os.MkdirAll(st.dir, 0o755); err != nil {
		return nil, false, err.Error()
	}

	dir := filepath.Join(st.dir, "lock")
	lock := &Lock{dir: dir}

	if lock.try(now) {
		return lock, true, ""
	}

	info, err := os.Stat(dir)
	if err != nil || now.Sub(info.ModTime()) <= stale {
		return nil, false, ""
	}

	owner := "an earlier check"
	if pid, err := os.ReadFile(filepath.Join(dir, "pid")); err == nil && len(pid) > 0 {
		owner = string(pid)
	}

	os.RemoveAll(dir)
	if lock.try(now) {
		return lock, true, owner
	}
	return nil, false, ""
}

func (l *Lock) try(now time.Time) bool {
	if err := os.Mkdir(l.dir, 0o755); err != nil {
		return false
	}
	l.held = true
	os.WriteFile(filepath.Join(l.dir, "pid"), []byte(strconv.Itoa(os.Getpid())), 0o644)
	os.Chtimes(l.dir, now, now)
	return true
}

func (l *Lock) Release() {
	if l != nil && l.held {
		os.RemoveAll(l.dir)
		l.held = false
	}
}

// What notify does to a state another sweep may be writing at the same moment: wait
// for the lock rather than clobber it, since a sweep takes a second or two.
func (st Store) Update(stale time.Duration, now func() time.Time, change func(*State)) error {
	for attempt := 0; attempt < 40; attempt++ {
		lock, ok, _ := st.Acquire(stale, now())
		if ok {
			defer lock.Release()

			state, err := st.Load()
			if err != nil {
				return err
			}
			change(state)
			return st.Save(state)
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("another check held the lock on %s for ten seconds", st.dir)
}
