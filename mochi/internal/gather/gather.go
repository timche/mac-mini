// Package gather decides which Discord messages a session is handed and writes them out as
// one transcript. The rule behind every window here: moderators suggest a feature or report
// an issue and discuss it, mostly in plain messages rather than replies, so the context has
// to hold the discussion and not only the latest messages.
package gather

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/chat"
)

const (
	// Discord's own cap on one page, and so on one `around`: about fifty either side.
	Page = 100

	ChannelWindow = 50
	ThreadWindow  = 100

	// A reply chain is followed this far up, and later replies to the message are searched
	// for this many pages after it, which bounds a mention of a month-old message to a
	// handful of calls.
	chainHops  = 25
	laterPages = 5
)

const (
	NoteReplied = "the message Tim replied to"
	NoteStarter = "the message this thread started from"
	NoteChain   = "earlier in the reply chain"
)

// Set is the messages chosen for a session, each at most once, with what is notable about
// some of them and what was left out.
type Set struct {
	Guild string
	msgs  map[string]*discordgo.Message
	notes map[string][]string
	Gaps  []string
}

func NewSet(guild string) *Set {
	return &Set{Guild: guild, msgs: map[string]*discordgo.Message{}, notes: map[string][]string{}}
}

func (s *Set) Add(m *discordgo.Message, notes ...string) {
	if m == nil {
		return
	}
	if _, ok := s.msgs[m.ID]; !ok {
		s.msgs[m.ID] = m
	}
	s.Note(m.ID, notes...)
}

func (s *Set) Note(id string, notes ...string) {
	for _, n := range notes {
		if !contains(s.notes[id], n) {
			s.notes[id] = append(s.notes[id], n)
		}
	}
}

func (s *Set) Has(id string) bool { return s.msgs[id] != nil }

func (s *Set) Remove(id string) { delete(s.msgs, id) }

func (s *Set) Len() int { return len(s.msgs) }

// The oldest message that is neither the one Tim replied to nor a thread's starter goes,
// and false when only those are left.
func (s *Set) DropOldest() bool {
	for _, m := range s.Messages() {
		if contains(s.notes[m.ID], NoteReplied) || contains(s.notes[m.ID], NoteStarter) {
			continue
		}
		s.Remove(m.ID)
		return true
	}
	return false
}

func (s *Set) Messages() []*discordgo.Message {
	out := make([]*discordgo.Message, 0, len(s.msgs))
	for _, m := range s.msgs {
		out = append(out, m)
	}
	chat.Sort(out)
	return out
}

// Tim replied to a message in a channel: the message, the chain of replies it answers, the
// discussion either side of it, and anything later that replied to it. Tim's own mention is
// the instruction and is not part of the context.
func Reply(api chat.API, mention *discordgo.Message) (*Set, error) {
	set := NewSet(mention.GuildID)
	channel := mention.ChannelID

	target, err := Referenced(api, mention)
	if err != nil {
		return nil, fmt.Errorf("the message Tim replied to could not be read: %w", err)
	}
	set.Add(target, NoteReplied)

	cur := target
	for hop := 0; hop < chainHops && cur.MessageReference != nil; hop++ {
		parent, err := Referenced(api, cur)
		if err != nil {
			set.Gaps = append(set.Gaps, "the reply chain above "+chat.Link(set.Guild, channel, cur.ID)+" could not be read further")
			break
		}
		if parent.ChannelID != "" && parent.ChannelID != channel {
			break
		}
		set.Add(parent, NoteChain)
		cur = parent
	}

	around, err := api.Messages(channel, Page, "", "", target.ID)
	if err != nil {
		return nil, fmt.Errorf("the discussion around the message could not be read: %w", err)
	}
	for _, m := range around {
		set.Add(m)
	}

	after, reached := target.ID, false
	for page := 0; page < laterPages && !reached; page++ {
		got, err := api.Messages(channel, Page, "", after, "")
		if err != nil || len(got) == 0 {
			reached = err == nil
			break
		}
		chat.Sort(got)
		for _, m := range got {
			if !chat.Before(m.ID, mention.ID) {
				reached = true
				break
			}
			if m.MessageReference != nil && m.MessageReference.MessageID == target.ID {
				set.Add(m)
			}
		}
		after = got[len(got)-1].ID
	}
	if !reached {
		set.Gaps = append(set.Gaps, "later replies to that message were searched only as far as "+chat.Link(set.Guild, channel, after))
	}

	set.Remove(mention.ID)
	return set, nil
}

