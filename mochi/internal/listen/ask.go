package listen

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/chat"
	"github.com/timche/mac-mini/mochi/internal/fence"
	"github.com/timche/mac-mini/mochi/internal/split"
	"github.com/timche/mac-mini/mochi/internal/state"
)

const (
	question    = "Separate sessions or one?"
	expireAfter = 24 * time.Hour
	idPrefix    = "mochi:"
)

// When the classifier cannot tell whether Tim means the items together or apart, it is his
// call: a question under his message, with a button for each answer.
func (h *Handler) ask(m *discordgo.Message, instruction string, answer split.Answer) {
	p := state.Pending{
		Nonce:       fence.Nonce(),
		Guild:       m.GuildID,
		Channel:     m.ChannelID,
		Mention:     m.ID,
		Instruction: instruction,
		Answer:      answer,
		Created:     h.Now().Unix(),
	}
	msg := chat.Quiet(question)
	msg.Reference = &discordgo.MessageReference{MessageID: m.ID, ChannelID: m.ChannelID}
	msg.Components = buttons(p, false)

	posted, err := h.API.Send(m.ChannelID, msg)
	if err != nil {
		h.fail(m, "", "the question could not be posted", err)
		return
	}
	p.Question = posted.ID
	if err := h.Pending.Put(p); err != nil {
		h.closeQuestion(p, question+" It could not be kept — mention mochi again.")
		h.fail(m, "", "the question could not be kept", err)
	}
}

// More than five items is never offered as separate sessions: only one.
func buttons(p state.Pending, disabled bool) []discordgo.MessageComponent {
	var row []discordgo.MessageComponent
	if n := len(p.Answer.Tasks); n > 1 && n <= split.MaxTasks {
		row = append(row, discordgo.Button{
			Label:    manyLabel(p.Answer.Tasks),
			Style:    discordgo.PrimaryButton,
			CustomID: idPrefix + p.Nonce + ":" + split.Many,
			Disabled: disabled,
		})
	}
	row = append(row, discordgo.Button{
		Label:    "1 session",
		Style:    discordgo.SecondaryButton,
		CustomID: idPrefix + p.Nonce + ":" + split.One,
		Disabled: disabled,
	})
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: row}}
}

// Discord's limit on a button's label is 80 characters.
func manyLabel(tasks []split.Task) string {
	titles := make([]string, len(tasks))
	for i, t := range tasks {
		titles[i] = t.Title
	}
	label := fmt.Sprintf("%d sessions: %s", len(tasks), strings.Join(titles, ", "))
	if r := []rune(label); len(r) > 80 {
		label = string(r[:79]) + "…"
	}
	return label
}

// A button press. Discord wants an answer within three seconds, so it is answered before the
// handler's lock, which a mention in progress can hold for a minute.
func (h *Handler) Interaction(i *discordgo.Interaction) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}
	rest, ok := strings.CutPrefix(i.MessageComponentData().CustomID, idPrefix)
	if !ok {
		return
	}
	nonce, choice, ok := strings.Cut(rest, ":")
	if !ok || (choice != split.One && choice != split.Many) {
		return
	}

	user := i.User
	if i.Member != nil && i.Member.User != nil {
		user = i.Member.User
	}
	if user == nil || user.ID != h.Cfg.User {
		h.respond(i, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content:         "Only Tim can choose.",
				Flags:           discordgo.MessageFlagsEphemeral,
				AllowedMentions: chat.NoMentions(),
			},
		})
		return
	}
	h.respond(i, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate})

	h.mu.Lock()
	defer h.mu.Unlock()
	p, found, err := h.Pending.Get(nonce)
	if err != nil {
		h.Log.Error("the pending questions could not be read", "error", err)
		return
	}
	if found {
		h.settle(p, choice == split.Many)
	}
}

