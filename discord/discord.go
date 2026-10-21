// Package discord is Discord's API v10 objects as this repository's tools read and send them:
// only the fields something here uses, under the JSON names Discord gives them. The REST
// client is in discord/rest and the gateway in discord/gateway.
package discord

import (
	"encoding/json"
	"io"
	"strings"
	"time"
)

// Redact takes a token out of a text before anything logs or posts it.
func Redact(text, token string) string {
	if token == "" {
		return text
	}
	return strings.ReplaceAll(text, token, "<token>")
}

type User struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
	Bot        bool   `json:"bot"`
}

// The name Discord shows: the display name a user set, or else the account's.
func (u *User) DisplayName() string {
	if u.GlobalName != "" {
		return u.GlobalName
	}
	return u.Username
}

type Member struct {
	User *User  `json:"user"`
	Nick string `json:"nick"`
}

type ChannelType int

const (
	ChannelTypeGuildText          ChannelType = 0
	ChannelTypeGuildNewsThread    ChannelType = 10
	ChannelTypeGuildPublicThread  ChannelType = 11
	ChannelTypeGuildPrivateThread ChannelType = 12
	ChannelTypeGuildForum         ChannelType = 15
	ChannelTypeGuildMedia         ChannelType = 16
)

type ForumTag struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// A thread or a forum post is a channel too: ParentID and AppliedTags are its, and
// AvailableTags the forum's.
type Channel struct {
	ID            string      `json:"id"`
	GuildID       string      `json:"guild_id"`
	ParentID      string      `json:"parent_id"`
	Name          string      `json:"name"`
	Type          ChannelType `json:"type"`
	AvailableTags []ForumTag  `json:"available_tags"`
	AppliedTags   []string    `json:"applied_tags"`
}

func (c *Channel) IsThread() bool {
	switch c.Type {
	case ChannelTypeGuildPublicThread, ChannelTypeGuildPrivateThread, ChannelTypeGuildNewsThread:
		return true
	}
	return false
}

type PremiumTier int

const (
	PremiumTier2 PremiumTier = 2
	PremiumTier3 PremiumTier = 3
)

// A guild as REST answers it, and as GUILD_CREATE delivers it with its roles.
type Guild struct {
	ID          string      `json:"id"`
	PremiumTier PremiumTier `json:"premium_tier"`
	Roles       []Role      `json:"roles"`
}

type RoleTags struct {
	// Set on an integration's managed role: the bot the role stands for, which is the role
	// Discord's autocomplete offers beside the bot when somebody types its name.
	BotID string `json:"bot_id"`
}

type Role struct {
	ID   string    `json:"id"`
	Tags *RoleTags `json:"tags"`
}

func (r Role) BotID() string {
	if r.Tags == nil {
		return ""
	}
	return r.Tags.BotID
}

type MessageType int

const (
	MessageTypeDefault              MessageType = 0
	MessageTypeChannelNameChange    MessageType = 4
	MessageTypeChannelPinnedMessage MessageType = 6
	MessageTypeReply                MessageType = 19
	MessageTypeThreadStarterMessage MessageType = 21
)

type MessageFlags int

const (
	MessageFlagsSuppressEmbeds MessageFlags = 1 << 2
	MessageFlagsEphemeral      MessageFlags = 1 << 6
)

type MessageAttachment struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	Size        int    `json:"size"`
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
}

type MessageReference struct {
	MessageID string `json:"message_id,omitempty"`
	ChannelID string `json:"channel_id,omitempty"`
	GuildID   string `json:"guild_id,omitempty"`
}

type Message struct {
	ID                string               `json:"id"`
	ChannelID         string               `json:"channel_id"`
	GuildID           string               `json:"guild_id"`
	Content           string               `json:"content"`
	Timestamp         time.Time            `json:"timestamp"`
	Type              MessageType          `json:"type"`
	Flags             MessageFlags         `json:"flags"`
	Author            *User                `json:"author"`
	Member            *Member              `json:"member"`
	Attachments       []*MessageAttachment `json:"attachments"`
	MessageReference  *MessageReference    `json:"message_reference"`
	ReferencedMessage *Message             `json:"referenced_message"`
}

type AllowedMentionType string

// Parse is never left out of the JSON: an empty list is what tells Discord that nothing in
// the message pings anybody, and a missing one would let it parse every mention. Nor is
// RepliedUser, so a reply pings its author only when it says so.
type MessageAllowedMentions struct {
	Parse       []AllowedMentionType `json:"parse"`
	Roles       []string             `json:"roles,omitempty"`
	Users       []string             `json:"users,omitempty"`
	RepliedUser bool                 `json:"replied_user"`
}

func (a MessageAllowedMentions) MarshalJSON() ([]byte, error) {
	type plain MessageAllowedMentions
	p := plain(a)
	if p.Parse == nil {
		p.Parse = []AllowedMentionType{}
	}
	return json.Marshal(p)
}

// A file to upload with a message, sent as multipart beside the message's JSON.
type File struct {
	Name        string
	ContentType string
	Reader      io.Reader
}

