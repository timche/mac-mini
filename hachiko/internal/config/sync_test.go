package config

import (
	"os"
	"path/filepath"
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
func TestTheShippedSyncConfigNamesTheProjectDocsAndIsLive(t *testing.T) {
	home := t.TempDir()

	cfg, err := LoadSync(filepath.Join("..", "..", "..", "home", ".config", "hachiko", "sync"), home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DryRun() {
		t.Error("the shipped config is a dry run, so the Mac commits and pushes nothing")
	}

	// The docs and nothing else: this repository is committed by the session that
	// changes it, and a path here is a path the daemon would commit behind one.
	want := map[string]time.Duration{
		filepath.Join(home, "projects", "docs"): time.Minute,
	}
	if len(cfg.Repos) != len(want) {
		t.Fatalf("got %d repos", len(cfg.Repos))
	}
	for _, repo := range cfg.Repos {
		delay, listed := want[repo.Path]
		if !listed {
			t.Errorf("%s is not a repository this config names", repo.Path)
			continue
		}
		if repo.PushDelay != delay {
			t.Errorf("%s pushes after %s, want %s", repo.Path, repo.PushDelay, delay)
		}
	}
}
