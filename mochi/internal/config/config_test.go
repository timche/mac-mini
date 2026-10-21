package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/timche/mac-mini/mochi/internal/project"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The file this repository ships, read through its own relative path, so the mapping Tim
// relies on is the one the tests read.
func TestTheShippedConfigMapsRepeek(t *testing.T) {
	_, here, _, _ := runtime.Caller(0)
	shipped := filepath.Join(filepath.Dir(here), "..", "..", "..", "home", ".config", "mochi", "config.json")

	cfg, err := Load(shipped, "/home/x")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.User != "94004892942741504" {
		t.Errorf("user = %q", cfg.User)
	}
	s, ok := cfg.Server("1220378778665095291")
	if !ok || s.Workspace != "repeek" || s.Repo != "/home/x/projects/repeek" {
		t.Errorf("repeek = %+v, %v", s, ok)
	}
	if _, ok := cfg.Server("1"); ok {
		t.Error("an unmapped guild was found")
	}
	if cfg.Split.Model != "sonnet" || cfg.Split.Effort != "medium" {
		t.Errorf("split = %+v", cfg.Split)
	}
}

func TestTheSplitModelDefaultsAndCannotBeAFlag(t *testing.T) {
	cfg, err := Load(write(t, `{"user": "94004892942741504"}`), "/home/x")
	if err != nil || cfg.Split.Model != "sonnet" || cfg.Split.Effort != "medium" {
		t.Errorf("defaults = %+v, %v", cfg.Split, err)
	}
	cfg, err = Load(write(t, `{"user": "94004892942741504", "split": {"model": "claude-haiku-4-5", "effort": "low"}}`), "/home/x")
	if err != nil || cfg.Split.Model != "claude-haiku-4-5" {
		t.Errorf("a model id = %+v, %v", cfg.Split, err)
	}
	if _, err := Load(write(t, `{"user": "94004892942741504", "split": {"model": "--dangerously-skip-permissions"}}`), "/home/x"); err == nil {
		t.Error("a flag was taken as a model")
	}
}

func TestAConfigThatWouldMisdirectIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"no user":         `{"server": [{"guild": "1220378778665095291", "workspace": "w", "repo": "/r"}]}`,
		"a typo in a key": `{"user": "94004892942741504", "server": [{"guild": "1220378778665095291", "workspce": "w", "repo": "/r"}]}`,
		"a guild twice": `{"user": "94004892942741504", "server": [` +
			`{"guild": "1220378778665095291", "workspace": "w", "repo": "/r"},` +
			`{"guild": "1220378778665095291", "workspace": "v", "repo": "/s"}]}`,
		"no repo":                            `{"user": "94004892942741504", "server": [{"guild": "1220378778665095291", "workspace": "w"}]}`,
		"a bad user":                         `{"user": "tim"}`,
		"a forum, which is the repository's": `{"user": "94004892942741504", "server": [{"guild": "1220378778665095291", "workspace": "w", "repo": "/r", "forum": "1562152175927558292"}]}`,
		"a field kept for code":              `{"user": "94004892942741504", "Home": "/elsewhere"}`,
		"two objects":                        `{"user": "94004892942741504"} {"user": "94004892942741504"}`,
		"an announce channel, which is the repository's": `{"user": "94004892942741504", "server": [{"guild": "1220378778665095291", "workspace": "w", "repo": "/r", "announce": "1378670204795617340"}]}`,
		"links, which are the repository's":              `{"user": "94004892942741504", "server": [{"guild": "1220378778665095291", "workspace": "w", "repo": "/r", "links": ["repeek.gg"]}]}`,
		"not JSON":                                       "user = \"94004892942741504\"\n",
	} {
		if _, err := Load(write(t, body), "/home/x"); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
}

func TestPathsComeFromTheEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MOCHI_CONFIG", write(t, `{"user": "94004892942741504"}`))
	t.Setenv("MOCHI_CACHE_DIR", "")
	t.Setenv("MOCHI_ENV_FILE", "")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "Library", "Caches", "mochi"); cfg.CacheDir != want {
		t.Errorf("cache = %s", cfg.CacheDir)
	}
	if !strings.HasSuffix(cfg.EnvFile, ".local/bin/mochi.env.op") {
		t.Errorf("env file = %s", cfg.EnvFile)
	}
	if cfg.SessionDir("123") != filepath.Join(cfg.CacheDir, "sessions", "123") {
		t.Errorf("session dir = %s", cfg.SessionDir("123"))
	}
}

func repoWith(t *testing.T, config string) string {
	t.Helper()
	repo := t.TempDir()
	if config != "" {
		os.MkdirAll(filepath.Join(repo, project.Dir), 0o755)
		if err := os.WriteFile(filepath.Join(repo, project.ConfigFile), []byte(config), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

// A server's settings are its repository's .mochi/config.json: none when it has none, and an
// error when it is broken.
func TestTheProjectsSettingsComeFromItsRepository(t *testing.T) {
	if p, err := (Server{Repo: repoWith(t, `{"announce": "1378670204795617399"}`)}).Project(nil); err != nil || p.Announce != "1378670204795617399" {
		t.Errorf("from the repository: %+v, %v", p, err)
	}
	if p, err := (Server{Repo: repoWith(t, "")}).Project(nil); err != nil || p.Forum != "" || p.Announce != "" || p.Links != nil {
		t.Errorf("none: %+v, %v", p, err)
	}
	if p, err := (Server{Repo: repoWith(t, `{"forum": "#mochi"}`)}).Project(nil); err == nil || p.Forum != "" {
		t.Errorf("broken: %+v, %v", p, err)
	}

	dangling := repoWith(t, "")
	os.Mkdir(filepath.Join(dangling, project.Dir), 0o755)
	os.Symlink(filepath.Join(dangling, "gone.json"), filepath.Join(dangling, project.ConfigFile))
	if p, err := (Server{Repo: dangling}).Project(nil); err == nil || p.Forum != "" {
		t.Errorf("a link that leads nowhere: %+v, %v", p, err)
	}
}

// A mapped repository with no .mochi/config.json is logged once, so a forum missing for that
// reason shows; one that has the file is not logged.
func TestARepositoryWithNoSettingsIsLoggedOnce(t *testing.T) {
	var out bytes.Buffer
	log := slog.New(slog.NewTextHandler(&out, nil))
	none := Server{Repo: repoWith(t, "")}
	for range 3 {
		none.Project(log)
	}
	(Server{Repo: repoWith(t, `{}`)}).Project(log)
	if n := strings.Count(out.String(), "has no .mochi/config.json"); n != 1 || !strings.Contains(out.String(), none.Repo) {
		t.Errorf("logged %d times:\n%s", n, out.String())
	}
}

// The check a post gets before its server is known allows every repository's domains, and says
// which repository's could not be read.
func TestEveryServersLinksAreKnownUpFront(t *testing.T) {
	cfg := Config{Servers: []Server{
		{Repo: repoWith(t, `{"links": ["repeek.gg"]}`)},
		{Repo: repoWith(t, `{"links": ["example.org"]}`)},
		{Repo: repoWith(t, "")},
		{Repo: repoWith(t, `{"links": "repeek.gg"}`)},
	}}
	links, err := cfg.Links()
	if strings.Join(links, " ") != "repeek.gg example.org" || err == nil || !strings.Contains(err.Error(), cfg.Servers[3].Repo) {
		t.Errorf("%v, %v", links, err)
	}
}
