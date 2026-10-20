package listen

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/split"
)

func firstPost(t *testing.T, f *fixture, thread string) *discordgo.MessageSend {
	t.Helper()
	for _, s := range f.api.Sent {
		if s.Channel == thread {
			return s.Msg
		}
	}
	t.Fatalf("nothing was posted in %s", thread)
	return nil
}

// The first post quotes what the session is about, under each author's name, line by line,
// with attachments as a count and mochi's mention out of Tim's words; it pings Tim alone and
// shows no preview.
func TestTheFirstPostQuotesWhatTheSessionIsAbout(t *testing.T) {
	f := newFixture(t)
	qol := f.api.Post(channel, 1, maker, "hey, some QOL things:\na) average elo\nb) map stats")
	qol.Attachments = []*discordgo.MessageAttachment{{ContentType: "image/png"}, {ContentType: "image/jpeg"}}
	m := f.mention(channel, 2, tim, "take a look\nat both", qol)
	m.Attachments = []*discordgo.MessageAttachment{{ContentType: "application/pdf"}}

	f.h.Handle(m)

	msg := firstPost(t, f, m.ID)
	want := startedPost(m.ID) + "\n" +
		"> **Maker-sama:** hey, some QOL things:\n> a) average elo\n> b) map stats (2 images)\n" +
		"> **tim:** take a look\n> at both (1 file)"
	if msg.Content != want {
		t.Errorf("first post:\n%s\nwant:\n%s", msg.Content, want)
	}
	if am := msg.AllowedMentions; am == nil || !reflect.DeepEqual(am.Users, []string{tim.ID}) || len(am.Parse) != 0 || len(am.Roles) != 0 {
		t.Errorf("allowed mentions = %+v", am)
	}
	if msg.Flags&discordgo.MessageFlagsSuppressEmbeds == 0 {
		t.Error("the link shows a preview")
	}
}

// Over Discord's limit, the replied-to message is shortened with "…"; Tim's words and the link
// stay whole.
func TestALongRepliedMessageIsShortenedAndTimsWordsAreNot(t *testing.T) {
	f := newFixture(t)
	long := f.api.Post(channel, 1, maker, strings.Repeat("a long suggestion ", 200))
	words := strings.Repeat("do this ", 60)
	m := f.mention(channel, 2, tim, words, long)

	f.h.Handle(m)

	text := firstPost(t, f, m.ID).Content
	if n := len([]rune(text)); n > 2000 {
		t.Errorf("first post is %d characters", n)
	}
	if head(text) != startedPost(m.ID) || !strings.Contains(text, "> **tim:** "+strings.TrimSpace(words)) {
		t.Errorf("the link or Tim's words were cut:\n%s", text)
	}
	if !strings.Contains(text, "> **Maker-sama:** a long suggestion") || !strings.Contains(text, "…\n> **tim:**") {
		t.Errorf("the replied-to message was not shortened with an ellipsis:\n%.300s", text)
	}
}

// One of several sessions says which task it covers, under the same quotes.
func TestASplitSessionsFirstPostSaysWhatItCovers(t *testing.T) {
	f := forumFixture(t)
	f.h.Split = answers(split.Answer{Decision: split.Many, Tasks: []split.Task{
		{Title: "Lobby average", Instruction: "add the lobby average"},
		{Title: "Login timeout", Instruction: "fix the login timeout"},
	}}, nil, nil)
	m := f.mention(channel, 1, tim, "add the lobby average, and separately fix the login timeout", nil)
	f.h.Handle(m)

	for i, title := range []string{"Lobby average", "Login timeout"} {
		text := firstPost(t, f, postID(i+1)).Content
		if !strings.HasSuffix(text, "\n> **tim:** add the lobby average, and separately fix the login timeout\nThis post covers: "+title) {
			t.Errorf("post %d:\n%s", i+1, text)
		}
	}
}

