package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/timche/mac-mini/mochi/internal/config"
	"path/filepath"
	"testing"
)

func TestHistoryTakesNoChannel(t *testing.T) {
	for _, args := range [][]string{
		{"--channel", "1300000000000000002"},
		{"1300000000000000002"},
		{"--before", "1300000000000000002", "--after", "1300000000000000003"},
	} {
		if _, err := historyArgs(args); err == nil {
			t.Errorf("%v parsed", args)
		}
	}
	q, err := historyArgs([]string{"--around", "1300000000000000002", "--limit", "100"})
	if err != nil || q.Around != "1300000000000000002" || q.Limit != 100 || q.Post {
		t.Errorf("%+v, %v", q, err)
	}
	if q, err := historyArgs([]string{"--post"}); err != nil || !q.Post {
		t.Errorf("--post: %+v, %v", q, err)
	}
}

func TestSplitEvalTakesAModelAndAnEffort(t *testing.T) {
	base := config.Split{Model: "sonnet", Effort: "medium"}
	s, err := splitArgs(base, []string{"--model", "haiku", "--effort", "low"})
	if err != nil || s != (config.Split{Model: "haiku", Effort: "low"}) {
		t.Errorf("%+v, %v", s, err)
	}
	if s, _ := splitArgs(base, nil); s != base {
		t.Errorf("the defaults are not the config's: %+v", s)
	}
	for _, args := range [][]string{{"--model", "--tools"}, {"cases.jsonl"}, {"--effort", ""}} {
		if _, err := splitArgs(base, args); err == nil {
			t.Errorf("%v parsed", args)
		}
	}
}

func TestPostTakesTextFilesAStateAndAPR(t *testing.T) {
	shot := filepath.Join(t.TempDir(), "shot.png")
	os.WriteFile(shot, []byte("png"), 0o644)

	p, err := postArgs([]string{"--review", "--pr", "https://github.com/timche/repeek/pull/42", "--file", shot, "--file", shot, "see", "this"}, nil)
	if err != nil || len(p.Files) != 2 || p.Text != "see this" || p.Settles != "❓" || p.PR != "https://github.com/timche/repeek/pull/42" {
		t.Errorf("%+v %v", p, err)
	}
	if p, _ := postArgs([]string{"--done", "merged"}, nil); p.Settles != "✅" {
		t.Errorf("--done settles %q", p.Settles)
	}
	if p, _ := postArgs([]string{"a note"}, nil); p.Settles != "" {
		t.Errorf("a plain post settles %q", p.Settles)
	}
	if p, err := postArgs([]string{"--done", "--close", "merged"}, nil); err != nil || !p.Close || p.Settles != "✅" {
		t.Errorf("--done --close: %+v, %v", p, err)
	}
	if p, err := postArgs([]string{"--type", "bug"}, nil); err != nil || p.Type != "bug" || p.Text != "" {
		t.Errorf("--type alone: %+v, %v", p, err)
	}
	for _, args := range [][]string{
		{},
		{"--file", shot},
		{"--file", "/nowhere", "hi"},
		{"see", "https://example.com"},
		{"--review", "--done", "x"},
		{"--pr", "https://github.com/timche/repeek/issues/42", "x"},
		{"--type", "chore"},
		{"--close", "x"},
		{"--review", "--close", "x"},
	} {
		if _, err := postArgs(args, nil); err == nil {
			t.Errorf("%v parsed", args)
		}
	}
}

// Before `op run` the session's server is not known yet, so a link any server allows passes, to
// be held to the session's own server once it is.
func TestPostTakesALinkSomeServerAllows(t *testing.T) {
	if _, err := postArgs([]string{"see https://repeek.gg/download"}, []string{"repeek.gg"}); err != nil {
		t.Error(err)
	}
	if _, err := postArgs([]string{"see https://example.com"}, []string{"repeek.gg"}); err == nil {
		t.Error("a link no server allows passed")
	}
}

