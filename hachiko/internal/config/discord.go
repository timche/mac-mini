package config

import (
	"os"
	"strconv"
	"strings"
)

// A webhook can post and nothing else, which is all an alert needs and not what Tim
// asked for: answering the on-call agent from his phone, where attaching to herdr is
// three minutes of work and reading a message is none. A bot can read a channel, so with
// one configured every message hachiko sends goes through it instead, each incident gets
// a thread of its own, and `hachiko listen` watches those threads for a reply.
//
// Off unless configured, and then nothing here runs: the webhook stays exactly as it was,
// which is the path that has to keep working on a Mac whose Discord application somebody
// deleted.
type DiscordConfig struct {
	ChannelID string
	UserID    string
}

// On only when both are set. A channel with no user to take replies from would be a
// listener that reads a channel and obeys anybody in it, and a user with no channel has
// nowhere to be read from.
func (d DiscordConfig) On() bool { return d.ChannelID != "" && d.UserID != "" }

// Neither is a secret — a channel id is in the URL of every message in it and a user id
// is on Tim's own profile — so they are a file in the checkout rather than a 1Password
// reference, and the file is empty until he fills it in. Only the bot token is a secret,
// and that is in the env file beside the webhook.
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
