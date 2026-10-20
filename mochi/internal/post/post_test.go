package post

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/chat/chattest"
	"github.com/timche/mac-mini/mochi/internal/mark"
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

	if err := Send(api, store, nil, repeek, thread, Post{Files: []string{shot}, Text: "Finished — @everyone <@94004892942741504>"}); err != nil {
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

	if err := Send(api, store, nil, repeek, thread, Post{Text: text}); err != nil {
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
		if err := Send(api, store, nil, repeek, th, Post{Text: "hello"}); err == nil {
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

	if err := Send(api, store, nil, repeek, thread, Post{Files: files, Text: "Finished: the overlay no longer covers the scoreboard."}); err != nil {
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
		if err := Send(api, store, nil, repeek, thread, Post{Files: f, Text: "x"}); err == nil {
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
		err := Send(api, store, nil, repeek, thread, Post{Text: text})
		if err == nil || !strings.Contains(err.Error(), "read in Discord by moderators") {
			t.Errorf("%q: %v", text, err)
		}
	}
	for _, text := range []string{
		"Finished: the overlay no longer covers the scoreboard on 1080p and 1440p.",
		"Win rate is now shown per map, 3/4 of maps checked, and/or per team.",
	} {
		if err := Send(api, store, nil, repeek, thread, Post{Text: text}); err != nil {
			t.Errorf("%q was refused: %v", text, err)
		}
	}
	if len(api.Sent) != 2 {
		t.Errorf("%d posts, want 2", len(api.Sent))
	}
}

// The one link a post may carry: a pull request, as its own line, with no preview.
var repeek Repos = func(string) (string, error) { return "timche/repeek", nil }

func TestAPullRequestIsItsOwnLineWithNoPreview(t *testing.T) {
	api, store := fixture(t)

	err := Send(api, store, nil, repeek, thread, Post{Text: "Ready: average elo now shows for each team.", PR: "https://github.com/timche/repeek/pull/42"})
	if err != nil {
		t.Fatal(err)
	}
	msg := api.Sent[0].Msg
	if msg.Content != "Ready: average elo now shows for each team.\n#42 (https://github.com/timche/repeek/pull/42)" {
		t.Errorf("content = %q", msg.Content)
	}
	if msg.Flags&discordgo.MessageFlagsSuppressEmbeds == 0 {
		t.Error("the link would show a preview")
	}

	for _, url := range []string{
		"https://github.com/timche/repeek/pull/42/files",
		"https://github.com/timche/repeek/issues/42",
		"http://github.com/timche/repeek/pull/42",
		"https://github.com.evil.example/timche/repeek/pull/42",
		"https://github.com/timche/repeek/pull/42?x=1",
		"https://github.com/timche/repeek/pull/abc",
	} {
		if err := Send(api, store, nil, repeek, thread, Post{Text: "x", PR: url}); err == nil {
			t.Errorf("%s was taken", url)
		}
	}
	if line, err := PRLine("https://github.com/timche/repeek/pull/7/"); err != nil || line != "#7 (https://github.com/timche/repeek/pull/7/)" {
		t.Errorf("line = %q, %v", line, err)
	}
	if len(api.Sent) != 1 {
		t.Errorf("%d posts, want 1", len(api.Sent))
	}
}

// A post with a state puts the session in it; one without leaves it as it is.
func TestAPostsStateReachesTheSession(t *testing.T) {
	api, store := fixture(t)
	marks := &mark.Marker{API: api, Path: filepath.Join(t.TempDir(), "marks.json"), Now: time.Now, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	origin := state.Ref{Channel: "c", Message: "m1"}
	marks.Started(thread, origin)

	Send(api, store, marks, repeek, thread, Post{Text: "a note"})
	if got := api.Showing("m1"); len(got) != 1 || got[0] != "⏳" {
		t.Errorf("a plain post changed the state: %v", got)
	}
	Send(api, store, marks, repeek, thread, Post{Text: "Ready for review.", Settles: mark.Asking})
	if got := api.Showing("m1"); len(got) != 1 || got[0] != "❓" {
		t.Errorf("after --review: %v", got)
	}
	Send(api, store, marks, repeek, thread, Post{Text: "Merged.", Settles: mark.Finished})
	if got := api.Showing("m1"); len(got) != 1 || got[0] != "✅" {
		t.Errorf("after --done: %v", got)
	}
}

func TestTheGuardCatchesEveryFormOfLinkAndPath(t *testing.T) {
	for _, text := range []string{
		"see example.com for it",
		"the docs are at repeek.gg/faq",
		"ask on support.faceit.com",
		"try mochi.dev",
		"it is on ftp://files.example/x",
		"or ssh://host",
		"mail mailto:tim@example.com",
		"open file:report.pdf",
		"the screenshot is in ~/Desktop",
		"the binary is /usr",
		"logged to /tmp/x",
		"changed src/app/main.ts",
		"see components/Elo.tsx today",
		"(docs/README.md)",
	} {
		if err := CheckText(text); err == nil {
			t.Errorf("%q passed", text)
		}
	}
	for _, text := range []string{
		"3/4 of maps, and/or per team, a/b tested",
		"e.g. on 1080p, version v1.2 and 3.5 seconds",
		"It works now. The overlay is gone.",
		"win rate 50/50, ratio 2:1",
		"Done, i.e. finished.",
	} {
		if err := CheckText(text); err != nil {
			t.Errorf("%q was refused: %v", text, err)
		}
	}
}

// Only a pull request of the repository the session works in.
func TestAPullRequestOfAnotherRepositoryIsRefused(t *testing.T) {
	api, store := fixture(t)
	for _, url := range []string{
		"https://github.com/someone/repeek/pull/1",
		"https://github.com/timche/mac-mini/pull/1",
	} {
		if err := Send(api, store, nil, repeek, thread, Post{Text: "x", PR: url}); err == nil {
			t.Errorf("%s was posted", url)
		}
	}
	if err := Send(api, store, nil, repeek, thread, Post{Text: "x", PR: "https://github.com/Timche/Repeek/pull/3"}); err != nil {
		t.Errorf("the session's own repository, differently cased, was refused: %v", err)
	}
	if err := Send(api, store, nil, nil, thread, Post{Text: "x", PR: "https://github.com/timche/repeek/pull/3"}); err == nil {
		t.Error("a pull request was posted with nothing to check it against")
	}
	if len(api.Sent) != 1 {
		t.Errorf("%d posts, want 1", len(api.Sent))
	}
}
