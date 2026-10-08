package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeSync(t *testing.T, body string) (string, string) {
	t.Helper()

	home := t.TempDir()
	path := filepath.Join(home, "sync")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path, home
}

func loadSync(t *testing.T, body string) Sync {
	t.Helper()

	cfg, err := LoadSync(writeSync(t, body))
	if err != nil {
		t.Fatalf("the config did not load: %v", err)
	}
	return cfg
}

func refuseSync(t *testing.T, body, because string) {
	t.Helper()

	_, err := LoadSync(writeSync(t, body))
	if err == nil {
		t.Fatalf("%q was accepted", body)
	}
	if !strings.Contains(err.Error(), because) {
		t.Fatalf("%q was refused with %q, which does not mention %q", body, err, because)
	}
}

func TestSyncDefaultsFillInAroundABareRepo(t *testing.T) {
	cfg := loadSync(t, "repo = /tmp/x\n")

	if len(cfg.Repos) != 1 {
		t.Fatalf("got %d repos", len(cfg.Repos))
	}
	repo := cfg.Repos[0]

	if repo.Remote != "origin" || !repo.Pull {
		t.Errorf("remote %q, pull %v", repo.Remote, repo.Pull)
	}
	if repo.Debounce != 5*time.Second || repo.PushDelay != 0 {
		t.Errorf("debounce %s, push delay %s", repo.Debounce, repo.PushDelay)
	}
	if repo.Recheck != 10*time.Minute || repo.FetchInterval != time.Minute {
		t.Errorf("recheck %s, fetch interval %s", repo.Recheck, repo.FetchInterval)
	}
	if cfg.Retry.Attempts != 6 || cfg.Retry.Base != 5*time.Second || cfg.Retry.Max != 5*time.Minute {
		t.Errorf("retry %+v", cfg.Retry)
	}
	if cfg.Mode != ModeLive || cfg.DryRun() {
		t.Errorf("mode %q", cfg.Mode)
	}
}

// The whole of the cutover: one line, and the daemon the agent starts changes nothing.
func TestSyncModeDryRun(t *testing.T) {
	cfg := loadSync(t, "mode = dry-run\nrepo = /tmp/x\n")
	if !cfg.DryRun() {
		t.Error("dry-run did not read as a dry run")
	}
}

// A key belongs to the repo above it, which is the whole of the file's nesting.
func TestSyncKeysBelongToTheRepoAboveThem(t *testing.T) {
	cfg := loadSync(t, `
attempts = 2
base = 250ms
max = 1s

repo = /tmp/docs
push_delay = 1m

repo = /tmp/machine
push_delay = 1h
debounce = 30s
pull = false
remote = upstream
recheck = 2m
fetch_interval = 0s
`)

	if cfg.Retry.Attempts != 2 || cfg.Retry.Base != 250*time.Millisecond || cfg.Retry.Max != time.Second {
		t.Errorf("retry %+v", cfg.Retry)
	}

	docs, machine := cfg.Repos[0], cfg.Repos[1]
	if docs.PushDelay != time.Minute || docs.Debounce != 5*time.Second || !docs.Pull {
		t.Errorf("the first repo took the second's keys: %+v", docs)
	}
	if machine.PushDelay != time.Hour || machine.Debounce != 30*time.Second {
		t.Errorf("machine %+v", machine)
	}
	if machine.Pull || machine.Remote != "upstream" {
		t.Errorf("machine %+v", machine)
	}
	if machine.Recheck != 2*time.Minute || machine.FetchInterval != 0 {
		t.Errorf("machine %+v", machine)
	}
}

func TestSyncExpandsATildeAgainstTheHomeItIsGiven(t *testing.T) {
	path, home := writeSync(t, "repo = ~/projects/docs\n")

	cfg, err := LoadSync(path, home)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "projects", "docs"); cfg.Repos[0].Path != want {
		t.Errorf("got %s, want %s", cfg.Repos[0].Path, want)
	}
}

func TestATildeExpandsOnlyAtTheFront(t *testing.T) {
	if got := ExpandTilde("/a/~/b", "/home/x"); got != "/a/~/b" {
		t.Errorf("got %s", got)
	}
	if got := ExpandTilde("~", "/home/x"); got != "/home/x" {
		t.Errorf("got %s", got)
	}
}

