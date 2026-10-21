package post

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/discord"

	"github.com/timche/mac-mini/mochi/internal/chat/chattest"
	"github.com/timche/mac-mini/mochi/internal/mark"
	"github.com/timche/mac-mini/mochi/internal/state"
)

const thread = "1300000000000000005"

func fixture(t *testing.T) (*chattest.Fake, state.Store) {
	t.Helper()
	api := chattest.New()
	api.AddChannel(&discord.Channel{ID: thread, Type: discord.ChannelTypeGuildPublicThread})
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

	if err := Send(api, store, nil, repeek, thread, Post{Files: []string{shot}, Roots: anywhere, Text: "Finished — @everyone <@94004892942741504>"}); err != nil {
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

	if err := Send(api, store, nil, repeek, thread, Post{Files: files, Roots: anywhere, Text: "Finished: the overlay no longer covers the scoreboard."}); err != nil {
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
	os.WriteFile(big, make([]byte, 10<<20+1), 0o644)
	for name, f := range map[string][]string{"eleven files": eleven, "a file over 10 MB": {big}, "no such file": {filepath.Join(dir, "gone.png")}} {
		if err := Send(api, store, nil, repeek, thread, Post{Files: f, Roots: anywhere, Text: "x"}); err == nil {
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
	if msg.Content != "Ready: average elo now shows for each team.\n\n#42 (https://github.com/timche/repeek/pull/42)" {
		t.Errorf("content = %q", msg.Content)
	}
	if msg.Flags&discord.MessageFlagsSuppressEmbeds == 0 {
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

// --type refiles a forum post and needs no text; in a thread it is refused before anything is
// posted.
func TestATypeRefilesAPostWithNoText(t *testing.T) {
	api, store := fixture(t)
	api.AddChannel(&discord.Channel{ID: "f1", AvailableTags: []discord.ForumTag{{ID: "t-run", Name: "Running"}, {ID: "t-bug", Name: "Bug"}}})
	marks := &mark.Marker{API: api, Path: filepath.Join(t.TempDir(), "marks.json"), Now: time.Now, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	marks.Posted(thread, "Overlay", "⏳ Overlay", "f1", "")
	marks.Started(thread)

	if err := Send(api, store, marks, repeek, thread, Post{Type: "bug"}); err != nil {
		t.Fatal(err)
	}
	if got := api.Tagged[thread]; len(got) != 1 || got[0] != "t-bug" {
		t.Errorf("tags = %v", got)
	}
	if len(api.Sent) != 0 {
		t.Errorf("a type with no text posted %d messages", len(api.Sent))
	}

	plain, store2 := fixture(t)
	other := &mark.Marker{API: plain, Path: filepath.Join(t.TempDir(), "marks.json"), Now: time.Now, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := Send(plain, store2, other, repeek, thread, Post{Text: "it is a bug", Type: "bug"}); !errors.Is(err, mark.ErrNoType) || len(plain.Sent) != 0 {
		t.Errorf("a thread's type: %v, %d posts", err, len(plain.Sent))
	}
}

// A post with a state puts the session in it, and its name with it; one without leaves both.
func TestAPostsStateReachesTheSession(t *testing.T) {
	api, store := fixture(t)
	marks := &mark.Marker{API: api, Path: filepath.Join(t.TempDir(), "marks.json"), Now: time.Now, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	marks.Named(thread, "Overlay", "⏳ Overlay")
	marks.Started(thread)
	shows := func(want string) {
		t.Helper()
		if got, _ := marks.Session(thread); got != want || api.Channels[thread].Name != want+" Overlay" {
			t.Errorf("state %q, name %q, want %q", got, api.Channels[thread].Name, want)
		}
	}

	Send(api, store, marks, repeek, thread, Post{Text: "a note"})
	if got, _ := marks.Session(thread); got != mark.Running {
		t.Errorf("a plain post changed the state: %q", got)
	}
	Send(api, store, marks, repeek, thread, Post{Text: "Ready for review.", Settles: mark.Asking})
	shows(mark.Asking)
	Send(api, store, marks, repeek, thread, Post{Text: "Merged.", Settles: mark.Finished})
	shows(mark.Finished)
	if len(api.Reactions) != 0 {
		t.Errorf("a post reacted: %v", api.Reactions)
	}
}

func TestTheGuardCatchesEveryFormOfLinkAndPath(t *testing.T) {
	for _, text := range []string{
		"see example.com for it",
		"the docs are at repeek.gg/faq",
		"ask on support.example.com",
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
		if err := CheckText(text, nil); err == nil {
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
		if err := CheckText(text, nil); err != nil {
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

// Newlines reach Discord as written.
func TestAPostKeepsItsLines(t *testing.T) {
	api, store := fixture(t)
	text := "**Average elo shows for each team**\n\n**What changed**\n- The lobby shows each team's average elo\n- Teams are compared at a glance\n\n**Screenshots**\n1. Before — no average\n2. After — the average beside each team"
	before, after := filepath.Join(t.TempDir(), "before.png"), filepath.Join(t.TempDir(), "after.png")
	os.WriteFile(before, []byte("png"), 0o644)
	os.WriteFile(after, []byte("png"), 0o644)
	if err := Send(api, store, nil, repeek, thread, Post{Text: text, Files: []string{before, after}, Roots: anywhere, PR: "https://github.com/timche/repeek/pull/9"}); err != nil {
		t.Fatal(err)
	}
	if got := api.Sent[0].Msg.Content; got != text+"\n\n#9 (https://github.com/timche/repeek/pull/9)" {
		t.Errorf("content:\n%s", got)
	}
}

// The product and FACEIT may be named; a page on them is still a link.
func TestRepeekAndFaceitMayBeNamed(t *testing.T) {
	for _, text := range []string{
		"Average elo now shows on repeek.gg for each team.",
		"It matches what FACEIT.com shows, and play.repeek.gg too.",
		"Same as on www-free faceit.com.",
	} {
		if err := CheckText(text, nil); err != nil {
			t.Errorf("%q was refused: %v", text, err)
		}
	}
	for _, text := range []string{
		"see repeek.gg/faq",
		"on faceit.com/en/players/x",
		"https://repeek.gg",
		"www.faceit.com",
		"notrepeek.gg.evil.com",
		"faceit.com.example.org",
		"repeek.gg and example.com",
	} {
		if err := CheckText(text, nil); err == nil {
			t.Errorf("%q passed", text)
		}
	}
}

// The proof heading names what is attached, and a post whose heading does not is refused.
func TestTheProofHeadingMustMatchTheFiles(t *testing.T) {
	dir := t.TempDir()
	shot, clip := filepath.Join(dir, "after.png"), filepath.Join(dir, "after.mov")
	os.WriteFile(shot, []byte("png"), 0o644)
	os.WriteFile(clip, []byte("mov"), 0o644)
	text := func(heading string) string { return "**Fixed**\n\n" + heading + "\n1. After — it works" }

	for _, ok := range []Post{
		{Text: text("**Screenshots**"), Files: []string{shot}},
		{Text: text("**Videos**"), Files: []string{clip}},
		{Text: text("**Screenshots and videos**"), Files: []string{shot, clip}},
		{Text: "**Fixed**\n\n- a note", Files: []string{clip}},
	} {
		if err := ok.Check(nil); err != nil {
			t.Errorf("%q with %v: %v", ok.Text, ok.Files, err)
		}
	}
	for _, bad := range []Post{
		{Text: text("**Screenshots**"), Files: []string{clip}},
		{Text: text("**Videos**"), Files: []string{shot}},
		{Text: text("**Screenshots**"), Files: []string{shot, clip}},
		{Text: text("**Screenshots and videos**"), Files: []string{shot}},
		{Text: text("**Screenshots**")},
	} {
		if err := bad.Check(nil); err == nil {
			t.Errorf("%q with %v was taken", bad.Text, bad.Files)
		}
	}
	err := Post{Text: text("**Screenshots**"), Files: []string{clip}}.Check(nil)
	if err == nil || !strings.Contains(err.Error(), "**Videos**") {
		t.Errorf("the refusal does not say what to use: %v", err)
	}
}

// Every image and video kind counts toward its heading, whatever the case of its extension.
func TestEveryMediaKindCountsTowardItsHeading(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.avif", "a.TIFF", "a.tif", "a.svg", "a.apng", "a.png"} {
		path := filepath.Join(dir, name)
		os.WriteFile(path, []byte("x"), 0o644)
		if err := CheckProof("**Screenshots**", []string{path}); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, name := range []string{"a.ogv", "a.WMV", "a.mp4"} {
		path := filepath.Join(dir, name)
		os.WriteFile(path, []byte("x"), 0o644)
		if err := CheckProof("**Videos**", []string{path}); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestAPullRequestAddressIsItsRepoAndNumber(t *testing.T) {
	repo, n, ok := ParsePR("https://github.com/timche/repeek/pull/42")
	if !ok || repo != "timche/repeek" || n != 42 {
		t.Errorf("= %q %d %v", repo, n, ok)
	}
	if _, _, ok := ParsePR("https://github.com/timche/repeek/issues/42"); ok {
		t.Error("an issue was taken for a pull request")
	}
}

// The pull request's line stands apart from the text, however the text ended.
func TestThePullRequestLineIsAParagraphOfItsOwn(t *testing.T) {
	api, store := fixture(t)
	if err := Send(api, store, nil, repeek, thread, Post{Text: "**Done**\n- the last point  \n\n \t\n", PR: "https://github.com/timche/repeek/pull/7"}); err != nil {
		t.Fatal(err)
	}
	if got := api.Sent[0].Msg.Content; got != "**Done**\n- the last point\n\n#7 (https://github.com/timche/repeek/pull/7)" {
		t.Errorf("content = %q", got)
	}
}

// A post whose checkout is everything, for the tests that are about something else.
var anywhere Roots = func(string) ([]string, error) { return []string{"/"}, nil }

// A file goes up only from inside the session's checkout, links followed, and never from its
// .git; one outside, or a link out of it, is refused before anything is posted.
func TestOnlyFilesInsideTheCheckoutGoUp(t *testing.T) {
	api, store := fixture(t)
	checkout, elsewhere := t.TempDir(), t.TempDir()
	in := filepath.Join(checkout, "build", "repeek-test-chrome.zip")
	os.MkdirAll(filepath.Dir(in), 0o755)
	os.WriteFile(in, []byte("zip"), 0o644)
	out := filepath.Join(elsewhere, "id_ed25519")
	os.WriteFile(out, []byte("key"), 0o600)
	link := filepath.Join(checkout, "innocent.png")
	os.Symlink(out, link)
	os.MkdirAll(filepath.Join(checkout, ".git"), 0o755)
	gitconfig := filepath.Join(checkout, ".git", "config")
	os.WriteFile(gitconfig, []byte("[remote]"), 0o644)
	scratchpad := t.TempDir()
	shot := filepath.Join(scratchpad, "session", "scratchpad", "after.png")
	os.MkdirAll(filepath.Dir(shot), 0o755)
	os.WriteFile(shot, []byte("png"), 0o644)
	root := Roots(func(string) ([]string, error) {
		return []string{checkout, scratchpad, filepath.Join(t.TempDir(), "gone")}, nil
	})

	if err := Send(api, store, nil, repeek, thread, Post{Text: "**Test build**\n1. Unzip it", Files: []string{in, shot}, Roots: root}); err != nil {
		t.Fatalf("a file in the checkout, under a heading of the project's own: %v", err)
	}
	upper := filepath.Join(checkout, ".GIT", "config")
	for name, file := range map[string]string{"outside": out, "a link out": link, "git's own": gitconfig, "git's own in capitals": upper, "with ..": filepath.Join(checkout, "..", filepath.Base(elsewhere), "id_ed25519")} {
		err := Send(api, store, nil, repeek, thread, Post{Text: "x", Files: []string{file}, Roots: root})
		if err == nil {
			t.Errorf("%s was posted", name)
		}
	}
	if err := Send(api, store, nil, repeek, thread, Post{Text: "x", Files: []string{in}}); err == nil {
		t.Error("a file was posted with no checkout to hold it to")
	}
	if len(api.Sent) != 1 {
		t.Errorf("%d posts, want 1", len(api.Sent))
	}
}

// The size a file may be is the server's: 10 MB, or more on a boosted one, and a refusal says
// to post without it.
func TestAFileIsHeldToTheServersUploadLimit(t *testing.T) {
	api, store := fixture(t)
	big := filepath.Join(t.TempDir(), "build.zip")
	os.WriteFile(big, make([]byte, 12<<20), 0o644)

	err := Send(api, store, nil, repeek, thread, Post{Text: "x", Files: []string{big}, Roots: anywhere})
	if err == nil || !strings.Contains(err.Error(), "12 MB, over the 10 MB") || !strings.Contains(err.Error(), "too large to attach") {
		t.Errorf("refusal = %v", err)
	}
	api.Tiers = map[string]discord.PremiumTier{"": discord.PremiumTier2}
	if err := Send(api, store, nil, repeek, thread, Post{Text: "x", Files: []string{big}, Roots: anywhere}); err != nil {
		t.Errorf("a boosted server refused 12 MB: %v", err)
	}
}

// Files with a secret's name, files git ignores in the checkout other than media and zips,
// and files linked under a second name are not posted; media and zips git ignores are.
func TestSecretLookingAndIgnoredFilesAreNotPosted(t *testing.T) {
	api, store := fixture(t)
	checkout := t.TempDir()
	for _, args := range [][]string{{"init", "-q", checkout}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	os.WriteFile(filepath.Join(checkout, ".gitignore"), []byte("local/\n"), 0o644)
	os.MkdirAll(filepath.Join(checkout, "local"), 0o755)
	write := func(rel string) string {
		p := filepath.Join(checkout, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x"), 0o644)
		return p
	}
	roots := Roots(func(string) ([]string, error) { return []string{checkout}, nil })

	png := filepath.Join(checkout, "local", "after.png")
	os.WriteFile(png, []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), 0o644)
	for _, ok := range []string{png, write("local/build.zip"), write("docs/notes.txt")} {
		if err := Send(api, store, nil, repeek, thread, Post{Text: "x", Files: []string{ok}, Roots: roots}); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	fakeShot := write("local/shot.png") // an extension that lies is no screenshot
	hard := write("docs/twice.txt")
	os.Link(hard, filepath.Join(checkout, "docs", "again.txt"))
	for _, bad := range []string{
		write(".env"), write(".env.local"), write("certs/server.PEM"), write("deploy.key"), write("store.p12"), write("a.pfx"),
		write("keys/id_ed25519"), write(".npmrc"), write(".netrc"), write(".pgpass"), write("aws/credentials"), write("release.keystore"),
		write(".git-credentials"), write("local/settings.json"), hard,
		fakeShot, write(".pypirc"), write(".htpasswd"), write("vault.kdbx"), write("AuthKey.p8"), write("infra/terraform.tfstate"),
	} {
		err := Send(api, store, nil, repeek, thread, Post{Text: "x", Files: []string{bad}, Roots: roots})
		if err == nil || !strings.Contains(err.Error(), "not posted") {
			t.Errorf("%s: %v", bad, err)
		}
	}
}

// What goes up is the file that was checked: a link's target, under its own name.
func TestTheCheckedFileIsTheOneSent(t *testing.T) {
	api, store := fixture(t)
	checkout := t.TempDir()
	target := filepath.Join(checkout, "after.png")
	os.WriteFile(target, []byte("the real one"), 0o644)
	link := filepath.Join(checkout, "shot.png")
	os.Symlink(target, link)
	roots := Roots(func(string) ([]string, error) { return []string{checkout}, nil })

	if err := Send(api, store, nil, repeek, thread, Post{Text: "x", Files: []string{link}, Roots: roots}); err != nil {
		t.Fatal(err)
	}
	if got := api.Sent[0].Files; len(got) != 1 || got["after.png"] != "the real one" {
		t.Errorf("files = %v", got)
	}
}

// Files each within the limit but over it together are refused as well.
func TestFilesAreHeldToTheLimitTogether(t *testing.T) {
	api, store := fixture(t)
	dir := t.TempDir()
	var files []string
	for _, name := range []string{"a.mov", "b.mov"} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, make([]byte, 6<<20), 0o644)
		files = append(files, p)
	}
	err := Send(api, store, nil, repeek, thread, Post{Text: "x", Files: files, Roots: anywhere})
	if err == nil || !strings.Contains(err.Error(), "12 MB together, over the 10 MB this server takes for one post") {
		t.Errorf("refusal = %v", err)
	}
	if len(api.Sent) != 0 {
		t.Error("a post too large together went up")
	}
}

// A post the scan finds a secret in, in its text or an attachment, is not sent and says what
// rule matched where; images and videos are not scanned; a scan that cannot run sends nothing;
// a checkout's own configuration is the one used.
func TestTheSecretScanStandsBeforeAPost(t *testing.T) {
	api, store := fixture(t)
	checkout, _ := filepath.EvalSymlinks(t.TempDir())
	os.WriteFile(filepath.Join(checkout, ".gitleaks.toml"), []byte(""), 0o644)
	build := filepath.Join(checkout, "build.zip")
	os.WriteFile(build, []byte("zip"), 0o644)
	shot := filepath.Join(checkout, "after.png")
	os.WriteFile(shot, []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), 0o644)
	roots := Roots(func(string) ([]string, error) { return []string{checkout}, nil })

	var scanned []string
	leak := ""
	run := func(_ context.Context, args []string, _ io.Reader) ([]byte, int, error) {
		target := args[len(args)-1]
		scanned = append(scanned, strings.Join(args, " "))
		if leak != "" && (target == leak || (leak == "text" && args[0] == "stdin")) {
			return []byte(`[{"RuleID":"aws-access-token","File":"` + target + `!config.txt","Secret":"REDACTED"}]`), 1, nil
		}
		return []byte("[]"), 0, nil
	}

	if err := Send(api, store, nil, repeek, thread, Post{Text: "ok", Files: []string{build, shot}, Roots: roots, Leaks: run}); err != nil {
		t.Fatal(err)
	}
	if len(scanned) != 2 || !strings.HasPrefix(scanned[0], "stdin ") || !strings.HasSuffix(scanned[1], " "+build) || !strings.Contains(scanned[1], "--config "+filepath.Join(checkout, ".gitleaks.toml")) {
		t.Errorf("scanned %q", scanned)
	}

	leak = build
	err := Send(api, store, nil, repeek, thread, Post{Text: "ok", Files: []string{build}, Roots: roots, Leaks: run})
	if err == nil || !strings.Contains(err.Error(), "found aws-access-token in "+build+"!config.txt") || strings.Contains(err.Error(), "REDACTED") {
		t.Errorf("a leak in a file: %v", err)
	}
	leak = "text"
	if err := Send(api, store, nil, repeek, thread, Post{Text: "the key", Roots: roots, Leaks: run}); err == nil || !strings.Contains(err.Error(), "in the post's text") {
		t.Errorf("a leak in the text: %v", err)
	}
	broken := func(context.Context, []string, io.Reader) ([]byte, int, error) {
		return nil, 0, errors.New("betterleaks is not on PATH")
	}
	if err := Send(api, store, nil, repeek, thread, Post{Text: "ok", Roots: roots, Leaks: broken}); err == nil || !strings.Contains(err.Error(), "the secret scan couldn't run") {
		t.Errorf("a scan that cannot run: %v", err)
	}
	if len(api.Sent) != 1 {
		t.Errorf("%d posts, want 1", len(api.Sent))
	}
}

// A file is media when its first bytes say so, not its name: every format mochi takes as an
// image or a video is recognised, and a key named .png is not.
func TestMediaIsKnownByItsBytes(t *testing.T) {
	dir := t.TempDir()
	for name, head := range map[string]string{
		"a.png": "\x89PNG\r\n\x1a\n", "a.jpg": "\xff\xd8\xff\xe0", "a.gif": "GIF89a", "a.webp": "RIFF\x00\x00\x00\x00WEBPVP8 ",
		"a.mp4": "\x00\x00\x00\x18ftypmp42", "a.mov": "\x00\x00\x00\x14ftypqt  ", "a.heic": "\x00\x00\x00\x18ftypheic", "a.avif": "\x00\x00\x00\x1cftypavif",
		"a.mkv": "\x1a\x45\xdf\xa3", "a.webm": "\x1a\x45\xdf\xa3", "a.avi": "RIFF\x00\x00\x00\x00AVI LIST", "a.ogv": "OggS\x00",
		"a.tiff": "II*\x00", "a.svg": "<?xml version=\"1.0\"?><svg xmlns=\"http://www.w3.org/2000/svg\">",
		"a.wmv": "\x30\x26\xb2\x75\x8e\x66\xcf\x11",
	} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(head), 0o644)
		if !isMedia(p) {
			t.Errorf("%s is not taken for media", name)
		}
	}
	for name, body := range map[string]string{"key.png": "-----BEGIN OPENSSH PRIVATE KEY-----", "notes.mov": "just text", "a.txt": "\x89PNG\r\n\x1a\n"} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(body), 0o644)
		if isMedia(p) {
			t.Errorf("%s is taken for media", name)
		}
	}
}

var stores = []string{"chromewebstore.google.com", "addons.mozilla.org", "repeek.gg"}

func allowed(string) []string { return stores }

// A server's own domains may be linked over https, and their subdomains; a look-alike, any
// other form of the same address and every other link are refused as before.
func TestAServersDomainsMayBeLinked(t *testing.T) {
	for _, text := range []string{
		"Get it from https://chromewebstore.google.com/detail/repeek/abcdef today.",
		"On Firefox: https://addons.mozilla.org/en-US/firefox/addon/repeek/",
		"See https://repeek.gg and https://www.repeek.gg/faq, or (https://play.repeek.gg).",
		"HTTPS://Repeek.GG/download",
		"Store: https://chromewebstore.google.com/detail/x, and repeek.gg as a name.",
	} {
		if err := CheckText(text, stores); err != nil {
			t.Errorf("%q was refused: %v", text, err)
		}
	}
	for _, text := range []string{
		"https://chromewebstore.google.com.evil.com/detail/x",
		"https://chromewebstore.google.com@evil.com/detail/x",
		"https://user@chromewebstore.google.com/detail/x",
		"https://evilchromewebstore.google.com/x",
		"https://addons.mozilla.org:8443/x",
		"http://chromewebstore.google.com/detail/x",
		"chromewebstore.google.com/detail/x",
		"www.repeek.gg",
		"https://example.com",
		"https://chromewebstore.google.com/detail/x and https://example.com",
		"https://chromewebstore.google.com/x see repeek.gg/faq",
		"https://google.com/x",
		"https://xn--repeek-gg.com",
		"ftp://repeek.gg/x",
		"https://repeek.gg/x then ~/Desktop/shot.png",
		"https://repeek.gg\\@evil.com",
	} {
		if err := CheckText(text, stores); err == nil {
			t.Errorf("%q passed", text)
		}
	}
	if err := CheckText("https://repeek.gg", nil); err == nil {
		t.Error("a link passed with no domains allowed")
	}
}

// A post with only allowed links keeps Discord's preview of them; one with a pull request line
// has none, for the pull request's sake.
func TestAnAllowedLinkKeepsItsPreviewUnlessThereIsAPullRequest(t *testing.T) {
	api, store := fixture(t)
	text := "**Repeek is in the Chrome Web Store**\n- https://chromewebstore.google.com/detail/repeek/abcdef"
	if err := Send(api, store, nil, repeek, thread, Post{Text: text, Links: allowed}); err != nil {
		t.Fatal(err)
	}
	if err := Send(api, store, nil, repeek, thread, Post{Text: text, Links: allowed, PR: "https://github.com/timche/repeek/pull/9"}); err != nil {
		t.Fatal(err)
	}
	if len(api.Sent) != 2 {
		t.Fatalf("sent = %d", len(api.Sent))
	}
	if api.Sent[0].Msg.Flags&discord.MessageFlagsSuppressEmbeds != 0 {
		t.Error("the allowed link's preview was suppressed")
	}
	if api.Sent[1].Msg.Flags&discord.MessageFlagsSuppressEmbeds == 0 {
		t.Error("the pull request's preview was not suppressed")
	}
	if err := Send(api, store, nil, repeek, thread, Post{Text: text}); err == nil {
		t.Error("a link was posted with no domains allowed for the server")
	}
}
