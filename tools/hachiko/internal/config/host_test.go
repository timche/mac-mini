package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

func stubTailnet(t *testing.T, name string, err error) {
	t.Helper()
	real := tailnetName
	tailnetName = func() (string, error) { return name, err }
	t.Cleanup(func() { tailnetName = real })
}

// A router renaming the Mac is what this exists for, so the kernel hostname may not win
// while the tailnet answers, and the name has to outlive a tailscale that stops answering.
func TestTheHostIsTheTailnetsNameAndOutlivesTailscale(t *testing.T) {
	dir := t.TempDir()
	load := func() Config {
		name, tailnet := machineName(dir)
		return Config{StateDir: dir, Host: name, hostFromTailnet: tailnet}
	}

	stubTailnet(t, "studio", nil)
	cfg := load()
	harness.Equal(t, cfg.Host, "studio", "the tailnet's name")
	if err := cfg.RememberHost(); err != nil {
		t.Fatal(err)
	}

	stubTailnet(t, "", errors.New("logged out"))
	harness.Equal(t, load().Host, "studio", "the last name the tailnet gave")

	stubTailnet(t, "studio-2", nil)
	harness.Equal(t, load().Host, "studio-2", "a name changed in the admin console")
}

// Every command loads its config, and `hachiko status` and the dry runs write nothing.
func TestLoadingTheHostWritesNothing(t *testing.T) {
	dir := t.TempDir()
	stubTailnet(t, "studio", nil)

	machineName(dir)
	if entries, _ := os.ReadDir(dir); len(entries) > 0 {
		t.Fatalf("loading the host wrote %d file(s)", len(entries))
	}
}

func TestAMacNeverOnATailnetIsItsHostname(t *testing.T) {
	stubTailnet(t, "", errors.New("no tailscale"))
	host, _ := os.Hostname()
	host, _, _ = strings.Cut(host, ".")

	name, _ := machineName(filepath.Join(t.TempDir(), "missing"))
	harness.Equal(t, name, host, "the kernel hostname")
}

func TestHachikoHostOverridesTheTailnet(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HACHIKO_HOST", "drill")
	stubTailnet(t, "studio", nil)

	harness.Equal(t, FromEnv().Host, "drill", "the override")
}