// Every one of these would otherwise be a repository published on a schedule nobody chose,
// or a repository not published at all, with nothing anywhere saying why.
func TestSyncRefusesAConfigItDoesNotUnderstand(t *testing.T) {
	refuseSync(t, "mode = dry-run\n", "names no repo")
	refuseSync(t, "", "names no repo")
	refuseSync(t, "repo = /tmp/x\npush_delay = tomorrow\n", "push_delay is a duration")
	refuseSync(t, "repo = /tmp/x\npush_dely = 1m\n", `"push_dely" is not a setting of a repo`)
	refuseSync(t, "push_delay = 1m\nrepo = /tmp/x\n", "comes before any repo")
	refuseSync(t, "mode = sometimes\nrepo = /tmp/x\n", "mode is live or dry-run")
	refuseSync(t, "attempts = none\nrepo = /tmp/x\n", "attempts is a whole number")
	refuseSync(t, "repo = /tmp/x\npull = maybe\n", "pull is true or false")
	refuseSync(t, "repo =\n", "repo needs a path")
	refuseSync(t, "repo = /tmp/x\nnonsense\n", "is not a `key = value` line")
	refuseSync(t, "repo = /tmp/x\nrepo = /tmp/x\n", "twice")
}

// An alias is the latest model of its kind for ever; an id is one that one day stops
// answering, leaving a daemon nobody is watching to fall back to the file list from then on.
func TestTheSubjectModelIsAnAliasOrNothing(t *testing.T) {
	if got := loadSync(t, "subject_model = haiku\nrepo = /tmp/x\n").SubjectModel; got != "haiku" {
		t.Errorf("got %q", got)
	}
	if got := loadSync(t, "repo = /tmp/x\n").SubjectModel; got != "" {
		t.Errorf("a config that names no model got %q", got)
	}
	if got := loadSync(t, "subject_model =\nrepo = /tmp/x\n").SubjectModel; got != "" {
		t.Errorf("an empty model got %q", got)
	}

	refuseSync(t, "subject_model = claude-haiku-4-5-20251001\nrepo = /tmp/x\n", "model alias")
	refuseSync(t, "subject_model = Haiku 4.5\nrepo = /tmp/x\n", "model alias")
}

// A repository with no `paths` is the whole repository, which is every repository synced
// before this existed.
func TestARepoWithNoPathsIsTheWholeRepository(t *testing.T) {
	repo := loadSync(t, "repo = /tmp/x\n").Repos[0]

	if repo.Limited() || repo.Pathspec() != nil {
		t.Errorf("%+v is limited to %v", repo, repo.Pathspec())
	}
}

