package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/logs"
	"github.com/timche/mac-mini/hachiko/internal/oncall"
	"github.com/timche/mac-mini/hachiko/internal/process"
	"github.com/timche/mac-mini/hachiko/internal/shibuya"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// Deps is everything a sweep learns about the machine or does to it. Each one is a
// function so that a test drives the whole of the decision-making without a stub
// binary on PATH, a disk to fill or an hour to wait.
type Deps struct {
	Now    func() time.Time
	Log    io.Writer
	Getpid func() int

	// The account hachiko is running as, which is Tim's. A hot process with another uid is
	// one he alone can stop, since sudo is on the on-call session's never list.
	Getuid func() int

	FreeKB func() (int64, error)
	// skip is what earlier sweeps found would not answer, on top of the folders this
	// never opens at all.
	BigFiles  func(skip []string) WalkResult
	Writers   func(path string) string
	Truncate  func(path string) error
	Processes func() ([]process.Process, error)
	CWD       func(pid int) string

	// Opens the on-call session and answers with where it is and whether the brief
	// reached it, which is what the message about to go out has to say.
	Oncall func(name, brief string) (oncall.Session, error)

	// What herdr says the on-call agent of a kind is doing — blocked, working, idle,
	// done, or gone when there is no such agent any more. Blocked is the whole of how
	// hachiko knows Tim has not answered yet.
	AgentStatus func(kind string) (string, error)

	// Cancels the question the agent is waiting on and hands it a prompt in its place.
	// It fails rather than prompting if the question is still up, since herdr refuses a
	// prompt to a blocked agent and a step recorded as done would never be tried again.
	//
	// The first value says whether the esc went, which is the difference between a question
	// still in front of Tim and one hachiko took away and then failed to replace.
	Interrupt func(kind, lead, data string) (bool, error)

	// The prompt on its own, for an agent with no question in the way: what hachiko owes
	// one whose question its own esc already took away, where a second esc would cancel
	// whatever the session has asked or started since.
	Prompt func(kind, lead, data string) error

	// Reaches the channel Tim watches. The only thing that ever sees the webhook or the bot
	// token, and it answers with the thread it opened when the message was the first of an
	// incident and the bot is configured.
	Send func(out discord.Outgoing) (string, error)

	// Tells shibuya this sweep happened, which is the half of the watch that is not on this
	// Mac. It answers with the state to remember and the one line to log when that state is
	// new, since a Mac with no token configured has nothing to say every five minutes.
	CheckIn func(in shibuya.Checkin) (string, string)
}

func realDeps(cfg config.Config) Deps {
	now := config.ClockFromEnv()
	log := logs.Logger{Out: os.Stdout, Now: now}

	// The volume free space is being counted on, so a path that has become a link to
	// another one is not truncated in its name.
	device, _ := deviceOf(cfg.Home)

	return Deps{
		Now:    now,
		Log:    os.Stdout,
		Getpid: os.Getpid,
		Getuid: os.Getuid,

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

		Processes: process.Sample,
		CWD:       process.CWD,

		Oncall: func(name, brief string) (oncall.Session, error) {
			return oncall.Oncaller{Cfg: cfg, Herdr: oncall.HerdrCLI, Now: now, Log: log}.Open(name, brief)
		},
		AgentStatus: func(kind string) (string, error) {
			return oncall.Oncaller{Cfg: cfg, Herdr: oncall.HerdrCLI, Now: now, Log: log}.Status(kind)
		},
		Interrupt: func(kind, lead, data string) (bool, error) {
			return oncall.Oncaller{Cfg: cfg, Herdr: oncall.HerdrCLI, Now: now, Log: log}.Interrupt(kind, lead, data)
		},
		Prompt: func(kind, lead, data string) error {
			return oncall.Oncaller{Cfg: cfg, Herdr: oncall.HerdrCLI, Now: now, Log: log}.PromptWith(kind, lead, "INCIDENT DATA", data)
		},
		Send:    func(out discord.Outgoing) (string, error) { return discord.SendThroughOP(cfg, out) },
		CheckIn: shibuya.New(cfg).Send,
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
	out, err := process.Run(process.LsofTimeout, "lsof", "-Fpc", "--", path)
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
			// Name first and the pid in parentheses, the same way every other process in a
			// message reads. One stable string, because it is compared across checks to spot
			// a writer that was not there when the question went up.
			found = append(found, wording.PIDLabel(line[1:], pid))
			pid = ""
		}
	}

	if len(found) == 0 {
		return "no writer lsof can see"
	}
	return strings.Join(found, ", ")
}
