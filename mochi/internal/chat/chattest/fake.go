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
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/chat"
)

type Sent struct {
	ID      string
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

	Standalone []string
	Members    []string

	Unreactions []string
	Renames     []string
	RenameErr   map[string]error

	// Forum posts as "<forum> <name>", each post's tags as last applied, and the threads
	// archived.
	Posts      []string
	Tagged     map[string][]string
	Archived   []string
	ArchiveErr error

	// Refuses an edit that carries tags, as Discord does without the permission for them; and
	// how many edits carried them.
	TagErr    error
	PostEdits int

	Edits     []*discordgo.MessageEdit
	Responses []*discordgo.InteractionResponse

	GuildRoles map[string][]chat.Role
	RoleReads  int

	// Runs while a roles request is in flight, for what the gateway says meanwhile.
	DuringRoles func()

	// mochi's own user, as Me answers, and each guild's boost tier.
	Bot   *discordgo.User
	Tiers map[string]discordgo.PremiumTier

	ThreadErr  error
	SendErr    map[string]error
	Tries      map[string]int
	next       int
	newThreads int
}

func New() *Fake {
	return &Fake{
		Channels:  map[string]*discordgo.Channel{},
		Msgs:      map[string][]*discordgo.Message{},
		Files:     map[string][]byte{},
		RenameErr: map[string]error{},
		Tagged:    map[string][]string{},
		SendErr:   map[string]error{},
		Tries:     map[string]int{},
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

// As Discord answers a message fetched by id: without its guild.
func (f *Fake) Message(channel, id string) (*discordgo.Message, error) {
	for _, m := range f.Msgs[channel] {
		if m.ID == id {
			fetched := *m
			fetched.GuildID = ""
			return &fetched, nil
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

func (f *Fake) Unreact(channel, message, emoji string) error {
	f.Unreactions = append(f.Unreactions, message+" "+emoji)
	return nil
}

// What mochi's reactions on a message are now: every one added and not taken back.
func (f *Fake) Showing(message string) []string {
	count := map[string]int{}
	var order []string
	for _, r := range f.Reactions {
		if id, emoji, _ := strings.Cut(r, " "); id == message {
			if count[emoji] == 0 {
				order = append(order, emoji)
			}
			count[emoji]++
		}
	}
	for _, r := range f.Unreactions {
		if id, emoji, _ := strings.Cut(r, " "); id == message {
			count[emoji]--
		}
	}
	var out []string
	for _, emoji := range order {
		if count[emoji] > 0 {
			out = append(out, emoji)
		}
	}
	return out
}

func (f *Fake) RenameThread(thread, name string) error {
	if err := f.RenameErr[thread]; err != nil {
		return err
	}
	if c, ok := f.Channels[thread]; ok {
		c.Name = name
	}
	f.Renames = append(f.Renames, thread+" "+name)
	return nil
}

func (f *Fake) EditPost(thread, name string, tags []string) error {
	if err := f.RenameErr[thread]; err != nil {
		return err
	}
	if f.TagErr != nil {
		return f.TagErr
	}
	if c, ok := f.Channels[thread]; ok {
		c.Name, c.AppliedTags = name, tags
	}
	f.Renames = append(f.Renames, thread+" "+name)
	f.Tagged[thread] = tags
	f.PostEdits++
	return nil
}

func (f *Fake) Archive(thread string) error {
	if f.ArchiveErr != nil {
		return f.ArchiveErr
	}
	f.Archived = append(f.Archived, thread)
	return nil
}

// Posts are numbered from 700000, and the first message is sent into the post under its id.
func (f *Fake) ForumPost(forum, name string, tags []string, first *discordgo.MessageSend) (*discordgo.Channel, error) {
	if f.ThreadErr != nil {
		return nil, f.ThreadErr
	}
	parent := f.Channels[forum]
	f.newThreads++
	id := ID(700_000 + f.newThreads)
	post := &discordgo.Channel{ID: id, GuildID: parent.GuildID, ParentID: forum, Name: name, Type: discordgo.ChannelTypeGuildPublicThread, AppliedTags: tags}
	f.AddChannel(post)
	f.Posts = append(f.Posts, forum+" "+name)
	f.Tagged[id] = tags
	f.Sent = append(f.Sent, Sent{ID: id, Channel: id, Msg: first})
	return post, nil
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

// New threads are numbered from 800000 so a test can tell them from messages.
func (f *Fake) NewThread(channel, name string) (*discordgo.Channel, error) {
	if f.ThreadErr != nil {
		return nil, f.ThreadErr
	}
	parent := f.Channels[channel]
	f.newThreads++
	thread := &discordgo.Channel{ID: ID(800_000 + f.newThreads), GuildID: parent.GuildID, ParentID: channel, Name: name, Type: discordgo.ChannelTypeGuildPublicThread}
	f.AddChannel(thread)
	f.Standalone = append(f.Standalone, channel+" "+name)
	return thread, nil
}

func (f *Fake) AddMember(thread, user string) error {
	f.Members = append(f.Members, thread+" "+user)
	return nil
}

func (f *Fake) Send(channel string, msg *discordgo.MessageSend) (*discordgo.Message, error) {
	f.Tries[channel]++
	if err := f.SendErr[channel]; err != nil {
		return nil, err
	}
	files := map[string]string{}
	for _, file := range msg.Files {
		body, _ := io.ReadAll(file.Reader)
		files[file.Name] = string(body)
	}
	f.next++
	id := ID(900_000 + f.next)
	f.Sent = append(f.Sent, Sent{ID: id, Channel: channel, Msg: msg, Files: files})
	return &discordgo.Message{ID: id, ChannelID: channel, Content: msg.Content}, nil
}

func (f *Fake) Edit(edit *discordgo.MessageEdit) error {
	f.Edits = append(f.Edits, edit)
	return nil
}

func (f *Fake) Respond(i *discordgo.Interaction, r *discordgo.InteractionResponse) error {
	f.Responses = append(f.Responses, r)
	return nil
}

func (f *Fake) Roles(guild string) ([]chat.Role, error) {
	f.RoleReads++
	if f.DuringRoles != nil {
		f.DuringRoles()
	}
	roles, ok := f.GuildRoles[guild]
	if !ok {
		return nil, fmt.Errorf("unknown guild %s", guild)
	}
	return roles, nil
}

func (f *Fake) Fetch(url string) (io.ReadCloser, error) {
	body, ok := f.Files[url]
	if !ok {
		return nil, fmt.Errorf("404 for %s", url)
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

func (f *Fake) Me() (*discordgo.User, error) {
	if f.Bot == nil {
		return nil, fmt.Errorf("no bot user")
	}
	return f.Bot, nil
}

func (f *Fake) Guild(id string) (*discordgo.Guild, error) {
	return &discordgo.Guild{ID: id, PremiumTier: f.Tiers[id]}, nil
}
