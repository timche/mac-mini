package gc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

func remove(t *testing.T, path string) {
	t.Helper()
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
}

func TestAProcessLeftInARemovedWorktreeIsStopped(t *testing.T) {
	f := newFixture(t)
	gone := f.herdrWorktree("app", "gone", false)

	f.sitting(4242, accountUID, gone, "/opt/homebrew/bin/node server.js")

	out := f.sweep()
	harness.Wants(t, out, "killing 4242 (/opt/homebrew/bin/node), left in the removed "+gone)
	harness.Equal(t, strings.Join(f.signals, ","), "4242:15", "the signals sent")
}

// Both halves matter: a cwd that is still there belongs to a session that is still working,
// and a removed subdirectory of a worktree that survived is the project's business.
func TestAProcessIsLeftAloneWhileEitherFolderIsThere(t *testing.T) {
	f := newFixture(t)

	live := f.herdrWorktree("app", "live", true)
	kept := f.herdrWorktree("app", "kept", true)

	f.sitting(100, accountUID, live, "node")
	f.sitting(101, accountUID, filepath.Join(kept, "build", "gone"), "node")

	harness.Equal(t, f.sweep(), "", "the log of a sweep with nothing to do")
	harness.Equal(t, len(f.signals), 0, "the signals sent")
}

// A sweep may not kill the shell, the agent or the launchd that started it.
func TestASweepNeverSignalsItsOwnTreeOrPidOne(t *testing.T) {
	f := newFixture(t)
	gone := f.herdrWorktree("app", "gone", false)

	f.pid = 9000
	f.sitting(9000, accountUID, gone, "hachiko")
	f.sitting(8000, accountUID, gone, "zsh")
	f.sitting(1, accountUID, gone, "launchd")
	f.procs[0].PPID = 8000
	f.procs[1].PPID = 1

	harness.Equal(t, f.sweep(), "", "the log of a sweep with nothing to do")
	harness.Equal(t, len(f.signals), 0, "the signals sent")
}

// sudo is not something a sweep has, so a daemon that happens to be sitting in a removed
// folder is nobody's to kill from here.
func TestAProcessOfAnotherAccountIsNamedAndLeftAlone(t *testing.T) {
	f := newFixture(t)
	gone := f.herdrWorktree("app", "gone", false)

	f.sitting(300, systemUID, gone, "/usr/libexec/somedaemon")

	out := f.sweep()
	harness.Wants(t, out, "left alone: 300 (/usr/libexec/somedaemon) is in the removed "+gone+
		" but is owned by root, which this account cannot signal")
	harness.Equal(t, len(f.signals), 0, "the signals sent")
}

// A removed worktree somewhere else is exactly the thing a sweep is for, so it is said out
// loud even though nothing here will touch it.
func TestAWorktreeOutsideBothRootsIsSaidAndNotTouched(t *testing.T) {
	f := newFixture(t)

	f.sitting(500, accountUID, filepath.Join(f.home, "elsewhere", "worktrees", "gone"), "node")
	f.sitting(501, accountUID, filepath.Join(f.home, "elsewhere", "build"), "node")

	out := f.sweep()
	harness.Wants(t, out, "left alone: 500 sits in ")
	harness.Wants(t, out, ", outside the worktree roots")
	harness.Lacks(t, out, "501")
	harness.Equal(t, len(f.signals), 0, "the signals sent")
}

// A dev server closing sockets and a database flushing both want a moment; what is still
// there after it was not going to leave.
func TestAProcessThatIgnoresSIGTERMIsKilled(t *testing.T) {
	f := newFixture(t)
	gone := f.herdrWorktree("app", "gone", false)

	f.sitting(4242, accountUID, gone, "node")
	f.stubborn[4242] = true

	out := f.sweep()
	harness.Wants(t, out, "4242 ignored SIGTERM, sending SIGKILL")
	harness.Equal(t, strings.Join(f.signals, ","), "4242:15,4242:9", "the signals sent")
	harness.Equal(t, len(f.sent), 0, "messages sent")
}

func TestAProcessThatSurvivesSIGKILLIsOneMessage(t *testing.T) {
	f := newFixture(t)
	gone := f.herdrWorktree("app", "gone", false)

	f.sitting(4242, accountUID, gone, "/opt/homebrew/bin/node server.js")
	f.stubborn[4242] = true
	f.immortal[4242] = true

	out := f.sweep()
	harness.Wants(t, out, "4242 is still there after SIGKILL")

	harness.Wants(t, f.lastSent(), "⚠️ A process in a removed worktree survived SIGKILL")
	harness.Wants(t, f.lastSent(), "**Process:** node (pid 4242)")
	harness.Wants(t, f.lastSent(), "**Needs you:** a process that survives SIGKILL")
}

func TestADryRunSignalsNothing(t *testing.T) {
	f := newFixture(t)
	gone := f.herdrWorktree("app", "gone", false)

	f.sitting(4242, accountUID, gone, "node")

	out := f.dryRun()
	harness.Wants(t, out, "would kill 4242 (node), left in the removed "+gone)
	harness.Equal(t, len(f.signals), 0, "the signals sent")
}

// A pid lsof saw and ps did not is one that started or ended between the two reads, and
// nothing is signalled on a guess about who owns it.
func TestAPidTheProcessSampleNeverSawIsLeftAlone(t *testing.T) {
	f := newFixture(t)
	gone := f.herdrWorktree("app", "gone", false)

	f.cwds = append(f.cwds, CWD{PID: 7777, Dir: gone})

	harness.Equal(t, f.sweep(), "", "the log of a sweep with nothing to do")
	harness.Equal(t, len(f.signals), 0, "the signals sent")
}

// A check with no process sample saw no processes at all, which is not the same as having
// seen none worth stopping.
func TestNoProcessSampleStopsNothing(t *testing.T) {
	f := newFixture(t)
	gone := f.herdrWorktree("app", "gone", false)

	f.sitting(4242, accountUID, gone, "node")
	f.procsErr = errOf("ps: signal: killed")

	out := f.sweep()
	harness.Wants(t, out, "there is no process sample")
	harness.Equal(t, len(f.signals), 0, "the signals sent")
}
