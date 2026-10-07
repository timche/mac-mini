package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Sync is every repository `hachiko sync` keeps upstream and every number it decides by.
// Read here beside the Discord config rather than in the sync package, because the watch
// reads the same list to say whether a repository has fallen behind and two parsers would
// be two notions of which repositories exist.
type Sync struct {
	// Whether the daemon this config starts changes anything. A file that says dry-run is a
	// sync installed and reporting what it would do, which is how it runs beside the daemon
	// it replaces; the cutover is this one line.
	Mode  string
	Retry SyncRetry
	Repos []SyncRepo
}

func (s Sync) DryRun() bool { return s.Mode == ModeDryRun }

const (
	ModeLive   = "live"
	ModeDryRun = "dry-run"
)

// How many times a push is tried against a remote that will not answer, and the ladder it
// backs off along. Global, because an unreachable remote is the network rather than a
// repository.
type SyncRetry struct {
	Attempts int
	Base     time.Duration
	Max      time.Duration
}

type SyncRepo struct {
	// As the config spells it, tilde and all. The sync package canonicalises it at startup,
	// since a path that is not a work tree is a config to reject rather than a repository to
	// watch.
	Path string

	Debounce      time.Duration
	Remote        string
	Pull          bool
	PushDelay     time.Duration
	Recheck       time.Duration
	FetchInterval time.Duration
}

// Five seconds outlasts one agent's burst of writes while being an order of magnitude
// faster than the timer this replaced; gitwatch's two seconds was judged too eager. Set it
// per repository for anything that is executed rather than read.
const defaultDebounce = 5 * time.Second

// Nothing waits by default: a repository whose pushes are expensive says so.
const defaultPushDelay = 0

// A minute is what a timer would have used, and the cost of a fetch that finds nothing is
// one round trip — so another machine's writes arrive about as fast as somebody switching
// to the other window notices.
const defaultFetchInterval = time.Minute

// While commits remain unpushed, the push is retried this often even with nothing written.
const defaultRecheck = 10 * time.Minute

func defaultSyncRepo(path string) SyncRepo {
	return SyncRepo{
		Path:          path,
		Debounce:      defaultDebounce,
		Remote:        "origin",
		Pull:          true,
		PushDelay:     defaultPushDelay,
		Recheck:       defaultRecheck,
		FetchInterval: defaultFetchInterval,
	}
}

func defaultSyncRetry() SyncRetry {
	return SyncRetry{Attempts: 6, Base: 5 * time.Second, Max: 5 * time.Minute}
}

// The same `key = value` lines every other file in ~/.config/hachiko is written in, with
// one addition: a `repo =` line starts a repository, and every key after it belongs to that
// repository until the next one. So the file reads top to bottom with no nesting and no
// second syntax, and the keys before the first repo are the global ones.
//
// A file that cannot be read, or that says something this does not understand, is an error
// rather than a default: a typo in a push delay would otherwise be a repository published
// on a schedule nobody chose.
func LoadSync(path, home string) (Sync, error) {
	file, err := os.ReadFile(path)
	if err != nil {
		return Sync{}, fmt.Errorf("%s could not be read, so there is nothing to sync: %w", path, err)
	}

	cfg := Sync{Mode: ModeLive, Retry: defaultSyncRetry()}

	for n, line := range strings.Split(string(file), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return Sync{}, syncError(path, n, "%q is not a `key = value` line", line)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)

		if key == "repo" {
			if value == "" {
				return Sync{}, syncError(path, n, "repo needs a path")
			}
			cfg.Repos = append(cfg.Repos, defaultSyncRepo(value))
			continue
		}

		if len(cfg.Repos) == 0 {
			if err := globalKey(&cfg, key, value); err != nil {
				return Sync{}, syncError(path, n, "%v", err)
			}
			continue
		}
		if err := repoKey(&cfg.Repos[len(cfg.Repos)-1], key, value); err != nil {
			return Sync{}, syncError(path, n, "%v", err)
		}
	}

	if len(cfg.Repos) == 0 {
		return Sync{}, fmt.Errorf("%s names no repo, so there is nothing to sync", path)
	}

	seen := map[string]bool{}
	for i := range cfg.Repos {
		cfg.Repos[i].Path = ExpandTilde(cfg.Repos[i].Path, home)
		if seen[cfg.Repos[i].Path] {
			return Sync{}, fmt.Errorf("%s names %s twice", path, cfg.Repos[i].Path)
		}
		seen[cfg.Repos[i].Path] = true
	}
	return cfg, nil
}

func syncError(path string, line int, format string, args ...any) error {
	return fmt.Errorf("%s line %d: %s", path, line+1, fmt.Sprintf(format, args...))
}

func globalKey(cfg *Sync, key, value string) error {
	switch key {
	case "mode":
		if value != ModeLive && value != ModeDryRun {
			return fmt.Errorf("mode is %s or %s, not %q", ModeLive, ModeDryRun, value)
		}
		cfg.Mode = value
	case "attempts":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 {
			return fmt.Errorf("attempts is a whole number of tries, not %q", value)
		}
		cfg.Retry.Attempts = n
	case "base":
		return readDuration(&cfg.Retry.Base, key, value)
	case "max":
		return readDuration(&cfg.Retry.Max, key, value)
	default:
		return fmt.Errorf("%q is not a setting, and comes before any repo", key)
	}
	return nil
}

func repoKey(repo *SyncRepo, key, value string) error {
	switch key {
	case "remote":
		if value == "" {
			return fmt.Errorf("remote needs a name")
		}
		repo.Remote = value
	case "pull":
		switch value {
		case "true":
			repo.Pull = true
		case "false":
			repo.Pull = false
		default:
			return fmt.Errorf("pull is true or false, not %q", value)
		}
	case "debounce":
		return readDuration(&repo.Debounce, key, value)
	case "push_delay":
		return readDuration(&repo.PushDelay, key, value)
	case "recheck":
		return readDuration(&repo.Recheck, key, value)
	case "fetch_interval":
		return readDuration(&repo.FetchInterval, key, value)
	default:
		return fmt.Errorf("%q is not a setting of a repo", key)
	}
	return nil
}

func readDuration(into *time.Duration, key, value string) error {
	d, err := time.ParseDuration(value)
	if err != nil || d < 0 {
		return fmt.Errorf("%s is a duration such as 30s, 5m or 1h, not %q", key, value)
	}
	*into = d
	return nil
}

// A path in a config file is written with a tilde, never with an account name: nothing here
// is written for one account. Only at the front, so a folder really called `~` further down
// a path is left alone.
func ExpandTilde(path, home string) string {
	switch {
	case home == "":
		return path
	case path == "~":
		return home
	case strings.HasPrefix(path, "~/"):
		return filepath.Join(home, path[2:])
	default:
		return path
	}
}
