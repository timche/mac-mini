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

	if err := Send(api, store, thread, shot, "PR: https://github.com/x/y/pull/1 — @everyone <@94004892942741504>"); err != nil {
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

	if err := Send(api, store, thread, "", text); err != nil {
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
		if err := Send(api, store, th, "", "hello"); err == nil {
			t.Errorf("posted to %q", th)
		}
	}
	if len(api.Sent) != 0 {
		t.Error("something was sent")
	}
}
