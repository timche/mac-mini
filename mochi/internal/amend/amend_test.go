package amend

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/timche/mac-mini/discord"

	"github.com/timche/mac-mini/mochi/internal/chat/chattest"
	"github.com/timche/mac-mini/mochi/internal/config"
	"github.com/timche/mac-mini/mochi/internal/state"
)

const (
	guild    = "1220378778665095291"
	foreign  = "1220378778665095299"
	announce = "1378670204795617340"
	forum    = "1562152175927558292"

	forumPost = "1562152175927558301" // another session's forum post
	theirs    = "1562152175927558302" // a thread somebody else started
	session   = "1562152175927558303" // this session's own thread
	general   = "1562152175927558304" // a channel mochi does not post in
	away      = "1562152175927558305" // a channel in a server mochi is not mapped to
)

var (
	bot = &discord.User{ID: "1500000000000000001", Username: "mochi", Bot: true}
	tim = &discord.User{ID: "94004892942741504", Username: "tim"}
)

type fixture struct {
	api   *chattest.Fake
	scope Scope
	mine  map[string]string
	other map[string]string
}

func setup(t *testing.T) fixture {
	t.Helper()
	api := chattest.New()
	api.Bot = bot
	for _, c := range []*discord.Channel{
		{ID: announce, GuildID: guild, Type: discord.ChannelTypeGuildText},
		{ID: forumPost, GuildID: guild, ParentID: forum, Type: discord.ChannelTypeGuildPublicThread},
		{ID: theirs, GuildID: guild, ParentID: general, Type: discord.ChannelTypeGuildPublicThread},
		{ID: session, GuildID: guild, ParentID: general, Type: discord.ChannelTypeGuildPublicThread},
		{ID: general, GuildID: guild, Type: discord.ChannelTypeGuildText},
		{ID: away, GuildID: foreign, Type: discord.ChannelTypeGuildText},
	} {
		api.AddChannel(c)
	}
	store := state.Store{Path: filepath.Join(t.TempDir(), "state.json")}
	for _, th := range []string{session, forumPost} {
		if err := store.Put(state.Session{Thread: th, Source: general, Guild: guild}); err != nil {
			t.Fatal(err)
		}
	}
	f := fixture{
		api:   api,
		scope: Scope{Servers: []config.Server{{Guild: guild, Repo: "/r", Announce: announce, Forum: forum, Links: []string{"chromewebstore.google.com", "repeek.gg"}}}, Store: store, Thread: session},
		mine:  map[string]string{},
		other: map[string]string{},
	}
	n := 0
	for _, ch := range []string{announce, forumPost, theirs, session, general, away} {
		n++
		f.mine[ch] = link(ch, api.Post(ch, n, bot, "**Old** <@94004892942741504>"))
		n++
		f.other[ch] = link(ch, api.Post(ch, n, tim, "hello"))
	}
	return f
}

func link(channel string, m *discord.Message) string {
	g := guild
	if channel == away {
		g = foreign
	}
	return "https://discord.com/channels/" + g + "/" + channel + "/" + m.ID
}

func repeek(string) (string, error) { return "timche/repeek", nil }

// mochi's own message, in the announce channel or this session's own thread,
// takes new text with nobody pinged and its attachments as they were.
func TestMochisOwnMessageIsEdited(t *testing.T) {
	f := setup(t)
	for _, ch := range []string{announce, session} {
		if err := Edit(f.api, f.scope, f.mine[ch], Change{Text: "**New** @everyone <@94004892942741504>", Repos: repeek}); err != nil {
			t.Errorf("%s: %v", ch, err)
		}
	}
	if len(f.api.Edits) != 2 {
		t.Fatalf("edits = %d", len(f.api.Edits))
	}
	e := f.api.Edits[0]
	if e.Channel != announce || e.ID != f.mine[announce][strings.LastIndex(f.mine[announce], "/")+1:] || *e.Content != "**New** @everyone <@94004892942741504>" {
		t.Errorf("edit = %+v", e)
	}
	if am := e.AllowedMentions; am == nil || am.Parse == nil || len(am.Parse) != 0 || len(am.Users) != 0 || len(am.Roles) != 0 {
		t.Errorf("the edit could ping: %+v", am)
	}
	if e.Attachments != nil {
		t.Error("the attachments were touched without --drop-files")
	}
	if e.Flags == nil || *e.Flags != 0 {
		t.Errorf("flags = %v", e.Flags)
	}
}

