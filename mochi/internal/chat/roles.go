package chat

import "github.com/timche/mac-mini/discord"

// A role, with the tags that name the bot an integration's managed role stands for: that role
// is what Discord's autocomplete offers beside the bot itself when somebody types its name.
type Role = discord.Role

// The id of the role that stands for this bot, or "" when the guild has none.
func BotRole(roles []Role, bot string) string {
	for _, r := range roles {
		if bot != "" && r.BotID() == bot {
			return r.ID
		}
	}
	return ""
}
