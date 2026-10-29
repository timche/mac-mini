package listen

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/discord"

	"github.com/timche/mac-mini/mochi/internal/chat"
	"github.com/timche/mac-mini/mochi/internal/chat/chattest"
	"github.com/timche/mac-mini/mochi/internal/mark"
	"github.com/timche/mac-mini/mochi/internal/project"
	"github.com/timche/mac-mini/mochi/internal/split"
)

// The server's forum for sessions, with tags on it as the real one had them, state tags
// included, which mochi no longer applies; Research is missing, so a tag the forum has not
// got is shown to be skipped.
const sessions = "1300000000000000005"

var forumTags = []discord.ForumTag{
	{ID: "t-running", Name: "Running"}, {ID: "t-waiting", Name: "Waiting on you"},
	{ID: "t-done", Name: "Done"}, {ID: "t-failed", Name: "Failed"},
	{ID: "t-feature", Name: "Feature"}, {ID: "t-bug", Name: "Bug"}, {ID: "t-improvement", Name: "Improvement"},
}

func forumFixture(t *testing.T) *fixture {
	f := newFixture(t)
	f.api.AddChannel(&discord.Channel{ID: sessions, GuildID: guild, Name: "mochi", Type: discord.ChannelTypeGuildForum, AvailableTags: forumTags})
	f.useForum(sessions)
	return f
}

// The server's repository names forum as its sessions' forum.
func (f *fixture) useForum(forum string) {
	f.t.Helper()
	withRepo(f.t, f, map[string]string{project.ConfigFile: `{"forum": "` + forum + `"}`})
}

func postID(n int) string { return chattest.ID(700_000 + n) }

// Tim's mention in a channel: a post in the forum, named and tagged for a running session and
// its kind of work, whose first message mentions him; he is made a member, and his mention gets
// a reply saying where it went.
func TestAMentionInAChannelOpensAForumPost(t *testing.T) {
	f := forumFixture(t)
	f.h.Split = answers(split.Answer{Decision: split.One, Title: "Hover blocked by the overlay", Type: split.Bug,
		Tasks: []split.Task{{Title: "Hover blocked by the overlay", Instruction: "have a look", Type: split.Bug}}}, nil, nil)
	m := f.mention(channel, 1, tim, "have a look", nil)

	f.h.Handle(m)

	p := postID(1)
	if !reflect.DeepEqual(f.api.Posts, []string{sessions + " ⏳ Hover blocked by the overlay"}) || len(f.api.Threads) != 0 {
		t.Fatalf("posts = %v, threads = %v", f.api.Posts, f.api.Threads)
	}
	if got := f.api.Tagged[p]; !reflect.DeepEqual(got, []string{"t-bug"}) {
		t.Errorf("tags = %v", got)
	}
	if posts := f.sentTo(p); len(posts) != 1 || head(posts[0]) != startedPost(m.ID) {
		t.Errorf("post's messages = %q", posts)
	}
	if !contains(f.api.Members, p+" "+tim.ID) {
		t.Errorf("members = %v", f.api.Members)
	}
	replies := f.sentTo(channel)
	want := "Created [Hover blocked by the overlay](<https://discord.com/channels/" + guild + "/" + p + ">)."
	if len(replies) != 1 || replies[0] != want {
		t.Errorf("replies = %q", replies)
	}
	if ref := f.api.Sent[len(f.api.Sent)-1].Msg.Reference; ref == nil || ref.MessageID != m.ID {
		t.Error("the reply does not answer Tim's message")
	}
	if f.api.Sent[len(f.api.Sent)-1].Msg.Flags&discord.MessageFlagsSuppressEmbeds == 0 {
		t.Error("the reply's link shows a preview")
	}
	rec := f.record(p)
	if rec.Source != channel || rec.LastMention != m.ID || rec.Agent != "mochi-"+p {
		t.Errorf("record = %+v", rec)
	}
	if !strings.Contains(onlyPrompt(t, f.herdr, "mochi-"+p), "have a look") {
		t.Error("the session was not handed Tim's words")
	}
	f.receipt(m.ID)
	f.quiet()
}

// A post's state is in its name alone: a change renames it and leaves its tags, under the
// rename limit; a type the forum has no tag for is not applied.
func TestAPostsStateIsInItsNameAlone(t *testing.T) {
	f := forumFixture(t)
	f.h.Split = answers(split.Answer{Decision: split.One, Title: "Steam login", Type: split.Research,
		Tasks: []split.Task{{Title: "Steam login", Instruction: "look into it", Type: split.Research}}}, nil, nil)
	f.h.Handle(f.mention(channel, 1, tim, "look into steam login", nil))
	p := postID(1)
	if got := f.api.Tagged[p]; len(got) != 0 {
		t.Errorf("tags at the start = %v", got)
	}

	f.h.Marks.Settled(p, mark.Asking)
	f.h.Marks.Settled(p, mark.Finished)
	if f.api.Channels[p].Name != "✅ Steam login" || len(f.api.Renames) != 2 || f.api.PostEdits != 0 {
		t.Fatalf("renames %v, tag edits %d", f.api.Renames, f.api.PostEdits)
	}
	// A third inside ten minutes waits, and goes once the window has passed.
	f.h.Marks.Settled(p, mark.Asking)
	if len(f.api.Renames) != 2 {
		t.Errorf("a rename over the limit went: %v", f.api.Renames)
	}
	f.at(f.h.Now().Add(11 * time.Minute))
	f.h.Marks.Retry()
	if f.api.Channels[p].Name != "❓ Steam login" || f.api.PostEdits != 0 {
		t.Errorf("name %q, tag edits %d", f.api.Channels[p].Name, f.api.PostEdits)
	}
}