// Tim answering in words: a reply to the question with the number of items or "all" or
// "separately" for separate sessions, "1" or "one" for one. False when it is not an answer.
func (h *Handler) textAnswer(m *discordgo.Message, text string) bool {
	if m.MessageReference == nil {
		return false
	}
	p, found, err := h.Pending.ByQuestion(m.MessageReference.MessageID)
	if err != nil || !found {
		return false
	}
	n := len(p.Answer.Tasks)
	many, ok := parseAnswer(text, n)
	switch {
	case !ok:
		return false
	case many && n > split.MaxTasks:
		// The question offered only one session; saying so beats silence.
		reply := chat.Quiet(fmt.Sprintf("Only 1 session is offered here (more than %d items).", split.MaxTasks))
		reply.Reference = &discordgo.MessageReference{MessageID: m.ID, ChannelID: m.ChannelID}
		if _, err := h.API.Send(m.ChannelID, reply); err != nil {
			h.Log.Error("the reply to an answer could not be posted", "error", h.redact(err))
		}
		return true
	}
	h.settle(p, many)
	return true
}

func parseAnswer(text string, n int) (many, ok bool) {
	switch strings.ToLower(strings.Trim(strings.TrimSpace(text), ".!")) {
	case "1", "one":
		return false, true
	case "all", "separately", "many", strconv.Itoa(n):
		return true, n > 1
	}
	return false, false
}

// The answer, with the handler's lock held: the question shows it and can be answered no
// more, then the sessions start the way an unasked mention's would.
func (h *Handler) settle(p state.Pending, many bool) {
	if h.Now().Sub(time.Unix(p.Created, 0)) >= expireAfter {
		h.expire(p)
		return
	}
	choice := "1 session."
	if many {
		choice = fmt.Sprintf("%d sessions.", len(p.Answer.Tasks))
	}
	h.closeQuestion(p, question+" "+choice)
	if err := h.Pending.Remove(p.Nonce); err != nil {
		h.Log.Error("an answered question could not be forgotten", "error", err)
	}

	m, err := h.API.Message(p.Channel, p.Mention)
	if err != nil {
		h.Log.Error("the mention a question was about could not be read", "message", p.Mention, "error", h.redact(err))
		return
	}
	ch, err := h.API.Channel(p.Channel)
	if err != nil {
		h.fail(m, "", "the channel could not be read", err)
		return
	}
	server, ok := h.Cfg.Server(p.Guild)
	if !ok {
		return
	}
	h.begin(server, ch, m, p.Instruction, h.replied(m), p.Answer, many)
}

// Questions unanswered for a day are closed, so an old button does not start sessions about a
// conversation that has moved on.
func (h *Handler) ExpirePending() {
	h.mu.Lock()
	defer h.mu.Unlock()

	all, err := h.Pending.Load()
	if err != nil {
		h.Log.Error("the pending questions could not be read", "error", err)
		return
	}
	for _, p := range all {
		if h.Now().Sub(time.Unix(p.Created, 0)) >= expireAfter {
			h.expire(p)
		}
	}
}

func (h *Handler) expire(p state.Pending) {
	h.closeQuestion(p, question+" Expired — mention mochi again.")
	if err := h.Pending.Remove(p.Nonce); err != nil {
		h.Log.Error("an expired question could not be forgotten", "error", err)
	}
}

func (h *Handler) closeQuestion(p state.Pending, content string) {
	components := buttons(p, true)
	err := h.API.Edit(&discordgo.MessageEdit{
		ID:              p.Question,
		Channel:         p.Channel,
		Content:         &content,
		Components:      &components,
		AllowedMentions: chat.NoMentions(),
	})
	if err != nil {
		h.Log.Error("the question could not be closed", "question", p.Question, "error", h.redact(err))
	}
}

func (h *Handler) respond(i *discordgo.Interaction, r *discordgo.InteractionResponse) {
	if err := h.API.Respond(i, r); err != nil {
		h.Log.Error("a button press could not be answered", "error", h.redact(err))
	}
}
