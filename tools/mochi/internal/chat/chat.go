// Package chat is the few Discord calls mochi makes, behind an interface a test can fake with
// the shared client's own types, and the live implementation of them over its REST client.
package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/timche/mac-mini/discord"
	"github.com/timche/mac-mini/discord/rest"

	"github.com/timche/mac-mini/mochi/internal/state"
)

type API interface {
	Channel(id string) (*discord.Channel, error)
	Message(channel, id string) (*discord.Message, error)

	// One page of at most 100, in whatever order Discord sends it; Sort puts it right.
	Messages(channel string, limit int, before, after, around string) ([]*discord.Message, error)

	React(channel, message, emoji string) error

	// Takes back a reaction of mochi's own, and nobody else's.
	Unreact(channel, message, emoji string) error

	// A thread's new name. ErrRateLimited when Discord says to wait, ErrForbidden when the
	// thread is not mochi's to rename.
	RenameThread(thread, name string) error
	StartThread(channel, message, name string) (*discord.Channel, error)

	// A public thread on no message, which is how one mention gets several.
	NewThread(channel, name string) (*discord.Channel, error)

	// A post in a forum, with its tags and its first message; the post's id is that
	// message's id too.
	ForumPost(forum, name string, tags []string, first *discord.MessageSend) (*discord.Channel, error)

	// A forum post's name and tags in one edit, which Discord limits as it does a rename; the
	// errors are RenameThread's.
	EditPost(thread, name string, tags []string) error

	// Takes a thread out of its forum's list until somebody posts in it again.
	Archive(thread string) error

	// Makes a user a member of a thread, so they follow it.
	AddMember(thread, user string) error
	Send(channel string, msg *discord.MessageSend) (*discord.Message, error)

	// A message mochi posted, changed: a question's buttons disabled once it is answered.
	Edit(edit *discord.MessageEdit) error

	// A message mochi posted, taken down by the session that posted it.
	Delete(channel, message string) error

	// The answer to a button press, which Discord wants within three seconds.
	Respond(i *discord.Interaction, r *discord.InteractionResponse) error

	// An attachment's bytes off Discord's CDN, which needs no token.
	Fetch(url string) (io.ReadCloser, error)

	// mochi's own user.
	Me() (*discord.User, error)

	// A guild, for its boost tier.
	Guild(id string) (*discord.Guild, error)
}

// UploadLimit is the most one file may be in a guild, by its boost tier: Discord's own 10 MB,
// 50 MB at tier 2 and 100 MB at tier 3. A guild that cannot be read gets the least.
func UploadLimit(api API, guild string) int64 {
	g, err := api.Guild(guild)
	if err != nil {
		return 10 << 20
	}
	switch g.PremiumTier {
	case discord.PremiumTier3:
		return 100 << 20
	case discord.PremiumTier2:
		return 50 << 20
	}
	return 10 << 20
}

// What mochi shows on Tim's message while it handles it, and takes back after; and what it
// leaves there once what he asked has reached the session.
const (
	Eyes      = "👀"
	Delivered = "👍"
)

// Every post mochi makes pings nobody: it quotes moderators' names and Tim's own mention
// back, and a session posting findings is not a reason to wake a server.
// A link in a post is for following, not for a preview card moderators have to scroll past.
func Quiet(content string) *discord.MessageSend {
	msg := &discord.MessageSend{Content: content, AllowedMentions: NoMentions()}
	if strings.Contains(content, "://") {
		msg.Flags |= discord.MessageFlagsSuppressEmbeds
	}
	return msg
}

func NoMentions() *discord.MessageAllowedMentions {
	return &discord.MessageAllowedMentions{Parse: []discord.AllowedMentionType{}}
}

// Pinging is a post that may mention one user and nobody else: Tim, in the two places mochi
// does — a thread it opened, and a session waiting on him.
func Pinging(user, content string) *discord.MessageSend {
	msg := Quiet(content)
	msg.AllowedMentions = &discord.MessageAllowedMentions{
		Parse: []discord.AllowedMentionType{},
		Users: []string{user},
	}
	return msg
}