func gitCheckout(t *testing.T, remote string) string {
	t.Helper()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	for _, args := range [][]string{{"init", "-q", dir}, {"-C", dir, "remote", "add", "origin", remote}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return dir
}

// Any session may announce, mochi's or not, from a checkout of a mapped repository: the server
// is the one whose repository has the checkout's origin, and everything that can be refused is
// refused before `op run`, which a PATH with git and no op shows by being the one error left.
func TestAnnounceFindsTheServerByTheCheckoutsOrigin(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	mapped := gitCheckout(t, "git@github.com:timche/repeek.git")
	os.Mkdir(filepath.Join(mapped, ".mochi"), 0o755)
	os.WriteFile(filepath.Join(mapped, ".mochi", "config.json"), []byte(`{"announce": "1378670204795617340", "links": ["repeek.gg"]}`), 0o644)
	other := gitCheckout(t, "git@github.com:timche/mac-mini.git")
	cfg := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(cfg, []byte(`{"user": "94004892942741504", "server": [`+
		`{"guild": "1220378778665095291", "workspace": "repeek", "repo": "`+mapped+`"},`+
		`{"guild": "1220378778665095292", "workspace": "mini", "repo": "`+other+`"}]}`), 0o644)
	env := filepath.Join(t.TempDir(), "mochi.env.op")
	os.WriteFile(env, []byte("MOCHI_DISCORD_BOT_TOKEN=op://dev/x/y\n"), 0o644)
	bin := t.TempDir()
	os.Symlink(git, filepath.Join(bin, "git"))
	t.Setenv("MOCHI_CONFIG", cfg)
	t.Setenv("MOCHI_ENV_FILE", env)
	t.Setenv("MOCHI_THREAD", "")
	t.Setenv("PATH", bin)

	worktree := gitCheckout(t, "https://github.com/timche/repeek")
	os.MkdirAll(filepath.Join(worktree, "src"), 0o755)
	t.Chdir(filepath.Join(worktree, "src"))
	if err := run([]string{"announce", "Average elo now shows for each team, see https://repeek.gg/elo"}); err == nil || err.Error() != "no op on PATH, so the bot token cannot be resolved" {
		t.Errorf("from a checkout of the mapped repository: %v", err)
	}
	if err := run([]string{"announce", "see https://example.com"}); err == nil || !strings.Contains(err.Error(), "example.com") {
		t.Errorf("a link the server does not allow: %v", err)
	}
	if err := run([]string{"announce", "--pr", "https://github.com/timche/mac-mini/pull/1", "Ready."}); err == nil || !strings.Contains(err.Error(), "timche/mac-mini") {
		t.Errorf("another repository's pull request: %v", err)
	}

	t.Chdir(other)
	if err := run([]string{"announce", "hi"}); err == nil || !strings.Contains(err.Error(), "no announce channel") {
		t.Errorf("a server with no announce channel: %v", err)
	}
	t.Chdir(gitCheckout(t, "git@github.com:someone/else.git"))
	if err := run([]string{"announce", "hi"}); err == nil || !strings.Contains(err.Error(), "no server is mapped") {
		t.Errorf("an unmapped repository: %v", err)
	}
	t.Chdir(t.TempDir())
	if err := run([]string{"announce", "hi"}); err == nil || !strings.Contains(err.Error(), "no checkout") {
		t.Errorf("no checkout: %v", err)
	}
}

// The announce channel and the domains are the mapped repository's, from its
// .mochi/config.json as it stands: none, or one that cannot be read, is no announcing.
func TestAnnounceReadsTheRepositorysSettings(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	mapped := gitCheckout(t, "git@github.com:timche/repeek.git")
	cfg := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(cfg, []byte(`{"user": "94004892942741504", "server": [{"guild": "1220378778665095291", "workspace": "repeek", "repo": "`+mapped+`"}]}`), 0o644)
	env := filepath.Join(t.TempDir(), "mochi.env.op")
	os.WriteFile(env, []byte("MOCHI_DISCORD_BOT_TOKEN=op://dev/x/y\n"), 0o644)
	bin := t.TempDir()
	os.Symlink(git, filepath.Join(bin, "git"))
	t.Setenv("MOCHI_CONFIG", cfg)
	t.Setenv("MOCHI_ENV_FILE", env)
	t.Setenv("MOCHI_THREAD", "")
	t.Setenv("PATH", bin)
	t.Chdir(gitCheckout(t, "https://github.com/timche/repeek"))

	if err := run([]string{"announce", "hi"}); err == nil || err.Error() != "this project has no announce channel in .mochi/config.json" {
		t.Errorf("no .mochi/config.json: %v", err)
	}

	os.Mkdir(filepath.Join(mapped, ".mochi"), 0o755)
	settings := filepath.Join(mapped, ".mochi", "config.json")
	os.WriteFile(settings, []byte(`{"announce": "1378670204795617340", "links": ["repeek.gg"]}`), 0o644)
	if err := run([]string{"announce", "Average elo now shows for each team, see https://repeek.gg/elo"}); err == nil || err.Error() != "no op on PATH, so the bot token cannot be resolved" {
		t.Errorf("from the repository's settings: %v", err)
	}

	os.WriteFile(settings, []byte(`{"links": ["repeek.gg"]}`), 0o644)
	if err := run([]string{"announce", "hi"}); err == nil || err.Error() != "this project has no announce channel in .mochi/config.json" {
		t.Errorf("no announce key: %v", err)
	}

	os.WriteFile(settings, []byte(`{"announce": "1378670204795617340", "links": ["repeek.gg"], "chanel": "1"}`), 0o644)
	if err := run([]string{"announce", "hi"}); err == nil || !strings.Contains(err.Error(), "announce channel is not known") || !strings.Contains(err.Error(), "chanel") {
		t.Errorf("broken: %v", err)
	}
	if err := run([]string{"announce", "see https://repeek.gg"}); err == nil || !strings.Contains(err.Error(), "found https://repeek.gg") || !strings.Contains(err.Error(), "chanel") {
		t.Errorf("broken, a link: %v", err)
	}
}

func TestAnnounceTakesTextFilesAndAPROnly(t *testing.T) {
	shot := filepath.Join(t.TempDir(), "shot.png")
	os.WriteFile(shot, []byte("png"), 0o644)
	p, err := announceArgs([]string{"--pr", "https://github.com/timche/repeek/pull/42", "--file", shot, "new", "elo"}, nil)
	if err != nil || len(p.Files) != 1 || p.Text != "new elo" || p.PR != "https://github.com/timche/repeek/pull/42" {
		t.Errorf("%+v %v", p, err)
	}
	for _, args := range [][]string{{}, {"--file", shot}, {"--done", "x"}, {"--review", "x"}, {"--type", "bug", "x"}, {"--close", "x"}} {
		if _, err := announceArgs(args, nil); err == nil {
			t.Errorf("%v parsed", args)
		}
	}
	args, err := textStdin([]string{"--file", shot}, strings.NewReader("**New**\n- elo\n"), func(a []string) error { _, err := announceArgs(a, nil); return err })
	if err != nil {
		t.Fatal(err)
	}
	if p, err := announceArgs(args, nil); err != nil || p.Text != "**New**\n- elo" {
		t.Errorf("%+v %v", p, err)
	}
}

const ownLink = "https://discord.com/channels/1220378778665095291/1378670204795617340/1400000000000000001"

func TestEditTakesALinkTextAndDropFiles(t *testing.T) {
	link, c, err := editArgs([]string{"--drop-files", "--pr", "https://github.com/timche/repeek/pull/4", ownLink, "fixed", "text"})
	if err != nil || link != ownLink || c.Text != "fixed text" || !c.DropFiles || c.PR != "https://github.com/timche/repeek/pull/4" {
		t.Errorf("%q %+v %v", link, c, err)
	}
	args, err := textStdin([]string{ownLink}, strings.NewReader("-- **Fixed**\n- now\n"), func(a []string) error { _, _, err := editArgs(a); return err })
	if err != nil {
		t.Fatal(err)
	}
	if _, c, err := editArgs(args); err != nil || c.Text != "-- **Fixed**\n- now" {
		t.Errorf("%+v %v", c, err)
	}
	for _, args := range [][]string{{}, {ownLink}, {"--drop-files", ownLink}, {"--review", ownLink, "x"}} {
		if _, _, err := editArgs(args); err == nil {
			t.Errorf("%v parsed", args)
		}
	}
	if link, err := deleteArgs([]string{ownLink}); err != nil || link != ownLink {
		t.Errorf("%q %v", link, err)
	}
	for _, args := range [][]string{{}, {ownLink, "x"}, {"--drop-files", ownLink}} {
		if _, err := deleteArgs(args); err == nil {
			t.Errorf("delete %v parsed", args)
		}
	}
}

// Any session may fix what it posted, with no MOCHI_THREAD; a link outside a mapped server, one
// that is no message's link, or text a post could not carry is refused before `op run`, which a
// PATH with no op shows by being the one error left otherwise.
func TestEditAndDeleteRefuseBeforeOp(t *testing.T) {
	repo := t.TempDir()
	os.Mkdir(filepath.Join(repo, ".mochi"), 0o755)
	os.WriteFile(filepath.Join(repo, ".mochi", "config.json"), []byte(`{"announce": "1378670204795617340", "links": ["repeek.gg"]}`), 0o644)
	cfg := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(cfg, []byte(`{"user": "94004892942741504", "server": [{"guild": "1220378778665095291", "workspace": "repeek", "repo": "`+repo+`"}]}`), 0o644)
	env := filepath.Join(t.TempDir(), "mochi.env.op")
	os.WriteFile(env, []byte("MOCHI_DISCORD_BOT_TOKEN=op://dev/x/y\n"), 0o644)
	t.Setenv("MOCHI_CONFIG", cfg)
	t.Setenv("MOCHI_ENV_FILE", env)
	t.Setenv("MOCHI_THREAD", "")
	t.Setenv("PATH", t.TempDir())

	const reachedOp = "no op on PATH, so the bot token cannot be resolved"
	for _, args := range [][]string{{"edit", ownLink, "Fixed, see https://repeek.gg/elo"}, {"delete", ownLink}} {
		if err := run(args); err == nil || err.Error() != reachedOp {
			t.Errorf("%v: %v", args, err)
		}
	}
	foreign := strings.Replace(ownLink, "1220378778665095291", "1220378778665095299", 1)
	for _, args := range [][]string{
		{"edit", foreign, "x"},
		{"delete", foreign},
		{"edit", "https://discord.gg/abc", "x"},
		{"delete", ownLink + "/x"},
		{"edit", ownLink, "see https://example.com"},
		{"edit", ownLink, "it is in ~/Library/Logs/mochi.log"},
	} {
		if err := run(args); err == nil || err.Error() == reachedOp {
			t.Errorf("%v: %v", args, err)
		}
	}
}

// The announce skill this repository ships: model-invocable, loaded only when the user asks to
// share work with the team, and independent of mochi's own sessions.
func TestTheAnnounceSkillSaysWhenAndHow(t *testing.T) {
	_, here, _, _ := runtime.Caller(0)
	body, err := os.ReadFile(filepath.Join(filepath.Dir(here), "..", "claude-plugins", "mochi", "skills", "announce", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	end := strings.Index(text[4:], "\n---\n")
	if !strings.HasPrefix(text, "---\nname: announce\ndescription: ") || end < 0 {
		t.Fatalf("no frontmatter:\n%.300s", text)
	}
	front := text[:end+4]
	for _, want := range []string{"share with the team", "announce this", "never otherwise"} {
		if !strings.Contains(front, want) {
			t.Errorf("the description lacks %q", want)
		}
	}
	if strings.Contains(front, "disable-model-invocation") || strings.Contains(front, "MOCHI_THREAD") {
		t.Errorf("the skill is not the session's to load whenever it is asked:\n%s", front)
	}
	// Where the project's settings live is the mochi skill's to say, not this one's.
	if strings.Contains(text, ".mochi") || strings.Contains(text, "MOCHI.md") {
		t.Error("the announce skill says where the project's settings live")
	}
	for _, want := range []string{
		"mochi announce",
		"mostly moderators who are not technical",
		"what players will notice",
		"**Screenshots**",
		"**Videos**",
		"Post right away",
		"the go-ahead",
		"its link",
		"No internals",
		"If something posted shouldn't be there, fix it with `mochi edit <link>` (or `mochi delete <link>`) right away and tell the user.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the skill lacks %q", want)
		}
	}
	if strings.Contains(text, "mochi post") {
		t.Error("the skill names mochi post, which is the mochi skill's")
	}
}

// The listener's preflight of a new build: the config read the way a start reads it.
func TestVersionChecksTheConfigWhenAsked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("MOCHI_CONFIG", path)
	if err := run([]string{"version"}); err != nil {
		t.Errorf("plain version read the config: %v", err)
	}
	if err := run([]string{"version", "--check-config"}); err == nil {
		t.Error("a missing config passed")
	}
	os.WriteFile(path, []byte(`{"user": "94004892942741504"}`), 0o644)
	if err := run([]string{"version", "--check-config"}); err != nil {
		t.Errorf("a good config failed: %v", err)
	}
}

