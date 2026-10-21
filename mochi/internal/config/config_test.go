package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
	if !ok || s.Workspace != "repeek" || s.Repo != "/home/x/projects/repeek" || s.Forum != "1562152175927558292" {
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
		"no repo":               `{"user": "94004892942741504", "server": [{"guild": "1220378778665095291", "workspace": "w"}]}`,
		"a bad user":            `{"user": "tim"}`,
		"a forum that is no id": `{"user": "94004892942741504", "server": [{"guild": "1220378778665095291", "workspace": "w", "repo": "/r", "forum": "#mochi"}]}`,
		"a field kept for code": `{"user": "94004892942741504", "Home": "/elsewhere"}`,
		"two objects":           `{"user": "94004892942741504"} {"user": "94004892942741504"}`,
		"not JSON":              "user = \"94004892942741504\"\n",
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
