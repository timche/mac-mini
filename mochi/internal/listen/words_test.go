package listen

import (
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/split"
)

// The names of what runs the work. Moderators read every word mochi posts, and none of these
// means anything to them.
var tools = regexp.MustCompile(`(?i)\b(herdr|claude|claude code|anthropic|lead|tabs?|workspaces?|worktrees?|1password|op|terminal)\b`)

// Everything a fixture's Discord was shown: posts, button and menu labels, edits, answers to
// presses, thread names.
func discordWords(f *fixture) []string {
	var out []string
	components := func(comps []discordgo.MessageComponent) {
		for _, row := range comps {
			r, ok := row.(discordgo.ActionsRow)
			if !ok {
				continue
			}
			for _, c := range r.Components {
				if b, ok := c.(discordgo.Button); ok {
					out = append(out, b.Label)
				}
			}
		}
	}
	for _, s := range f.api.Sent {
		out = append(out, s.Msg.Content)
		components(s.Msg.Components)
	}
	for _, e := range f.api.Edits {
		if e.Content != nil {
			out = append(out, *e.Content)
		}
		if e.Components != nil {
			components(*e.Components)
		}
	}
	for _, r := range f.api.Responses {
		if r.Data != nil {
			out = append(out, r.Data.Content)
		}
	}
	out = append(out, f.api.Threads...)
	out = append(out, f.api.Standalone...)
	out = append(out, f.api.Renames...)
	return out
}

func noTools(t *testing.T, name string, f *fixture) {
	t.Helper()
	for _, text := range discordWords(f) {
		if found := tools.FindString(text); found != "" {
			t.Errorf("%s: %q names %q", name, text, found)
		}
	}
}

// Every path that posts, edits or names something in Discord, driven once.
func TestNothingPostedNamesTheToolsBehindIt(t *testing.T) {
	flows := map[string]func(f *fixture){
		"a session starts": func(f *fixture) {
			f.h.Handle(f.mention(channel, 1, tim, "research", nil))
		},
		"it does not start": func(f *fixture) {
			f.herdr.startFails = true
			f.h.Handle(f.mention(channel, 1, tim, "research", nil))
		},
		"its instructions do not land": func(f *fixture) {
			f.herdr.promptFails = true
			f.h.Handle(f.mention(channel, 1, tim, "research", nil))
		},
		"no thread can be opened": func(f *fixture) {
			f.api.ThreadErr = errors.New("403")
			f.h.Handle(f.mention(channel, 1, tim, "research", nil))
		},
		"a split, one failing": func(f *fixture) {
			f.h.Split = splitsInto(abc, nil, nil)
			f.herdr.failFrom = 3
			f.h.Handle(f.mention(channel, 1, tim, "research a, b and c separately", nil))
		},
		"too many": func(f *fixture) {
			six := make([]split.Task, 6)
			for i := range six {
				six[i] = split.Task{Title: "item", Instruction: "look"}
			}
			f.h.Split = answers(split.Answer{Decision: split.Many, Tasks: six}, split.TooMany{N: 6}, nil)
			f.h.Handle(f.mention(channel, 1, tim, "six of them separately", nil))
		},
		"a forum post that asks for a split": func(f *fixture) {
			f.h.Split = splitsInto(abc, nil, nil)
			f.api.AddChannel(&discordgo.Channel{ID: forum, GuildID: guild, Type: discordgo.ChannelTypeGuildForum})
			f.api.AddChannel(&discordgo.Channel{ID: post, GuildID: guild, ParentID: forum, Name: "Ideas", Type: discordgo.ChannelTypeGuildPublicThread})
			f.h.Handle(f.mention(post, 1, tim, "research a, b and c separately", nil))
		},
		"a question, pressed by others and by Tim": func(f *fixture) {
			f.h.Split = answers(split.Answer{Decision: split.Ask, Title: "QOL suggestions", Tasks: abc}, nil, nil)
			f.h.Handle(f.mention(channel, 1, tim, "take a look at the three suggestions", nil))
			b := buttonsOf(f.t, f.api.Sent[0].Msg.Components)
			press(f, imp, b[0].CustomID)
			press(f, tim, b[0].CustomID)
		},
		"a question that expires": func(f *fixture) {
			f.h.Split = answers(split.Answer{Decision: split.Ask, Title: "QOL", Tasks: abc}, nil, nil)
			f.h.Handle(f.mention(channel, 1, tim, "take a look", nil))
			f.h.Now = func() time.Time { return time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC) }
			f.h.ExpirePending()
		},
		"more than five offered as one": func(f *fixture) {
			var six []split.Task
			for i := 0; i < 6; i++ {
				six = append(six, split.Task{Title: "item", Instruction: "look"})
			}
			f.h.Split = answers(split.Answer{Decision: split.Ask, Tasks: six}, nil, nil)
			f.h.Handle(f.mention(channel, 1, tim, "look at these", nil))
			answer := f.api.Post(channel, 2, tim, "all")
			answer.MessageReference = &discordgo.MessageReference{MessageID: f.api.Sent[0].ID, ChannelID: channel}
			f.h.Handle(answer)
		},
		"a follow-up to a session waiting on a question": func(f *fixture) {
			first := f.mention(channel, 1, tim, "start", nil)
			f.h.Handle(first)
			f.herdr.agents["mochi-"+first.ID] = "blocked"
			f.h.Handle(f.mention(first.ID, 2, tim, "also b)", nil))
		},
		"a follow-up to a session that ended": func(f *fixture) {
			first := f.mention(channel, 1, tim, "start", nil)
			f.h.Handle(first)
			delete(f.herdr.agents, "mochi-"+first.ID)
			f.h.Handle(f.mention(first.ID, 2, tim, "again", nil))
		},
	}
	for name, flow := range flows {
		f := newFixture(t)
		flow(f)
		if len(discordWords(f)) == 0 {
			t.Errorf("%s: nothing reached Discord, so nothing was checked", name)
		}
		noTools(t, name, f)
	}
}