// Link is a message's or a channel's address, or nothing when it does not know the guild or
// the channel: an address with an empty part opens nothing and draws a generic preview.
func Link(guild, channel, message string) string {
	if guild == "" || channel == "" {
		return ""
	}
	link := "https://discord.com/channels/" + guild + "/" + channel
	if message != "" {
		link += "/" + message
	}
	return link
}

// GuildOf is the guild a session belongs to: its record's, or the source channel's when a
// record was saved without one, since a message fetched over REST carries no guild.
func GuildOf(api API, rec state.Session) string {
	if rec.Guild != "" {
		return rec.Guild
	}
	if ch, err := api.Channel(rec.Source); err == nil {
		return ch.GuildID
	}
	return ""
}

// InGuild refuses a channel that is not in guild: a project's settings name channels by id
// alone, and one copied from another server would take its posts there.
func InGuild(api API, channel, guild string) error {
	ch, err := api.Channel(channel)
	if err != nil {
		return fmt.Errorf("channel %s could not be read: %w", channel, err)
	}
	if ch.GuildID != guild {
		return fmt.Errorf("channel %s is not in this server", channel)
	}
	return nil
}

// TagIDs are the ids of a forum's tags with these names, in their order. A name the forum has
// no tag for is skipped, so a tag somebody deleted or never made is simply not applied.
func TagIDs(api API, forum string, names []string) ([]string, error) {
	ch, err := api.Channel(forum)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, name := range names {
		for _, tag := range ch.AvailableTags {
			if strings.EqualFold(tag.Name, name) {
				ids = append(ids, tag.ID)
				break
			}
		}
	}
	return ids, nil
}

// Snowflakes are timestamps, so their order is the messages' order. Compared as numbers,
// since ids of different lengths compare wrongly as strings.
func Before(a, b string) bool {
	x, errA := strconv.ParseUint(a, 10, 64)
	y, errB := strconv.ParseUint(b, 10, 64)
	if errA != nil || errB != nil {
		return a < b
	}
	return x < y
}

func Sort(msgs []*discord.Message) {
	sort.SliceStable(msgs, func(i, j int) bool { return Before(msgs[i].ID, msgs[j].ID) })
}

func Name(m *discord.Message) string {
	if m.Member != nil && m.Member.Nick != "" {
		return m.Member.Nick
	}
	if m.Author == nil {
		return "unknown"
	}
	return m.Author.DisplayName()
}

// Taken out of every error text before it is logged or posted, whatever put it there.
func Redact(err error, token string) string {
	if err == nil {
		return ""
	}
	return discord.Redact(err.Error(), token)
}

type Live struct {
	REST *rest.Client

	// Attachments can be tens of megabytes, which the API client's timeout is not for.
	Download *http.Client
}

func NewLive(c *rest.Client) Live {
	return Live{REST: c, Download: &http.Client{Timeout: 2 * time.Minute, CheckRedirect: stayOnCDN}}
}

// An attachment's URL is in a message somebody else wrote, so mochi fetches only from
// Discord's own CDN, over https, and follows no redirect off it.
var cdnHosts = map[string]bool{"cdn.discordapp.com": true, "media.discordapp.net": true}

func OnCDN(u *url.URL) bool {
	return u.Scheme == "https" && cdnHosts[u.Hostname()] && u.Port() == "" && u.User == nil
}

func stayOnCDN(req *http.Request, via []*http.Request) error {
	if !OnCDN(req.URL) {
		return fmt.Errorf("a redirect to %s, off Discord's CDN", req.URL.Host)
	}
	if len(via) >= 5 {
		return errors.New("too many redirects")
	}
	return nil
}

func (l Live) Channel(id string) (*discord.Channel, error) { return l.REST.Channel(id) }

func (l Live) Message(channel, id string) (*discord.Message, error) {
	return l.REST.Message(channel, id)
}

func (l Live) Messages(channel string, limit int, before, after, around string) ([]*discord.Message, error) {
	return l.REST.Messages(channel, limit, before, after, around)
}

func (l Live) React(channel, message, emoji string) error {
	return l.REST.ReactionAdd(channel, message, emoji)
}

