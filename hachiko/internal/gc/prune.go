package gc

import (
	"strings"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// The two halves of docker that grow whether or not a worktree was ever removed, which is
// what makes this the one pass here that is about nothing having been left behind: a week of
// builds is layers nothing refers to any more and buildkit cache nothing will reuse, and
// neither of them goes when a worktree does. Everything else is still somebody's — never a
// volume, which is a database, never a container, never a network, and never `-a`, which
// would take the base images a project still names and the next build would only pull again.
const (
	subjectCache  = "build cache"
	subjectImages = "dangling images"
)

// Once a day. The first prune of a day reclaims everything the ones after it would have, so
// six an hour would be a hundred and forty subprocesses to be told 0B a hundred and thirty
// nine times — and a prune is the longest-running thing this sweep does.
func (s *sweeper) prune(state *State, now time.Time) {
	last := time.Unix(state.Pruned, 0)
	next := last.Add(s.cfg.GCPruneEvery)
	due := state.Pruned == 0 || !now.Before(next)

	if !s.docker() || !due {
		// Neither of those is a failure and neither is worth a line: a Mac with OrbStack
		// stopped has no cache to prune, and the nine sweeps an hour that are not the daily one
		// have nothing to do here. What they may not do is read as a prune that has started
		// working again, since that is what the failure model reads a sweep not failing as.
		s.keepOpenPrunes(state)

		if s.dry && due {
			s.say("docker is not up, so nothing would be pruned")
		}
		if s.dry && !due {
			s.say("docker was pruned %s ago, so nothing would be pruned for another %s",
				wording.DurationPhrase(now.Sub(last)), wording.DurationPhrase(next.Sub(now)))
		}
		return
	}

	if s.dry {
		s.say("would run %s", pruneCommand(subjectCache, s.cfg.GCPruneAge))
		s.say("would run %s", pruneCommand(subjectImages, s.cfg.GCPruneAge))
		return
	}

	// Written down before either command runs, so a prune that is killed halfway or that takes
	// its timeout twice is still a prune that was tried today. A docker that fails every one of
	// them would otherwise be two subprocesses every ten minutes for as long as it keeps
	// failing, which is the thing the day between them exists to stop.
	state.Pruned = now.Unix()

	cache, cacheErr := s.deps.PruneBuilder(s.cfg.GCPruneAge)
	if cacheErr != nil {
		s.pruneFailed(subjectCache, lastLine(cache, cacheErr))
	}

	images, imagesErr := s.deps.PruneImages(s.cfg.GCPruneAge)
	if imagesErr != nil {
		s.pruneFailed(subjectImages, lastLine(images, imagesErr))
	}

	// One line, the space each of them got back, and nothing on the hundreds of layers it
	// walked to decide: what a log is read for here is whether the disk went anywhere.
	//
	// And nothing at all where neither of them got anything back, which is what all but the
	// first prune of a week finds. A prune that reclaimed nothing did nothing, and this log is
	// one line per thing done.
	cacheSize, imagesSize := reclaimed(cache, cacheErr), reclaimed(images, imagesErr)
	if cacheSize == nothing && imagesSize == nothing {
		return
	}

	s.say("pruned what docker had not touched for %s: build cache %s, dangling images %s",
		safe(s.cfg.GCPruneAge), cacheSize, imagesSize)
}

func (s *sweeper) pruneFailed(subject, detail string) {
	s.failed(Failure{
		Kind:    failedDockerPrune,
		Subject: subject,
		Label:   pruneCommand(subject, s.cfg.GCPruneAge),
		Detail:  detail,
	})
}

// A prune failure that is still open on a sweep which did not prune. The failure model says
// one thing has stopped failing when a sweep did not fail at it, and that is right for the
// four passes that run every ten minutes — but the next prune is a day away, so the same
// reading here would be a 🟢 about a command nothing has run since it failed.
//
// Silent and recorded as already said, so nothing is sent a second time: what is carried is
// the failure, not the message.
func (s *sweeper) keepOpenPrunes(state *State) {
	for _, key := range sortedKeys(state.Posted) {
		rec := state.Posted[key]
		if rec.Kind != failedDockerPrune {
			continue
		}
		s.failures = append(s.failures, Failure{
			Kind:    rec.Kind,
			Subject: rec.Subject,
			Label:   pruneCommand(rec.Subject, s.cfg.GCPruneAge),
		})
	}
}

// The command as Tim would run it, built out of the subject and the age and nothing that
// arrived from the daemon: a line he is told to paste is a line nothing but this file wrote.
func pruneCommand(subject, until string) string {
	what := "image"
	if subject == subjectCache {
		what = "builder"
	}
	return "docker " + what + " prune --force --filter until=" + until
}

// How docker spells having reclaimed nothing, which is the common case and the one a quiet
// sweep says nothing about.
const nothing = "0B"

// What docker says it got back, which it spells two ways: `docker image prune` prints "Total
// reclaimed space:" and buildkit's own prune prints "Total:" and a tab. Both are read, because
// which of them a version prints is not something to depend on — and a prune that printed
// neither is said to have said nothing rather than guessed at.
func reclaimed(out []byte, err error) string {
	if err != nil {
		return "it would not prune"
	}

	for _, line := range strings.Split(string(out), "\n") {
		for _, label := range []string{"Total reclaimed space:", "Total:"} {
			rest, found := strings.CutPrefix(strings.TrimSpace(line), label)
			if size := strings.TrimSpace(rest); found && size != "" {
				return safe(size)
			}
		}
	}
	return "an amount it did not print"
}