// Outside a session there is no thread to post to, and saying so costs no 1Password read.
func TestPostAndHistoryOutsideASessionStopBeforeOp(t *testing.T) {
	t.Setenv("MOCHI_THREAD", "")
	t.Setenv("MOCHI_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	os.WriteFile(os.Getenv("MOCHI_CONFIG"), []byte(`{"user": "94004892942741504"}`), 0o644)
	t.Setenv("PATH", t.TempDir())

	for _, args := range [][]string{{"post", "hi"}, {"history"}} {
		if err := run(args); err == nil || err.Error() == "no op on PATH, so the bot token cannot be resolved" {
			t.Errorf("%v: %v", args, err)
		}
	}
}

// A post of several lines comes on stdin, and its lines reach the post as written.
func TestPostTextCanComeOnStdin(t *testing.T) {
	text := "**Average elo shows for each team**\n\n**What changed**\n- The lobby shows each team's average elo\n"
	args, err := postStdin([]string{"--review"}, strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	p, err := postArgs(args, nil)
	if err != nil || p.Text != strings.TrimSpace(text) || p.Settles != "❓" {
		t.Errorf("%+v %v", p, err)
	}

	// Text given as an argument leaves stdin alone.
	args, _ = postStdin([]string{"inline"}, strings.NewReader("not read"))
	if p, _ := postArgs(args, nil); p.Text != "inline" {
		t.Errorf("text = %q", p.Text)
	}
	if args, _ := postStdin(nil, strings.NewReader("   ")); args != nil {
		if _, err := postArgs(args, nil); err == nil {
			t.Error("an empty stdin was a post")
		}
	}
}

// The hook's command: no output, no config, nothing written outside a mochi session.
func TestSessionEventIsSilentAndNeedsNoConfig(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("MOCHI_CACHE_DIR", cache)
	t.Setenv("MOCHI_CONFIG", filepath.Join(t.TempDir(), "missing.toml"))

	t.Setenv("MOCHI_THREAD", "")
	if err := run([]string{"session-event", "stop"}); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Join(cache, "events")); len(entries) != 0 {
		t.Errorf("wrote %v outside a session", entries)
	}

	t.Setenv("MOCHI_THREAD", "1562147652450394154")
	if err := run([]string{"session-event", "ask"}); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Join(cache, "events")); len(entries) != 1 {
		t.Errorf("events = %v", entries)
	}
}

// The scratch folder is named as Claude Code names it: the project's path with every character
// but a letter or a digit a dash, under the user's id.
func TestTheScratchFolderIsNamedAsClaudeCodeNamesIt(t *testing.T) {
	t.Setenv("CLAUDE_CODE_TMPDIR", "/somewhere/else")
	want := fmt.Sprintf("/Users/timche/.cache/claude-tmp/claude-%d/-Users-timche--mac-mini", os.Getuid())
	if got := scratch("/Users/timche", "/Users/timche/.mac-mini"); got != want {
		t.Errorf("%q, want %q", got, want)
	}
	if got := scratch("/Users/x", "/Users/x/projects/repeek"); !strings.HasSuffix(got, "/-Users-x-projects-repeek") {
		t.Errorf("%q", got)
	}
}