// --drop-files sends an empty list of attachments, which is what removes them all.
func TestDropFilesRemovesEveryAttachment(t *testing.T) {
	f := setup(t)
	if err := Edit(f.api, f.scope, f.mine[announce], Change{Text: "Fixed.", DropFiles: true}); err != nil {
		t.Fatal(err)
	}
	if a := f.api.Edits[0].Attachments; a == nil || len(*a) != 0 {
		t.Errorf("attachments = %v", a)
	}
}

// A pull request's line is added as a post adds it, with previews suppressed; an allowed link
// alone keeps its preview.
func TestAnEditCarriesAPullRequestAndAllowedLinks(t *testing.T) {
	f := setup(t)
	if err := Edit(f.api, f.scope, f.mine[session], Change{Text: "Ready.", PR: "https://github.com/timche/repeek/pull/9", Repos: repeek}); err != nil {
		t.Fatal(err)
	}
	if e := f.api.Edits[0]; *e.Content != "Ready.\n\n#9 (https://github.com/timche/repeek/pull/9)" || *e.Flags&discord.MessageFlagsSuppressEmbeds == 0 {
		t.Errorf("edit = %q, %v", *e.Content, *e.Flags)
	}
	if err := Edit(f.api, f.scope, f.mine[session], Change{Text: "Get it at https://chromewebstore.google.com/detail/x"}); err != nil {
		t.Fatal(err)
	}
	if e := f.api.Edits[1]; *e.Flags != 0 {
		t.Errorf("the allowed link's preview was suppressed: %v", *e.Flags)
	}
	if err := Edit(f.api, f.scope, f.mine[session], Change{Text: "Ready.", PR: "https://github.com/timche/mac-mini/pull/9", Repos: repeek}); err == nil {
		t.Error("another repository's pull request was edited in")
	}
}

