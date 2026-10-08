package watch

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/logs"
	"github.com/timche/mac-mini/hachiko/internal/oncall"
	"github.com/timche/mac-mini/hachiko/internal/process"
	"github.com/timche/mac-mini/hachiko/internal/shibuya"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
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

	// When `hachiko gc` last finished a sweep, and whether this Mac has the agent that runs
	// it at all. A Mac without one is not a Mac whose sweep has stopped.
	GCLastRun func() (time.Time, bool)

	// The same for `hachiko sync`, which writes a heartbeat rather than a last-run stamp
	// because it never finishes a run; then the repositories it is meant to be keeping
	// upstream, out of sync's own config so the two cannot be two lists, and what git says
	// about each one.
	SyncLastBeat  func() (time.Time, bool)
	SyncRepos     func() []SyncRepo
	SyncRepoState func(repo SyncRepo) SyncRepoState

	// And the same for `hachiko listen`, which writes a heartbeat only once it is configured:
	// answering from Discord is off unless Tim has configured it, and an unanswered reply on a
	// Mac where nobody asked for one is not a fault. A listener that is configured and cannot
	// run keeps the heartbeat it has, or writes one where it has never had any, that being a
	// listener that has stopped.
	ListenLastBeat func() (time.Time, bool)

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

		FreeKB: func() (int64, error) { return FreeKB(cfg.Home) },
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

		GCLastRun: func() (time.Time, bool) { return gcLastRun(cfg) },

		SyncLastBeat:  func() (time.Time, bool) { return syncLastBeat(cfg) },
		SyncRepos:     func() []SyncRepo { return syncRepos(cfg) },
		SyncRepoState: syncRepoState,

		ListenLastBeat: func() (time.Time, bool) { return listenLastBeat(cfg) },

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
func FreeKB(path string) (int64, error) {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return 0, fmt.Errorf("statfs %s: %w", path, err)
	}
	return int64(fs.Bavail) * int64(fs.Bsize) / 1024, nil
}

// The nearest thing the watch has to a stamp of its own: the state file it writes at the end
// of every check. Nothing here reads it — a watch cannot tell whether it is running, which is
// shibuya's half of this — and `hachiko status` is what it is for, so there is no falling back
// to the plist's modification time: a time that is not a check's would read as one.
func watchLastRun(cfg config.Config) (time.Time, bool) {
	if _, err := os.Stat(cfg.WatchPlist); err != nil {
		return time.Time{}, false
	}
	state, err := os.Stat(statedir.Store{Dir: cfg.StateDir}.Path())
	if err != nil {
		return time.Time{}, false
	}
	return state.ModTime(), true
}

// The stamp's modification time, or the plist's where there is no stamp yet. The plist is
// also what says the sweep is meant to be running at all: there is no agent on a Mac
// install.sh has not reached, and nothing to report about a sweep nothing runs.
func gcLastRun(cfg config.Config) (time.Time, bool) {
	plist, err := os.Stat(cfg.GCPlist)
	if err != nil {
		return time.Time{}, false
	}
	if stamp, err := os.Stat(cfg.GCStamp()); err == nil {
		return stamp.ModTime(), true
	}
	return plist.ModTime(), true
}

// The heartbeat's modification time, or the plist's where there is no heartbeat yet. The
// plist is also what says sync is meant to be running at all: there is no agent on a Mac
// install.sh has not reached, and nothing to report about a sync nothing runs.
func syncLastBeat(cfg config.Config) (time.Time, bool) {
	plist, err := os.Stat(cfg.SyncPlist)
	if err != nil {
		return time.Time{}, false
	}
	if beat, err := os.Stat(cfg.SyncStamp()); err == nil {
		return beat.ModTime(), true
	}
	return plist.ModTime(), true
}

// The listener's heartbeat, and no falling back to the plist's modification time the way
// the two above do. A listener with no heartbeat is one nobody has configured, which is what
// answering from Discord being off looks like for the life of the Mac: the feature ships off,
// so counting from the plist would alert on every Mac that has never turned it on. The
// listener removes its own heartbeat when nothing is configured, so turning the feature off
// is read as off rather than as a listener that stopped ten minutes ago — and writes or keeps
// one as soon as a channel and a user are set, since from then on a listener that is not
// polling is one which has stopped.
//
// The plist still has to be there, because the message names the one command that starts the
// agent again and there is no such agent without it.
func listenLastBeat(cfg config.Config) (time.Time, bool) {
	if _, err := os.Stat(cfg.ListenPlist); err != nil {
		return time.Time{}, false
	}
	beat, err := os.Stat(cfg.ListenStamp())
	if err != nil {
		return time.Time{}, false
	}
	return beat.ModTime(), true
}

// Sync's own config, and a config that will not parse is no list at all: the daemon refuses
// to start on one, so there is nothing for the watch to check and the daemon's own absence is
// what gets reported.
func syncRepos(cfg config.Config) []SyncRepo {
	loaded, err := config.LoadSync(cfg.SyncConfig, cfg.Home)
	if err != nil {
		return nil
	}

	repos := make([]SyncRepo, 0, len(loaded.Repos))
	for _, repo := range loaded.Repos {
		repos = append(repos, SyncRepo{Path: repo.Path, PushDelay: repo.PushDelay, Paths: repo.Paths})
	}
	return repos
}

// Two git commands in a repository the watch does not otherwise touch, both read-only and
// both bounded: a git that hangs on a mount that has gone away may not cost the Mac its
// monitor. Nothing is read from a remote, so neither of them goes near the network.
//
// `--no-optional-locks` because read-only is not lock-free: a plain `git status` refreshes the
// index and takes `index.lock` to do it, and these are the repositories `hachiko sync` is
// committing in while this runs. The check that asks whether sync is falling behind may not be
// what makes it fail.
//
// Both commands are narrowed to what sync is answerable for, by the same rules sync itself
// works to: the pathspec for the tree, and the trailer for the commits. A limited
// repository's other files are a session's, and neither a change left uncommitted in one nor
// a commit of a session's left unpushed is sync falling behind.
func syncRepoState(repo SyncRepo) SyncRepoState {
	limit := config.Pathspec(repo.Paths)
	git := func(args ...string) ([]byte, error) {
		args = append([]string{"--no-optional-locks", "-C", repo.Path}, args...)
		return process.Run(gitTimeout, "git", append(args, limit...)...)
	}

	status, err := git("status", "--porcelain=v1")
	if err != nil {
		return SyncRepoState{}
	}

	state := SyncRepoState{Read: true, Dirty: len(strings.TrimSpace(string(status))) > 0}

	// A branch with no upstream has nothing to be late against, and git says so by failing.
	// In a limited repository the commits counted are sync's own, by the trailer it marks
	// them with: a commit of a session's is not one sync would push on its own, so one left
	// sitting there is not sync running late.
	stamps, err := git(append([]string{"log", "--format=%ct", "@{upstream}..HEAD"},
		config.OwnCommits(repo.Paths)...)...)
	if err != nil {
		return state
	}

	lines := strings.Fields(string(stamps))
	if len(lines) == 0 {
		return state
	}
	if seconds, err := strconv.ParseInt(lines[len(lines)-1], 10, 64); err == nil {
		state.Oldest = time.Unix(seconds, 0)
	}
	return state
}

const gitTimeout = 10 * time.Second

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
