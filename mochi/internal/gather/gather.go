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

	"github.com/timche/mac-mini/discord"

	"github.com/timche/mac-mini/mochi/internal/chat"
)

const (
	// Discord's own cap on one page, and so on one `around`: about fifty either side.
	Page = 100

	ChannelWindow = 50

	// What a mention that replies to nothing is about: the last few messages before it.
	FocusWindow = 5

	// How many of the later replies to the message Tim replied to count as the focus.
	FocusReplies = 50

	ThreadWindow = 100

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
	NoteMention = "Tim's mention of mochi"

	// The two halves of a session's context. Everything gathered is kept, but most of a busy
	// channel is about something else, and the session has to know which part Tim meant.
	FocusHeading       = "FOCUS"
	SurroundingHeading = "SURROUNDING CHANNEL (may be unrelated; read only what bears on the focus)"
)

// Set is the messages chosen for a session, each at most once, with what is notable about
// some of them and what was left out.
type Set struct {
	Guild string
	msgs  map[string]*discord.Message
	notes map[string][]string
	focus map[string]bool
	Gaps  []string

	// Tim's account, labelled as the author of the instruction so a moderator who calls
	// themselves Tim cannot pass for him.
	Tim string
}

func NewSet(guild string) *Set {
	return &Set{Guild: guild, msgs: map[string]*discord.Message{}, notes: map[string][]string{}, focus: map[string]bool{}}
}

// What Tim's instruction is about, rendered first under its own heading and never trimmed.
func (s *Set) Focus(m *discord.Message, notes ...string) {
	if m == nil {
		return
	}
	s.Add(m, notes...)
	s.focus[m.ID] = true
}

func (s *Set) Add(m *discord.Message, notes ...string) {
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

// The oldest surrounding message goes, and false when only the focus is left.
func (s *Set) DropOldest() bool {
	for _, m := range s.Messages() {
		if s.focus[m.ID] {
			continue
		}
		s.Remove(m.ID)
		return true
	}
	return false
}

func (s *Set) Messages() []*discord.Message {
	out := make([]*discord.Message, 0, len(s.msgs))
	for _, m := range s.msgs {
		out = append(out, m)
	}
	chat.Sort(out)
	return out
}

// Tim replied to a message in a channel: the message, the chain of replies it answers, the
// discussion either side of it, and anything later that replied to it. Tim's own mention is
// the instruction and is not part of the context.
func Reply(api chat.API, mention *discord.Message) (*Set, error) {
	set := NewSet(mention.GuildID)
	set.Tim = authorID(mention)
	channel := mention.ChannelID

	target, err := Referenced(api, mention)
	if err != nil {
		return nil, fmt.Errorf("the message Tim replied to could not be read: %w", err)
	}
	set.Focus(target, NoteReplied)

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
		set.Focus(parent, NoteChain)
		cur = parent
	}

	around, err := api.Messages(channel, Page, "", "", target.ID)
	if err != nil {
		return nil, fmt.Errorf("the discussion around the message could not be read: %w", err)
	}
	for _, m := range around {
		set.Add(m)
	}

	var replies []*discord.Message
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
				replies = append(replies, m)
			}
		}
		after = got[len(got)-1].ID
	}
	if !reached {
		set.Gaps = append(set.Gaps, "later replies to that message were searched only as far as "+chat.Link(set.Guild, channel, after))
	}
	// The most recent of them are the focus and the rest stay as surrounding, so a message
	// replied to hundreds of times cannot make a focus the prompt cap has to break.
	for i, m := range replies {
		if i >= len(replies)-FocusReplies {
			set.Focus(m)
		} else {
			set.Add(m)
		}
	}

	set.Remove(mention.ID)
	return set, nil
}

// A mention inside a thread or a forum post: the whole of it from its starter, as far as
// one page before the mention reaches, and the starter on its own when it does not. The
// focus is the starter and the mention itself, with what it replies to.
func Thread(api chat.API, thread *discord.Channel, mention *discord.Message) (*Set, error) {
	set := NewSet(mention.GuildID)
	set.Tim = authorID(mention)

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
			set.Focus(starter, NoteStarter)
			if len(page) == ThreadWindow {
				oldest := set.Messages()[1].ID
				set.Gaps = append(set.Gaps, "messages between the starter and "+chat.Link(set.Guild, thread.ID, oldest)+" are not shown")
			}
		}
	} else {
		set.Focus(set.msgs[thread.ID], NoteStarter)
	}
	set.Focus(mention, NoteMention)

	if err := markReplied(api, set, mention); err != nil {
		set.Gaps = append(set.Gaps, err.Error())
	}
	return set, nil
}

// A mention in a channel that answers nothing in particular: the recent conversation, the
// last few messages of it the focus.
func Channel(api chat.API, mention *discord.Message) (*Set, error) {
	set := NewSet(mention.GuildID)
	set.Tim = authorID(mention)

	page, err := api.Messages(mention.ChannelID, ChannelWindow, mention.ID, "", "")
	if err != nil {
		return nil, fmt.Errorf("the channel could not be read: %w", err)
	}
	chat.Sort(page)
	for i, m := range page {
		if i >= len(page)-FocusWindow {
			set.Focus(m)
		} else {
			set.Add(m)
		}
	}
	return set, nil
}

