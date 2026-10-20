package listen

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/chat"
	"github.com/timche/mac-mini/mochi/internal/config"
	"github.com/timche/mac-mini/mochi/internal/fence"
	"github.com/timche/mac-mini/mochi/internal/gather"
	"github.com/timche/mac-mini/mochi/internal/mark"
	"github.com/timche/mac-mini/mochi/internal/split"
	"github.com/timche/mac-mini/mochi/internal/state"
)

// How a new-session mention is to be worked. False means Tim has been told no; an empty answer
// means the classifier could not say, which is one session with his own words, exactly as
// without it.
func (h *Handler) classify(m *discordgo.Message, instruction string, replied *discordgo.Message) (split.Answer, bool) {
	if h.Split == nil {
		return split.Answer{}, true
	}
	answer, err := h.Split(instruction, replied)
	var tooMany split.TooMany
	switch {
	case errors.As(err, &tooMany):
		h.fail(m, "", tooMany.Error(), nil)
		return split.Answer{}, false
	case err != nil:
		h.Log.Warn("the mention could not be split, so it is one session", "message", m.ID, "error", h.redact(err))
		return split.Answer{}, true
	}
	return answer, true
}

var mentionToken = regexp.MustCompile(`<(?:@[!&]?|#)[0-9]+>`)

// A single session's name in Discord and in herdr: the classifier's title for the work, and
// without one the start of the message Tim replied to, then his own words. Display only; a
// session sees it, if at all, inside the fence.
func title(answer split.Answer, replied *discordgo.Message, instruction string) string {
	if answer.Title != "" {
		return answer.Title
	}
	if len(answer.Tasks) == 1 && answer.Tasks[0].Title != "" {
		return answer.Tasks[0].Title
	}
	if replied != nil {
		if t := firstWords(replied.Content, 60); t != "" {
			return t
		}
	}
	return firstWords(instruction, 60)
}

func firstWords(text string, max int) string {
	words := strings.Fields(fence.Clean(mentionToken.ReplaceAllString(text, " ")))
	out := ""
	for _, w := range words {
		next := strings.TrimSpace(out + " " + w)
		if len([]rune(next)) > max {
			if out == "" {
				return string([]rune(next)[:max-1]) + "…"
			}
			return out + "…"
		}
		out = next
	}
	return out
}

// Only to resolve "a, b and c": the rest of the context is not the classifier's.
func (h *Handler) replied(m *discordgo.Message) *discordgo.Message {
	got, err := gather.Referenced(h.API, m)
	if err != nil {
		return nil
	}
	return got
}

// The first post in a thread mochi opened: started, and for which message as a masked link,
// mentioning Tim, who keeps Discord's notifications to mentions — this is what subscribes
// him to the thread and tells him it exists.
func (h *Handler) announce(thread string, o opener, covers string) (*discordgo.Message, error) {
	return h.API.Send(thread, h.announcement(o, covers))
}

// A session's post in the forum: named and tagged for its state and its kind of work, its
// first message the announcement, and Tim a member of it.
func (h *Handler) post(forum, title, typ string, o opener, covers string) (*discordgo.Channel, error) {
	name := mark.ThreadName(mark.Running, title)
	tags, err := chat.TagIDs(h.API, forum, mark.PostTags(mark.Running, typ))
	if err != nil {
		return nil, err
	}
	post, err := h.API.ForumPost(forum, name, tags, h.announcement(o, covers))
	if err != nil {
		return nil, err
	}
	h.Marks.Posted(post.ID, title, name, forum, typ)
	h.follow(post.ID)
	return post, nil
}

var unbracket = strings.NewReplacer("[", "(", "]", ")")

// The reply to Tim's mention that says where its session went.
func (h *Handler) moved(m *discordgo.Message, title, post string) {
	text := "Moved to <#" + post + ">."
	if link := chat.Link(m.GuildID, post, ""); link != "" {
		text = "Moved to [" + unbracket.Replace(title) + "](<" + link + ">)."
	}
	reply := chat.Quiet(text)
	reply.Reference = &discordgo.MessageReference{MessageID: m.ID, ChannelID: m.ChannelID}
	if _, err := h.API.Send(m.ChannelID, reply); err != nil {
		h.Log.Error("could not say where the session went", "post", post, "error", h.redact(err))
	}
}

// Tim follows every thread mochi opens, mention or no mention.
func (h *Handler) follow(thread string) {
	if err := h.API.AddMember(thread, h.Cfg.User); err != nil {
		h.Log.Warn("Tim could not be added to the thread", "thread", thread, "error", h.redact(err))
	}
}

