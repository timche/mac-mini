package listen

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/timche/mac-mini/discord"
	gw "github.com/timche/mac-mini/discord/gateway"
	"github.com/timche/mac-mini/discord/rest"

	"github.com/timche/mac-mini/mochi/internal/chat"
	"github.com/timche/mac-mini/mochi/internal/config"
	"github.com/timche/mac-mini/mochi/internal/event"
	"github.com/timche/mac-mini/mochi/internal/gh"
	"github.com/timche/mac-mini/mochi/internal/herdr"
	"github.com/timche/mac-mini/mochi/internal/mark"
	"github.com/timche/mac-mini/mochi/internal/op"
	"github.com/timche/mac-mini/mochi/internal/self"
	"github.com/timche/mac-mini/mochi/internal/split"
	"github.com/timche/mac-mini/mochi/internal/state"
)

const (
	// launchd starts the agent again five minutes after every exit, and every start is an
	// `op run` against the service account's daily limit. A token that will not resolve, or
	// one Discord refuses, is not going to fix itself in five minutes, so a start that meets
	// either waits this long before trying again: 48 reads a day rather than 288.
	retryAfter = 30 * time.Minute

	beatEvery   = 30 * time.Second
	prsEvery    = 2 * time.Minute
	eventsEvery = time.Second
	pruneEvery  = time.Hour
	keepGone    = 7 * 24 * time.Hour
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

	client := rest.New(token)
	live := chat.NewLive(client)
	h := &Handler{
		Cfg:     cfg,
		API:     live,
		Herdr:   herdr.CLI,
		Store:   state.Store{Path: cfg.StatePath()},
		Pending: state.PendingStore{Path: cfg.PendingPath()},
		Waits:   state.WaitStore{Path: cfg.WaitsPath()},
		Watches: state.WatchStore{Path: cfg.WatchesPath()},
		GH:      gh.CLI,
		Marks:   mark.Marker{API: live, Path: cfg.MarksPath(), Now: time.Now, Log: log},
		Now:     time.Now,
		Log:     log,
		Token:   token,
		Split: split.Splitter{
			Run:     split.CLI,
			Model:   cfg.Split.Model,
			Effort:  cfg.Split.Effort,
			Timeout: split.Timeout,
		}.Split,
	}

	s := gw.New(token, discord.IntentsGuildMessages|discord.IntentsMessageContent)
	s.Log = log
	s.Handlers = gw.Handlers{
		Ready: func(r *discord.Ready) {
			h.SetBot(r.User.ID)
			log.Info("connected to Discord", "as", r.User.Username, "servers", len(r.Guilds))
		},
		MessageCreate: h.Handle,
		// A button press arrives over the gateway with no intent of its own, and is answered
		// with its own token, so it needs no permission beyond what posting the question did.
		InteractionCreate: h.Interaction,
	}

	g := gateway{
		open: func() error {
			addr, err := client.Gateway()
			if err != nil {
				return err
			}
			s.URL = addr
			return s.Open()
		},
		sleep: time.Sleep,
		now:   time.Now,
		stamp: filepath.Join(cfg.CacheDir, "identify"),
		log:   log,
		scrub: func(err error) string { return chat.Redact(err, token) },
	}
	// connect spaces the first identify itself; one the session makes later, after Discord
	// dropped it, is spaced here.
	var opened atomic.Bool
	s.BeforeIdentify = func() {
		if opened.Load() {
			g.spaceIdentify()
		}
	}
	err := g.connect()
	if errors.Is(err, ErrRefused) {
		log.Error("Discord refused the connection for good, so mochi starts again for a fresh token", "retry", retryAfter.String())
		time.Sleep(retryAfter)
	}
	if err != nil {
		return err
	}
	opened.Store(true)
	defer s.Close()

	// A session's hooks leave an event the moment its turn ends; read every second, which is a
	// directory listing and nothing more when there is none.
	event.Clean(cfg.EventsDir())
	go func() {
		for range time.Tick(eventsEvery) {
			h.ConsumeEvents()
		}
	}()

	binary := self.Looking(self.Stat)
	refresh := newRefresher(time.Now(), runWrapper(cfg.Home))
	var pruned, looked time.Time
	var lost down
	for {
		time.Sleep(beatEvery)

		if err := s.Err(); err != nil {
			log.Error("Discord closed the gateway for good, so mochi starts again for a fresh token", "error", chat.Redact(err, token), "retry", retryAfter.String())
			time.Sleep(retryAfter)
			h.Lock()
			return fmt.Errorf("%w: %s", ErrRefused, chat.Redact(err, token))
		}
		up := connected(s)
		if up {
			beat(cfg, time.Now())
		}
		if err := lost.observe(up, time.Now()); err != nil {
			h.Lock()
			return err
		}
		h.ExpirePending()
		h.Marks.Retry()
		if err := refresh.tick(time.Now()); err != nil {
			log.Warn("the wrapper could not be run to pick up a change to mochi", "error", err)
		}
		if time.Since(looked) > prsEvery {
			h.WatchPRs()
			looked = time.Now()
		}
		if time.Since(pruned) > pruneEvery {
			h.Prune(keepGone)
			pruned = time.Now()
		}
		if binary.Replaced() {
			h.Lock()
			err := inPlace.run(s.Close, log)
			if !errors.Is(err, errKept) {
				return err
			}
			binary.Skip()
			h.Unlock()
		}
	}
}

// A beat is a gateway Discord is answering, not a process that is running: a listener
// whose connection has died and not come back is the stoppage the watch is there to see.
func connected(s *gw.Session) bool {
	ready, ack := s.Status()
	return ready && time.Since(ack) < 3*time.Minute
}

func beat(cfg config.Config, at time.Time) {
	path := cfg.Heartbeat()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	os.WriteFile(path, []byte(strconv.FormatInt(at.Unix(), 10)+"\n"), 0o644)
}
