package gather

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/chat/chattest"
)

const (
	guild   = "1220378778665095291"
	channel = "1300000000000000001"
	forum   = "1300000000000000002"
	post    = "1300000000000000003"
)

var (
	tim   = &discordgo.User{ID: "94004892942741504", Username: "tim"}
	maker = &discordgo.User{ID: "1400000000000000001", Username: "maker", GlobalName: "Maker-sama"}
	other = &discordgo.User{ID: "1400000000000000002", Username: "other"}
	mochi = &discordgo.User{ID: "1400000000000000003", Username: "mochi", Bot: true}
)

func server() *chattest.Fake {
	f := chattest.New()
	f.AddChannel(&discordgo.Channel{ID: channel, GuildID: guild, Name: "moderators", Type: discordgo.ChannelTypeGuildText})
	return f
}

func reply(m *discordgo.Message, to *discordgo.Message) *discordgo.Message {
	m.MessageReference = &discordgo.MessageReference{MessageID: to.ID, ChannelID: to.ChannelID}
	return m
}

func ids(set *Set) map[string]bool {
	out := map[string]bool{}
	for _, m := range set.Messages() {
		out[m.ID] = true
	}
	return out
}

// The case the whole package is for: a moderator's message from two hundred messages ago,
// discussed in plain messages around it, replied to now. The latest twenty would hold none
// of that discussion.
func TestAReplyToAnOldMessageGathersTheDiscussionAroundIt(t *testing.T) {
	f := server()
	for n := 1; n <= 99; n++ {
		f.Post(channel, n, other, "chatter")
	}
	qol := f.Post(channel, 100, maker, "QOL: a) average elo per team b) map stats")
	f.Post(channel, 101, other, "a) would be great")
	parent := f.Post(channel, 90, other, "what do mods want next?")
	reply(qol, parent)
	for n := 102; n <= 299; n++ {
		f.Post(channel, n, other, "later chatter")
	}
	late := reply(f.Post(channel, 250, other, "+1 to the QOL list"), qol)
	far := reply(f.Post(channel, 290, other, "still want a)"), qol)
	mention := reply(f.Post(channel, 300, tim, "<@"+mochi.ID+"> research a) average elo per team"), qol)

	set, err := Reply(f, mention)
	if err != nil {
		t.Fatal(err)
	}
	got := ids(set)

	for _, want := range []*discordgo.Message{qol, parent, late, far} {
		if !got[want.ID] {
			t.Errorf("missing %s (%q)", want.ID, want.Content)
		}
	}
	if !got[chattest.ID(101)] || !got[chattest.ID(60)] || !got[chattest.ID(140)] {
		t.Error("the discussion either side of the message is missing")
	}
	if got[chattest.ID(298)] {
		t.Error("the latest chatter was gathered as if it were the discussion")
	}
	if got[mention.ID] {
		t.Error("Tim's own mention is in the context rather than only the instruction")
	}

	out := Render(set, nil)
	if !strings.Contains(out, "(the message Tim replied to)") {
		t.Errorf("the replied-to message is not marked:\n%s", out)
	}
	if !strings.Contains(out, "Maker-sama ("+maker.ID+") — https://discord.com/channels/"+guild+"/"+channel+"/"+qol.ID) {
		t.Error("a message lacks its author or its link")
	}
	if !strings.Contains(out, "[2026-10-20 13:40 UTC]") {
		t.Error("a message lacks its time")
	}
	if strings.Index(out, "what do mods want next?") > strings.Index(out, "QOL: a)") {
		t.Error("the transcript is not in the order things were said")
	}
}

