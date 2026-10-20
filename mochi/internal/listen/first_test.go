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
		"> **Maker-sama:** hey, some QOL things:\n> a) average elo\n> b) map stats (2 images)\n\n" +
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
	if !strings.Contains(text, "> **Maker-sama:** a long suggestion") || !strings.Contains(text, "…\n\n> **tim:**") {
		t.Errorf("the replied-to message was not shortened with an ellipsis:\n%.300s", text)
	}
}

// Each session's first post ends on the classifier's summary of its own task, after the same
// quotes; one with no summary has no such line.
func TestAFirstPostEndsOnItsTasksSummary(t *testing.T) {
	f := forumFixture(t)
	f.h.Split = answers(split.Answer{Decision: split.Many, Tasks: []split.Task{
		{Title: "Lobby average", Instruction: "add the lobby average", Summary: "Show the average elo of the whole lobby."},
		{Title: "Login timeout", Instruction: "fix the login timeout"},
	}}, nil, nil)
	m := f.mention(channel, 1, tim, "add the lobby average, and separately fix the login timeout", nil)
	f.h.Handle(m)

	quoted := "\n> **tim:** add the lobby average, and separately fix the login timeout"
	if text := firstPost(t, f, postID(1)).Content; !strings.HasSuffix(text, quoted+"\n\n**Summary:** Show the average elo of the whole lobby.") {
		t.Errorf("post 1:\n%s", text)
	}
	if text := firstPost(t, f, postID(2)).Content; !strings.HasSuffix(text, quoted) {
		t.Errorf("post 2:\n%s", text)
	}

	// One session takes the summary of the work as a whole; a classifier that failed gives none.
	g := forumFixture(t)
	g.h.Split = answers(split.Answer{Decision: split.One, Title: "Overlay", Summary: "Hovering names works again.",
		Tasks: []split.Task{{Title: "Overlay", Instruction: "fix it"}}}, nil, nil)
	g.h.Handle(g.mention(channel, 1, tim, "fix the overlay", nil))
	if text := firstPost(t, g, postID(1)).Content; !strings.HasSuffix(text, "\n\n**Summary:** Hovering names works again.") {
		t.Errorf("one session:\n%s", text)
	}
	h := forumFixture(t)
	h.h.Handle(h.mention(channel, 1, tim, "fix the overlay", nil))
	if text := firstPost(t, h, postID(1)).Content; strings.Contains(text, "Summary") {
		t.Errorf("a summary with no classifier:\n%s", text)
	}
}

// Over the limit the replied-to message gives way first, then the summary; Tim's words and the
// link stay whole.
func TestTheSummaryGivesWayAfterTheRepliedMessage(t *testing.T) {
	f := newFixture(t)
	long := f.api.Post(channel, 1, maker, strings.Repeat("a long suggestion ", 200))
	words := strings.TrimSpace(strings.Repeat("do this ", 200))
	f.h.Split = answers(split.Answer{Decision: split.One, Title: "T", Summary: strings.Repeat("plain words ", 25),
		Tasks: []split.Task{{Title: "T", Instruction: "x"}}}, nil, nil)
	m := f.mention(channel, 2, tim, words, long)
	f.h.Handle(m)

	text := firstPost(t, f, m.ID).Content
	if n := len(utf16.Encode([]rune(text))); n >= 2000 {
		t.Errorf("first post is %d UTF-16 units", n)
	}
	if head(text) != startedPost(m.ID) || !strings.Contains(text, "> **tim:** "+words) {
		t.Errorf("the link or Tim's words were cut:\n%.300s", text)
	}
	if strings.Contains(text, "Maker-sama") {
		t.Error("the replied-to message stayed while the summary gave way")
	}
	if !strings.Contains(text, "\n\n**Summary:** plain words") || !strings.HasSuffix(text, "…") {
		t.Errorf("the summary was not shortened to fit:\n…%s", text[len(text)-200:])
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

// A title or a summary the classifier took from somebody else's words is plain text wherever
// mochi writes it: no masked link, mention or formatting of its own, in the summary line and
// the split reply.
func TestAHostileTitleIsPlainText(t *testing.T) {
	f := forumFixture(t)
	hostile := "[Session started](https://evil.example) @everyone <@&1500000000000000001> **now**"
	f.h.Split = answers(split.Answer{Decision: split.Many, Tasks: []split.Task{
		{Title: hostile, Instruction: "a", Summary: "[Session started] @everyone <@&1500000000000000001> **now**"},
		{Title: "Login timeout", Instruction: "b"},
	}}, nil, nil)
	m := f.mention(channel, 1, tim, "a, and separately b", nil)
	f.h.Handle(m)

	safe := strings.ReplaceAll(`\[Session started\]\(https://ZWSPevil.example\) @ZWSPeveryone \<@ZWSP&1500000000000000001\> \*\*now\*\*`, "ZWSP", "\u200b")
	if text := firstPost(t, f, postID(1)).Content; !strings.HasSuffix(text, "\n\n**Summary:** "+strings.ReplaceAll(`\[Session started\] @ZWSPeveryone \<@ZWSP&1500000000000000001\> \*\*now\*\*`, "ZWSP", "\u200b")) {
		t.Errorf("summary line:\n%s", text)
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
		"> **Odd Name \\*\\*bold\\*\\*:** one\n> two\n> three\n> four\n> five\n> six\n\n" +
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
	if !utf8.ValidString(text) || !strings.Contains(text, "…\n\n> **tim:** look 😀") {
		t.Errorf("not cut on a character boundary with an ellipsis:\n%.200s", text)
	}
}

// A summary with a link or a path in it is left out rather than posted in mochi's voice.
func TestASummaryWithALinkOrAPathIsLeftOut(t *testing.T) {
	for _, summary := range []string{"Players are sent to evil.com to log in.", "See https://example.com for it.", "The fix is in ~/projects/x.go."} {
		f := forumFixture(t)
		f.h.Split = answers(split.Answer{Decision: split.One, Title: "Login", Summary: summary,
			Tasks: []split.Task{{Title: "Login", Instruction: "fix it"}}}, nil, nil)
		f.h.Handle(f.mention(channel, 1, tim, "fix the login", nil))
		if text := firstPost(t, f, postID(1)).Content; strings.Contains(text, "Summary") {
			t.Errorf("%q was posted:\n%s", summary, text)
		}
	}
}
