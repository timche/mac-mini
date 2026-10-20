package listen

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/split"
	"github.com/timche/mac-mini/mochi/internal/state"
)

// Tim's live case: "take a look at the three suggestions", which says nothing about whether
// they are one piece of work or three.
func askFixture(t *testing.T, tasks []split.Task) (*fixture, *discordgo.Message, *discordgo.Message) {
	t.Helper()
	f := newFixture(t)
	f.h.Split = answers(split.Answer{Decision: split.Ask, Title: "QOL suggestions", Tasks: tasks}, nil, nil)
	qol := f.api.Post(channel, 1, maker, "QOL: a) average elo per team b) map stats c) match history export")
	m := f.mention(channel, 2, tim, "take a look at the three suggestions", qol)
	f.h.Handle(m)

	sent := f.sentTo(channel)
	if len(sent) != 1 || sent[0] != question {
		t.Fatalf("sent = %q", sent)
	}
	return f, m, &discordgo.Message{ID: f.api.Sent[0].ID, ChannelID: channel}
}

func buttonsOf(t *testing.T, comps []discordgo.MessageComponent) []discordgo.Button {
	t.Helper()
	if len(comps) != 1 {
		t.Fatalf("%d component rows", len(comps))
	}
	var out []discordgo.Button
	for _, c := range comps[0].(discordgo.ActionsRow).Components {
		out = append(out, c.(discordgo.Button))
	}
	return out
}

func press(f *fixture, user *discordgo.User, id string) {
	f.h.Interaction(&discordgo.Interaction{
		Type:      discordgo.InteractionMessageComponent,
		Data:      discordgo.MessageComponentInteractionData{CustomID: id},
		Member:    &discordgo.Member{User: user},
		ChannelID: channel,
	})
}

func (f *fixture) pending() map[string]state.Pending {
	f.t.Helper()
	all, err := f.h.Pending.Load()
	if err != nil {
		f.t.Fatal(err)
	}
	return all
}

func TestAnUnclearMentionIsAQuestionWithAButtonPerAnswer(t *testing.T) {
	f, m, _ := askFixture(t, abc)

	if len(f.herdr.calls) != 0 || len(f.api.Threads) != 0 {
		t.Error("something started before Tim answered")
	}
	q := f.api.Sent[0].Msg
	if q.Reference == nil || q.Reference.MessageID != m.ID {
		t.Error("the question is not a reply to Tim's mention")
	}
	b := buttonsOf(t, q.Components)
	if len(b) != 2 || b[0].Label != "3 sessions: Average elo per team, Map stats, Match history export" || b[1].Label != "1 session" {
		t.Errorf("buttons = %+v", b)
	}
	if len([]rune(b[0].Label)) > 80 || b[0].Disabled {
		t.Errorf("label %q", b[0].Label)
	}
	if !f.api.ReactedWith(m.ID, "👀") || f.api.ReactedWith(m.ID, "✅") {
		t.Errorf("reactions = %v", f.api.Reactions)
	}
	if len(f.pending()) != 1 {
		t.Error("the question was not kept")
	}
	f.quiet()
}

func TestTimPressingSeparateStartsOneSessionPerItem(t *testing.T) {
	f, m, _ := askFixture(t, abc)
	b := buttonsOf(t, f.api.Sent[0].Msg.Components)

	press(f, tim, b[0].CustomID)

	if len(f.api.Responses) != 1 || f.api.Responses[0].Type != discordgo.InteractionResponseDeferredMessageUpdate {
		t.Errorf("responses = %+v", f.api.Responses)
	}
	if len(f.api.Edits) != 1 || *f.api.Edits[0].Content != question+" 3 sessions." {
		t.Fatalf("edits = %+v", f.api.Edits)
	}
	for _, button := range buttonsOf(t, *f.api.Edits[0].Components) {
		if !button.Disabled {
			t.Error("a button is still live")
		}
	}
	if len(f.api.Standalone) != 3 || len(f.herdr.called("tab create")) != 3 {
		t.Errorf("threads %v", f.api.Standalone)
	}
	if !f.api.ReactedWith(m.ID, "✅") || len(f.pending()) != 0 {
		t.Error("the answer was not finished")
	}
}

func TestTimPressingOneStartsOneSessionNamedForTheWhole(t *testing.T) {
	f, m, _ := askFixture(t, abc)
	b := buttonsOf(t, f.api.Sent[0].Msg.Components)

	press(f, tim, b[1].CustomID)

	if len(f.api.Threads) != 1 || f.api.Threads[0] != m.ID+" mochi: QOL suggestions" || len(f.api.Standalone) != 0 {
		t.Errorf("threads %v, standalone %v", f.api.Threads, f.api.Standalone)
	}
	prompt := onlyPrompt(t, f.herdr, "mochi-"+m.ID)
	if got := strings.TrimSpace(between(t, prompt, "INSTRUCTION FROM TIM")); got != "take a look at the three suggestions" {
		t.Errorf("instruction = %q", got)
	}
	if *f.api.Edits[0].Content != question+" 1 session." || !f.api.ReactedWith(m.ID, "✅") {
		t.Error("the answer was not shown or finished")
	}
}

