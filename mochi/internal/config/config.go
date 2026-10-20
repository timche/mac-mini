// Package config is what mochi knows before it talks to anybody: the one account it takes
// instructions from, which Discord server maps to which herdr workspace and repository, and
// where its own files live.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

type Server struct {
	Guild     string `toml:"guild"`
	Workspace string `toml:"workspace"`
	Repo      string `toml:"repo"`
}

// The model that decides whether a mention is one task or several, chosen by measuring
// candidates with `mochi split-eval`: the cheapest and fastest that rarely gets it wrong.
type Split struct {
	Model  string `toml:"model"`
	Effort string `toml:"effort"`
}

type Config struct {
	User    string   `toml:"user"`
	Servers []Server `toml:"server"`
	Split   Split    `toml:"split"`

	Home string `toml:"-"`

	// The binary, the state, the heartbeat and every session's attachments: all of it is
	// mochi's own and none of it belongs in a repository.
	CacheDir string `toml:"-"`

	// Beside the wrapper, where mise links both of them.
	EnvFile string `toml:"-"`
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

func (c Config) StatePath() string { return filepath.Join(c.CacheDir, "state.json") }
func (c Config) Heartbeat() string { return filepath.Join(c.CacheDir, "heartbeat") }

// The marker the outer half leaves before it hands over to `op run`, which the inner half
// removes once it has a token: one still there on the next start means 1Password did not
// answer last time.
func (c Config) OpAttempt() string { return filepath.Join(c.CacheDir, "op-attempt") }

func (c Config) SessionDir(thread string) string {
	return filepath.Join(c.CacheDir, "sessions", thread)
}

func FromEnv() (Config, error) {
	home := os.Getenv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}

	path := envOr("MOCHI_CONFIG", filepath.Join(home, ".config", "mochi", "config.toml"))
	cfg, err := Load(path, home)
	if err != nil {
		return Config{}, err
	}

	cfg.CacheDir = envOr("MOCHI_CACHE_DIR", filepath.Join(home, "Library", "Caches", "mochi"))
	cfg.EnvFile = envOr("MOCHI_ENV_FILE", filepath.Join(home, ".local", "bin", "mochi.env.op"))
	return cfg, nil
}

// Strict, because every value ends up in a herdr call or a Discord request path: a key
// mochi does not know is a typo, and a typo in the user is a bot that obeys nobody.
func Load(path, home string) (Config, error) {
	var cfg Config
	meta, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if extra := meta.Undecoded(); len(extra) > 0 {
		return Config{}, fmt.Errorf("%s: unknown key %s", path, extra[0])
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
	if !word.MatchString(cfg.Split.Model) || !word.MatchString(cfg.Split.Effort) {
		return Config{}, fmt.Errorf("%s: [split] model and effort are each one word, like sonnet and medium", path)
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
