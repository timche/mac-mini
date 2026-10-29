package watch

import (
	"time"

	"github.com/timche/mac-mini/hachiko/internal/statedir"
)

// How often the forum is gone through. Housekeeping rather than watching, and it costs an
// `op run` against the service account's day, so once an hour against the check's twelve.
const postSweepEvery = time.Hour

// The forum's own housekeeping: a post about an incident that was resolved days ago is
// nothing anybody is coming back to — what was decided about it is in the project docs and
// what was read is in a log — so it goes, and the forum reads as what has happened lately
// rather than everything that ever did. shibuya's posts go the same way, being in the same
// forum with the same tags.
//
// What may be deleted is decided by Discord rather than by this state file: the post is in
// the forum, it carries the Resolved tag, it is archived, and it was archived longer ago
// than HACHIKO_POST_KEEP. So a post somebody reopened by writing in it stays, and an open
// incident is never touched.
func (s sweeper) prunePosts(state *statedir.State, now time.Time) {
	if s.cfg.PostKeep <= 0 || s.cfg.Discord.ChannelID == "" {
		return
	}
	if now.Sub(time.Unix(state.PostsSwept, 0)) < postSweepEvery {
		return
	}

	// Written whether or not it worked, so a forum that cannot be read is tried again in an
	// hour rather than on every check for the life of the Mac.
	state.PostsSwept = now.Unix()

	deleted, err := s.deps.PrunePosts(s.cfg.PostKeep)
	for _, line := range deleted {
		s.say("%s", line)
	}
	if err != nil {
		s.say("the resolved posts in the forum could not be tidied up: %v", err)
	}
}