func TestOnlyTimCanChoose(t *testing.T) {
	f, _, _ := askFixture(t, abc)
	b := buttonsOf(t, f.api.Sent[0].Msg.Components)

	press(f, imp, b[0].CustomID)

	if len(f.api.Responses) != 1 {
		t.Fatalf("responses = %+v", f.api.Responses)
	}
	r := f.api.Responses[0]
	if r.Type != discordgo.InteractionResponseChannelMessageWithSource || r.Data.Content != "Only Tim can choose." || r.Data.Flags != discordgo.MessageFlagsEphemeral {
		t.Errorf("response = %+v", r.Data)
	}
	if len(f.herdr.calls) != 0 || len(f.api.Edits) != 0 || len(f.pending()) != 1 {
		t.Error("somebody else's press did something")
	}

	press(f, tim, b[1].CustomID)
	if len(f.herdr.called("tab create")) != 1 {
		t.Error("Tim could not choose after somebody else tried")
	}
}

func TestTimCanAnswerInWords(t *testing.T) {
	for text, threads := range map[string]int{"3": 3, "all": 3, "separately": 3, "one": 0, "1": 0} {
		f, _, q := askFixture(t, abc)
		answer := f.api.Post(channel, 3, tim, text)
		answer.MessageReference = &discordgo.MessageReference{MessageID: q.ID, ChannelID: channel}

		f.h.Handle(answer)

		if len(f.api.Standalone) != threads || len(f.herdr.called("tab create")) != max(threads, 1) {
			t.Errorf("%q: %d threads, %d tabs", text, len(f.api.Standalone), len(f.herdr.called("tab create")))
		}
		if len(f.pending()) != 0 {
			t.Errorf("%q: the question is still open", text)
		}
	}

	f, _, q := askFixture(t, abc)
	chat := f.api.Post(channel, 3, tim, "hmm, not sure yet")
	chat.MessageReference = &discordgo.MessageReference{MessageID: q.ID, ChannelID: channel}
	f.h.Handle(chat)
	if len(f.herdr.calls) != 0 || len(f.pending()) != 1 {
		t.Error("a reply that is no answer was taken as one")
	}
}

func TestAnUnansweredQuestionExpiresAfterADay(t *testing.T) {
	f, _, _ := askFixture(t, abc)
	b := buttonsOf(t, f.api.Sent[0].Msg.Components)

	f.h.ExpirePending()
	if len(f.api.Edits) != 0 {
		t.Fatal("a fresh question expired")
	}

	f.h.Now = func() time.Time { return time.Date(2026, 10, 21, 15, 5, 0, 0, time.UTC) }
	f.h.ExpirePending()
	if len(f.api.Edits) != 1 || *f.api.Edits[0].Content != question+" Expired — mention mochi again." {
		t.Fatalf("edits = %+v", f.api.Edits)
	}
	for _, button := range buttonsOf(t, *f.api.Edits[0].Components) {
		if !button.Disabled {
			t.Error("an expired question kept a live button")
		}
	}
	if len(f.pending()) != 0 {
		t.Error("an expired question was kept")
	}

	press(f, tim, b[0].CustomID)
	if len(f.herdr.calls) != 0 {
		t.Error("an expired button started a session")
	}
}

// A press that arrives after a day, before the sweep has run, is the same expiry.
func TestALatePressIsExpiredRatherThanStarted(t *testing.T) {
	f, _, _ := askFixture(t, abc)
	b := buttonsOf(t, f.api.Sent[0].Msg.Components)
	f.h.Now = func() time.Time { return time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC) }

	press(f, tim, b[0].CustomID)

	if len(f.herdr.calls) != 0 || len(f.api.Edits) != 1 || !strings.Contains(*f.api.Edits[0].Content, "Expired") {
		t.Errorf("edits = %+v", f.api.Edits)
	}
}

// The question outlives the process that asked it.
func TestAnAnswerIsTakenAfterARestart(t *testing.T) {
	f, m, _ := askFixture(t, abc)
	b := buttonsOf(t, f.api.Sent[0].Msg.Components)

	restarted := &Handler{
		Cfg: f.h.Cfg, API: f.api, Herdr: f.herdr.run,
		Store: f.h.Store, Pending: f.h.Pending,
		Now: f.h.Now, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	restarted.SetBot(bot.ID)
	f.h = restarted

	press(f, tim, b[0].CustomID)

	if len(f.api.Standalone) != 3 || !f.api.ReactedWith(m.ID, "✅") {
		t.Errorf("after a restart: %v", f.api.Standalone)
	}
}

// More than five items are never offered as separate sessions.
func TestMoreThanFiveItemsAreOfferedOnlyAsOne(t *testing.T) {
	var six []split.Task
	for i := 0; i < 6; i++ {
		six = append(six, split.Task{Title: "item", Instruction: "look at it"})
	}
	f, _, q := askFixture(t, six)
	b := buttonsOf(t, f.api.Sent[0].Msg.Components)
	if len(b) != 1 || b[0].Label != "1 session" {
		t.Errorf("buttons = %+v", b)
	}

	answer := f.api.Post(channel, 3, tim, "all")
	answer.MessageReference = &discordgo.MessageReference{MessageID: q.ID, ChannelID: channel}
	f.h.Handle(answer)
	if len(f.api.Standalone) != 0 {
		t.Error("six sessions were started")
	}
}

func TestAPressOnSomebodyElsesButtonIsIgnored(t *testing.T) {
	f := newFixture(t)
	press(f, tim, "other-bot:123:go")
	if len(f.api.Responses) != 0 {
		t.Error("mochi answered a button that is not its own")
	}
}
