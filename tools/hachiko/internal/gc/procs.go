package gc

import (
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/process"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// A dev server closing sockets and a database flushing both want a moment; what is still
// there after it was not going to leave.
const termGrace = 10

// What a process gets to disappear in after SIGKILL before the sweep says it did not. The
// kernel does not refuse a SIGKILL, so anything still answering is in an uninterruptible
// wait — and the one false reading would be a zombie, which the launchd these are
// reparented to reaps in microseconds.
const killGrace = 2 * time.Second

// A process whose cwd no longer exists, inside a worktree that no longer exists. Both
// halves matter: a cwd that is still there belongs to a session that is still working, and
// a removed subdirectory of a worktree that survived is the project's business rather than
// this sweep's.
func (s *sweeper) processes() {
	procs, err := s.deps.Processes()
	if err != nil {
		s.say("there is no process sample, so nothing left in a removed worktree was touched: %v", err)
		return
	}

	open, err := s.deps.CWDs()
	if err != nil {
		s.say("lsof would not say which directory anything is in, so nothing left in a removed worktree was touched: %v", err)
		return
	}

	mine := process.OwnTree(procs, s.deps.Getpid())
	uid := s.deps.Getuid()

	known := make(map[int]process.Process, len(procs))
	for _, p := range procs {
		known[p.PID] = p
	}

	seen := map[int]bool{}
	var victims []process.Process

	for _, c := range open {
		// Never pid 1 and never anything between this process and it, so a sweep cannot kill
		// the shell, the agent or launchd that started it.
		if c.PID <= 1 || mine[c.PID] || seen[c.PID] || c.Dir == "" {
			continue
		}
		if s.deps.IsDir(c.Dir) {
			continue
		}
		seen[c.PID] = true

		worktree, inRoot := worktreeOf(s.cfg.HerdrRoot, c.Dir)
		if !inRoot {
			// Outside both roots, so not this sweep's to kill. Said out loud anyway: a removed
			// worktree somewhere else is exactly the thing a sweep is for.
			if strings.Contains(c.Dir, "/worktrees/") {
				s.say("left alone: %d sits in %s, outside the worktree roots", c.PID, safe(c.Dir))
			}
			continue
		}
		if s.deps.IsDir(worktree) {
			continue
		}

		// A pid lsof saw and ps did not is one that started or ended between the two reads.
		// Nothing is signalled on a guess about who owns it, and the sweep ten minutes from
		// now sees whichever of the two it was.
		p, ok := known[c.PID]
		if !ok {
			continue
		}

		// Only this account's. sudo is not something a sweep has, so a process of another uid
		// is one it cannot signal anyway — and a daemon that happens to be sitting in a
		// removed folder is nobody's to kill from here.
		if p.UID != uid {
			s.say("left alone: %d (%s) is in the removed %s but is owned by %s, which this account cannot signal",
				p.PID, safe(p.Path()), safe(worktree), safe(p.Owner()))
			continue
		}

		if s.dry {
			s.say("would kill %d (%s), left in the removed %s", p.PID, safe(p.Path()), safe(worktree))
			continue
		}

		s.say("killing %d (%s), left in the removed %s", p.PID, safe(p.Path()), safe(worktree))
		victims = append(victims, p)
	}

	s.stop(victims)
}

func (s *sweeper) stop(victims []process.Process) {
	if len(victims) == 0 {
		return
	}

	for _, p := range victims {
		s.deps.Signal(p.PID, syscall.SIGTERM)
	}

	for waited := 0; waited < termGrace; waited++ {
		if len(s.stillThere(victims)) == 0 {
			return
		}
		s.deps.Sleep(time.Second)
	}

	stubborn := s.stillThere(victims)
	for _, p := range stubborn {
		s.say("%d ignored SIGTERM, sending SIGKILL", p.PID)
		s.deps.Signal(p.PID, syscall.SIGKILL)
	}
	if len(stubborn) == 0 {
		return
	}

	s.deps.Sleep(killGrace)
	for _, p := range s.stillThere(stubborn) {
		s.failed(Failure{
			Kind:    failedKill,
			Subject: strconv.Itoa(p.PID),
			Label:   wording.PIDLabel(wording.Safe(p.Name(), wording.NameLimit), strconv.Itoa(p.PID)),
			Detail:  safe(p.Command),
		})
	}
}

// Signal 0, which is the one way to ask whether a pid is still there without touching it.
func (s *sweeper) stillThere(of []process.Process) []process.Process {
	var alive []process.Process
	for _, p := range of {
		if s.deps.Signal(p.PID, 0) == nil {
			alive = append(alive, p)
		}
	}
	return alive
}
