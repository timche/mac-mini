package watch

import (
	"fmt"
	"strings"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

const (
	labelWaiting = "Waiting"
	labelOldest  = "Oldest unpushed commit"
)

// SyncRepo is one repository `hachiko sync` is meant to be keeping upstream, as the watch
// reads it out of sync's own config: the same file, so the list the watch checks cannot be a
// different list from the one sync syncs.
type SyncRepo struct {
	Path      string
	PushDelay time.Duration
}

// What git says about one of them. Read says whether git answered at all — a repository
// nothing could be read from is one the watch says nothing about, since a missing clone and
// a sync that has fallen behind are not the same thing.
type SyncRepoState struct {
	Read   bool
	Dirty  bool
	Oldest time.Time
}

// The two ways a repository falls behind with sync alive and reporting nothing: a commit that
// is well past the delay it was meant to go up after, and a tree that has stayed dirty long
// enough that the debounce cannot be what is holding it.
const (
	behindLate  = "late"
	behindDirty = "dirty"
)

// The half of sync the liveness check cannot see: a repository it is syncing happily and
// getting nowhere with. Run only with sync alive, by the caller — a sync that is down is
// already one message, and saying that every repository it was syncing has fallen behind is
// the same news four more times.
//
// Not an incident and no on-call session, for the same reason the liveness check is neither:
// the answer is already written in the message, and it is Tim's.
func (s sweeper) syncRepos(state *statedir.State, now time.Time) {
	going := map[string]time.Duration{}

	for _, repo := range s.deps.SyncRepos() {
		read := s.deps.SyncRepoState(repo.Path)
		if !read.Read {
			continue
		}

		// Generous on purpose: a push that is a few minutes late is a retry ladder working,
		// and what this is for is one that has silently stopped.
		if !read.Oldest.IsZero() {
			if waited := now.Sub(read.Oldest); waited > repo.PushDelay+s.cfg.SyncLateAfter {
				going[behindLate+" "+repo.Path] = waited
			}
		}
		if read.Dirty {
			going[behindDirty+" "+repo.Path] = 0
		}
	}

	for _, key := range statedir.SortedKeys(going) {
		s.sayBehind(state, key, going[key], now)
	}
	for _, key := range statedir.SortedKeys(state.SyncBehind) {
		if _, still := going[key]; !still {
			s.sayCaughtUp(state, key, now)
		}
	}
}

func (s sweeper) sayBehind(state *statedir.State, key string, waited time.Duration, now time.Time) {
	was, known := state.SyncBehind[key]

	rec := statedir.Behind{Since: now.Unix()}
	if known {
		rec = was
	}

	kind, path := splitKey(key)

	// A dirty tree is only news once the debounce cannot be what is holding it, and the
	// debounce is seconds: what is measured is how long the watch itself has seen it dirty,
	// since nothing else on this Mac records when a file was written.
	if kind == behindDirty && now.Sub(time.Unix(rec.Since, 0)) <= s.cfg.SyncDirtyAfter {
		s.remember(state, key, rec)
		return
	}
	if rec.Said {
		s.remember(state, key, rec)
		return
	}

	span := now.Sub(time.Unix(rec.Since, 0))
	if kind == behindLate {
		span = waited
	}

	if s.dry {
		s.say("would report that %s %s", path, behindPhrase(kind, span))
		s.remember(state, key, rec)
		return
	}
	s.say("%s %s, and sync is running", path, behindPhrase(kind, span))

	out := discord.Outgoing{Text: s.behindMessage(kind, path, span), Thread: rec.Thread}
	if rec.Thread == "" {
		out.OpenThread = key
	}

	thread, err := s.deps.Send(out)
	if err != nil {
		s.say("the message about %s did not send and is left to the next check: %v", path, err)
	} else {
		rec.Said = true
		if thread != "" {
			rec.Thread = thread
		}
	}
	s.remember(state, key, rec)
}

func (s sweeper) sayCaughtUp(state *statedir.State, key string, now time.Time) {
	rec := state.SyncBehind[key]
	kind, path := splitKey(key)

	// Nobody was ever told, so there is nothing to tell them is over.
	if !rec.Said {
		delete(state.SyncBehind, key)
		return
	}
	if s.dry {
		s.say("would report that %s has caught up", path)
		return
	}

	s.say("%s has caught up", path)

	message := wording.Lead(wording.MarkerRecovered, caughtUpSentence(kind)).
		Field(labelRepoPath, wording.CodeSpan(wording.Safe(path, wording.PathLimit))).
		Field(labelWaiting, wording.DurationPhrase(now.Sub(time.Unix(rec.Since, 0)))).
		About("", s.cfg.Host).
		String()

	if _, err := s.deps.Send(discord.Outgoing{Text: message, Thread: rec.Thread}); err != nil {
		s.say("the message saying %s has caught up did not send and is left to the next check: %v", path, err)
		return
	}
	delete(state.SyncBehind, key)
}

const labelRepoPath = "Repository"

func (s sweeper) behindMessage(kind, path string, span time.Duration) string {
	m := wording.Lead(wording.MarkerDegraded, behindSentence(kind)).
		Field(labelRepoPath, wording.CodeSpan(wording.Safe(path, wording.PathLimit)))

	if kind == behindLate {
		m.Field(labelOldest, wording.DurationPhrase(span)+" old").
			Field(wording.LabelWhy, "Sync is running and says nothing is wrong, so this is not "+
				"a push that failed: the commit is well past the delay it was meant to go up "+
				"after and has not gone.")
	} else {
		m.Field(labelWaiting, wording.DurationPhrase(span)).
			Field(wording.LabelWhy, "Sync is running and says nothing is wrong, so this is not "+
				"a commit that failed: the tree has been dirty far longer than the debounce, "+
				"and nothing written there is even committed, let alone upstream.")
	}

	return m.Can("**You can run:** "+
		wording.CodeSpan("tail ~/Library/Logs/hachiko-sync.log")+
		" — it says what the last pass did, and "+
		wording.CodeSpan(fmt.Sprintf("launchctl kickstart -k gui/%d/%s", s.deps.Getuid(), s.cfg.SyncLabel()))+
		" starts a fresh one.").
		About("", s.cfg.Host).
		String()
}

func behindSentence(kind string) string {
	if kind == behindLate {
		return "A repository's commits are well past their push delay"
	}
	return "A repository has been uncommitted for far longer than its debounce"
}

func caughtUpSentence(kind string) string {
	if kind == behindLate {
		return "The repository whose commits were late has pushed them"
	}
	return "The repository that would not commit has committed"
}

func behindPhrase(kind string, span time.Duration) string {
	if kind == behindLate {
		return fmt.Sprintf("has an unpushed commit %s old", hmStr(span))
	}
	return fmt.Sprintf("has been uncommitted for %s", hmStr(span))
}

func (s sweeper) remember(state *statedir.State, key string, rec statedir.Behind) {
	if state.SyncBehind == nil {
		state.SyncBehind = map[string]statedir.Behind{}
	}
	state.SyncBehind[key] = rec
}

// A `<kind> <path>` key back into its halves. At the first space, because a kind holds none
// and a path readily does.
func splitKey(key string) (string, string) {
	kind, path, _ := strings.Cut(key, " ")
	return kind, path
}
