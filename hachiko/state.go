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

	// Directories that did not answer a read in time, and when. Remembered so that a
	// walk does not spend the same seconds on the same hung open every five minutes, and
	// retried after a while so that one slow read under disk pressure — which is exactly
	// when a runaway writer is thrashing the volume — does not blind the watch to a
	// whole tree for good.
	Stalled []Stall `json:"stalled,omitempty"`

	Pending map[string]Pending `json:"pending,omitempty"`

	// One per kind, because there is one on-call agent per kind and one question at a
	// time in front of it.
	Waiting map[string]Waiting `json:"waiting,omitempty"`

	// The Discord thread each open incident has, when the bot is configured. It is what
	// puts a reminder under the alert it is about, what `hachiko notify` posts into, and
	// what `hachiko listen` reads a reply out of.
	Threads map[string]string `json:"threads,omitempty"`
}

type DiskSample struct {
	At    int64            `json:"at,omitempty"`
	Files map[string]int64 `json:"files,omitempty"`

	// Free space at that sample, so the rate the disk is actually losing can be read
	// against the rate the files hachiko can see are gaining. A writer it never found is
	// still taking the disk.
	FreeKB int64 `json:"free_kb,omitempty"`
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

// Stall is a directory the walk gave up on, with the first time it did and the last.
type Stall struct {
	Dir     string `json:"dir"`
	FirstAt int64  `json:"first_at"`
	LastAt  int64  `json:"last_at"`
}

// Which of them are still being skipped, and which are due to be tried again. A hung open
// costs one directory timeout to retry, so an hour is cheap; a tree that has come back
// being invisible until somebody edits a state file is not.
func dueForRetry(stalled []Stall, now time.Time, after time.Duration) (skip []string, retry []string) {
	for _, s := range stalled {
		if now.Sub(time.Unix(s.LastAt, 0)) >= after {
			retry = append(retry, s.Dir)
		} else {
			skip = append(skip, s.Dir)
		}
	}
	return skip, retry
}

// Pending is an incident an on-call session was opened for and has not reported on.
type Pending struct {
	OpenedAt int64  `json:"opened_at"`
	Tab      string `json:"tab"`
	Details  string `json:"details"`
}

// Waiting is an on-call agent that has put its question in front of Tim, and how long
// it has been there. Since is zero until a check has actually seen the agent blocked,
// so the clock starts at the question rather than at the brief.
//
// Nudged is when hachiko itself cancelled the question, kept as the record of it: the
// agent leaving `blocked` is what Tim answering and what hachiko's own esc both look like
// from herdr, so neither of them ends a wait — only an outcome, a fresh question, a closed
// session or the deadline followed by silence does.
type Waiting struct {
	Incident string   `json:"incident"`
	Tab      string   `json:"tab"`
	Opened   int64    `json:"opened"`
	Since    int64    `json:"since,omitempty"`
	Nudged   int64    `json:"nudged,omitempty"`
	Steps    []string `json:"steps,omitempty"`

	// The prompt hachiko's own esc left owing, and the steps it completes once it lands: the
	// question went and what was to replace it did not, so the next check sends that prompt
	// alone. A second esc there would cancel whatever the session asked or started in the
	// meantime, and nothing is owed to the question any more — it is gone.
	//
	// Safe to keep and send again because it is hachiko's own words and nothing else: every
	// name a path or a command line could have put in the prompt is in the data, which is
	// measured again when the retry goes out.
	Owed      string   `json:"owed,omitempty"`
	OwedSteps []string `json:"owed_steps,omitempty"`

	// When the agent was first seen with no question up and nothing in flight, which is the
	// start of the grace it gets to report an outcome before hachiko says that nothing did.
	// Cleared whenever it has something to say again and whenever hachiko hands it anything,
	// so the grace runs from the moment it went quiet and never from before the last thing
	// it was asked.
	Settled int64 `json:"settled,omitempty"`

	// When a check first projected that free space would reach the critical threshold before
	// the handover. The second check to say so is what hands the decision over, because one
	// interval is an extrapolation and the thing it authorises is a kill.
	Worsening int64 `json:"worsening,omitempty"`

	// What the agent said in its first report it would do if nobody answered, which is
	// what the warning quotes rather than guessing at.
	Default string `json:"default,omitempty"`

	Asked Asked `json:"asked,omitempty"`
}

// Asked is the incident as it stood when the question went up, which is the only thing
// a material change can be measured against: the options in front of Tim are about
// these numbers, and once they have moved far enough the question is the wrong one.
type Asked struct {
	At      int64            `json:"at,omitempty"`
	FreeKB  int64            `json:"free_kb,omitempty"`
	Level   int64            `json:"level,omitempty"`
	Sizes   map[string]int64 `json:"sizes,omitempty"`
	Writers []string         `json:"writers,omitempty"`
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

func mergeSorted(lists ...[]string) []string {
	seen := map[string]bool{}
	var out []string

	for _, list := range lists {
		for _, v := range list {
			if !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	sort.Strings(out)
	return out
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
//
// The file holds the two things about the report the next sweep has a use for and nothing
// else of it, the report itself having already gone to the channel: the option the agent
// would fall back on, and whether this was the message that said the incident is resolved.
//
// A session sends more than two messages — its analysis, the same question posted into the
// Discord thread, an update when the situation moves — so a marker merges with the one
// already there rather than replacing it. Overwriting lost the fallback option every time
// a later message happened not to repeat it, and the warning then had nothing to quote.
func (st Store) MarkReported(incident, fallback string, outcome bool) error {
	if !incidentID.MatchString(incident) {
		return fmt.Errorf("%q is not an incident id", incident)
	}
	if err := os.MkdirAll(st.reportedDir(), 0o755); err != nil {
		return err
	}

	was := st.readReport(incident)
	if fallback == "" {
		fallback = was.fallback
	}
	outcome = outcome || was.outcome

	body := "fallback: " + strings.ReplaceAll(fallback, "\n", " ") + "\n"
	if outcome {
		body = "outcome\n" + body
	}
	return os.WriteFile(filepath.Join(st.reportedDir(), incident), []byte(body), 0o644)
}

type report struct {
	fallback string
	outcome  bool
}

// A marker written before this format existed is one line of fallback with no key on it,
// which is still the fallback and still not an outcome.
func (st Store) readReport(incident string) report {
	if !incidentID.MatchString(incident) {
		return report{}
	}
	file, err := os.ReadFile(filepath.Join(st.reportedDir(), incident))
	if err != nil {
		return report{}
	}

	out := report{}
	for _, line := range strings.Split(string(file), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "outcome":
			out.outcome = true
		case strings.HasPrefix(line, "fallback:"):
			out.fallback = strings.TrimSpace(strings.TrimPrefix(line, "fallback:"))
		case line != "" && out.fallback == "":
			out.fallback = line
		}
	}
	return out
}

func (st Store) ReportedFallback(incident string) string { return st.readReport(incident).fallback }

// Only the message the session marked as the outcome ends the wait. Reading any second
// report as one ended it on the question the session had just posted into Discord, which is
// the one message the orders on that path require it to send.
func (st Store) ReportedOutcome(incident string) bool { return st.readReport(incident).outcome }

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

// The one action an on-call session has asked Tim to approve with a code. A file of its
// own rather than a field in the state, for the reason `notify`'s marker is: the session
// writes it while a sweep may be holding the lock, and `hachiko listen` reads it on a
// five-second loop that may not wait for either.
//
// It is what makes a code approve one thing: a code with no request open approves nothing,
// and the approval the listener hands the agent names the action the agent itself
// registered rather than whatever it has decided to do since.
func (st Store) approvalPath(incident string) string {
	return filepath.Join(st.dir, "approvals", incident)
}

func (st Store) RequestApproval(incident, action string) error {
	if !incidentID.MatchString(incident) {
		return fmt.Errorf("%q is not an incident id", incident)
	}
	if strings.TrimSpace(action) == "" {
		return errors.New("an approval request has to say which action it is for")
	}
	if err := os.MkdirAll(filepath.Dir(st.approvalPath(incident)), 0o755); err != nil {
		return err
	}
	// Clipped here rather than at the caller, because what an action names is a path and a
	// command chosen by whatever filled the disk, and it goes back into a prompt.
	return os.WriteFile(st.approvalPath(incident), []byte(safe(action, replyLimit)), 0o644)
}

func (st Store) OpenApproval(incident string) string {
	if !incidentID.MatchString(incident) {
		return ""
	}
	action, err := os.ReadFile(st.approvalPath(incident))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(action))
}

// One request, one approval. The next action the session wants approved is a request of
// its own, and a code it already used is no help with it.
func (st Store) CloseApproval(incident string) {
	if incidentID.MatchString(incident) {
		os.Remove(st.approvalPath(incident))
	}
}

// A marker for an incident nothing is waiting on any more, which is what a report for
// an incident that had already been superseded leaves behind. An incident still owing a
// report and one already waiting on its question both keep theirs: the second is where
// the session's outcome arrives, which may land in the seconds this sweep has left.
func (st Store) ForgetReportedExcept(keep map[string]bool) {
	entries, err := os.ReadDir(st.reportedDir())
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !keep[entry.Name()] {
			os.Remove(filepath.Join(st.reportedDir(), entry.Name()))
		}
	}
}