// A mention inside a thread or a forum post: the whole of it from its starter, as far as
// one page before the mention reaches, and the starter on its own when it does not.
func Thread(api chat.API, thread *discordgo.Channel, mention *discordgo.Message) (*Set, error) {
	set := NewSet(mention.GuildID)

	page, err := api.Messages(thread.ID, ThreadWindow, mention.ID, "", "")
	if err != nil {
		return nil, fmt.Errorf("the thread could not be read: %w", err)
	}
	for _, m := range page {
		set.Add(m)
	}

	if !set.Has(thread.ID) {
		// A forum post's starter is inside the post, and a thread opened on a channel message
		// has that message in the parent under the thread's own id.
		starter, err := api.Message(thread.ID, thread.ID)
		if err != nil && thread.ParentID != "" {
			starter, err = api.Message(thread.ParentID, thread.ID)
		}
		if err == nil {
			set.Add(starter, NoteStarter)
			if len(page) == ThreadWindow {
				oldest := set.Messages()[1].ID
				set.Gaps = append(set.Gaps, "messages between the starter and "+chat.Link(set.Guild, thread.ID, oldest)+" are not shown")
			}
		}
	} else {
		set.Note(thread.ID, NoteStarter)
	}

	if err := markReplied(api, set, mention); err != nil {
		set.Gaps = append(set.Gaps, err.Error())
	}
	return set, nil
}

// A mention in a channel that answers nothing in particular: the recent conversation.
func Channel(api chat.API, mention *discordgo.Message) (*Set, error) {
	set := NewSet(mention.GuildID)

	page, err := api.Messages(mention.ChannelID, ChannelWindow, mention.ID, "", "")
	if err != nil {
		return nil, fmt.Errorf("the channel could not be read: %w", err)
	}
	for _, m := range page {
		set.Add(m)
	}
	return set, nil
}

// What others wrote in a session's thread between Tim's last mention and this one. Tim's
// own messages and mochi's are not news to the session; everybody else's are, bots
// included, as data.
func Since(api chat.API, thread, after string, mention *discordgo.Message, skip ...string) (*Set, error) {
	set := NewSet(mention.GuildID)

	for page := 0; page < laterPages; page++ {
		got, err := api.Messages(thread, Page, "", after, "")
		if err != nil {
			return nil, fmt.Errorf("the thread could not be read: %w", err)
		}
		if len(got) == 0 {
			return set, nil
		}
		chat.Sort(got)
		for _, m := range got {
			if !chat.Before(m.ID, mention.ID) {
				return set, nil
			}
			if m.Author != nil && !contains(skip, m.Author.ID) {
				set.Add(m)
			}
		}
		after = got[len(got)-1].ID
	}
	set.Gaps = append(set.Gaps, "messages after "+chat.Link(set.Guild, thread, after)+" and before this mention were not read")
	return set, nil
}

// One page of a channel, which is what `mochi history` hands a session.
func Paged(api chat.API, guild, channel string, limit int, before, after, around string) (*Set, error) {
	got, err := api.Messages(channel, limit, before, after, around)
	if err != nil {
		return nil, err
	}
	set := NewSet(guild)
	for _, m := range got {
		set.Add(m)
	}
	return set, nil
}

func markReplied(api chat.API, set *Set, mention *discordgo.Message) error {
	if mention.MessageReference == nil {
		return nil
	}
	target, err := Referenced(api, mention)
	if err != nil {
		return errors.New("the message Tim replied to could not be read")
	}
	set.Add(target, NoteReplied)
	return nil
}

func Referenced(api chat.API, m *discordgo.Message) (*discordgo.Message, error) {
	if m.ReferencedMessage != nil {
		return m.ReferencedMessage, nil
	}
	if m.MessageReference == nil {
		return nil, errors.New("it replies to nothing")
	}
	channel := m.MessageReference.ChannelID
	if channel == "" {
		channel = m.ChannelID
	}
	return api.Message(channel, m.MessageReference.MessageID)
}

// Files is each attachment's line in the transcript, by attachment id.
type Files map[string]string

// Every message carries its author, its time and its link, so the session can quote it and
// Tim can open it.
func Render(set *Set, files Files) string {
	var b strings.Builder
	for _, gap := range set.Gaps {
		b.WriteString("Not shown: " + gap + ".\n")
	}
	if len(set.Gaps) > 0 {
		b.WriteString("\n")
	}

	for i, m := range set.Messages() {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "[%s] %s — %s\n", stamp(m.Timestamp), chat.Name(m), chat.Link(set.Guild, m.ChannelID, m.ID))
		for _, n := range set.notes[m.ID] {
			b.WriteString("(" + n + ")\n")
		}
		if ref := m.MessageReference; ref != nil && ref.MessageID != "" && m.Type != discordgo.MessageTypeThreadStarterMessage {
			channel := ref.ChannelID
			if channel == "" {
				channel = m.ChannelID
			}
			b.WriteString("replying to " + chat.Link(set.Guild, channel, ref.MessageID) + "\n")
		}
		if text := strings.TrimSpace(m.Content); text != "" {
			b.WriteString(text + "\n")
		} else if len(m.Attachments) == 0 {
			b.WriteString("(no text)\n")
		}
		for _, a := range m.Attachments {
			line, ok := files[a.ID]
			if !ok {
				line = "not downloaded: " + a.Filename
			}
			b.WriteString("attachment: " + line + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