// A post made while states had tags of their own loses its state tag with its next change, in
// the same edit as the name, and is renamed alone after that.
func TestAnOldPostsStateTagGoesWithItsNextChange(t *testing.T) {
	f := forumFixture(t)
	f.h.Split = answers(split.Answer{Decision: split.One, Title: "Overlay", Type: split.Bug,
		Tasks: []split.Task{{Title: "Overlay", Instruction: "fix it", Type: split.Bug}}}, nil, nil)
	f.h.Handle(f.mention(channel, 1, tim, "fix the overlay", nil))
	p := postID(1)
	old := filepath.Join(f.cfg.StateDir, "marks.json")
	body, _ := os.ReadFile(old)
	body = []byte(strings.ReplaceAll(string(body), `"Bug"`, `"Running", "Bug"`))
	os.WriteFile(old, body, 0o600)

	f.h.Marks.Settled(p, mark.Asking)
	if got := f.api.Tagged[p]; !reflect.DeepEqual(got, []string{"t-bug"}) || f.api.PostEdits != 1 || f.api.Channels[p].Name != "❓ Overlay" {
		t.Errorf("tags %v after %d tag edits, name %q", got, f.api.PostEdits, f.api.Channels[p].Name)
	}
	f.h.Marks.Settled(p, mark.Finished)
	if f.api.PostEdits != 1 || f.api.Channels[p].Name != "✅ Overlay" {
		t.Errorf("the next change edited the tags again: %d", f.api.PostEdits)
	}
}

// Tags Discord refuses leave the name following the state on its own.
func TestRefusedTagsLeaveTheNameFollowing(t *testing.T) {
	f := forumFixture(t)
	f.h.Handle(f.mention(channel, 1, tim, "fix the login", nil))
	p := postID(1)
	f.api.TagErr = chat.ErrForbidden

	if err := f.h.Marks.Typed(p, split.Bug); err != nil {
		t.Fatal(err)
	}
	f.h.Marks.Settled(p, mark.Asking)
	if f.api.Channels[p].Name != "❓ fix the login" || len(f.api.Tagged[p]) != 0 {
		t.Errorf("name %q, tags %v after a refusal", f.api.Channels[p].Name, f.api.Tagged[p])
	}
}

// The session refiles its post under another kind of work; a session in a thread has none.
func TestASessionRefilesItsPostsType(t *testing.T) {
	f := forumFixture(t)
	f.h.Handle(f.mention(channel, 1, tim, "look at the overlay", nil))
	p := postID(1)
	if err := f.h.Marks.Typed(p, split.Bug); err != nil {
		t.Fatal(err)
	}
	if got := f.api.Tagged[p]; !reflect.DeepEqual(got, []string{"t-bug"}) {
		t.Errorf("tags = %v", got)
	}
	if err := f.h.Marks.Typed(p, "chore"); err == nil {
		t.Error("a type that is none of the four was taken")
	}

	g := newFixture(t)
	m := g.mention(channel, 1, tim, "look at the overlay", nil)
	g.h.Handle(m)
	if err := g.h.Marks.Typed(m.ID, split.Bug); !errors.Is(err, mark.ErrNoType) {
		t.Errorf("a thread took a type: %v", err)
	}
}

// Tim's follow-up inside a post mochi made reaches its session, with what was said there.
func TestAFollowUpInAPostIsRelayed(t *testing.T) {
	f := forumFixture(t)
	f.h.Handle(f.mention(channel, 1, tim, "look at the overlay", nil))
	p := postID(1)
	f.api.Post(p, 2, other, "it happens on 1080p too")
	f.h.Handle(f.mention(p, 3, tim, "also check 4k", nil))

	prompts := f.herdr.prompts["mochi-"+p]
	if len(prompts) != 2 || !strings.Contains(prompts[1], "also check 4k") || !strings.Contains(prompts[1], "it happens on 1080p too") {
		t.Errorf("prompts = %q", prompts)
	}
	if len(f.api.Posts) != 1 {
		t.Errorf("a follow-up opened another post: %v", f.api.Posts)
	}
}

