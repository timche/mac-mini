package history

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/chat/chattest"
	"github.com/timche/mac-mini/mochi/internal/config"
	"github.com/timche/mac-mini/mochi/internal/state"
)

const (
	guild   = "1220378778665095291"
	source  = "1300000000000000001"
	other   = "1300000000000000002"
	session = "1300000000000000005"
)

func fixture(t *testing.T) (config.Config, *chattest.Fake) {
	t.Helper()
	cfg := config.Config{CacheDir: t.TempDir()}
	api := chattest.New()
	api.Bot = &discordgo.User{ID: "1400000000000000003", Username: "mochi", Bot: true}
	api.AddChannel(&discordgo.Channel{ID: source, GuildID: guild, Type: discordgo.ChannelTypeGuildText})
	api.AddChannel(&discordgo.Channel{ID: other, GuildID: guild, Type: discordgo.ChannelTypeGuildText})
	user := &discordgo.User{ID: "1400000000000000001", Username: "maker"}
	for n := 1; n <= 300; n++ {
		api.Post(source, n, user, "topic message")
		api.Post(other, 1000+n, user, "private channel")
	}
	if err := (state.Store{Path: cfg.StatePath()}).Put(state.Session{Thread: session, Source: source, Guild: guild}); err != nil {
		t.Fatal(err)
	}
	return cfg, api
}

// --post reads the session's own thread or post, and still nothing else.
func TestHistoryReadsTheSessionsOwnPost(t *testing.T) {
	cfg, api := fixture(t)
	api.AddChannel(&discordgo.Channel{ID: session, GuildID: guild, Type: discordgo.ChannelTypeGuildPublicThread})
	api.Post(session, 5000, &discordgo.User{ID: "1400000000000000002", Username: "mod"}, "in the post")
	var out strings.Builder

	if err := Show(cfg, api, session, Query{Limit: 50, Post: true}, &out); err != nil {
		t.Fatal(err)
	}
	if text := out.String(); !strings.Contains(text, "in the post") || strings.Contains(text, "topic message") {
		t.Errorf("wrong channel:\n%.300s", text)
	}
	if api.Pages[0] != session+" before= after= around= limit=50" {
		t.Errorf("asked Discord for %q", api.Pages[0])
	}
}

func TestHistoryPagesBackThroughTheSourceChannel(t *testing.T) {
	cfg, api := fixture(t)
	var out strings.Builder

	if err := Show(cfg, api, session, Query{Before: chattest.ID(201), Limit: 100}, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if strings.Count(text, "topic message") != 100 || strings.Contains(text, "private channel") {
		t.Errorf("wrong page:\n%.400s", text)
	}
	if !strings.Contains(text, "mochi history --before "+chattest.ID(101)) || !strings.Contains(text, "--after "+chattest.ID(200)) {
		t.Errorf("no way to page further:\n%.300s", text)
	}
	if !strings.Contains(text, "----- BEGIN DISCORD HISTORY ") || !strings.Contains(text, "----- END DISCORD HISTORY ") {
		t.Error("the page is not fenced")
	}
	if strings.Index(text, chattest.ID(101)+"\n") > strings.Index(text, chattest.ID(200)+"\n") {
		t.Error("not oldest first")
	}
	if api.Pages[0] != source+" before="+chattest.ID(201)+" after= around= limit=100" {
		t.Errorf("asked Discord for %q", api.Pages[0])
	}

	out.Reset()
	if err := Show(cfg, api, session, Query{Before: chattest.ID(1), Limit: 50}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "No messages") {
		t.Errorf("the start of the channel reads %q", out.String())
	}
}

// The source is mochi's own record of where the session started, never an argument, so a
// session cannot read the rest of the server.
func TestHistoryRefusesAnythingButTheSessionsOwnSource(t *testing.T) {
	cfg, api := fixture(t)
	var out strings.Builder

	for _, thread := range []string{"", other, "1300000000000000099"} {
		if err := Show(cfg, api, thread, Query{Limit: 50}, &out); err == nil {
			t.Errorf("history ran for thread %q", thread)
		}
	}
	if len(api.Pages) != 0 || out.Len() != 0 {
		t.Error("something was read")
	}
}

func TestAQueryIsOneDirectionAndAPage(t *testing.T) {
	for _, q := range []Query{
		{Before: chattest.ID(1), After: chattest.ID(2), Limit: 10},
		{Before: "../channels/1", Limit: 10},
		{Limit: 0},
		{Limit: 101},
	} {
		if q.Check() == nil {
			t.Errorf("%+v passed", q)
		}
	}
}

// History leaves out mochi's own posts and Discord's notices, as a relay does.
func TestHistoryLeavesOutMochiAndDiscordsNotices(t *testing.T) {
	cfg, api := fixture(t)
	api.AddChannel(&discordgo.Channel{ID: session, GuildID: guild, Type: discordgo.ChannelTypeGuildPublicThread})
	api.Post(session, 5000, api.Bot, "Session started for the message")
	renamed := api.Post(session, 5001, api.Bot, "⏳ the title")
	renamed.Type = discordgo.MessageTypeChannelNameChange
	api.Post(session, 5002, &discordgo.User{ID: "1400000000000000002", Username: "mod"}, "a moderator's word")
	var out strings.Builder

	if err := Show(cfg, api, session, Query{Limit: 50, Post: true}, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "a moderator's word") || strings.Contains(text, "Session started") || strings.Contains(text, "the title") {
		t.Errorf("history:\n%s", text)
	}
}

// When mochi's own id cannot be read, history shows everything rather than nothing.
func TestHistoryWithoutMochisIdShowsEverything(t *testing.T) {
	cfg, api := fixture(t)
	api.Bot = nil
	var out strings.Builder
	if err := Show(cfg, api, session, Query{Limit: 10}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "topic message") {
		t.Errorf("history:\n%.300s", out.String())
	}
}
