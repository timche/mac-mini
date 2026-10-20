package listen

import (
	"errors"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/chat"
	"github.com/timche/mac-mini/mochi/internal/config"
	"github.com/timche/mac-mini/mochi/internal/gather"
	"github.com/timche/mac-mini/mochi/internal/split"
	"github.com/timche/mac-mini/mochi/internal/state"
)

// The tasks a new-session mention asks for, when there is more than one. False means Tim has
// been told no; anything the classifier could not answer is one session with his own words,
// exactly as without it.
func (h *Handler) tasks(m *discordgo.Message, instruction string) ([]split.Task, bool) {
	if h.Split == nil {
		return nil, true
	}
	tasks, err := h.Split(instruction, h.replied(m))
	var tooMany split.TooMany
	switch {
	case errors.As(err, &tooMany):
		h.fail(m, "", tooMany.Error(), nil)
		return nil, false
	case err != nil:
		h.Log.Warn("the mention could not be split, so it is one session", "message", m.ID, "error", h.redact(err))
		return nil, true
	case len(tasks) < 2:
		return nil, true
	}
	return tasks, true
}

// Only to resolve "a, b and c": the rest of the context is not the classifier's.
func (h *Handler) replied(m *discordgo.Message) *discordgo.Message {
	if m.ReferencedMessage != nil || m.MessageReference == nil {
		return m.ReferencedMessage
	}
	ref := m.MessageReference
	channel := ref.ChannelID
	if channel == "" {
		channel = m.ChannelID
	}
	got, err := h.API.Message(channel, ref.MessageID)
	if err != nil {
		return nil
	}
	return got
}

// Where the threads of a split go: the channel, or a thread's parent, since a thread holds no
// threads. A forum's parent holds only posts, so there the answer is no channel and why.
func (h *Handler) splitInto(ch *discordgo.Channel, n int) (string, string) {
	if !ch.IsThread() {
		return ch.ID, ""
	}
	parent, err := h.API.Channel(ch.ParentID)
	if err == nil && parent.Type != discordgo.ChannelTypeGuildForum && parent.Type != discordgo.ChannelTypeGuildMedia {
		return parent.ID, ""
	}
	return "", fmt.Sprintf("this asked for %d separate tasks, but threads cannot be opened beside a forum post, so it is one session with the whole instruction", n)
}

// A thread, a tab and a session per task, all with the same context, then one reply to Tim
// listing them.
func (h *Handler) openMany(server config.Server, ch *discordgo.Channel, m *discordgo.Message, into string, tasks []split.Task, set *gather.Set) {
	lines, failed := make([]string, 0, len(tasks)), 0

	for i, task := range tasks {
		thread, err := h.API.NewThread(into, threadName(task.Title))
		if err != nil {
			h.Log.Error("a thread could not be opened for a task", "task", i+1, "error", h.redact(err))
			lines = append(lines, "❌ "+task.Title+": a thread could not be opened")
			failed++
			continue
		}

		session := state.Session{Thread: thread.ID, Source: ch.ID, Guild: m.GuildID, LastMention: m.ID}
		note := fmt.Sprintf("Tim's message asked for %d separate tasks, and this session is task %d of them: its instruction below is that task alone.", len(tasks), i+1)
		text := opening(ch, m, task.Instruction, set, h.files(thread.ID, set), note)
		if err := h.start(server, &session, task.Title, text); err != nil {
			h.say(thread.ID, "❌ "+err.Error())
			lines = append(lines, fmt.Sprintf("❌ <#%s> %s: %s", thread.ID, task.Title, err))
			failed++
			continue
		}

		h.say(thread.ID, fmt.Sprintf("session started in %s: %s, for %s", server.Workspace, session.Tab, chat.Link(m.GuildID, m.ChannelID, m.ID)))
		lines = append(lines, fmt.Sprintf("<#%s> %s", thread.ID, task.Title))
	}

	reply := chat.Quiet(strings.Join(lines, "\n"))
	reply.Reference = &discordgo.MessageReference{MessageID: m.ID, ChannelID: m.ChannelID}
	if _, err := h.API.Send(m.ChannelID, reply); err != nil {
		h.Log.Error("the list of sessions could not be posted", "error", h.redact(err))
	}
	if failed > 0 {
		h.react(m, chat.Cross)
		return
	}
	h.react(m, chat.Check)
}