func (l Live) Unreact(channel, message, emoji string) error {
	return l.REST.ReactionRemove(channel, message, emoji)
}

// Gone says Discord no longer has the channel, a thread somebody deleted: nothing sent to it
// will ever land.
func Gone(err error) bool {
	var refused *rest.Error
	switch {
	case errors.Is(err, ErrUnknownChannel):
		return true
	case errors.As(err, &refused):
		return refused.Code == rest.CodeUnknownChannel || refused.Status == http.StatusNotFound
	}
	return false
}

var (
	ErrUnknownChannel = errors.New("Discord has no such channel")
	ErrRateLimited    = errors.New("Discord asked to wait before renaming again")
	ErrForbidden      = errors.New("the thread is not mochi's to rename")
)

// Without waiting out a limit inside the call, which for a rename is ten minutes; the caller
// schedules the rename instead.
func (l Live) RenameThread(thread, name string) error {
	return l.edit(thread, &discord.ChannelEdit{Name: name})
}

func (l Live) EditPost(thread, name string, tags []string) error {
	return l.edit(thread, &discord.ChannelEdit{Name: name, AppliedTags: &tags})
}

func (l Live) Archive(thread string) error {
	archived := true
	return l.edit(thread, &discord.ChannelEdit{Archived: &archived})
}

func (l Live) edit(thread string, e *discord.ChannelEdit) error {
	_, err := l.REST.EditChannel(thread, e, rest.NoWait())
	var limited *rest.RateLimitError
	var refused *rest.Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &limited):
		return ErrRateLimited
	case errors.As(err, &refused) && refused.Status == http.StatusForbidden:
		return ErrForbidden
	case errors.As(err, &refused) && (refused.Code == rest.CodeMissingAccess || refused.Code == rest.CodeMissingPermissions):
		return ErrForbidden
	}
	return err
}

// A day, which is long enough for a session's findings to land in a thread Discord has not
// hidden yet; a post into an archived thread unarchives it anyway.
func (l Live) StartThread(channel, message, name string) (*discord.Channel, error) {
	return l.REST.StartThread(channel, message, &discord.ThreadStart{
		Name:                name,
		AutoArchiveDuration: 1440,
	})
}

func (l Live) Edit(edit *discord.MessageEdit) error {
	_, err := l.REST.EditMessage(edit)
	return err
}

func (l Live) Delete(channel, message string) error {
	return l.REST.DeleteMessage(channel, message)
}

func (l Live) Respond(i *discord.Interaction, r *discord.InteractionResponse) error {
	return l.REST.Respond(i, r)
}

func (l Live) AddMember(thread, user string) error {
	return l.REST.AddThreadMember(thread, user)
}

func (l Live) NewThread(channel, name string) (*discord.Channel, error) {
	return l.REST.NewThread(channel, &discord.ThreadStart{
		Name:                name,
		Type:                discord.ChannelTypeGuildPublicThread,
		AutoArchiveDuration: 1440,
	})
}

// A week, the longest Discord offers, since a post whose session is still working should stay
// in the forum's list; mochi archives one itself three days after it is done.
func (l Live) ForumPost(forum, name string, tags []string, first *discord.MessageSend) (*discord.Channel, error) {
	return l.REST.ForumPost(forum, &discord.ThreadStart{
		Name:                name,
		AutoArchiveDuration: 10080,
		AppliedTags:         tags,
	}, first)
}

func (l Live) Send(channel string, msg *discord.MessageSend) (*discord.Message, error) {
	return l.REST.Send(channel, msg)
}

func (l Live) Me() (*discord.User, error) { return l.REST.Me() }

func (l Live) Guild(id string) (*discord.Guild, error) { return l.REST.Guild(id) }

func (l Live) Fetch(raw string) (io.ReadCloser, error) {
	u, err := url.Parse(raw)
	if err != nil || !OnCDN(u) {
		return nil, errors.New("not a file on Discord's CDN")
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := l.Download.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("the CDN answered %s", resp.Status)
	}
	return resp.Body, nil
}
