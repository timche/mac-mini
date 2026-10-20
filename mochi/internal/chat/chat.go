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
func Quiet(content string) *discordgo.MessageSend {
	return &discordgo.MessageSend{Content: content, AllowedMentions: NoMentions()}
}

func NoMentions() *discordgo.MessageAllowedMentions {
	return &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}}
}

func Link(guild, channel, message string) string {
	link := "https://discord.com/channels/" + guild + "/" + channel
	if message != "" {
		link += "/" + message
	}
	return link
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
	_, err := l.S.ChannelEditComplex(thread, &discordgo.ChannelEdit{Name: name}, discordgo.WithRetryOnRatelimit(false))
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

func (l Live) NewThread(channel, name string) (*discordgo.Channel, error) {
	return l.S.ThreadStartComplex(channel, &discordgo.ThreadStart{
		Name:                name,
		Type:                discordgo.ChannelTypeGuildPublicThread,
		AutoArchiveDuration: 1440,
	})
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