// Everything posted in a session's thread between Tim's last mention and this one: the
// moderators' answers, his own messages, and what the session itself posted, marked as its own
// so it can tell what it said from what was said back.
func Since(api chat.API, thread, after string, mention *discord.Message, self string) (*Set, error) {
	set := NewSet(mention.GuildID)
	set.Tim = authorID(mention)

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
			if said(m, self) {
				set.Add(m)
			}
		}
		after = got[len(got)-1].ID
	}
	set.Gaps = append(set.Gaps, "messages after "+chat.Link(set.Guild, thread, after)+" and before this mention were not read")
	return set, nil
}

// One page of a channel, which is what `mochi history` hands a session.
func Paged(api chat.API, guild, channel string, limit int, before, after, around, self string) (*Set, error) {
	got, err := api.Messages(channel, limit, before, after, around)
	if err != nil {
		return nil, err
	}
	set := NewSet(guild)
	for _, m := range got {
		if said(m, self) {
			set.Add(m)
		}
	}
	return set, nil
}

// What somebody said, as against mochi's own posts — the session's, the pings, the
// "Created" replies — and the notices Discord writes itself: a rename, a pin, a thread made. A reply
// is said, and keeps the message it answers as a link, whoever wrote that.
func said(m *discord.Message, self string) bool {
	if m.Author == nil || (self != "" && m.Author.ID == self) {
		return false
	}
	return m.Type == discord.MessageTypeDefault || m.Type == discord.MessageTypeReply
}

func markReplied(api chat.API, set *Set, mention *discord.Message) error {
	if mention.MessageReference == nil {
		return nil
	}
	target, err := Referenced(api, mention)
	if err != nil {
		return errors.New("the message Tim replied to could not be read")
	}
	set.Focus(target, NoteReplied)
	return nil
}

func Referenced(api chat.API, m *discord.Message) (*discord.Message, error) {
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
// Tim can open it. A set with a focus is two sections, the focus first, each in time order.
func Render(set *Set, files Files) string {
	var b strings.Builder
	for _, gap := range set.Gaps {
		b.WriteString("Not shown: " + gap + ".\n")
	}
	if len(set.Gaps) > 0 {
		b.WriteString("\n")
	}

	var focus, rest []*discord.Message
	for _, m := range set.Messages() {
		if set.focus[m.ID] {
			focus = append(focus, m)
		} else {
			rest = append(rest, m)
		}
	}
	if len(focus) == 0 {
		writeMessages(&b, set, rest, files)
		return strings.TrimRight(b.String(), "\n")
	}

	b.WriteString(FocusHeading + "\n\n")
	writeMessages(&b, set, focus, files)
	if len(rest) > 0 {
		b.WriteString("\n" + SurroundingHeading + "\n\n")
		writeMessages(&b, set, rest, files)
	}
	return strings.TrimRight(b.String(), "\n")
}

func writeMessages(b *strings.Builder, set *Set, msgs []*discord.Message, files Files) {
	for i, m := range msgs {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(b, "[%s] %s — %s\n", stamp(m.Timestamp), set.author(m), chat.Link(set.Guild, m.ChannelID, m.ID))
		for _, n := range set.notes[m.ID] {
			b.WriteString("(" + n + ")\n")
		}
		if ref := m.MessageReference; ref != nil && ref.MessageID != "" && m.Type != discord.MessageTypeThreadStarterMessage {
			channel := ref.ChannelID
			if channel == "" {
				channel = m.ChannelID
			}
			b.WriteString("replying to " + chat.Link(set.Guild, channel, ref.MessageID) + "\n")
		}
		if text := strings.TrimSpace(m.Content); text != "" {
			b.WriteString(quote(text) + "\n")
		} else if len(m.Attachments) == 0 {
			b.WriteString("(no text)\n")
		}
		for _, a := range m.Attachments {
			line, ok := files[a.ID]
			if !ok {
				line = "not downloaded: " + a.Filename
			}
			b.WriteString("attachment: " + oneLine(line) + "\n")
		}
	}
}

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }

// Everything a message's author wrote is quoted line by line, so no body can start a line of
// the transcript's own: a heading, a "[time] name — link" record, or a note.
func quote(text string) string {
	return "> " + strings.ReplaceAll(text, "\n", "\n> ")
}

// A display name is the author's to choose, so it is one line, without the characters that
// frame a record, and followed by the account id it cannot choose.
func authorID(m *discord.Message) string {
	if m.Author == nil {
		return ""
	}
	return m.Author.ID
}

func (s *Set) author(m *discord.Message) string {
	switch {
	case m.Author == nil:
	case s.Tim != "" && m.Author.ID == s.Tim:
		return "Tim (instruction author)"
	}
	return author(m)
}

func author(m *discord.Message) string {
	name := strings.Map(func(r rune) rune {
		switch r {
		case '[', ']', '—', '>', '(', ')':
			return ' '
		}
		return r
	}, oneLine(chat.Name(m)))
	name = strings.Join(strings.Fields(name), " ")
	if r := []rune(name); len(r) > 40 {
		name = string(r[:40]) + "…"
	}
	if name == "" {
		name = "unnamed"
	}
	if m.Author != nil && m.Author.ID != "" {
		return name + " (" + m.Author.ID + ")"
	}
	return name
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
