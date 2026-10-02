package main

import (
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

	FreeKB    func() (int64, error)
	BigFiles  func() []FileSize
	Writers   func(path string) string
	Truncate  func(path string) error
	Processes func() ([]Process, error)
	CWD       func(pid int) string

	// Opens the on-call session and answers with the label of the tab it is waiting
	// in, which is what the message about to go out names.
	Oncall func(name, brief string) (string, error)

	// Reaches the channel Tim watches. The only thing that ever sees the webhook.
	Send func(message string) error
}

func realDeps(cfg Config) Deps {
	return Deps{
		Now:    clockFromEnv(),
		Log:    os.Stdout,
		Getpid: os.Getpid,

		FreeKB:   func() (int64, error) { return freeKB(cfg.Home) },
		BigFiles: func() []FileSize { return bigFiles(cfg.Roots(), cfg.PrunedPaths(), cfg.BigKB) },
		Writers:  writers,
		Truncate: func(path string) error { return os.Truncate(path, 0) },

		Processes: sampleProcesses,
		CWD:       processCWD,

		Oncall: func(name, brief string) (string, error) { return openOncall(cfg, herdrCLI, name, brief) },
		Send:   func(message string) error { return sendThroughOP(cfg, message) },
	}
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

// pid and command of whoever holds the file open. Named rather than acted on: the
// point of an alert is that a person decides what to do about the writer.
func writers(path string) string {
	out, err := exec.Command("lsof", "-Fpc", "--", path).Output()
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
	out, err := exec.Command("lsof", "-a", "-p", fmt.Sprint(pid), "-d", "cwd", "-Fn").Output()
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
