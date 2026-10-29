package config

import (
	"path/filepath"
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// Both are ids in a request path, so anything that is not a snowflake is nothing.
func TestTheConfigFileTakesOnlySnowflakes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "discord")

	harness.WriteFile(t, path, `# the channel hachiko alerts to
channel = 1234567890123456789
user   =   987654321098765432
`)
	cfg := readDiscordConfig(path)
	harness.Equal(t, cfg.ChannelID, "1234567890123456789", "the channel id")
	harness.Equal(t, cfg.UserID, "987654321098765432", "the user id")
	harness.Equal(t, cfg.On(), true, "whether the bot is on")

	harness.WriteFile(t, path, "channel =\nuser =\n")
	harness.Equal(t, readDiscordConfig(path).On(), false, "whether an empty file turns the bot on")

	for _, bad := range []string{
		"channel = ../../etc/passwd\nuser = 1\n",
		"channel = 1/messages\nuser = 1\n",
		"channel = <#1234>\nuser = 1\n",
	} {
		harness.WriteFile(t, path, bad)
		harness.Equal(t, readDiscordConfig(path).ChannelID, "", "the channel read from "+bad)
	}

	harness.Equal(t, readDiscordConfig(filepath.Join(dir, "nothing-here")).On(), false,
		"whether a missing file turns the bot on")
}