// A split opens a post per task, each tagged with its own kind, and one reply listing them.
func TestASplitOpensAPostPerTask(t *testing.T) {
	f := forumFixture(t)
	f.h.Split = answers(split.Answer{Decision: split.Many, Title: "Two things", Type: split.Feature, Tasks: []split.Task{
		{Title: "Lobby average", Instruction: "add the lobby average", Type: split.Feature},
		{Title: "Login timeout", Instruction: "fix the login timeout", Type: split.Bug},
	}}, nil, nil)
	m := f.mention(channel, 1, tim, "add the lobby average, and separately fix the login timeout", nil)
	f.h.Handle(m)

	if len(f.api.Posts) != 2 || len(f.api.Standalone) != 0 {
		t.Fatalf("posts = %v, threads = %v", f.api.Posts, f.api.Standalone)
	}
	if !reflect.DeepEqual(f.api.Tagged[postID(1)], []string{"t-feature"}) || !reflect.DeepEqual(f.api.Tagged[postID(2)], []string{"t-bug"}) {
		t.Errorf("tags = %v", f.api.Tagged)
	}
	replies := f.sentTo(channel)
	want := "<#" + postID(1) + "> Lobby average\n<#" + postID(2) + "> Login timeout"
	if len(replies) != 1 || replies[0] != want {
		t.Errorf("replies = %q", replies)
	}
	for i := 1; i <= 2; i++ {
		f.shows(postID(i))
		if posts := f.sentTo(postID(i)); len(posts) != 1 || head(posts[0]) != startedPost(m.ID) {
			t.Errorf("post %d messages = %q", i, posts)
		}
	}
	f.quiet()
}

// A done post leaves the forum's list after three days, once; one that is working again is
// not archived.
func TestADonePostIsArchivedAfterThreeDays(t *testing.T) {
	f := forumFixture(t)
	f.h.Handle(f.mention(channel, 1, tim, "look at the overlay", nil))
	p := postID(1)
	start := f.h.Now()

	f.h.Marks.Settled(p, mark.Finished)
	f.at(start.Add(71 * time.Hour))
	f.h.Marks.Retry()
	if len(f.api.Archived) != 0 {
		t.Fatal("archived before three days")
	}
	f.at(start.Add(72 * time.Hour))
	f.h.Marks.Retry()
	f.h.Marks.Retry()
	if !reflect.DeepEqual(f.api.Archived, []string{p}) {
		t.Fatalf("archived = %v", f.api.Archived)
	}

	g := forumFixture(t)
	g.h.Handle(g.mention(channel, 1, tim, "look at the overlay", nil))
	g.h.Marks.Settled(postID(1), mark.Finished)
	g.h.Marks.Started(postID(1))
	g.at(g.h.Now().Add(96 * time.Hour))
	g.h.Marks.Retry()
	if len(g.api.Archived) != 0 {
		t.Errorf("a working post was archived: %v", g.api.Archived)
	}
}

// A mention inside somebody else's forum post makes that post the session's, with no new one;
// a thread opened before the forum was set up goes on as a thread.
func TestPostsAndThreadsMochiDidNotMakeForTheForumStayAsTheyAre(t *testing.T) {
	f := forumFixture(t)
	f.api.AddChannel(&discord.Channel{ID: forum, GuildID: guild, Type: discord.ChannelTypeGuildForum})
	f.api.AddChannel(&discord.Channel{ID: post, GuildID: guild, ParentID: forum, Name: "Ideas", Type: discord.ChannelTypeGuildPublicThread})
	f.h.Handle(f.mention(post, 1, tim, "look at this", nil))
	if len(f.api.Posts) != 0 || f.record(post).Agent != "mochi-"+post {
		t.Errorf("posts = %v", f.api.Posts)
	}

	g := newFixture(t)
	first := g.mention(channel, 1, tim, "look at this", nil)
	g.h.Handle(first)
	g.api.AddChannel(&discord.Channel{ID: sessions, GuildID: guild, Type: discord.ChannelTypeGuildForum, AvailableTags: forumTags})
	g.useForum(sessions)
	g.h.Handle(g.mention(first.ID, 2, tim, "and this", nil))
	if len(g.api.Posts) != 0 || len(g.herdr.prompts["mochi-"+first.ID]) != 2 {
		t.Errorf("an old thread's follow-up: posts %v, prompts %d", g.api.Posts, len(g.herdr.prompts["mochi-"+first.ID]))
	}
}

// A post that could not be opened is a ❌ on Tim's message, with no session.
func TestAPostThatCannotBeOpenedFailsWithAReason(t *testing.T) {
	f := forumFixture(t)
	f.api.ThreadErr = errors.New("missing access")
	m := f.mention(channel, 1, tim, "look at this", nil)
	f.h.Handle(m)
	if !f.seenOnly(m.ID) || len(f.herdr.called("agent start")) != 0 {
		t.Error("no ❌, or a session started anyway")
	}
	if replies := f.sentTo(channel); len(replies) != 1 || !strings.Contains(replies[0], "❌ A post could not be opened for this.") {
		t.Errorf("replies = %q", replies)
	}
}