type MessageSend struct {
	Content         string                  `json:"content,omitempty"`
	Components      []MessageComponent      `json:"components,omitempty"`
	Files           []*File                 `json:"-"`
	AllowedMentions *MessageAllowedMentions `json:"allowed_mentions,omitempty"`
	Reference       *MessageReference       `json:"message_reference,omitempty"`
	Flags           MessageFlags            `json:"flags,omitempty"`
}

// ID and Channel say which message, and are the request's path rather than its body.
type MessageEdit struct {
	ID      string `json:"-"`
	Channel string `json:"-"`

	Content         *string                 `json:"content,omitempty"`
	Components      *[]MessageComponent     `json:"components,omitempty"`
	AllowedMentions *MessageAllowedMentions `json:"allowed_mentions,omitempty"`
}

type ComponentType int

const (
	ActionsRowComponent ComponentType = 1
	ButtonComponent     ComponentType = 2
)

// A message component as sent: each kind writes its own type into its JSON.
type MessageComponent interface {
	json.Marshaler
	Type() ComponentType
}

type ActionsRow struct {
	Components []MessageComponent
}

func (ActionsRow) Type() ComponentType { return ActionsRowComponent }

func (r ActionsRow) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type       ComponentType      `json:"type"`
		Components []MessageComponent `json:"components"`
	}{ActionsRowComponent, r.Components})
}

type ButtonStyle int

const (
	PrimaryButton   ButtonStyle = 1
	SecondaryButton ButtonStyle = 2
)

type Button struct {
	Label    string
	Style    ButtonStyle
	CustomID string
	Disabled bool
}

func (Button) Type() ComponentType { return ButtonComponent }

func (b Button) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type     ComponentType `json:"type"`
		Label    string        `json:"label,omitempty"`
		Style    ButtonStyle   `json:"style"`
		CustomID string        `json:"custom_id,omitempty"`
		Disabled bool          `json:"disabled"`
	}{ButtonComponent, b.Label, b.Style, b.CustomID, b.Disabled})
}

type InteractionType int

const InteractionMessageComponent InteractionType = 3

// What a pressed button sends: its custom id. Any other kind of interaction's data reads as
// this with nothing in it, which is all a tool that only has buttons needs.
type MessageComponentInteractionData struct {
	CustomID      string        `json:"custom_id"`
	ComponentType ComponentType `json:"component_type"`
}

type Interaction struct {
	ID        string                          `json:"id"`
	AppID     string                          `json:"application_id"`
	Type      InteractionType                 `json:"type"`
	Data      MessageComponentInteractionData `json:"data"`
	GuildID   string                          `json:"guild_id"`
	ChannelID string                          `json:"channel_id"`

	// Member in a guild, User in a DM; never both.
	Member *Member `json:"member"`
	User   *User   `json:"user"`

	// What the answer is sent with, in place of the bot's own token, for fifteen minutes.
	Token string `json:"token"`
}

func (i *Interaction) MessageComponentData() MessageComponentInteractionData { return i.Data }

type InteractionResponseType int

const (
	InteractionResponseChannelMessageWithSource InteractionResponseType = 4
	InteractionResponseDeferredMessageUpdate    InteractionResponseType = 6
)

type InteractionResponseData struct {
	Content         string                  `json:"content,omitempty"`
	Components      []MessageComponent      `json:"components,omitempty"`
	Flags           MessageFlags            `json:"flags,omitempty"`
	AllowedMentions *MessageAllowedMentions `json:"allowed_mentions,omitempty"`
}

type InteractionResponse struct {
	Type InteractionResponseType  `json:"type"`
	Data *InteractionResponseData `json:"data,omitempty"`
}

// A thread to start, on a message, on none, or as a forum post.
type ThreadStart struct {
	Name                string      `json:"name"`
	AutoArchiveDuration int         `json:"auto_archive_duration,omitempty"`
	Type                ChannelType `json:"type,omitempty"`
	AppliedTags         []string    `json:"applied_tags,omitempty"`
}

// A channel's changes: a field left nil is left as it is.
type ChannelEdit struct {
	Name        string    `json:"name,omitempty"`
	Archived    *bool     `json:"archived,omitempty"`
	AppliedTags *[]string `json:"applied_tags,omitempty"`
}

// Gateway intents, which say what events a connection is sent.
type Intent int

const (
	IntentsGuilds         Intent = 1 << 0
	IntentsGuildMessages  Intent = 1 << 9
	IntentsMessageContent Intent = 1 << 15
)

// What READY carries: the bot itself, the guilds it is in (unavailable until each one's
// GUILD_CREATE), and what a resume needs.
type Ready struct {
	User             *User   `json:"user"`
	Guilds           []Guild `json:"guilds"`
	SessionID        string  `json:"session_id"`
	ResumeGatewayURL string  `json:"resume_gateway_url"`
}

// GUILD_ROLE_CREATE and GUILD_ROLE_UPDATE.
type GuildRole struct {
	GuildID string `json:"guild_id"`
	Role    Role   `json:"role"`
}

type GuildRoleDelete struct {
	GuildID string `json:"guild_id"`
	RoleID  string `json:"role_id"`
}
