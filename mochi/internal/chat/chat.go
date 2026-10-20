// Package chat is the few Discord calls mochi makes, behind an interface a test can fake
// with discordgo's own types, and the live implementation of them over a discordgo session.
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

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/state"
)

type API interface {
	Channel(id string) (*discordgo.Channel, error)
	Message(channel, id string) (*discordgo.Message, error)

	// One page of at most 100, in whatever order Discord sends it; Sort puts it right.
	Messages(channel string, limit int, before, after, around string) ([]*discordgo.Message, error)

	React(channel, message, emoji string) error

	// Takes back a reaction of mochi's own, and nobody else's.
	Unreact(channel, message, emoji string) error

	// A thread's new name. ErrRateLimited when Discord says to wait, ErrForbidden when the
	// thread is not mochi's to rename.
	RenameThread(thread, name string) error
	StartThread(channel, message, name string) (*discordgo.Channel, error)

	// A public thread on no message, which is how one mention gets several.
	NewThread(channel, name string) (*discordgo.Channel, error)

	// A post in a forum, with its tags and its first message; the post's id is that
	// message's id too.
	ForumPost(forum, name string, tags []string, first *discordgo.MessageSend) (*discordgo.Channel, error)

	// A forum post's name and tags in one edit, which Discord limits as it does a rename; the
	// errors are RenameThread's.
	EditPost(thread, name string, tags []string) error

	// Takes a thread out of its forum's list until somebody posts in it again.
	Archive(thread string) error

	// Makes a user a member of a thread, so they follow it.
	AddMember(thread, user string) error
	Send(channel string, msg *discordgo.MessageSend) (*discordgo.Message, error)

	// A message mochi posted, changed: a question's buttons disabled once it is answered.
	Edit(edit *discordgo.MessageEdit) error

	// The answer to a button press, which Discord wants within three seconds.
	Respond(i *discordgo.Interaction, r *discordgo.InteractionResponse) error

	// Every role of a guild, with the tags that say which one is a bot's.
	Roles(guild string) ([]Role, error)

	// An attachment's bytes off Discord's CDN, which needs no token.
	Fetch(url string) (io.ReadCloser, error)
}

const (
	Eyes  = "👀"
	Check = "✅"
	Cross = "❌"
)

// Every post mochi makes pings nobody: it quotes moderators' names and Tim's own mention
// back, and a session posting findings is not a reason to wake a server.
// A link in a post is for following, not for a preview card moderators have to scroll past.
func Quiet(content string) *discordgo.MessageSend {
	msg := &discordgo.MessageSend{Content: content, AllowedMentions: NoMentions()}
	if strings.Contains(content, "://") {
		msg.Flags |= discordgo.MessageFlagsSuppressEmbeds
	}
	return msg
}

func NoMentions() *discordgo.MessageAllowedMentions {
	return &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}}
}

