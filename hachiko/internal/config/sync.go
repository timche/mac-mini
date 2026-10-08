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

	// Which model writes a commit subject, as an alias, or "" for the file list sync wrote
	// before this. Global, because what it answers is "what changed here", which is the same
	// question in every repository.
	SubjectModel string
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

	// The paths inside the repository a pass may touch, relative to its root and normalised,
	// or none at all for the whole repository. What it is for is a repository whose other
	// files are somebody's to commit: a session's work, in a tree a daemon also writes to.
	Paths []string

	Debounce      time.Duration
	Remote        string
	Pull          bool
	PushDelay     time.Duration
	Recheck       time.Duration
	FetchInterval time.Duration
}

func (r SyncRepo) Limited() bool { return len(r.Paths) > 0 }

// The `-- <paths>` every git command that reads or writes a limited repository's tree ends
// in, and nothing where the whole repository is synced. Here rather than in each caller, so
// the daemon, the watch and the screen cannot read different trees.
func (r SyncRepo) Pathspec() []string { return Pathspec(r.Paths) }

// The trailer sync puts on the commits it makes in a limited repository, and nowhere else.
// What it is for is the push: a commit of a session's in the same branch carries none, which
// is how sync — and the watch reading the same branch — tell a commit that is sync's to
// publish from one that is a session's to finish.
const SyncedTrailer = "Synced-by: hachiko sync"

// The `git log` arguments that pick sync's own commits out of a range, and none for a
// repository synced whole, where every commit in it is sync's to push.
func OwnCommits(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	return []string{"--fixed-strings", "--grep=" + SyncedTrailer}
}

func Pathspec(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	return append([]string{"--"}, paths...)
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

	// Which repositories asked for pulling in so many words, since the default is true and
	// the check below has to tell a repository that says `pull = true` from one that says
	// nothing: the first is a contradiction to refuse and the second is a default to change.
	var pulls []bool

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
			pulls = append(pulls, false)
			continue
		}

		if len(cfg.Repos) == 0 {
			if err := globalKey(&cfg, key, value); err != nil {
				return Sync{}, syncError(path, n, "%v", err)
			}
			continue
		}
		last := len(cfg.Repos) - 1
		if err := repoKey(&cfg.Repos[last], key, value); err != nil {
			return Sync{}, syncError(path, n, "%v", err)
		}
		if key == "pull" {
			pulls[last] = cfg.Repos[last].Pull
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

		// A `paths` line can come before or after `pull`, so the two are reconciled here
		// rather than as either is read. Pulling is off by default on a limited repository
		// and may not be turned on: `git pull --rebase --autostash` stashes and reapplies the
		// whole working tree, so a pull is exactly the thing that would take a session's work
		// outside those paths through a rebase and hand back a conflict in files sync was told
		// to leave alone. There is no pull git can limit to a pathspec.
		if cfg.Repos[i].Limited() {
			if pulls[i] {
				return Sync{}, fmt.Errorf("%s: %s is limited to paths and pulls, and no pull "+
					"can leave work outside those paths alone", path, cfg.Repos[i].Path)
			}
			cfg.Repos[i].Pull = false
		}
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
	case "subject_model":
		// An alias and never a pinned model id: an alias is the latest of its kind for ever,
		// where an id is a model that one day stops answering and a daemon nobody is watching
		// would fall back to the file list from then on with one line in a log to say so.
		if value != "" && !isModelAlias(value) {
			return fmt.Errorf("subject_model is a model alias such as haiku, not %q", value)
		}
		cfg.SubjectModel = value
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
	case "paths":
		one, err := repoRelative(value)
		if err != nil {
			return err
		}
		for _, already := range repo.Paths {
			if already == one {
				return fmt.Errorf("paths names %s twice", one)
			}
		}
		repo.Paths = append(repo.Paths, one)
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

// Letters alone, which is every alias there is and no id: an id carries its version and its
// date, so a digit or a dash is what tells the two apart.
func isModelAlias(value string) bool {
	for _, r := range value {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

// One `paths` value, normalised to the form git is handed. Relative and inside the
// repository, because a pathspec that escapes the root is a pass committing somewhere nobody
// configured, and normalised here rather than by git so the path in a message, in the config
// and on the command line are the one path.
func repoRelative(value string) (string, error) {
	inside := func() error {
		return fmt.Errorf("paths is a path inside the repository, relative to its root, not %q", value)
	}

	switch {
	case value == "":
		return "", fmt.Errorf("paths needs a path inside the repository")
	case filepath.IsAbs(value), strings.HasPrefix(value, "~"):
		return "", inside()

	// git reads what follows as a pathspec, where a leading colon is magic — `:(exclude)`,
	// `:/`, `:!` — and `*?[` are a glob matched against the whole tree. Both would make the
	// set of files a pass may touch something other than the folder written here, and
	// `:(exclude)home` would be a pass committing everything but it. A path is a path.
	case strings.HasPrefix(value, ":"), strings.ContainsAny(value, "*?["):
		return "", fmt.Errorf("paths is one plain path, not a pathspec or a glob, so %q is "+
			"refused: a leading `:` is magic to git and `*?[` match the whole tree", value)
	}

	clean := filepath.Clean(value)
	switch {
	case clean == "..", strings.HasPrefix(clean, ".."+string(filepath.Separator)):
		return "", inside()
	case clean == ".":
		// The whole repository is what a repo with no `paths` already is, and a `.` that read
		// as one would be a limited repository every rule below treats as limited while git
		// matches everything in it.
		return "", fmt.Errorf("paths is one path inside the repository; a repo with no paths " +
			"is the whole of it")
	}
	return clean, nil
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
