package post

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/chat/chattest"
	"github.com/timche/mac-mini/mochi/internal/state"
)

const thread = "1300000000000000005"

func fixture(t *testing.T) (*chattest.Fake, state.Store) {
	t.Helper()
	api := chattest.New()
	api.AddChannel(&discordgo.Channel{ID: thread, Type: discordgo.ChannelTypeGuildPublicThread})
	store := state.Store{Path: filepath.Join(t.TempDir(), "state.json")}
	if err := store.Put(state.Session{Thread: thread, Source: "1300000000000000001"}); err != nil {
		t.Fatal(err)
	}
	return api, store
}

func TestAPostGoesToTheSessionsThreadWithNoPings(t *testing.T) {
	api, store := fixture(t)
	shot := filepath.Join(t.TempDir(), "shot.png")
	os.WriteFile(shot, []byte("png"), 0o644)

	if err := Send(api, store, thread, []string{shot}, "Finished — @everyone <@94004892942741504>"); err != nil {
		t.Fatal(err)
	}

	if len(api.Sent) != 1 || api.Sent[0].Channel != thread {
		t.Fatalf("sent = %+v", api.Sent)
	}
	am := api.Sent[0].Msg.AllowedMentions
	if am == nil || am.Parse == nil || len(am.Parse) != 0 {
		t.Error("the post could ping")
	}
	if api.Sent[0].Files["shot.png"] != "png" {
		t.Errorf("files = %v", api.Sent[0].Files)
	}
}

func TestALongPostIsSplitBetweenLines(t *testing.T) {
	api, store := fixture(t)
	text := strings.Repeat(strings.Repeat("x", 99)+"\n", 50)

	if err := Send(api, store, thread, nil, text); err != nil {
		t.Fatal(err)
	}
	if len(api.Sent) != 3 {
		t.Fatalf("parts = %d", len(api.Sent))
	}
	var joined []string
	for _, s := range api.Sent {
		if len(s.Msg.Content) > 2000 || strings.HasPrefix(s.Msg.Content, "\n") {
			t.Errorf("a part is %d long", len(s.Msg.Content))
		}
		joined = append(joined, s.Msg.Content)
	}
	if strings.Join(joined, "\n") != strings.TrimRight(text, "\n") {
		t.Error("the parts do not add up to the post")
	}
}

// The variable alone would let a session post anywhere the bot can.
func TestAThreadMochiDidNotOpenIsRefused(t *testing.T) {
	api, store := fixture(t)
	for _, th := range []string{"", "1300000000000000099"} {
		if err := Send(api, store, th, nil, "hello"); err == nil {
			t.Errorf("posted to %q", th)
		}
	}
	if len(api.Sent) != 0 {
		t.Error("something was sent")
	}
}

func TestAPostCarriesSeveralFilesWithinDiscordsLimits(t *testing.T) {
	api, store := fixture(t)
	dir := t.TempDir()
	var files []string
	for _, name := range []string{"before.png", "after.png", "walkthrough.mp4"} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(name), 0o644)
		files = append(files, p)
	}

	if err := Send(api, store, thread, files, "Finished: the overlay no longer covers the scoreboard."); err != nil {
		t.Fatal(err)
	}
	if got := api.Sent[0].Files; len(got) != 3 || got["after.png"] != "after.png" || got["walkthrough.mp4"] != "walkthrough.mp4" {
		t.Errorf("files = %v", got)
	}

	eleven := make([]string, 11)
	for i := range eleven {
		eleven[i] = files[0]
	}
	big := filepath.Join(dir, "huge.mov")
	os.WriteFile(big, make([]byte, MaxFileSize+1), 0o644)
	for name, f := range map[string][]string{"eleven files": eleven, "a file over 10 MB": {big}, "no such file": {filepath.Join(dir, "gone.png")}} {
		if err := Send(api, store, thread, f, "x"); err == nil {
			t.Errorf("%s was posted", name)
		}
	}
	if len(api.Sent) != 1 {
		t.Errorf("%d posts, want 1", len(api.Sent))
	}
}

// Posts are Discord-only: links and file paths are refused with the reason, attachments are not.
func TestAPostWithALinkOrAPathIsRefused(t *testing.T) {
	api, store := fixture(t)
	for _, text := range []string{
		"Fixed, see https://github.com/x/y/pull/1",
		"preview at http://localhost:3000/elo",
		"details on www.example.com",
		"join discord.gg/abcdef",
		"or discord.com/invite/abcdef",
		"the log is in /Users/timche/projects/repeek/log.txt",
		"see ~/Library/Caches/mochi/sessions/1/brief.md",
		"(/private/tmp/shot.png)",
	} {
		err := Send(api, store, thread, nil, text)
		if err == nil || !strings.Contains(err.Error(), "read in Discord by moderators") {
			t.Errorf("%q: %v", text, err)
		}
	}
	for _, text := range []string{
		"Finished: the overlay no longer covers the scoreboard on 1080p and 1440p.",
		"Win rate is now shown per map, 3/4 of maps checked, and/or per team.",
	} {
		if err := Send(api, store, thread, nil, text); err != nil {
			t.Errorf("%q was refused: %v", text, err)
		}
	}
	if len(api.Sent) != 2 {
		t.Errorf("%d posts, want 2", len(api.Sent))
	}
}