// Pinging is a post that may mention one user and nobody else: Tim, in the two places mochi
// does — a thread it opened, and a session waiting on him.
func Pinging(user, content string) *discordgo.MessageSend {
	msg := Quiet(content)
	msg.AllowedMentions = &discordgo.MessageAllowedMentions{
		Parse: []discordgo.AllowedMentionType{},
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

func Sort(msgs []*discordgo.Message) {
	sort.SliceStable(msgs, func(i, j int) bool { return Before(msgs[i].ID, msgs[j].ID) })
}

func Name(m *discordgo.Message) string {
	if m.Member != nil && m.Member.Nick != "" {
		return m.Member.Nick
	}
	if m.Author == nil {
		return "unknown"
	}
	return m.Author.DisplayName()
}

// Taken out of every error text before it is logged or posted, whatever the library put
// in it.
func Redact(err error, token string) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	if token != "" {
		text = strings.ReplaceAll(text, token, "<token>")
	}
	return text
}

type Live struct {
	S *discordgo.Session

	// Attachments can be tens of megabytes, which discordgo's own client times out on.
	Download *http.Client
}

func NewLive(s *discordgo.Session) Live {
	return Live{S: s, Download: &http.Client{Timeout: 2 * time.Minute, CheckRedirect: stayOnCDN}}
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

func (l Live) Channel(id string) (*discordgo.Channel, error) { return l.S.Channel(id) }

func (l Live) Message(channel, id string) (*discordgo.Message, error) {
	return l.S.ChannelMessage(channel, id)
}

func (l Live) Messages(channel string, limit int, before, after, around string) ([]*discordgo.Message, error) {
	return l.S.ChannelMessages(channel, limit, before, after, around)
}

func (l Live) React(channel, message, emoji string) error {
	return l.S.MessageReactionAdd(channel, message, emoji)
}

func (l Live) Unreact(channel, message, emoji string) error {
	return l.S.MessageReactionRemove(channel, message, emoji, "@me")
}

// Gone says Discord no longer has the channel, a thread somebody deleted: nothing sent to it
// will ever land.
func Gone(err error) bool {
	var rest *discordgo.RESTError
	switch {
	case errors.Is(err, ErrUnknownChannel):
		return true
	case errors.As(err, &rest) && rest.Message != nil && rest.Message.Code == discordgo.ErrCodeUnknownChannel:
		return true
	case errors.As(err, &rest) && rest.Response != nil && rest.Response.StatusCode == http.StatusNotFound:
		return true
	}
	return false
}

var (
	ErrUnknownChannel = errors.New("Discord has no such channel")
	ErrRateLimited    = errors.New("Discord asked to wait before renaming again")
	ErrForbidden      = errors.New("the thread is not mochi's to rename")
)

// Without discordgo's own retry, which would wait out a ten-minute rename limit inside the
// call; the caller schedules the rename instead.
func (l Live) RenameThread(thread, name string) error {
	return l.edit(thread, &discordgo.ChannelEdit{Name: name})
}

func (l Live) EditPost(thread, name string, tags []string) error {
	return l.edit(thread, &discordgo.ChannelEdit{Name: name, AppliedTags: &tags})
}

func (l Live) Archive(thread string) error {
	archived := true
	return l.edit(thread, &discordgo.ChannelEdit{Archived: &archived})
}

func (l Live) edit(thread string, e *discordgo.ChannelEdit) error {
	_, err := l.S.ChannelEditComplex(thread, e, discordgo.WithRetryOnRatelimit(false))
	var limited *discordgo.RateLimitError
	var rest *discordgo.RESTError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &limited):
		return ErrRateLimited
	case errors.As(err, &rest) && rest.Response != nil && rest.Response.StatusCode == http.StatusTooManyRequests:
		return ErrRateLimited
	case errors.As(err, &rest) && rest.Response != nil && rest.Response.StatusCode == http.StatusForbidden:
		return ErrForbidden
	case errors.As(err, &rest) && rest.Message != nil && (rest.Message.Code == discordgo.ErrCodeMissingAccess || rest.Message.Code == discordgo.ErrCodeMissingPermissions):
		return ErrForbidden
	}
	return err
}

// A day, which is long enough for a session's findings to land in a thread Discord has not
// hidden yet; a post into an archived thread unarchives it anyway.
func (l Live) StartThread(channel, message, name string) (*discordgo.Channel, error) {
	return l.S.MessageThreadStartComplex(channel, message, &discordgo.ThreadStart{
		Name:                name,
		AutoArchiveDuration: 1440,
	})
}

func (l Live) Edit(edit *discordgo.MessageEdit) error {
	_, err := l.S.ChannelMessageEditComplex(edit)
	return err
}

func (l Live) Respond(i *discordgo.Interaction, r *discordgo.InteractionResponse) error {
	return l.S.InteractionRespond(i, r)
}

func (l Live) AddMember(thread, user string) error {
	return l.S.ThreadMemberAdd(thread, user)
}

func (l Live) NewThread(channel, name string) (*discordgo.Channel, error) {
	return l.S.ThreadStartComplex(channel, &discordgo.ThreadStart{
		Name:                name,
		Type:                discordgo.ChannelTypeGuildPublicThread,
		AutoArchiveDuration: 1440,
	})
}

// A week, the longest Discord offers, since a post whose session is still working should stay
// in the forum's list; mochi archives one itself three days after it is done.
func (l Live) ForumPost(forum, name string, tags []string, first *discordgo.MessageSend) (*discordgo.Channel, error) {
	return l.S.ForumThreadStartComplex(forum, &discordgo.ThreadStart{
		Name:                name,
		AutoArchiveDuration: 10080,
		AppliedTags:         tags,
	}, first)
}

func (l Live) Send(channel string, msg *discordgo.MessageSend) (*discordgo.Message, error) {
	return l.S.ChannelMessageSendComplex(channel, msg)
}

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
