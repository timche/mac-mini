package listen

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/chat"
	"github.com/timche/mac-mini/mochi/internal/config"
	"github.com/timche/mac-mini/mochi/internal/herdr"
	"github.com/timche/mac-mini/mochi/internal/op"
	"github.com/timche/mac-mini/mochi/internal/self"
	"github.com/timche/mac-mini/mochi/internal/state"
)

const (
	// launchd starts the agent again five minutes after every exit, and every start is an
	// `op run` against the service account's daily limit. A token that will not resolve, or
	// one Discord refuses, is not going to fix itself in five minutes, so a start that meets
	// either waits this long before trying again: 48 reads a day rather than 288.
	retryAfter = 30 * time.Minute

	beatEvery  = 30 * time.Second
	pruneEvery = time.Hour
	keepGone   = 7 * 24 * time.Hour
)

// The outer half, which holds no token: it hands over to `op run`, unless the last start's
// hand-over never reached the inner half, in which case it waits first.
func Run(cfg config.Config, log *slog.Logger) error {
	if err := os.MkdirAll(cfg.CacheDir, 0o700); err != nil {
		return err
	}
	if info, err := os.Stat(cfg.OpAttempt()); err == nil {
		if wait := retryAfter - time.Since(info.ModTime()); wait > 0 {
			log.Warn("the last start never got a token from 1Password, so this one waits before asking again", "wait", wait.Round(time.Second).String())
			time.Sleep(wait)
		}
	}
	if err := os.WriteFile(cfg.OpAttempt(), nil, 0o600); err != nil {
		return err
	}
	return op.Reenter(cfg.EnvFile, []string{"listen"})
}

// The inner half, under `op run`. It returns only when the wrapper has built a new binary
// behind it, or when it could not connect at all.
func Serve(cfg config.Config, log *slog.Logger) error {
	token := os.Getenv(op.TokenVar)
	if token == "" {
		log.Error("the bot token did not resolve, so mochi is not listening", "retry", retryAfter.String())
		time.Sleep(retryAfter)
		return errors.New("no bot token")
	}
	os.Remove(cfg.OpAttempt())

	s, err := discordgo.New("Bot " + token)
	if err != nil {
		return errors.New(chat.Redact(err, token))
	}
	s.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsMessageContent

	h := &Handler{
		Cfg:   cfg,
		API:   chat.NewLive(s),
		Herdr: herdr.CLI,
		Store: state.Store{Path: cfg.StatePath()},
		Now:   time.Now,
		Log:   log,
		Token: token,
	}
	s.AddHandler(func(_ *discordgo.Session, r *discordgo.Ready) {
		h.SetBot(r.User.ID)
		log.Info("connected to Discord", "as", r.User.Username, "servers", len(r.Guilds))
	})
	s.AddHandler(func(_ *discordgo.Session, m *discordgo.MessageCreate) { h.Handle(m.Message) })

	if err := s.Open(); err != nil {
		log.Error("could not connect to Discord", "error", chat.Redact(err, token), "retry", retryAfter.String())
		time.Sleep(retryAfter)
		return errors.New("could not connect to Discord")
	}
	defer s.Close()

	binary := self.Looking(self.Stat)
	refresh := newRefresher(time.Now(), runWrapper(cfg.Home))
	var pruned time.Time
	for {
		time.Sleep(beatEvery)

		if connected(s) {
			beat(cfg, time.Now())
		}
		if err := refresh.tick(time.Now()); err != nil {
			log.Warn("the wrapper could not be run to pick up a change to mochi", "error", err)
		}
		if time.Since(pruned) > pruneEvery {
			h.Prune(keepGone)
			pruned = time.Now()
		}
		if binary.Replaced() {
			h.Lock()
			log.Info("a new mochi binary is in place, so this one exits for it")
			return self.ErrReplaced
		}
	}
}

// A beat is a gateway Discord is answering, not a process that is running: a listener
// whose connection has died and not come back is the stoppage the watch is there to see.
func connected(s *discordgo.Session) bool {
	s.RLock()
	defer s.RUnlock()
	return s.DataReady && time.Since(s.LastHeartbeatAck) < 3*time.Minute
}

func beat(cfg config.Config, at time.Time) {
	path := cfg.Heartbeat()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	os.WriteFile(path, []byte(strconv.FormatInt(at.Unix(), 10)+"\n"), 0o644)
}
