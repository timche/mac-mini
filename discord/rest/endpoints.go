package rest

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/timche/mac-mini/discord"
)

func (c *Client) get(path string, out any) error {
	return c.do(request{method: http.MethodGet, path: path}, out)
}

func (c *Client) call(method, path string, body, out any, opts ...Option) error {
	r, err := c.jsonRequest(method, path, body)
	if err != nil {
		return err
	}
	return c.do(r, out, opts...)
}

// The gateway's address, which Discord asks clients to fetch rather than assume.
func (c *Client) Gateway() (string, error) {
	var g struct {
		URL string `json:"url"`
	}
	if err := c.get("/gateway", &g); err != nil {
		return "", err
	}
	if g.URL == "" {
		return "", errors.New("Discord named no gateway")
	}
	return g.URL, nil
}

func (c *Client) Me() (*discord.User, error) {
	var u discord.User
	return orNil(&u, c.get("/users/@me", &u))
}

func (c *Client) Guild(id string) (*discord.Guild, error) {
	var g discord.Guild
	return orNil(&g, c.get("/guilds/"+url.PathEscape(id), &g))
}

func (c *Client) Channel(id string) (*discord.Channel, error) {
	var ch discord.Channel
	return orNil(&ch, c.get("/channels/"+url.PathEscape(id), &ch))
}

func (c *Client) EditChannel(id string, edit *discord.ChannelEdit, opts ...Option) (*discord.Channel, error) {
	var ch discord.Channel
	return orNil(&ch, c.call(http.MethodPatch, "/channels/"+url.PathEscape(id), edit, &ch, opts...))
}

func (c *Client) Message(channel, id string) (*discord.Message, error) {
	var m discord.Message
	return orNil(&m, c.get(messages(channel)+"/"+url.PathEscape(id), &m))
}

// One page, in whatever order Discord sends it. Empty ids and a limit of 0 are left out.
func (c *Client) Messages(channel string, limit int, before, after, around string) ([]*discord.Message, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	for key, id := range map[string]string{"before": before, "after": after, "around": around} {
		if id != "" {
			q.Set(key, id)
		}
	}
	path := messages(channel)
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var page []*discord.Message
	return page, c.get(path, &page)
}

func (c *Client) Send(channel string, msg *discord.MessageSend) (*discord.Message, error) {
	var m discord.Message
	return orNil(&m, c.withFiles(http.MethodPost, messages(channel), msg, msg.Files, &m))
}

func (c *Client) EditMessage(edit *discord.MessageEdit) (*discord.Message, error) {
	var m discord.Message
	return orNil(&m, c.call(http.MethodPatch, messages(edit.Channel)+"/"+url.PathEscape(edit.ID), edit, &m))
}

func (c *Client) DeleteMessage(channel, id string) error {
	return c.call(http.MethodDelete, messages(channel)+"/"+url.PathEscape(id), nil, nil)
}

func (c *Client) ReactionAdd(channel, message, emoji string) error {
	return c.call(http.MethodPut, reaction(channel, message, emoji), nil, nil)
}

// Takes back the bot's own reaction, and nobody else's.
func (c *Client) ReactionRemove(channel, message, emoji string) error {
	return c.call(http.MethodDelete, reaction(channel, message, emoji), nil, nil)
}

func (c *Client) StartThread(channel, message string, t *discord.ThreadStart) (*discord.Channel, error) {
	var ch discord.Channel
	return orNil(&ch, c.call(http.MethodPost, messages(channel)+"/"+url.PathEscape(message)+"/threads", t, &ch))
}

func (c *Client) NewThread(channel string, t *discord.ThreadStart) (*discord.Channel, error) {
	var ch discord.Channel
	return orNil(&ch, c.call(http.MethodPost, "/channels/"+url.PathEscape(channel)+"/threads", t, &ch))
}

// A forum post and its first message in one request; the post's id is the message's too.
func (c *Client) ForumPost(forum string, t *discord.ThreadStart, first *discord.MessageSend) (*discord.Channel, error) {
	body := struct {
		*discord.ThreadStart
		Message *discord.MessageSend `json:"message"`
	}{t, first}
	var ch discord.Channel
	return orNil(&ch, c.withFiles(http.MethodPost, "/channels/"+url.PathEscape(forum)+"/threads", body, first.Files, &ch))
}

func (c *Client) AddThreadMember(thread, user string) error {
	return c.call(http.MethodPut, "/channels/"+url.PathEscape(thread)+"/thread-members/"+url.PathEscape(user), nil, nil)
}

// The answer to an interaction, sent with the interaction's own token.
func (c *Client) Respond(i *discord.Interaction, r *discord.InteractionResponse) error {
	return c.call(http.MethodPost, "/interactions/"+url.PathEscape(i.ID)+"/"+url.PathEscape(i.Token)+"/callback", r, nil)
}

func (c *Client) withFiles(method, path string, payload any, files []*discord.File, out any) error {
	if len(files) == 0 {
		return c.call(method, path, payload, out)
	}
	r, err := multipartBody(payload, files)
	if err != nil {
		return err
	}
	r.method, r.path = method, path
	return c.do(r, out)
}

func orNil[T any](v *T, err error) (*T, error) {
	if err != nil {
		return nil, err
	}
	return v, nil
}

func messages(channel string) string { return "/channels/" + url.PathEscape(channel) + "/messages" }

func reaction(channel, message, emoji string) string {
	return messages(channel) + "/" + url.PathEscape(message) + "/reactions/" + url.PathEscape(emoji) + "/@me"
}
