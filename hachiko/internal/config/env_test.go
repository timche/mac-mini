package config

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// The numbers and paths nothing else reads back. Every one of them is a knob so that a test
// can trip the arithmetic with megabytes and minutes, which also means a default somebody
// changed by accident is a default no test would otherwise notice.
func TestTheListenerDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := FromEnv()

	harness.Equal(t, cfg.ListenStaleAfter, 10*time.Minute, "how old the listener's heartbeat may get")
	harness.Equal(t, cfg.ListenPlist,
		filepath.Join(home, "Library", "LaunchAgents", "io.github.timche.hachiko-listen.plist"),
		"the listener's plist")

	// Spelled from the plist's own name, so the `launchctl kickstart` in a message cannot
	// name an agent other than the one whose absence decided whether to look at all.
	harness.Equal(t, cfg.ListenLabel(), "io.github.timche.hachiko-listen", "the listener's label")

	// Beside what the listener already keeps for itself, and in no file the sweep owns.
	harness.Equal(t, cfg.ListenStamp(), filepath.Join(cfg.StateDir, "listen", "heartbeat"),
		"where the listener's heartbeat goes")
}

func TestAKnobOverridesItsDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HACHIKO_LISTEN_STALE", "120")
	t.Setenv("HACHIKO_LISTEN_PLIST", "/somewhere/else.plist")

	cfg := FromEnv()

	harness.Equal(t, cfg.ListenStaleAfter, 2*time.Minute, "how old the listener's heartbeat may get")
	harness.Equal(t, cfg.ListenLabel(), "else", "the listener's label")
}
