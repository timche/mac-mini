// Package amend is `mochi edit` and `mochi delete`, which let a session that posted something
// that should not be there fix it at once: only a message of mochi's own, and only where mochi
// posts in a server it is mapped to.
package amend

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/timche/mac-mini/discord"

	"github.com/timche/mac-mini/mochi/internal/chat"
	"github.com/timche/mac-mini/mochi/internal/config"
	"github.com/timche/mac-mini/mochi/internal/leaks"
	"github.com/timche/mac-mini/mochi/internal/post"
	"github.com/timche/mac-mini/mochi/internal/state"
)

// Discord's own limit for a message, which an edit cannot split as a post does.
const maxLength = 2000

var messageLink = regexp.MustCompile(`\Ahttps://(?:(?:ptb|canary)\.)?discord\.com/channels/([0-9]{17,20})/([0-9]{17,20})/([0-9]{17,20})\z`)

type Target struct{ Guild, Channel, Message string }

func Parse(link string) (Target, error) {
	m := messageLink.FindStringSubmatch(link)
	if m == nil {
		return Target{}, fmt.Errorf("%q is not a message's link: copy it in Discord with Copy Message Link, https://discord.com/channels/<server>/<channel>/<message>", link)
	}
	return Target{Guild: m[1], Channel: m[2], Message: m[3]}, nil
}

// Scope is where mochi posts: each server's announce channel, the threads and forum posts it
// started, and the threads its sessions work in.
type Scope struct {
	Servers []config.Server
	Store   state.Store
}

// Server is the mapped server a link is in.
func (s Scope) Server(link string) (Target, config.Server, error) {
	t, err := Parse(link)
	if err != nil {
		return t, config.Server{}, err
	}
	for _, server := range s.Servers {
		if server.Guild == t.Guild {
			return t, server, nil
		}
	}
	return t, config.Server{}, errors.New("the message is in a server mochi is not mapped to, so it is not mochi's to change")
}

// Change is an edit's new text and what goes with it.
type Change struct {
	Text      string
	PR        string
	DropFiles bool

	// The secret scan the new text goes through; nil scans nothing.
	Leaks leaks.Runner

	// The checkout whose own scanner configuration applies, if there is one.
	Base string

	Repos post.Repos
}

// Check holds the text to a post's rules for a server with these domains.
func (c Change) Check(links []string) error {
	if strings.TrimSpace(c.Text) == "" {
		return errors.New("an edit takes the new text, as an argument or on stdin")
	}
	if err := post.CheckText(c.Text, links); err != nil {
		return err
	}
	if c.PR != "" {
		if _, err := post.PRLine(c.PR); err != nil {
			return err
		}
	}
	return nil
}

func Edit(api chat.API, s Scope, link string, c Change) error {
	t, server, m, err := s.find(api, link)
	if err != nil {
		return err
	}
	if err := c.Check(server.Links); err != nil {
		return err
	}
	if c.PR != "" {
		if err := post.CheckRepo(c.PR, server.Guild, c.Repos); err != nil {
			return err
		}
	}
	var kept []string
	if !c.DropFiles {
		for _, a := range m.Attachments {
			kept = append(kept, a.Filename)
		}
	}
	if err := post.CheckProof(c.Text, kept); err != nil {
		return err
	}
	var bases []string
	if c.Base != "" {
		bases = []string{c.Base}
	}
	if err := post.Scan(c.Leaks, bases, c.Text, nil); err != nil {
		return err
	}

	text := strings.TrimSpace(c.Text)
	var flags discord.MessageFlags
	if c.PR != "" {
		line, _ := post.PRLine(c.PR)
		text += "\n\n" + line
		flags |= discord.MessageFlagsSuppressEmbeds
	}
	if n := utf8.RuneCountInString(text); n > maxLength {
		return fmt.Errorf("the new text is %d characters, over the %d Discord takes for one message: shorten it", n, maxLength)
	}
	edit := &discord.MessageEdit{ID: t.Message, Channel: t.Channel, Content: &text, AllowedMentions: chat.NoMentions(), Flags: &flags}
	if c.DropFiles {
		none := []discord.MessageAttachment{}
		edit.Attachments = &none
	}
	return api.Edit(edit)
}

func Delete(api chat.API, s Scope, link string) error {
	t, _, _, err := s.find(api, link)
	if err != nil {
		return err
	}
	return api.Delete(t.Channel, t.Message)
}

// find is the message a link names, once it is known to be mochi's own in a channel mochi posts
// in, in the server the link says.
func (s Scope) find(api chat.API, link string) (Target, config.Server, *discord.Message, error) {
	t, server, err := s.Server(link)
	if err != nil {
		return t, server, nil, err
	}
	ch, err := api.Channel(t.Channel)
	if err != nil {
		return t, server, nil, fmt.Errorf("the channel could not be read: %v", err)
	}
	if ch.GuildID != t.Guild {
		return t, server, nil, errors.New("the link's channel is not in the server it names")
	}
	me, err := api.Me()
	if err != nil {
		return t, server, nil, fmt.Errorf("mochi's own user could not be read: %v", err)
	}
	_, session, err := s.Store.Get(t.Channel)
	if err != nil {
		return t, server, nil, err
	}
	owned := ch.IsThread() && ch.OwnerID == me.ID
	if t.Channel != server.Announce && !session && !owned {
		return t, server, nil, errors.New("the message is not where mochi posts — its announce channel, a thread or forum post it started, or a session's thread — so it is not changed")
	}
	m, err := api.Message(t.Channel, t.Message)
	if err != nil {
		return t, server, nil, fmt.Errorf("the message could not be read: %v", err)
	}
	if m.Author == nil || m.Author.ID != me.ID {
		return t, server, nil, errors.New("the message is not mochi's own, so it is not changed")
	}
	return t, server, m, nil
}