func TestAMentionInAForumPostIsHandedTheWholePost(t *testing.T) {
	f := server()
	f.AddChannel(&discordgo.Channel{ID: forum, GuildID: guild, Type: discordgo.ChannelTypeGuildForum})
	thread := &discordgo.Channel{ID: post, GuildID: guild, ParentID: forum, Type: discordgo.ChannelTypeGuildPublicThread}
	f.AddChannel(thread)

	starter := f.Post(post, 0, maker, "Bug: elo resets after a draw")
	starter.ID = post
	f.Msgs[post][0] = starter
	for n := 1; n <= 30; n++ {
		f.Post(post, n, other, "me too")
	}
	mention := f.Post(post, 31, tim, "<@"+mochi.ID+"> look into this")

	set, err := Thread(f, thread, mention)
	if err != nil {
		t.Fatal(err)
	}
	if set.Len() != 32 || !set.Has(post) {
		t.Errorf("gathered %d messages, starter present %v", set.Len(), set.Has(post))
	}
	if out := Render(set, nil); !strings.Contains(out, "(the message this thread started from)") {
		t.Error("the starter is not marked")
	}
}

func TestALongThreadKeepsItsStarterAndSaysWhatIsMissing(t *testing.T) {
	f := server()
	thread := &discordgo.Channel{ID: chattest.ID(0), GuildID: guild, ParentID: channel, Type: discordgo.ChannelTypeGuildPublicThread}
	f.AddChannel(thread)
	starter := f.Post(channel, 0, maker, "the idea")
	for n := 1; n <= 150; n++ {
		f.Post(thread.ID, n, other, "discussion")
	}
	mention := f.Post(thread.ID, 151, tim, "<@"+mochi.ID+"> go")

	set, err := Thread(f, thread, mention)
	if err != nil {
		t.Fatal(err)
	}
	if !set.Has(starter.ID) || set.Len() != ThreadWindow+2 {
		t.Errorf("gathered %d messages, starter present %v", set.Len(), set.Has(starter.ID))
	}
	if len(set.Gaps) != 1 || !strings.Contains(Render(set, nil), "Not shown: messages between the starter") {
		t.Errorf("gaps = %v", set.Gaps)
	}
}

func TestAMentionThatRepliesToNothingIsHandedTheRecentChannel(t *testing.T) {
	f := server()
	for n := 1; n <= 80; n++ {
		f.Post(channel, n, other, "chatter")
	}
	mention := f.Post(channel, 81, tim, "<@"+mochi.ID+"> summarise")

	set, err := Channel(f, mention)
	if err != nil {
		t.Fatal(err)
	}
	got := ids(set)
	if set.Len() != ChannelWindow || !got[chattest.ID(80)] || got[chattest.ID(30)] || got[mention.ID] {
		t.Errorf("gathered %d messages", set.Len())
	}
}

