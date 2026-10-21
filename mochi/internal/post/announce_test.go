package post

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/timche/mac-mini/discord"

	"github.com/timche/mac-mini/mochi/internal/chat/chattest"
	"github.com/timche/mac-mini/mochi/internal/mark"
)

const (
	guild    = "1220378778665095291"
	announce = "1378670204795617340"
)

// The server's announce channel, and one of the same kind in another server.
const elsewhere = "1378670204795617399"

func announceAPI() *chattest.Fake {
	api := chattest.New()
	api.AddChannel(&discord.Channel{ID: announce, GuildID: guild, Type: discord.ChannelTypeGuildText})
	api.AddChannel(&discord.Channel{ID: elsewhere, GuildID: "1220378778665095299", Type: discord.ChannelTypeGuildText})
	return api
}

// A channel the settings name in another server, or one that cannot be read, gets nothing.
func TestAnAnnouncementGoesNowhereOutsideTheServer(t *testing.T) {
	api := announceAPI()
	for _, channel := range []string{elsewhere, "1378670204795617311"} {
		if _, err := Announce(api, guild, channel, repeek, Post{Text: "Ready."}); err == nil || !strings.Contains(err.Error(), channel) {
			t.Errorf("%s: %v", channel, err)
		}
	}
	if len(api.Sent) != 0 {
		t.Errorf("sent = %+v", api.Sent)
	}
}

// An announcement goes to the server's announce channel, with no session behind it: no thread,
// no state, and nobody pinged, Tim included.
func TestAnAnnouncementGoesToTheChannelAndPingsNobody(t *testing.T) {
	api := announceAPI()
	shot := filepath.Join(t.TempDir(), "after.png")
	os.WriteFile(shot, []byte("\x89PNG\r\n\x1a\n"), 0o644)
	text := "**Average elo for each team** @everyone <@94004892942741504> <@&1220378778665095292>\n- https://chromewebstore.google.com/detail/repeek/abcdef\n\n**Screenshots**\n1. After"

	msg, err := Announce(api, guild, announce, repeek, Post{Text: text, Files: []string{shot}, Roots: anywhere, Links: allowed})
	if err != nil {
		t.Fatal(err)
	}
	if len(api.Sent) != 1 || api.Sent[0].Channel != announce || msg.ID != api.Sent[0].ID {
		t.Fatalf("sent = %+v, %+v", api.Sent, msg)
	}
	am := api.Sent[0].Msg.AllowedMentions
	if am == nil || am.Parse == nil || len(am.Parse) != 0 || len(am.Users) != 0 || len(am.Roles) != 0 || am.RepliedUser {
		t.Errorf("the announcement could ping: %+v", am)
	}
	if api.Sent[0].Msg.Flags&discord.MessageFlagsSuppressEmbeds != 0 {
		t.Error("the store listing's preview was suppressed")
	}
	if api.Sent[0].Files["after.png"] == "" {
		t.Errorf("files = %v", api.Sent[0].Files)
	}
	if len(api.Reactions) != 0 || len(api.Renames) != 0 || len(api.Threads) != 0 {
		t.Error("an announcement changed something besides posting")
	}
}

// A pull request of the server's repository goes as its own line, with no previews.
func TestAnAnnouncementTakesAPullRequestOfItsRepository(t *testing.T) {
	api := announceAPI()
	if _, err := Announce(api, guild, announce, repeek, Post{Text: "Ready.", PR: "https://github.com/timche/repeek/pull/9"}); err != nil {
		t.Fatal(err)
	}
	if got := api.Sent[0].Msg; got.Content != "Ready.\n\n#9 (https://github.com/timche/repeek/pull/9)" || got.Flags&discord.MessageFlagsSuppressEmbeds == 0 {
		t.Errorf("msg = %+v", got)
	}
	if _, err := Announce(api, guild, announce, repeek, Post{Text: "Ready.", PR: "https://github.com/timche/mac-mini/pull/9"}); err == nil {
		t.Error("another repository's pull request was announced")
	}
}

// Every guard of a post stands before an announcement too.
func TestAnAnnouncementIsHeldToAPostsGuards(t *testing.T) {
	api := announceAPI()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	inside := Roots(func(string) ([]string, error) { return []string{dir}, nil })
	key := filepath.Join(dir, ".env")
	os.WriteFile(key, []byte("X=1"), 0o644)
	outside := filepath.Join(t.TempDir(), "shot.png")
	os.WriteFile(outside, []byte("\x89PNG\r\n\x1a\n"), 0o644)
	leak := func(_ context.Context, args []string, _ io.Reader) ([]byte, int, error) {
		return []byte(`[{"RuleID":"generic-api-key","File":"stdin"}]`), 1, nil
	}
	api.Tiers = map[string]discord.PremiumTier{}
	big := filepath.Join(dir, "big.mp4")
	os.WriteFile(big, make([]byte, 11<<20), 0o644)

	for name, p := range map[string]Post{
		"a link":                    {Text: "see https://example.com", Links: allowed},
		"a path":                    {Text: "the log is in ~/Library/Logs/x.log", Links: allowed},
		"a look-alike":              {Text: "https://chromewebstore.google.com.evil.com/x", Links: allowed},
		"a secret's file":           {Text: "x", Files: []string{key}, Roots: inside},
		"a file outside":            {Text: "x", Files: []string{outside}, Roots: inside},
		"a leak":                    {Text: "x", Roots: inside, Leaks: leak},
		"a file over the limit":     {Text: "x", Files: []string{big}, Roots: inside},
		"a heading with no files":   {Text: "**Screenshots**\n1. x"},
		"a state":                   {Text: "x", Settles: mark.Finished},
		"a type":                    {Text: "x", Type: "bug"},
		"no text":                   {},
		"no channel to announce in": {Text: "x"},
	} {
		channel := announce
		if name == "no channel to announce in" {
			channel = ""
		}
		if _, err := Announce(api, guild, channel, repeek, p); err == nil {
			t.Errorf("%s: announced", name)
		} else if strings.Contains(err.Error(), "REDACTED") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(api.Sent) != 0 {
		t.Errorf("%d announced", len(api.Sent))
	}
}
