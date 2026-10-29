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

// Scope is where a session may change mochi's messages: its server's announce channel, which
// any session posts to, and a session of mochi's own thread or post, which only it posts to.
type Scope struct {
	Servers []config.Server
	Store   state.Store

	// MOCHI_THREAD, empty in a session mochi did not start.
	Thread string
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
	settings, unread := server.Project(nil)
	if err := c.Check(settings.Links); err != nil {
		return post.NoLinksBecause(err, unread)
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

	text := post.WithPR(strings.TrimSpace(c.Text), c.PR)
	var flags discord.MessageFlags
	if c.PR != "" {
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
	// Deleting a thread's or a forum post's first message leaves the post with nothing on
	// top; an edit fixes it instead.
	if t.Message == t.Channel {
		return errors.New("the message starts its thread or post, so it is not deleted: change it with mochi edit instead")
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
	settings, unread := server.Project(nil)
	if t.Channel != settings.Announce {
		if s.Thread == "" || t.Channel != s.Thread {
			if unread != nil {
				return t, server, nil, fmt.Errorf("the message is not in this session's own thread, and the server's announce channel is not known, since %v, so it is not changed", unread)
			}
			return t, server, nil, errors.New("the message is neither in the server's announce channel nor in this session's own thread, so it is not changed")
		}
		// MOCHI_THREAD alone would let a session name any thread.
		if _, ok, err := s.Store.Get(t.Channel); err != nil || !ok {
			return t, server, nil, errors.New("MOCHI_THREAD names no thread mochi started a session for, so it is not changed")
		}
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