// Everything since Tim's previous mention, his own messages and mochi's included, the session's
// own posts marked as its own — what it said, and what moderators said back after it.
func TestARelayCarriesEverythingSinceTheLastMention(t *testing.T) {
	f := server()
	thread := chattest.ID(10)
	f.AddChannel(&discordgo.Channel{ID: thread, GuildID: guild, ParentID: channel, Type: discordgo.ChannelTypeGuildPublicThread})
	f.Post(thread, 11, mochi, "session started in repeek: moderators")
	first := f.Post(thread, 12, tim, "<@"+mochi.ID+"> start")
	f.Post(thread, 13, maker, "also b) please")
	finished := f.Post(thread, 14, mochi, "Finished: the overlay no longer covers the scoreboard.")
	f.Post(thread, 15, tim, "noted")
	renamed := f.Post(thread, 16, mochi, "⏳ Overlay")
	renamed.Type = discordgo.MessageTypeChannelNameChange
	pinned := f.Post(thread, 17, maker, "")
	pinned.Type = discordgo.MessageTypeChannelPinnedMessage
	answer := f.Post(thread, 18, other, "still covers it on 1440p")
	answer.Type = discordgo.MessageTypeReply
	answer.MessageReference = &discordgo.MessageReference{MessageID: finished.ID, ChannelID: thread}
	mention := f.Post(thread, 19, tim, "<@"+mochi.ID+"> also check b)")
	f.Post(thread, 20, other, "after the mention")

	set, err := Since(f, thread, first.ID, mention, mochi.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := ids(set)
	if set.Len() != 3 || !got[chattest.ID(13)] || !got[chattest.ID(15)] || !got[chattest.ID(18)] {
		t.Errorf("relayed %v", got)
	}
	out := Render(set, nil)
	if strings.Contains(out, "Finished:") || strings.Contains(out, "⏳ Overlay") {
		t.Errorf("mochi's own posts or Discord's notices were relayed:\n%s", out)
	}
	if !strings.Contains(out, "replying to "+chattest.ID(14)) && !strings.Contains(out, "replying to https://discord.com/channels/"+guild+"/"+thread+"/"+finished.ID) {
		t.Errorf("a reply to mochi's post lost its link:\n%s", out)
	}
}

func TestAttachmentsAreDownloadedWithinTheirCaps(t *testing.T) {
	f := server()
	dir := filepath.Join(t.TempDir(), "sessions", "123")

	m := f.Post(channel, 1, maker, "screens")
	m.Attachments = []*discordgo.MessageAttachment{
		{ID: "a1", Filename: "shot one.png", URL: "https://cdn/1", Size: 400, ContentType: "image/png"},
		{ID: "a2", Filename: "huge.mp4", URL: "https://cdn/2", Size: 5000},
		{ID: "a3", Filename: "../../etc/passwd", URL: "https://cdn/3", Size: 400},
		{ID: "a4", Filename: "liar.bin", URL: "https://cdn/4", Size: 10},
		{ID: "a5", Filename: "one-too-many.png", URL: "https://cdn/5", Size: 400},
	}
	f.Files["https://cdn/1"] = []byte(strings.Repeat("x", 400))
	f.Files["https://cdn/3"] = []byte(strings.Repeat("y", 400))
	f.Files["https://cdn/4"] = []byte(strings.Repeat("z", 2000))
	f.Files["https://cdn/5"] = []byte(strings.Repeat("w", 400))

	files := Downloader{API: f, Dir: dir, PerFile: 1000, Total: 1000}.Fetch([]*discordgo.Message{m})

	shot := filepath.Join(dir, m.ID+"-a1-shot_one.png")
	if body, err := os.ReadFile(shot); err != nil || len(body) != 400 {
		t.Errorf("the screenshot was not saved: %v", err)
	}
	if files["a1"] != shot+" (image/png, 400 bytes)" {
		t.Errorf("a1 = %q", files["a1"])
	}
	if !strings.Contains(files["a2"], "skipped huge.mp4") || !strings.Contains(files["a2"], "limit for one file") {
		t.Errorf("a2 = %q", files["a2"])
	}
	if !strings.HasPrefix(files["a3"], dir+"/") {
		t.Errorf("a name escaped the session folder: %q", files["a3"])
	}
	if !strings.Contains(files["a4"], "larger than Discord said") {
		t.Errorf("a4 = %q", files["a4"])
	}
	if !strings.Contains(files["a5"], "the session already holds 800 bytes") {
		t.Errorf("a5 = %q", files["a5"])
	}
	if _, err := os.Stat(filepath.Join(dir, m.ID+"-a4-liar.bin")); err == nil {
		t.Error("a download past the limit was kept")
	}

	out := Render(&Set{Guild: guild, msgs: map[string]*discordgo.Message{m.ID: m}, notes: map[string][]string{}}, files)
	if !strings.Contains(out, "attachment: "+shot) || !strings.Contains(out, "attachment: skipped huge.mp4") {
		t.Errorf("the transcript does not list the files:\n%s", out)
	}

	// Already there is not downloaded again, which is what lets a relay or a page of history
	// share the folder.
	delete(f.Files, "https://cdn/1")
	again := Downloader{API: f, Dir: dir, PerFile: 1000, Total: 1000}.Fetch([]*discordgo.Message{m})
	if again["a1"] != files["a1"] {
		t.Errorf("a file already downloaded was fetched again: %q", again["a1"])
	}
}
