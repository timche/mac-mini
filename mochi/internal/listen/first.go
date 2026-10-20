package listen

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/chat"
)

// Discord's limit on one message, with room to spare.
const maxFirstPost = 1990

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

	text := head + "\n" + tail
	if r := o.replied; r != nil {
		budget := maxFirstPost - len([]rune(text)) - 1
		if q := quoteWithin(chat.Name(r), r.Content, r.Attachments, budget); q != "" {
			text = head + "\n" + q + "\n" + tail
		}
	}
	if r := []rune(text); len(r) > maxFirstPost {
		// Only an instruction near Discord's own limit gets here, and a post that fails helps
		// nobody.
		text = string(r[:maxFirstPost-1]) + "…"
	}
	return chat.Pinging(h.Cfg.User, text)
}

// A message as a Discord block quote under its author's name, every line of it quoted, with
// what would ping a crowd or a role made inert and attachments as a count.
func quote(author, text string, files []*discordgo.MessageAttachment) string {
	text = inert(strings.TrimSpace(text))
	if note := attached(files); note != "" {
		text = strings.TrimSpace(text + " " + note)
	}
	lines := strings.Split(text, "\n")
	lines[0] = "**" + inert(author) + ":** " + lines[0]
	for i, line := range lines {
		lines[i] = "> " + line
	}
	return strings.Join(lines, "\n")
}

// The quote, its text shortened with "…" until it fits within budget runes; nothing when even
// the author alone does not.
func quoteWithin(author, text string, files []*discordgo.MessageAttachment, budget int) string {
	q := quote(author, text, files)
	r := []rune(strings.TrimSpace(text))
	for len([]rune(q)) > budget && len(r) > 0 {
		cut := max(0, len(r)-max(1, len([]rune(q))-budget))
		r = []rune(strings.TrimSpace(string(r[:cut])))
		q = quote(author, string(r)+"…", files)
	}
	if len([]rune(q)) > budget {
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
