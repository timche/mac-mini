package main

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Process is one line of the whole-machine sample. CPU is the cumulative time the
// process has had, never ps's own %cpu, which on macOS is a decaying average over
// the process's whole life: it understates a worker that has just gone wrong and
// overstates one that has finished.
type Process struct {
	PID   int
	PPID  int
	RSSKB int64
	CPU   time.Duration

	// Who owns it. The uid is what anything decides by, because it is a number ps cannot
	// print ambiguously; the name is for the message, and ps is the only thing here that can
	// resolve one — macOS keeps its daemon accounts in Directory Services and not in
	// /etc/passwd, which is all a CGO_ENABLED=0 binary could read for itself.
	UID  int
	User string

	// The start date as ps printed it, which is half of the identity: a pid is
	// reused, and the history behind one belongs to whoever held it.
	Start     string
	StartedAt time.Time

	Command string
}

func (p Process) Key() string { return strconv.Itoa(p.PID) + ":" + p.Start }

// The executable, which is the first token of the command line.
func (p Process) Path() string {
	path, _, _ := strings.Cut(p.Command, " ")
	return path
}

func (p Process) Name() string { return filepath.Base(p.Path()) }

// What to call the owner in a message: the name ps resolved, or the uid when it gave none.
func (p Process) Owner() string {
	if p.User != "" {
		return p.User
	}
	return strconv.Itoa(p.UID)
}

// One ps for the whole machine. `command` is last because it is the one field that
// can hold a space, -ww because ps otherwise truncates it to a terminal width, and
// LC_ALL=C because the start date is five tokens whose month and weekday names are
// the locale's otherwise — and that date is an identity this compares as a string.
//
// `user` goes last of the fixed fields and after `lstart`, which is five tokens at a fixed
// position: a name with a space in it would then cost a token off the front of the command
// line rather than shifting every number behind it, and the numbers are what every rule
// here decides by.
func psCommand(ctx context.Context) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "ps", "-A", "-ww", "-o", "pid=,ppid=,uid=,rss=,time=,lstart=,user=,command=")
	cmd.Env = append(cmd.Environ(), "LC_ALL=C")
	return cmd
}

func sampleProcesses() ([]Process, error) {
	ctx, cancel := context.WithTimeout(context.Background(), sampleTimeout)
	defer cancel()

	out, err := psCommand(ctx).Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	return parseProcesses(string(out)), nil
}

func parseProcesses(out string) []Process {
	var procs []Process

	for _, line := range strings.Split(out, "\n") {
		p, ok := parseProcess(line)
		if ok {
			procs = append(procs, p)
		}
	}
	return procs
}

func parseProcess(line string) (Process, bool) {
	// pid, ppid, uid, rss, time, the five tokens of the start date, then the owner's name;
	// the command is whatever is left, with its own spacing kept.
	head, rest, ok := splitFields(line, 11)
	if !ok || rest == "" {
		return Process{}, false
	}

	pid, err := strconv.Atoi(head[0])
	if err != nil {
		return Process{}, false
	}
	ppid, err := strconv.Atoi(head[1])
	if err != nil {
		return Process{}, false
	}
	uid, err := strconv.Atoi(head[2])
	if err != nil {
		return Process{}, false
	}
	rss, err := strconv.ParseInt(head[3], 10, 64)
	if err != nil {
		return Process{}, false
	}
	cpu, ok := parseCPUTime(head[4])
	if !ok {
		return Process{}, false
	}

	start := strings.Join(head[5:10], " ")
	startedAt, err := time.ParseInLocation("Mon Jan 2 15:04:05 2006", start, time.Local)
	if err != nil {
		startedAt = time.Time{}
	}

	return Process{
		PID:       pid,
		PPID:      ppid,
		UID:       uid,
		User:      head[10],
		RSSKB:     rss,
		CPU:       cpu,
		Start:     start,
		StartedAt: startedAt,
		Command:   rest,
	}, true
}

// n whitespace-separated fields and then the remainder verbatim, so a command line
// keeps the spacing it was started with.
func splitFields(line string, n int) ([]string, string, bool) {
	fields := make([]string, 0, n)
	rest := line

	for len(fields) < n {
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			return nil, "", false
		}
		end := strings.IndexAny(rest, " \t")
		if end < 0 {
			fields = append(fields, rest)
			rest = ""
			continue
		}
		fields = append(fields, rest[:end])
		rest = rest[end:]
	}

	return fields, strings.TrimLeft(rest, " \t"), len(fields) == n
}

// ps prints cumulative time as [[dd-]hh:]mm:ss.ss.
func parseCPUTime(s string) (time.Duration, bool) {
	days := 0.0
	if before, after, found := strings.Cut(s, "-"); found {
		d, err := strconv.ParseFloat(before, 64)
		if err != nil {
			return 0, false
		}
		days, s = d, after
	}

	parts := strings.Split(s, ":")
	var h, m, sec float64

	switch len(parts) {
	case 3:
		h, m, sec = mustFloat(parts[0]), mustFloat(parts[1]), mustFloat(parts[2])
	case 2:
		m, sec = mustFloat(parts[0]), mustFloat(parts[1])
	default:
		return 0, false
	}

	total := days*86400 + h*3600 + m*60 + sec
	if total < 0 {
		return 0, false
	}
	// Rounded to the millisecond rather than scaled straight to nanoseconds, which
	// turns ps's hundredths of a second into a value that is a nanosecond out.
	return time.Duration(math.Round(total*1000)) * time.Millisecond, true
}

func mustFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// Everything between a pid and pid 1, so hachiko never reports itself, the shell
// launchd started it from, or launchd. Read out of the sample it already took
// rather than from a ps of its own.
func ownTree(procs []Process, pid int) map[int]bool {
	parent := make(map[int]int, len(procs))
	for _, p := range procs {
		parent[p.PID] = p.PPID
	}

	tree := map[int]bool{pid: true}
	for cur := pid; cur > 1; {
		next, ok := parent[cur]
		if !ok || next == cur || next <= 1 {
			break
		}
		tree[next] = true
		cur = next
	}
	return tree
}
