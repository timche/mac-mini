package watch

import (
	"errors"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// Housekeeping rather than watching, and it costs an `op run`, so once an hour against the
// check's twelve.
func TestTheForumIsTidiedUpOnceAnHour(t *testing.T) {
	f := newFixture(t)
	f.cfg.PostKeep = 72 * time.Hour
	f.cfg.Discord = config.DiscordConfig{ChannelID: "forum", UserID: "tim"}
	f.prunedLines = []string{`deleted the resolved post "Disk filling", closed 2026-10-14`}

	log := f.sweep()
	harness.Equal(t, len(f.pruned), 1, "times the forum was gone through")
	harness.Equal(t, f.pruned[0], 72*time.Hour, "how long a resolved post is kept")
	harness.Wants(t, log, `deleted the resolved post "Disk filling"`)

	f.at(300).sweep()
	harness.Equal(t, len(f.pruned), 1, "times the forum was gone through five minutes later")

	f.at(3900).sweep()
	harness.Equal(t, len(f.pruned), 2, "times the forum was gone through an hour later")
}

func TestNothingIsDeletedWithNoForumOrNoAgeToKeepThemFor(t *testing.T) {
	t.Run("no channel", func(t *testing.T) {
		f := newFixture(t)
		f.cfg.PostKeep = 72 * time.Hour

		f.sweep()
		harness.Equal(t, len(f.pruned), 0, "times the forum was gone through")
	})

	t.Run("nothing to keep them for", func(t *testing.T) {
		f := newFixture(t)
		f.cfg.Discord = config.DiscordConfig{ChannelID: "forum", UserID: "tim"}

		f.sweep()
		harness.Equal(t, len(f.pruned), 0, "times the forum was gone through")
	})
}

// A forum that cannot be read is a line and nothing else: there is nothing to answer, and
// the next hour tries again.
func TestAForumThatCannotBeReadIsSaidAndLeftAnHour(t *testing.T) {
	f := newFixture(t)
	f.cfg.PostKeep = 72 * time.Hour
	f.cfg.Discord = config.DiscordConfig{ChannelID: "forum", UserID: "tim"}
	f.prunedErr = errors.New("Discord answered 403 to GET /channels/forum")

	log := f.sweep()
	harness.Wants(t, log, "could not be tidied up")
	harness.Equal(t, f.sentCount(), 0, "messages sent about it")

	f.at(300).sweep()
	harness.Equal(t, len(f.pruned), 1, "times the forum was gone through five minutes later")
}

func TestADryRunDeletesNothing(t *testing.T) {
	f := newFixture(t)
	f.cfg.PostKeep = 72 * time.Hour
	f.cfg.Discord = config.DiscordConfig{ChannelID: "forum", UserID: "tim"}

	log := f.dryRun()
	harness.Equal(t, len(f.pruned), 0, "times the forum was gone through")
	harness.Wants(t, log, "would look through the forum for posts resolved more than 3 days ago")
}
