package gc

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// A week of grace on top of the session being over, because `claude --resume` reuses the
// folder, and herdr resumes every session it had after a restart.
const scratchGrace = 7 * 24 * time.Hour

// Session scratch folders, one per session under a folder per project. Only a directory
// named like a session id is ever removed: `bash-edit-diff`, `tasks` and whatever else
// Claude Code keeps beside them belong to no session, and the folders a level up —
// cc-socks, tart — belong to no project.
//
// The project folder goes only when the last session in it did, and with rmdir, so anything
// left makes it stay.
func (s *sweeper) scratch(now time.Time) {
	root := filepath.Join(s.cfg.ScratchRoot, fmt.Sprintf("claude-%d", s.deps.Getuid()))
	if !s.deps.IsDir(root) {
		return
	}

	slugs, err := s.deps.ReadDir(root)
	if err != nil {
		s.say("%s could not be read, so no scratch folder was swept: %v", safe(root), err)
		return
	}

	live := s.liveSessions()
	since := now.Add(-scratchGrace)

	for _, slug := range slugs {
		dir := filepath.Join(root, slug)
		if !s.deps.IsDir(dir) {
			continue
		}

		names, err := s.deps.ReadDir(dir)
		if err != nil {
			s.say("%s could not be read, so the sessions in it were left alone: %v", safe(dir), err)
			continue
		}

		kept := 0
		for _, name := range names {
			session := filepath.Join(dir, name)

			// A .DS_Store Finder left behind keeps the folder too, which is what rmdir would do
			// with it anyway.
			switch {
			case strings.HasPrefix(name, "."),
				!s.deps.IsDir(session),
				!isSessionID(name),
				live[name],
				s.deps.TouchedSince(session, since):
				kept++
				continue
			}

			if s.dry {
				s.say("would remove scratch folder %s, whose session is over", safe(session))
				continue
			}

			s.say("removing scratch folder %s, whose session is over", safe(session))
			if err := s.deps.Remove(session); err != nil {
				kept++
				s.failed(Failure{Kind: failedRemove, Subject: session, Detail: err.Error()})
			}
		}

		if kept > 0 {
			continue
		}

		if s.dry {
			s.say("would remove the empty project folder %s", safe(dir))
			continue
		}

		// A failed rmdir is not a failure worth a message: the only thing that makes one fail
		// here is something still in the folder, which is the folder staying and is what
		// everything above it is for.
		if s.deps.Rmdir(dir) == nil {
			s.say("removed the empty project folder %s", safe(dir))
		}
	}
}

// The session ids of every Claude Code process still running. The record outlives the
// process it describes, so the pid in the filename is what decides whether a session is
// live, and the id inside is only what it claims.
//
// A record that is not valid JSON claims no session, which is safe on its own because the
// folder of a session live enough to be half-written was written to this week.
func (s *sweeper) liveSessions() map[string]bool {
	dir := filepath.Join(s.cfg.Home, ".claude", "sessions")

	names, err := s.deps.ReadDir(dir)
	if err != nil {
		return nil
	}

	live := map[string]bool{}
	for _, name := range names {
		pid, ok := strings.CutSuffix(name, ".json")
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(pid); err != nil || n <= 0 || s.deps.Signal(n, 0) != nil {
			continue
		}

		body, err := s.deps.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}

		var record struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(body, &record) == nil && record.SessionID != "" {
			live[record.SessionID] = true
		}
	}
	return live
}

// A UUID as Claude Code writes one, which is the one shape of name this sweep will remove.
func isSessionID(name string) bool {
	groups := strings.Split(name, "-")
	if len(groups) != 5 {
		return false
	}

	for i, want := range []int{8, 4, 4, 4, 12} {
		if len(groups[i]) != want {
			return false
		}
		for _, r := range groups[i] {
			hex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
			if !hex {
				return false
			}
		}
	}
	return true
}