// Repeated, because one key per line is the whole of this file's syntax and a list would be
// a second one. The pathspec is what every git command that reads or writes the tree ends in.
func TestPathsAreRepeatableAndBecomeAPathspec(t *testing.T) {
	repo := loadSync(t, "repo = /tmp/x\npaths = home/.claude\npaths = home/.config\n").Repos[0]

	if !repo.Limited() {
		t.Fatalf("%+v is not limited", repo)
	}
	want := []string{"--", "home/.claude", "home/.config"}
	if got := repo.Pathspec(); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Normalised here rather than left to git, so the path in a message, the path in the config
// and the path on the command line are one path.
func TestAPathIsNormalised(t *testing.T) {
	repo := loadSync(t, "repo = /tmp/x\npaths = ./home/x/../.claude/\n").Repos[0]

	if want := []string{"home/.claude"}; !reflect.DeepEqual(repo.Paths, want) {
		t.Errorf("got %q, want %q", repo.Paths, want)
	}
}

// There is no pull git can limit to a pathspec: `pull --rebase --autostash` stashes and
// reapplies the whole working tree, which is exactly the work outside those paths that a
// limited repository exists to leave alone. So pulling is off, and a config that says
// otherwise is a contradiction rather than a preference.
func TestALimitedRepoDoesNotPull(t *testing.T) {
	repo := loadSync(t, "repo = /tmp/x\npaths = home/.claude\n").Repos[0]
	if repo.Pull {
		t.Error("a limited repository pulls")
	}

	// Either order, since a key belongs to the repo above it and `paths` may be written
	// under `pull` as readily as over it.
	for _, body := range []string{
		"repo = /tmp/x\npaths = home/.claude\npull = true\n",
		"repo = /tmp/x\npull = true\npaths = home/.claude\n",
	} {
		refuseSync(t, body, "no pull can leave work outside those paths alone")
	}

	// And `pull = false` beside paths is the default said out loud, not a contradiction.
	if loadSync(t, "repo = /tmp/x\npaths = home/.claude\npull = false\n").Repos[0].Pull {
		t.Error("pull = false pulls")
	}
}

// A pathspec that escapes the root is a pass committing somewhere nobody configured, and a
// `paths = .` is a repository every rule treats as limited while git matches all of it.
func TestSyncRefusesAPathThatIsNotInsideTheRepository(t *testing.T) {
	for _, value := range []string{"/etc", "~/elsewhere", "..", "../sibling", "home/../.."} {
		refuseSync(t, "repo = /tmp/x\npaths = "+value+"\n", "inside the repository")
	}
	refuseSync(t, "repo = /tmp/x\npaths = .\n", "a repo with no paths is the whole of it")
	refuseSync(t, "repo = /tmp/x\npaths =\n", "paths needs a path")
	refuseSync(t, "repo = /tmp/x\npaths = home/.claude\npaths = home/.claude\n", "twice")
}

func TestSyncRefusesAConfigThatIsNotThere(t *testing.T) {
	_, err := LoadSync(filepath.Join(t.TempDir(), "absent"), "/home/x")
	if err == nil {
		t.Fatal("a missing config was accepted")
	}
	if !strings.Contains(err.Error(), "there is nothing to sync") {
		t.Errorf("got %q", err)
	}
}

// The line number, because a config with a typo in it is read by somebody looking for the
// line to fix.
func TestSyncNamesTheLineItRefused(t *testing.T) {
	_, err := LoadSync(writeSync(t, "repo = /tmp/x\n\n# a note\ndebounce = soon\n"))
	if err == nil || !strings.Contains(err.Error(), "line 4") {
		t.Fatalf("got %v", err)
	}
}

// The file this repository ships, read as the Mac reads it.
func TestTheShippedSyncConfigNamesTheDocsAndThisRepositorysClaudeFiles(t *testing.T) {
	home := t.TempDir()

	cfg, err := LoadSync(filepath.Join("..", "..", "..", "home", ".config", "hachiko", "sync"), home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DryRun() {
		t.Error("the shipped config is a dry run, so the Mac commits and pushes nothing")
	}
	if cfg.SubjectModel != "haiku" {
		t.Errorf("subjects are written by %q", cfg.SubjectModel)
	}

	// Two repositories, and the Mac's own one is limited to the files nothing else commits:
	// the rest of it is a session's, and a daemon on the whole of it would commit a
	// half-written script behind one.
	want := []SyncRepo{
		{
			Path:      filepath.Join(home, "projects", "docs"),
			PushDelay: time.Minute,
			Debounce:  5 * time.Second,
			Pull:      true,
		},
		{
			Path:      filepath.Join(home, ".mac-mini"),
			Paths:     []string{filepath.Join("home", ".claude")},
			PushDelay: time.Minute,
			Debounce:  2 * time.Minute,
		},
	}
	if len(cfg.Repos) != len(want) {
		t.Fatalf("got %d repos", len(cfg.Repos))
	}

	for i, repo := range cfg.Repos {
		if repo.Path != want[i].Path {
			t.Errorf("repo %d is %s, want %s", i, repo.Path, want[i].Path)
			continue
		}
		if !reflect.DeepEqual(repo.Paths, want[i].Paths) {
			t.Errorf("%s is limited to %q, want %q", repo.Path, repo.Paths, want[i].Paths)
		}
		if repo.PushDelay != want[i].PushDelay || repo.Debounce != want[i].Debounce {
			t.Errorf("%s commits after %s and pushes after %s, want %s and %s",
				repo.Path, repo.Debounce, repo.PushDelay, want[i].Debounce, want[i].PushDelay)
		}
		// A limited repository may not pull, since a rebase and its autostash are what would
		// take a session's work outside those paths with them.
		if repo.Pull != want[i].Pull {
			t.Errorf("%s pulls: %v", repo.Path, repo.Pull)
		}
	}
}
