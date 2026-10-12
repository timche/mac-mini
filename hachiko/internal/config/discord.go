package config

import (
	"os"
	"strconv"
	"strings"
)

// Where hachiko posts and whose reply it takes. The channel is what every message goes
// to, so a Mac with none configured has nowhere to send an alert; the account is what
// makes answering the on-call agent from Discord possible, where attaching to herdr is
// three minutes of work and reading a message is none.
type DiscordConfig struct {
	ChannelID string
	UserID    string
}

// Whether a reply can be taken, which needs both: a channel with no account behind it
// would be a listener that obeys anybody in it, and an account with no channel has nowhere
// to be read from. Sending needs the channel alone.
func (d DiscordConfig) On() bool { return d.ChannelID != "" && d.UserID != "" }

// Neither is a secret — a channel id is in the URL of every message in it and a user id
// is on Tim's own profile — so they are a file in the checkout rather than a 1Password
// reference, and the file is empty until he fills it in. Only the bot token is a secret,
// and that is an op:// reference in the env file beside the wrapper.
func readDiscordConfig(path string) DiscordConfig {
	file, err := os.ReadFile(path)
	if err != nil {
		return DiscordConfig{}
	}

	cfg := DiscordConfig{}
	for _, line := range strings.Split(string(file), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "channel":
			cfg.ChannelID = DiscordID(value)
		case "user":
			cfg.UserID = DiscordID(value)
		}
	}
	return cfg
}

// A snowflake and nothing else, because both of these end up in a request path. Discord
// prints them as decimal integers, and Tim copies them out of the client.
func DiscordID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if _, err := strconv.ParseUint(value, 10, 64); err != nil {
		return ""
	}
	return value
}
