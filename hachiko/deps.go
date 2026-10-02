package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Deps is everything a sweep learns about the machine or does to it. Each one is a
// function so that a test drives the whole of the decision-making without a stub
// binary on PATH, a disk to fill or an hour to wait.
type Deps struct {
	Now    func() time.Time
	Log    io.Writer
	Getpid func() int

	FreeKB func() (int64, error)
	// skip is what earlier sweeps found would not answer, on top of the folders this
	// never opens at all.
	BigFiles  func(skip []string) WalkResult
	Writers   func(path string) string
	Truncate  func(path string) error
	Processes func() ([]Process, error)
	CWD       func(pid int) string

	// Opens the on-call session and answers with where it is and whether the brief
	// reached it, which is what the message about to go out has to say.
	Oncall func(name, brief string) (OncallSession, error)

	// What herdr says the on-call agent of a kind is doing — blocked, working, idle,
	// done, or gone when there is no such agent any more. Blocked is the whole of how
	// hachiko knows Tim has not answered yet.
	AgentStatus func(kind string) (string, error)

	// Cancels the question the agent is waiting on and hands it a prompt in its place.
	// It fails rather than prompting if the question is still up, since herdr refuses a
	// prompt to a blocked agent and a step recorded as done would never be tried again.
	Interrupt func(kind, lead, data string) error

	// Reaches the channel Tim watches. The only thing that ever sees the webhook.
	Send func(message string) error
}

const logTime = "2006-01-02T15:04:05-0700"

// One line per thing that happened and nothing at all on a quiet check: this runs
// every five minutes forever, into a log somebody reads only when something is wrong.
type logger struct {
	out io.Writer
	now func() time.Time
}

func (l logger) say(format string, args ...any) {
	fmt.Fprintf(l.out, "%s hachiko: %s\n", l.now().Format(logTime), fmt.Sprintf(format, args...))
}

func realDeps(cfg Config) Deps {
	now := clockFromEnv()
	log := logger{out: os.Stdout, now: now}

	// The volume free space is being counted on, so a path that has become a link to
	// another one is not truncated in its name.
	device, _ := deviceOf(cfg.Home)

	return Deps{
		Now:    now,
		Log:    os.Stdout,
		Getpid: os.Getpid,

		FreeKB: func() (int64, error) { return freeKB(cfg.Home) },
		BigFiles: func(skip []string) WalkResult {
			return Walk{
				Roots:  cfg.Roots(),
				Pruned: append(cfg.PrunedPaths(), skip...),
				MinKB:  cfg.BigKB,
				// Wall clock, not the sweep's: these deadlines are about how long an open
				// has really been waiting, which a simulated clock would never reach.
				Now:         time.Now,
				DirTimeout:  cfg.DirTimeout,
				WalkTimeout: cfg.WalkTimeout,
			}.Run()
		},
		Writers:  writers,
		Truncate: func(path string) error { return truncateLog(path, device) },

		Processes: sampleProcesses,
		CWD:       processCWD,

		Oncall: func(name, brief string) (OncallSession, error) {
			return oncaller{cfg: cfg, run: herdrCLI, now: now, log: log}.open(name, brief)
		},
		AgentStatus: func(kind string) (string, error) {
			return oncaller{cfg: cfg, run: herdrCLI, now: now, log: log}.status(kind)
		},
		Interrupt: func(kind, lead, data string) error {
			return oncaller{cfg: cfg, run: herdrCLI, now: now, log: log}.interrupt(kind, lead, data)
		},
		Send: func(message string) error { return sendThroughOP(cfg, message) },
	}
}

// Every subprocess here is one a stale mount or a dead socket could stop for good,
// and a sweep that never returns is one holding the lock that keeps the next twelve
// from running. ps and lsof get seconds; op and herdr get longer, since one resolves
// a reference over the network and the other starts a session.
const (
	sampleTimeout = 15 * time.Second
	lsofTimeout   = 10 * time.Second
	herdrTimeout  = 30 * time.Second
	opTimeout     = 90 * time.Second
)

func output(limit time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()

	return exec.CommandContext(ctx, name, args...).Output()
}

// What df reads, without a df: statfs answers for the volume a path is on, in the
// blocks available to somebody who is not root, which is the number that decides
// whether a build has room.
func freeKB(path string) (int64, error) {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return 0, fmt.Errorf("statfs %s: %w", path, err)
	}
	return int64(fs.Bavail) * int64(fs.Bsize) / 1024, nil
}

func deviceOf(path string) (int32, bool) {
	info, err := os.Lstat(path)
	if err != nil {
		return -1, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return -1, false
	}
	return st.Dev, true
}

// Never an rm and never a kill: the writer keeps its descriptor and its offset, so a
// log it appends to goes on working and the space comes back at once.
//
// O_NOFOLLOW and then a second look at what was actually opened, because minutes pass
// between the walk that chose this path and the decision to empty it, and the one
// thing that may not happen is emptying a file somebody swapped a link in for.
func truncateLog(path string, device int32) error {
	file, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is no longer a regular file", path)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || (device >= 0 && st.Dev != device) {
		return fmt.Errorf("%s is no longer on the volume this is counting space on", path)
	}

	return file.Truncate(0)
}

// pid and command of whoever holds the file open. Named rather than acted on: the
// point of an alert is that a person decides what to do about the writer.
func writers(path string) string {
	out, err := output(lsofTimeout, "lsof", "-Fpc", "--", path)
	if err != nil && len(out) == 0 {
		return "no writer lsof can see"
	}

	var found []string
	pid := ""
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "p"):
			pid = line[1:]
		case strings.HasPrefix(line, "c") && pid != "":
			found = append(found, fmt.Sprintf("%s (%s)", pid, line[1:]))
			pid = ""
		}
	}

	if len(found) == 0 {
		return "no writer lsof can see"
	}
	return strings.Join(found, ", ")
}

// lsof for the hot pids alone: asking it about every process on the machine costs
// more than the sweep does.
func processCWD(pid int) string {
	out, err := output(lsofTimeout, "lsof", "-a", "-p", fmt.Sprint(pid), "-d", "cwd", "-Fn")
	if err != nil && len(out) == 0 {
		return ""
	}

	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") {
			return line[1:]
		}
	}
	return ""
}
