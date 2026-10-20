package listen

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/chat"
	"github.com/timche/mac-mini/mochi/internal/fence"
)

// Discord's limit on one message is 2,000, which it may count in UTF-16 code units; this
// leaves room to spare.
const maxFirstPost = 1950

// What a session's first post is about: Tim's mention, the words of it that are his
// instruction, and the message he replied to, if any.
type opener struct {
	mention     *discordgo.Message
	replied     *discordgo.Message
	instruction string
}

// The first post in a thread or a forum post mochi opened. Its first line mentions Tim, who
// keeps Discord's notifications to mentions, and links his message, which Discord shows as a
// pill and a forum shows on its card; then the messages it is about, quoted, so the team sees
// what the session is for without leaving it. covers names the one task of several it is for.
func (h *Handler) announcement(o opener, covers string) *discordgo.MessageSend {
	m := o.mention
	head := "Session started for <@" + h.Cfg.User + ">'s message."
	if link := chat.Link(m.GuildID, m.ChannelID, m.ID); link != "" {
		head = "Session started for <@" + h.Cfg.User + ">'s message: " + link
	}
	tail := quote(chat.Name(m), o.instruction, m.Attachments)
	if covers != "" {
		tail += "\n" + covers
	}

	// Each message is a quote block of its own, a blank line between them, so Discord shows
	// them apart.
	text := head + "\n" + tail
	if r := o.replied; r != nil {
		budget := maxFirstPost - units(text) - 2
		if q := quoteWithin(chat.Name(r), r.Content, r.Attachments, budget); q != "" {
			text = head + "\n" + q + "\n\n" + tail
		}
	}
	if units(text) > maxFirstPost {
		// Only an instruction near Discord's own limit gets here, and a post that fails helps
		// nobody.
		text = within(text, maxFirstPost-1) + "…"
	}
	return chat.Pinging(h.Cfg.User, text)
}

// Discord may count a message in UTF-16 code units, where a character outside the Basic
// Multilingual Plane, an emoji, is two.
func units(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r >= 0x10000 {
			n++
		}
	}
	return n
}

// The longest start of s within n code units, cut between characters.
func within(s string, n int) string {
	used := 0
	for i, r := range s {
		w := 1
		if r >= 0x10000 {
			w = 2
		}
		if used+w > n {
			return s[:i]
		}
		used += w
	}
	return s
}

// Every way a line can end, as one, so no line of a quote starts without its "> ".
var breaks = strings.NewReplacer("\r\n", "\n", "\r", "\n", "\u2028", "\n", "\u2029", "\n", "\u0085", "\n")

func lines(text string) string { return fence.Clean(breaks.Replace(text)) }

var markdown = strings.NewReplacer(
	"\\", "\\\\", "[", "\\[", "]", "\\]", "(", "\\(", ")", "\\)", "<", "\\<", ">", "\\>",
	"*", "\\*", "_", "\\_", "~", "\\~", "`", "\\`", "|", "\\|",
)

// Plain is a name or a title, which somebody else wrote, made into one line of plain text in
// mochi's own voice: no link, mention, formatting or line break of its own.
func plain(text string) string {
	text = markdown.Replace(inert(strings.Join(strings.Fields(lines(text)), " ")))
	// A bare address is a link in Discord whatever escapes it carries.
	return strings.ReplaceAll(text, "://", "://\u200b")
}

// A message as a Discord block quote under its author's name, every line of it quoted, with
// what would ping a crowd or a role made inert and attachments as a count.
func quote(author, text string, files []*discordgo.MessageAttachment) string {
	text = inert(strings.TrimSpace(lines(text)))
	if note := attached(files); note != "" {
		text = strings.TrimSpace(text + " " + note)
	}
	quoted := strings.Split(text, "\n")
	quoted[0] = "**" + plain(author) + ":** " + quoted[0]
	for i, line := range quoted {
		quoted[i] = "> " + line
	}
	return strings.Join(quoted, "\n")
}

// The quote, its text shortened with "…" until it fits within budget code units; nothing
// when even the author alone does not.
func quoteWithin(author, text string, files []*discordgo.MessageAttachment, budget int) string {
	q := quote(author, text, files)
	r := []rune(strings.TrimSpace(text))
	for units(q) > budget && len(r) > 0 {
		cut := max(0, len(r)-max(1, (units(q)-budget+1)/2))
		r = []rune(strings.TrimSpace(string(r[:cut])))
		q = quote(author, string(r)+"…", files)
	}
	if units(q) > budget {
		return ""
	}
	return q
}

var crowd = strings.NewReplacer("@everyone", "@\u200beveryone", "@here", "@\u200bhere", "<@&", "<@\u200b&")

// Quoted text renders as it was written but pings no crowd and no role, whatever the post's
// allowed mentions say.
func inert(text string) string { return crowd.Replace(text) }

func attached(files []*discordgo.MessageAttachment) string {
	images, others := 0, 0
	for _, f := range files {
		if strings.HasPrefix(f.ContentType, "image/") {
			images++
		} else {
			others++
		}
	}
	var parts []string
	if images > 0 {
		parts = append(parts, plural(images, "image"))
	}
	if others > 0 {
		parts = append(parts, plural(others, "file"))
	}
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
