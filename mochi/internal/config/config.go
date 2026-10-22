// Package config is what mochi knows before it talks to anybody: the one account it takes
// instructions from, which Discord server maps to which herdr workspace and repository, and
// where its own files live.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/timche/mac-mini/mochi/internal/project"
)

type Server struct {
	Guild     string `json:"guild"`
	Workspace string `json:"workspace"`
	Repo      string `json:"repo"`
}

var unset sync.Map

// Project is the settings in the server's repository, read afresh, and none when it has no
// .mochi/config.json, which is logged once per repository when log is given: otherwise a forum
// missing for that reason would look like mochi ignoring it.
func (s Server) Project(log *slog.Logger) (project.Settings, error) {
	p, err := project.Load(s.Repo)
	if errors.Is(err, project.ErrMissing) {
		if _, logged := unset.LoadOrStore(s.Repo, true); !logged && log != nil {
			log.Warn("the repository has no "+project.ConfigFile+", so it has no forum, announce channel or links", "repo", s.Repo)
		}
		return project.Settings{}, nil
	}
	return p, err
}

// The model that decides whether a mention is one task or several, chosen by measuring
// candidates with `mochi split-eval`: the cheapest and fastest that rarely gets it wrong.
type Split struct {
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

func (s Split) Check() error {
	if !word.MatchString(s.Model) || !word.MatchString(s.Effort) {
		return fmt.Errorf("split's model and effort are each one word, like sonnet and medium")
	}
	return nil
}

type Config struct {
	User    string   `json:"user"`
	Servers []Server `json:"server"`
	Split   Split    `json:"split"`

	Home string `json:"-"`

	// The binary, the state, the heartbeat and every session's attachments: all of it is
	// mochi's own and none of it belongs in a repository.
	CacheDir string `json:"-"`

	// Beside the wrapper, where mise links both of them.
	EnvFile string `json:"-"`

	// The standing orders every session mochi starts gets at the top of its brief, in this
	// checkout's mochi plugin; the wrapper names it from the checkout it runs from.
	Briefing string `json:"-"`
}

var (
	snowflake = regexp.MustCompile(`\A[0-9]{17,20}\z`)

	// Both go to claude as flag values, so neither may look like a flag.
	word = regexp.MustCompile(`\A[a-z0-9][a-z0-9.\[\]-]*\z`)
)

func IsSnowflake(id string) bool { return snowflake.MatchString(id) }

func (c Config) Server(guild string) (Server, bool) {
	for _, s := range c.Servers {
		if s.Guild == guild {
			return s, true
		}
	}
	return Server{}, false
}

// Every link any server allows, for the check a post gets before its server is known, and why a
// repository's allow none.
func (c Config) Links() ([]string, error) {
	var all []string
	var errs []error
	for _, s := range c.Servers {
		p, err := s.Project(nil)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.Repo, err))
		}
		all = append(all, p.Links...)
	}
	return all, errors.Join(errs...)
}

func (c Config) StatePath() string { return filepath.Join(c.CacheDir, "state.json") }
func (c Config) Heartbeat() string { return filepath.Join(c.CacheDir, "heartbeat") }

// Questions mochi has asked Tim and not had answered, kept on disk so a restart can still take
// the answer.
func (c Config) PendingPath() string { return filepath.Join(c.CacheDir, "pending.json") }

// Which sessions are waiting on Tim, and whether he has been told since his last word to them.
func (c Config) WaitsPath() string { return filepath.Join(c.CacheDir, "waits.json") }

// The pull requests sessions posted that mochi follows.
func (c Config) WatchesPath() string { return filepath.Join(c.CacheDir, "prs.json") }

// What mochi's reactions and thread names show, shared by the listener and `mochi post`.
func (c Config) MarksPath() string { return filepath.Join(c.CacheDir, "marks.json") }

// The marker the outer half leaves before it hands over to `op run`, which the inner half
// removes once it has a token: one still there on the next start means 1Password did not
// answer last time.
func (c Config) OpAttempt() string { return filepath.Join(c.CacheDir, "op-attempt") }

func (c Config) SessionDir(thread string) string {
	return filepath.Join(c.CacheDir, "sessions", thread)
}

func cacheDir(home string) string {
	return envOr("MOCHI_CACHE_DIR", filepath.Join(home, "Library", "Caches", "mochi"))
}

// Where sessions' hooks leave their events: known without the config file, since the hook
// runs before anything else is read and has to be done in milliseconds.
func EventsDirFromEnv() string {
	home := os.Getenv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return filepath.Join(cacheDir(home), "events")
}

func (c Config) EventsDir() string { return filepath.Join(c.CacheDir, "events") }

func FromEnv() (Config, error) {
	home := os.Getenv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}

	path := envOr("MOCHI_CONFIG", filepath.Join(home, ".config", "mochi", "config.json"))
	cfg, err := Load(path, home)
	if err != nil {
		return Config{}, err
	}

	cfg.CacheDir = cacheDir(home)
	cfg.EnvFile = envOr("MOCHI_ENV_FILE", filepath.Join(home, ".local", "bin", "mochi.env.op"))
	cfg.Briefing = envOr("MOCHI_BRIEFING", filepath.Join(home, ".mac-mini", "claude-plugins", "mochi", "briefing.md"))
	return cfg, nil
}

// Strict, because every value ends up in a herdr call or a Discord request path: a key
// mochi does not know is a typo, and a typo in the user is a bot that obeys nobody.
func Load(path, home string) (Config, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if dec.More() {
		return Config{}, fmt.Errorf("%s: more than one JSON object", path)
	}

	if !IsSnowflake(cfg.User) {
		return Config{}, fmt.Errorf("%s: user is not a Discord id", path)
	}
	seen := map[string]bool{}
	for i, s := range cfg.Servers {
		switch {
		case !IsSnowflake(s.Guild):
			return Config{}, fmt.Errorf("%s: server %d has no Discord guild id", path, i+1)
		case seen[s.Guild]:
			return Config{}, fmt.Errorf("%s: guild %s is mapped twice", path, s.Guild)
		case strings.TrimSpace(s.Workspace) == "":
			return Config{}, fmt.Errorf("%s: guild %s names no herdr workspace", path, s.Guild)
		case strings.TrimSpace(s.Repo) == "":
			return Config{}, fmt.Errorf("%s: guild %s names no repository", path, s.Guild)
		}
		seen[s.Guild] = true
		cfg.Servers[i].Repo = expand(s.Repo, home)
	}

	if cfg.Split.Model == "" {
		cfg.Split.Model = "sonnet"
	}
	if cfg.Split.Effort == "" {
		cfg.Split.Effort = "medium"
	}
	if err := cfg.Split.Check(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}

	cfg.Home = home
	return cfg, nil
}

func expand(path, home string) string {
	if path == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return path
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
