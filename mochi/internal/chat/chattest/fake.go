// Package chattest is an in-memory Discord for the tests of every package that talks to one,
// answering in discordgo's own types and paging the way the API documents.
package chattest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/chat"
)

type Sent struct {
	Channel string
	Msg     *discordgo.MessageSend
	Files   map[string]string
}

type Fake struct {
	Channels map[string]*discordgo.Channel
	Msgs     map[string][]*discordgo.Message
	Files    map[string][]byte

	Reactions []string
	Threads   []string
	Sent      []Sent
	Pages     []string

	ThreadErr error
	next      int
}

func New() *Fake {
	return &Fake{
		Channels: map[string]*discordgo.Channel{},
		Msgs:     map[string][]*discordgo.Message{},
		Files:    map[string][]byte{},
	}
}

var Epoch = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

// Ids are numbers and so is time, so message n of a test is at Epoch plus n minutes and
// every id sorts in the order it was posted.
func ID(n int) string { return strconv.Itoa(1_000_000_000_000_000_000 + n) }

func (f *Fake) AddChannel(c *discordgo.Channel) { f.Channels[c.ID] = c }

func (f *Fake) Post(channel string, n int, author *discordgo.User, content string) *discordgo.Message {
	m := &discordgo.Message{
		ID:        ID(n),
		ChannelID: channel,
		Content:   content,
		Author:    author,
		Timestamp: Epoch.Add(time.Duration(n) * time.Minute),
	}
	if c, ok := f.Channels[channel]; ok {
		m.GuildID = c.GuildID
	}
	f.Msgs[channel] = append(f.Msgs[channel], m)
	chat.Sort(f.Msgs[channel])
	return m
}

func (f *Fake) Channel(id string) (*discordgo.Channel, error) {
	if c, ok := f.Channels[id]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("unknown channel %s", id)
}

func (f *Fake) Message(channel, id string) (*discordgo.Message, error) {
	for _, m := range f.Msgs[channel] {
		if m.ID == id {
			return m, nil
		}
	}
	return nil, fmt.Errorf("unknown message %s in %s", id, channel)
}

// Newest first, except for an after, which is how Discord documents it and why nothing may
// rely on the order a page arrives in.
func (f *Fake) Messages(channel string, limit int, before, after, around string) ([]*discordgo.Message, error) {
	if _, ok := f.Channels[channel]; !ok {
		return nil, fmt.Errorf("unknown channel %s", channel)
	}
	f.Pages = append(f.Pages, fmt.Sprintf("%s before=%s after=%s around=%s limit=%d", channel, before, after, around, limit))
	if limit <= 0 || limit > 100 {
		return nil, errors.New("limit must be 1 to 100")
	}

	all := f.Msgs[channel]
	var out []*discordgo.Message
	switch {
	case around != "":
		pivot := sort.Search(len(all), func(i int) bool { return !chat.Before(all[i].ID, around) })
		lo := max(0, pivot-limit/2)
		hi := min(len(all), lo+limit)
		out = append(out, all[lo:hi]...)
	case after != "":
		for _, m := range all {
			if chat.Before(after, m.ID) && len(out) < limit {
				out = append(out, m)
			}
		}
		return out, nil
	default:
		for _, m := range all {
			if before == "" || chat.Before(m.ID, before) {
				out = append(out, m)
			}
		}
		out = out[max(0, len(out)-limit):]
	}
	reversed := make([]*discordgo.Message, len(out))
	for i, m := range out {
		reversed[len(out)-1-i] = m
	}
	return reversed, nil
}

func (f *Fake) React(channel, message, emoji string) error {
	f.Reactions = append(f.Reactions, message+" "+emoji)
	return nil
}

func (f *Fake) StartThread(channel, message, name string) (*discordgo.Channel, error) {
	if f.ThreadErr != nil {
		return nil, f.ThreadErr
	}
	parent := f.Channels[channel]
	thread := &discordgo.Channel{ID: message, GuildID: parent.GuildID, ParentID: channel, Name: name, Type: discordgo.ChannelTypeGuildPublicThread}
	f.AddChannel(thread)
	f.Threads = append(f.Threads, message+" "+name)
	return thread, nil
}

func (f *Fake) Send(channel string, msg *discordgo.MessageSend) (*discordgo.Message, error) {
	files := map[string]string{}
	for _, file := range msg.Files {
		body, _ := io.ReadAll(file.Reader)
		files[file.Name] = string(body)
	}
	f.Sent = append(f.Sent, Sent{Channel: channel, Msg: msg, Files: files})
	f.next++
	return &discordgo.Message{ID: ID(900_000 + f.next), ChannelID: channel, Content: msg.Content}, nil
}

func (f *Fake) Fetch(url string) (io.ReadCloser, error) {
	body, ok := f.Files[url]
	if !ok {
		return nil, fmt.Errorf("404 for %s", url)
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

func (f *Fake) ReactedWith(message, emoji string) bool {
	for _, r := range f.Reactions {
		if r == message+" "+emoji {
			return true
		}
	}
	return false
}
