package chat

import (
	"encoding/json"

	"github.com/bwmarrin/discordgo"
)

// A role as Discord sends it, with the tags discordgo's own Role leaves out: an integration's
// managed role carries the bot's user id there, and that role is what Discord's autocomplete
// offers beside the bot itself when somebody types its name.
type Role struct {
	ID   string `json:"id"`
	Tags *struct {
		BotID string `json:"bot_id"`
	} `json:"tags"`
}

func (r Role) BotID() string {
	if r.Tags == nil {
		return ""
	}
	return r.Tags.BotID
}

// The id of the role that stands for this bot, or "" when the guild has none.
func BotRole(roles []Role, bot string) string {
	for _, r := range roles {
		if bot != "" && r.BotID() == bot {
			return r.ID
		}
	}
	return ""
}

// Raw rather than discordgo's GuildRoles, which drops the tags.
func (l Live) Roles(guild string) ([]Role, error) {
	endpoint := discordgo.EndpointGuildRoles(guild)
	body, err := l.S.RequestWithBucketID("GET", endpoint, nil, endpoint)
	if err != nil {
		return nil, err
	}
	var roles []Role
	if err := json.Unmarshal(body, &roles); err != nil {
		return nil, err
	}
	return roles, nil
}