// What would ping a crowd or a role in quoted text is made inert; it still reads the same.
func TestQuotedMentionsPingNobody(t *testing.T) {
	f := newFixture(t)
	loud := f.api.Post(channel, 1, maker, "@everyone look, <@&1500000000000000001> and @here too")
	m := f.mention(channel, 2, tim, "check this @here", loud)

	f.h.Handle(m)

	text := firstPost(t, f, m.ID).Content
	for _, raw := range []string{"@everyone", "@here", "<@&"} {
		if strings.Contains(text, raw) {
			t.Errorf("%q is live in:\n%s", raw, text)
		}
	}
	for _, inert := range []string{"@\u200beveryone", "<@\u200b&1500000000000000001>", "@\u200bhere too", "check this @\u200bhere"} {
		if !strings.Contains(text, inert) {
			t.Errorf("%q is missing from:\n%s", inert, text)
		}
	}
	f.quiet()
}

// A title the classifier took from somebody else's words is plain text wherever mochi writes
// it: no masked link, mention or formatting of its own, in the covers line and the split reply.
func TestAHostileTitleIsPlainText(t *testing.T) {
	f := forumFixture(t)
	hostile := "[Session started](https://evil.example) @everyone <@&1500000000000000001> **now**"
	f.h.Split = answers(split.Answer{Decision: split.Many, Tasks: []split.Task{
		{Title: hostile, Instruction: "a"},
		{Title: "Login timeout", Instruction: "b"},
	}}, nil, nil)
	m := f.mention(channel, 1, tim, "a, and separately b", nil)
	f.h.Handle(m)

	safe := strings.ReplaceAll(`\[Session started\]\(https://ZWSPevil.example\) @ZWSPeveryone \<@ZWSP&1500000000000000001\> \*\*now\*\*`, "ZWSP", "\u200b")
	if text := firstPost(t, f, postID(1)).Content; !strings.HasSuffix(text, "\nThis post covers: "+safe) {
		t.Errorf("covers line:\n%s", text)
	}
	replies := f.sentTo(channel)
	if len(replies) != 1 || !strings.HasPrefix(replies[0], "<#"+postID(1)+"> "+safe+"\n") {
		t.Errorf("split reply = %q", replies)
	}
	for _, text := range append(replies, firstPost(t, f, postID(1)).Content) {
		if strings.Contains(text, "](https://evil") || strings.Contains(text, "@everyone") {
			t.Errorf("a live link or ping in %q", text)
		}
	}
}

// Every way a line can end becomes a quoted line of its own, in the text and in a name.
func TestEveryLineOfAQuoteIsQuoted(t *testing.T) {
	f := newFixture(t)
	odd := &discordgo.User{ID: "1400000000000000009", Username: "odd", GlobalName: "Odd\rName\u2028**bold**"}
	sly := f.api.Post(channel, 1, odd, "one\rtwo\u2028three\u2029four\u0085five\r\nsix")
	m := f.mention(channel, 2, tim, "look\u2028here", sly)
	f.h.Handle(m)

	text := firstPost(t, f, m.ID).Content
	want := startedPost(m.ID) + "\n" +
		"> **Odd Name \\*\\*bold\\*\\*:** one\n> two\n> three\n> four\n> five\n> six\n" +
		"> **tim:** look\n> here"
	if text != want {
		t.Errorf("first post:\n%q\nwant:\n%q", text, want)
	}
	for _, r := range []string{"\r", "\u2028", "\u2029", "\u0085"} {
		if strings.Contains(text, r) {
			t.Errorf("%q survived", r)
		}
	}
}

// Emoji count twice in UTF-16, which Discord may count in: the post stays under its limit.
func TestAnEmojiHeavyPostStaysUnderDiscordsLimit(t *testing.T) {
	f := newFixture(t)
	loud := f.api.Post(channel, 1, maker, strings.Repeat("🔥🎮", 1500))
	m := f.mention(channel, 2, tim, "look 😀", loud)
	f.h.Handle(m)

	text := firstPost(t, f, m.ID).Content
	if n := len(utf16.Encode([]rune(text))); n >= 2000 {
		t.Errorf("first post is %d UTF-16 units", n)
	}
	if !utf8.ValidString(text) || !strings.Contains(text, "…\n> **tim:** look 😀") {
		t.Errorf("not cut on a character boundary with an ellipsis:\n%.200s", text)
	}
}
