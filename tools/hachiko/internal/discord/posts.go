package discord

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
)

// The incident forum fills up with posts about incidents that are over. A resolved post
// that has been closed for days is nothing anybody is coming back to — the decision behind
// it is in the project docs and the reading that caused it is in a log — so it goes, and the
// forum stays a list of what happened lately rather than everything that ever did.
//
// This is the one thing hachiko deletes that it cannot put back, so what may be deleted is
// narrow and read off Discord rather than off hachiko's own state: the post is in that
// forum, it carries the forum's Resolved tag, it is archived, and it was archived longer ago
// than the age Tim set. An open post is never touched, and neither is one somebody reopened
// by writing in it. shibuya's posts go the same way, since they are in the same forum with
// the same tags.
func PruneThroughOP(cfg config.Config, keep time.Duration) ([]string, error) {
	if cfg.Discord.ChannelID == "" {
		return nil, errors.New(noChannel)
	}

	op, err := LookOp()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(cfg.EnvFile); err != nil {
		return nil, fmt.Errorf("%s is missing, so the forum was not tidied up", cfg.EnvFile)
	}

	self, err := os.Executable()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()

	args := append([]string{"run"}, EnvFiles(cfg)...)
	args = append(args, "--", self, "--prune-posts",
		"--channel", cfg.Discord.ChannelID,
		"--keep", strconv.FormatInt(int64(keep.Seconds()), 10))

	cmd := exec.CommandContext(ctx, op, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return lines(stdout.String()), errors.New(detail)
	}

	// A post it could not delete is a line the child wrote to stderr, which the log would
	// otherwise never see: it is not a failure of the whole sweep, since the rest were
	// deleted and the next sweep tries this one again.
	if note := strings.TrimSpace(stderr.String()); note != "" {
		fmt.Fprintln(os.Stderr, note)
	}
	return lines(stdout.String()), nil
}

// The far end of that re-exec, reached only under `op run`. One line per post deleted on
// stdout, one per post that would not go on stderr.
func PruneMode(channel string, keep time.Duration, now time.Time) error {
	if channel == "" {
		return errors.New(noChannel)
	}

	token := strings.TrimSpace(os.Getenv("HACHIKO_DISCORD_BOT_TOKEN"))
	if token == "" {
		return errors.New("HACHIKO_DISCORD_BOT_TOKEN is empty, so the op:// reference did not resolve")
	}
	return prune(NewBot(token), channel, keep, now, os.Stdout, os.Stderr)
}

func prune(bot Bot, channel string, keep time.Duration, now time.Time, out, errs io.Writer) error {
	forum, err := bot.Channel(channel)
	if err != nil {
		return err
	}

	// A text channel has no posts to close and nothing tagged, so there is nothing here to
	// do; and a forum with no Resolved tag is one where nothing can be read as resolved,
	// which is a forum this leaves alone rather than guessing about.
	if !forum.Forum() {
		return nil
	}
	resolved := forum.Tag(tagResolved)
	if resolved == "" {
		return nil
	}

	before := ""
	for {
		posts, more, err := bot.ClosedPosts(channel, before)
		if err != nil {
			return err
		}

		for _, post := range posts {
			closed := post.ClosedAt()
			if !post.ThreadMetadata.Archived || !post.HasTag(resolved) || closed.IsZero() {
				continue
			}
			if now.Sub(closed) < keep {
				continue
			}

			if err := bot.DeletePost(post.ID); err != nil {
				// Said and not retried: a post Discord refuses to delete is one it will refuse
				// again in the same way, and a loop over it would spend the sweep on one post.
				fmt.Fprintf(errs, "the resolved post %s could not be deleted: %v\n", post.ID, err)
				continue
			}
			fmt.Fprintf(out, "deleted the resolved post %q, closed %s\n",
				post.Name, closed.Format("2006-01-02"))
		}

		if !more || len(posts) == 0 {
			return nil
		}
		// Discord pages these by the archive timestamp to continue from, so the oldest of
		// this page is where the next one starts.
		before = posts[len(posts)-1].ThreadMetadata.ArchiveTimestamp
	}
}

func lines(text string) []string {
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			kept = append(kept, line)
		}
	}
	return kept
}