// An edit's text is held to a post's guards, the secret scan included.
func TestAnEditIsHeldToAPostsGuards(t *testing.T) {
	f := setup(t)
	leak := func(_ context.Context, args []string, _ io.Reader) ([]byte, int, error) {
		return []byte(`[{"RuleID":"generic-api-key","File":"stdin","Secret":"REDACTED"}]`), 1, nil
	}
	for name, c := range map[string]Change{
		"a link":                     {Text: "see https://example.com"},
		"a look-alike":               {Text: "https://chromewebstore.google.com.evil.com/x"},
		"a path":                     {Text: "it is in ~/Library/Logs/mochi.log"},
		"a leak":                     {Text: "the key", Leaks: leak},
		"a heading with no files":    {Text: "**Screenshots**\n1. After", DropFiles: true},
		"no text":                    {Text: " "},
		"more than Discord takes":    {Text: strings.Repeat("x", 2001)},
		"a pull request not on file": {Text: "x", PR: "https://github.com/timche/repeek/issues/9", Repos: repeek},
	} {
		if err := Edit(f.api, f.scope, f.mine[announce], c); err == nil {
			t.Errorf("%s: edited", name)
		} else if strings.Contains(err.Error(), "REDACTED") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(f.api.Edits) != 0 {
		t.Errorf("%d edits", len(f.api.Edits))
	}
}

// Nobody else's message, nowhere mochi does not post, nothing outside its servers, and nothing
// a link does not name the way Discord writes one.
func TestOnlyMochisMessagesWhereItPostsAreChanged(t *testing.T) {
	f := setup(t)
	refused := map[string]string{
		"Tim's message in the announce channel":       f.other[announce],
		"Tim's message in mochi's post":               f.other[forumPost],
		"Tim's message in a session's thread":         f.other[session],
		"mochi's message in another session's post":   f.mine[forumPost],
		"mochi's message in somebody's thread":        f.mine[theirs],
		"mochi's message in a plain channel":          f.mine[general],
		"mochi's message in a foreign server":         f.mine[away],
		"a channel the link puts in the wrong server": strings.Replace(f.mine[away], foreign, guild, 1),
		"a message not there":                         "https://discord.com/channels/" + guild + "/" + announce + "/1999999999999999999",
		"http":                                        strings.Replace(f.mine[announce], "https://", "http://", 1),
		"another host":                                strings.Replace(f.mine[announce], "discord.com", "discord.com.evil.com", 1),
		"no message":                                  "https://discord.com/channels/" + guild + "/" + announce,
		"a query":                                     f.mine[announce] + "?x=1",
		"a trailing path":                             f.mine[announce] + "/x",
		"not an id":                                   "https://discord.com/channels/" + guild + "/" + announce + "/abc",
		"a DM":                                        "https://discord.com/channels/@me/" + announce + "/1300000000000000001",
		"nothing":                                     "",
	}
	for name, l := range refused {
		if err := Edit(f.api, f.scope, l, Change{Text: "x"}); err == nil {
			t.Errorf("edit, %s: allowed", name)
		}
		if err := Delete(f.api, f.scope, l); err == nil {
			t.Errorf("delete, %s: allowed", name)
		}
	}
	if len(f.api.Edits) != 0 || len(f.api.Deletes) != 0 {
		t.Errorf("edits %d, deletes %v", len(f.api.Edits), f.api.Deletes)
	}
}

func TestMochisOwnMessageIsDeleted(t *testing.T) {
	f := setup(t)
	for _, ch := range []string{announce, session} {
		if err := Delete(f.api, f.scope, f.mine[ch]); err != nil {
			t.Errorf("%s: %v", ch, err)
		}
	}
	if len(f.api.Deletes) != 2 || !strings.HasPrefix(f.api.Deletes[0], announce+" ") {
		t.Errorf("deletes = %v", f.api.Deletes)
	}
	for _, l := range []string{"https://ptb.discord.com/channels/", "https://canary.discord.com/channels/"} {
		if _, err := Parse(l + guild + "/" + announce + "/1300000000000000001"); err != nil {
			t.Errorf("%s: %v", l, err)
		}
	}
}

// A session mochi did not start, or one whose MOCHI_THREAD has no record, changes only what is in
// the announce channel.
func TestOutsideItsOwnThreadASessionChangesOnlyTheAnnounceChannel(t *testing.T) {
	for _, thread := range []string{"", theirs} {
		f := setup(t)
		f.scope.Thread = thread
		if err := Edit(f.api, f.scope, f.mine[session], Change{Text: "x"}); err == nil {
			t.Errorf("thread %q: edited a session's thread", thread)
		}
		if err := Delete(f.api, f.scope, f.mine[theirs]); err == nil {
			t.Errorf("thread %q: deleted in an unrecorded thread", thread)
		}
		if err := Edit(f.api, f.scope, f.mine[announce], Change{Text: "x"}); err != nil {
			t.Errorf("thread %q: %v", thread, err)
		}
		if err := Delete(f.api, f.scope, f.mine[announce]); err != nil {
			t.Errorf("thread %q: %v", thread, err)
		}
	}
}

// The message a thread or forum post starts with shares its id, and is edited but never deleted.
func TestAStarterMessageIsEditedButNotDeleted(t *testing.T) {
	f := setup(t)
	f.api.Msgs[session] = append(f.api.Msgs[session], &discord.Message{ID: session, ChannelID: session, Author: bot, Content: "Session started"})
	starter := "https://discord.com/channels/" + guild + "/" + session + "/" + session
	if err := Delete(f.api, f.scope, starter); err == nil || !strings.Contains(err.Error(), "starts its thread") {
		t.Errorf("delete: %v", err)
	}
	if err := Edit(f.api, f.scope, starter, Change{Text: "Session started"}); err != nil {
		t.Errorf("edit: %v", err)
	}
	if len(f.api.Deletes) != 0 || len(f.api.Edits) != 1 {
		t.Errorf("deletes %v, edits %d", f.api.Deletes, len(f.api.Edits))
	}
}