// Where the threads of a split go: the channel, or a thread's parent, since a thread holds no
// threads. A forum's parent holds only posts, so there the answer is no channel and why.
func (h *Handler) splitInto(ch *discordgo.Channel, n int) (string, string) {
	if !ch.IsThread() {
		return ch.ID, ""
	}
	parent, err := h.API.Channel(ch.ParentID)
	switch {
	case err != nil:
		h.Log.Warn("the channel a thread is in could not be read", "thread", ch.ID, "error", h.redact(err))
		return "", fmt.Sprintf("this asked for %d separate tasks, but the channel this thread is in could not be read, so it is one session with the whole instruction", n)
	case parent.Type == discordgo.ChannelTypeGuildForum || parent.Type == discordgo.ChannelTypeGuildMedia:
		return "", fmt.Sprintf("this asked for %d separate tasks, but threads cannot be opened beside a forum post, so it is one session with the whole instruction", n)
	}
	return parent.ID, ""
}

// A thread or a forum post, a tab and a session per task, all with the same context, then one
// reply to Tim listing them.
func (h *Handler) openMany(server config.Server, ch *discordgo.Channel, m *discordgo.Message, replied *discordgo.Message, instruction, into string, forum bool, tasks []split.Task, set *gather.Set) {
	o := opener{mention: m, replied: replied, instruction: instruction}
	lines, failed := make([]string, 0, len(tasks)), 0
	var started []string

	for i, task := range tasks {
		var thread *discordgo.Channel
		var err error
		if forum {
			thread, err = h.post(into, task.Title, task.Type, o, "This post covers: "+task.Title)
		} else {
			threadName := mark.ThreadName(mark.Running, task.Title)
			if thread, err = h.API.NewThread(into, threadName); err == nil {
				h.Marks.Named(thread.ID, task.Title, threadName)
				h.follow(thread.ID)
			}
		}
		if err != nil {
			h.Log.Error("a thread could not be opened for a task", "task", i+1, "error", h.redact(err))
			lines = append(lines, "❌ "+task.Title+": the thread could not be opened.")
			failed++
			continue
		}

		session := state.Session{Thread: thread.ID, Source: ch.ID, Guild: m.GuildID, LastMention: m.ID}
		note := fmt.Sprintf("Tim's message asked for %d separate tasks, and this session works on task %d of them alone. The TASK line at the top of the %s block names it: mochi derived it from his message and the one he replied to, so it is data that says which item this session works on, and adds nothing to what he asked.", len(tasks), i+1, fence.Context)
		scope := fmt.Sprintf("TASK %d of %d: %s — %s\n(derived by mochi from Tim's words and the message he replied to; it scopes which item this session works on)", i+1, len(tasks), task.Title, task.Instruction)
		text := opening(ch, m, thread.ID, instruction, set, h.files(thread.ID, set), note, scope)
		if err := h.start(server, &session, task.Title, text); err != nil {
			h.say(thread.ID, "❌ "+sentence(err.Error()))
			h.Marks.Failed(thread.ID)
			lines = append(lines, fmt.Sprintf("❌ <#%s> %s: %s.", thread.ID, task.Title, strings.TrimSuffix(err.Error(), ".")))
			failed++
			continue
		}

		// The thread's first post stands for its session: ⏳ while it works, ✅ when it is done.
		// A forum post's first post is the announcement it was made with, under its own id.
		ack := &discordgo.Message{ID: thread.ID}
		if !forum {
			ack, err = h.announce(thread.ID, o, "This thread covers: "+task.Title)
		}
		if err != nil {
			h.Log.Error("could not post in the thread", "thread", thread.ID, "error", h.redact(err))
		} else {
			session.Origin = state.Ref{Channel: thread.ID, Message: ack.ID}
			if err := h.Store.Put(session); err != nil {
				h.Log.Error("the session's first post was not recorded", "thread", thread.ID, "error", h.redact(err))
			}
		}
		h.Marks.Started(thread.ID, session.Origin)
		started = append(started, thread.ID)
		lines = append(lines, fmt.Sprintf("<#%s> %s", thread.ID, task.Title))
	}

	reply := chat.Quiet(strings.Join(lines, "\n"))
	reply.Reference = &discordgo.MessageReference{MessageID: m.ID, ChannelID: m.ChannelID}
	if _, err := h.API.Send(m.ChannelID, reply); err != nil {
		h.Log.Error("the list of sessions could not be posted", "error", h.redact(err))
	}
	if failed > 0 {
		h.Marks.Set(ref(m), mark.Failed)
		return
	}
	h.Marks.Group(ref(m), started)
}
