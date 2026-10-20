package listen

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/chat"
)

// The live bug: a split started from a button press built its links from a message fetched
// by id, which Discord sends without its guild, and every link came out
// discord.com/channels//… — in the thread's first post and in every session's brief.
func TestASessionStartedFromAnAnswerLinksWithItsGuild(t *testing.T) {
	f, m, _ := askFixture(t, abc)
	press(f, tim, buttonsOf(t, f.api.Sent[0].Msg.Components)[0].CustomID)

	if len(f.api.Standalone) != 3 {
		t.Fatalf("threads = %v", f.api.Standalone)
	}
	var texts []string
	for _, s := range f.api.Sent {
		texts = append(texts, s.Msg.Content)
	}
	for i := range abc {
		thread := chanID(i)
		texts = append(texts, f.herdr.raw["mochi-"+thread]...)
		if rec := f.record(thread); rec.Guild != guild {
			t.Errorf("session %d was recorded without its guild: %q", i+1, rec.Guild)
		}
	}
	for _, text := range texts {
		if strings.Contains(text, "channels//") {
			t.Errorf("a link with an empty part:\n%.300s", text)
		}
	}
	want := startedPost(m.ID)
	if got := f.sentTo(chanID(0)); len(got) == 0 || head(got[0]) != want {
		t.Errorf("first post = %q", got)
	}
}

// Every post with a link in it shows no preview card.
func TestAPostWithALinkHasNoPreview(t *testing.T) {
	if chat.Quiet("Session started for [Tim's message](<https://discord.com/channels/1/2/3>).").Flags&discordgo.MessageFlagsSuppressEmbeds == 0 {
		t.Error("a post with a link would show a preview")
	}
	if chat.Quiet("Session started.").Flags != 0 {
		t.Error("a post without a link had its flags changed")
	}
}

// A link with no guild or no channel is no link at all.
func TestALinkIsNeverMadeWithAnEmptyPart(t *testing.T) {
	for _, parts := range [][3]string{{"", "2", "3"}, {"1", "", "3"}, {"", "", ""}} {
		if got := chat.Link(parts[0], parts[1], parts[2]); got != "" {
			t.Errorf("Link%q = %q", parts, got)
		}
	}
	if got := chat.Link("1", "2", ""); got != "https://discord.com/channels/1/2" {
		t.Errorf("channel link = %q", got)
	}
}

// A record saved without its guild — the three sessions started from the live press — is
// mended by the next follow-up, which has one.
func TestARecordWithoutItsGuildIsMendedByAFollowUp(t *testing.T) {
	f, thread := startedSession(t)
	rec := f.record(thread)
	rec.Guild = ""
	f.h.Store.Put(rec)

	f.h.Handle(f.mention(thread, 5, tim, "also this", nil))

	if got := f.record(thread).Guild; got != guild {
		t.Errorf("guild = %q", got)
	}
}
