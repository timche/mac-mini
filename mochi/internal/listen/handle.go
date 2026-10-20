// Package listen is `mochi listen`: the gateway connection, and what a mention from Tim
// does — a new Claude Code session in herdr, or his follow-up relayed to one already
// running.
package listen

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/chat"
	"github.com/timche/mac-mini/mochi/internal/config"
	"github.com/timche/mac-mini/mochi/internal/gather"
	"github.com/timche/mac-mini/mochi/internal/herdr"
	"github.com/timche/mac-mini/mochi/internal/state"
)

// What the session's tab carries, so `mochi post` and `mochi history` know which session
// they are in without being told.
const ThreadVar = "MOCHI_THREAD"

type Handler struct {
	Cfg   config.Config
	API   chat.API
	Herdr herdr.Runner
	Store state.Store
	Now   func() time.Time
	Log   *slog.Logger
	Token string

	// One mention at a time: each is a handful of Discord calls and a herdr start, and two
	// at once would race on the state file and on the agent name.
	mu  sync.Mutex
	bot string
}

func (h *Handler) SetBot(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.bot = id
}

// Held across a handled mention, so a listener exiting for a new binary never abandons one
// halfway through.
func (h *Handler) Lock()   { h.mu.Lock() }
func (h *Handler) Unlock() { h.mu.Unlock() }

func (h *Handler) Handle(m *discordgo.Message) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Tim's account and nobody else's: a mention is an instruction to an agent that can
	// change his repositories.
	if m.Author == nil || m.Author.Bot || m.Author.ID != h.Cfg.User {
		return
	}
	server, ok := h.Cfg.Server(m.GuildID)
	if !ok || h.bot == "" || !mentions(m.Content, h.bot) {
		return
	}
	instruction := strings.TrimSpace(mentionOf(h.bot).ReplaceAllString(m.Content, ""))

	ch, err := h.API.Channel(m.ChannelID)
	if err != nil {
		h.fail(m, "", "the channel could not be read", err)
		return
	}

	rec, found, err := h.Store.Get(m.ChannelID)
	if err != nil {
		h.fail(m, "", "mochi's state file could not be read", err)
		return
	}

	h.react(m, chat.Eyes)
	if found {
		h.relay(server, ch, rec, m, instruction)
		return
	}
	h.open(server, ch, m, instruction)
}

func (h *Handler) open(server config.Server, ch *discordgo.Channel, m *discordgo.Message, instruction string) {
	var (
		set *gather.Set
		err error
	)
	switch {
	case ch.IsThread():
		set, err = gather.Thread(h.API, ch, m)
	case m.MessageReference != nil:
		set, err = gather.Reply(h.API, m)
	default:
		set, err = gather.Channel(h.API, m)
	}
	if err != nil {
		h.fail(m, "", "the conversation could not be read", err)
		return
	}

	// A thread cannot hold a thread, so a mention inside one makes that thread the session's.
	thread := ch.ID
	if !ch.IsThread() {
		opened, err := h.API.StartThread(m.ChannelID, m.ID, threadName(instruction))
		if err != nil {
			h.fail(m, "", "a thread could not be opened on this message", err)
			return
		}
		thread = opened.ID
	}

	session := state.Session{Thread: thread, Source: ch.ID, Guild: m.GuildID, LastMention: m.ID}
	text := opening(ch, m, instruction, set, h.files(thread, set), "")
	if err := h.start(server, &session, ch.Name, text); err != nil {
		h.fail(m, thread, err.Error(), nil)
		return
	}

	h.say(thread, fmt.Sprintf("session started in %s: %s", server.Workspace, session.Tab))
	h.react(m, chat.Check)
}

// Tim's message goes to the session with what everybody else wrote in the thread since his
// last mention, and nothing of theirs reaches it otherwise.
func (h *Handler) relay(server config.Server, ch *discordgo.Channel, rec state.Session, m *discordgo.Message, instruction string) {
	if _, err := herdr.Status(h.Herdr, rec.Agent); errors.Is(err, herdr.ErrGone) {
		h.restart(server, ch, rec, m, instruction)
		return
	} else if err != nil {
		h.fail(m, rec.Thread, "herdr could not be asked about the session", err)
		return
	}

	others, err := gather.Since(h.API, rec.Thread, rec.LastMention, m, h.Cfg.User, h.bot)
	if err != nil {
		h.fail(m, rec.Thread, "the thread could not be read", err)
		return
	}

	err = herdr.Prompt(h.Herdr, rec.Agent, followUp(m, instruction, others, h.files(rec.Thread, others)))
	switch {
	case errors.Is(err, herdr.ErrBlocked):
		h.fail(m, rec.Thread, fmt.Sprintf("the session is waiting on a question in %s: %s, so this did not reach it — answer it there first", rec.Workspace, rec.Tab), nil)
		return
	case err != nil:
		h.fail(m, rec.Thread, "the message did not reach the session", err)
		return
	}

	rec.LastMention, rec.Updated = m.ID, h.Now().Unix()
	if err := h.Store.Put(rec); err != nil {
		h.Log.Error("the relay was delivered but not recorded", "thread", rec.Thread, "error", h.redact(err))
	}
	h.react(m, chat.Check)
}

// The session Tim closed is replaced by one that reads the whole thread, so nothing said in
// it is lost, and the thread is told.
func (h *Handler) restart(server config.Server, ch *discordgo.Channel, rec state.Session, m *discordgo.Message, instruction string) {
	set, err := gather.Thread(h.API, ch, m)
	if err != nil {
		h.fail(m, rec.Thread, "the thread could not be read", err)
		return
	}

	rec.LastMention = m.ID
	note := "The session that was working on this thread has gone, so this is a new one: the thread so far is the context."
	text := opening(ch, m, instruction, set, h.files(rec.Thread, set), note)
	if err := h.start(server, &rec, ch.Name, text); err != nil {
		h.fail(m, rec.Thread, err.Error(), nil)
		return
	}

	h.say(rec.Thread, fmt.Sprintf("the previous session is gone, so a new one started in %s: %s", server.Workspace, rec.Tab))
	h.react(m, chat.Check)
}

// The record is written as soon as the agent exists, so a follow-up finds it even when the
// first prompt did not land.
func (h *Handler) start(server config.Server, rec *state.Session, channelName, text string) error {
	rec.Agent = "mochi-" + rec.Thread
	rec.Workspace = server.Workspace
	rec.Tab = tabLabel(channelName, h.Now())
	rec.Updated = h.Now().Unix()

	if !herdr.ValidName(rec.Agent) {
		return fmt.Errorf("%s is not a name herdr will take", rec.Agent)
	}

	workspace, err := herdr.Workspace(h.Herdr, server.Workspace, server.Repo)
	if err != nil {
		return h.herdrFailed("the herdr workspace could not be found or made", err)
	}
	pane, err := herdr.Tab(h.Herdr, workspace, rec.Tab, server.Repo, ThreadVar+"="+rec.Thread)
	if err != nil {
		return h.herdrFailed("a herdr tab could not be opened", err)
	}
	if err := herdr.Start(h.Herdr, rec.Agent, pane); err != nil {
		return h.herdrFailed("Claude Code did not start in "+rec.Workspace+": "+rec.Tab, err)
	}
	if err := h.Store.Put(*rec); err != nil {
		h.Log.Error("the session started but was not recorded, so a follow-up will start another", "thread", rec.Thread, "error", h.redact(err))
	}
	if err := herdr.Prompt(h.Herdr, rec.Agent, "/lead "+text); err != nil {
		return h.herdrFailed("Claude Code started in "+rec.Workspace+": "+rec.Tab+" but the prompt did not reach it", err)
	}
	return nil
}

func (h *Handler) herdrFailed(reason string, err error) error {
	h.Log.Error(reason, "error", h.redact(err))
	return errors.New(reason)
}

func (h *Handler) files(thread string, set *gather.Set) gather.Files {
	return gather.Downloader{
		API:     h.API,
		Dir:     h.Cfg.SessionDir(thread),
		PerFile: gather.PerFile,
		Total:   gather.SessionTotal,
	}.Fetch(set.Messages())
}

// A ❌ and one line saying why, in the session's thread when there is one and as a reply to
// Tim's message when there is not.
func (h *Handler) fail(m *discordgo.Message, thread, reason string, err error) {
	h.Log.Error(reason, "message", m.ID, "error", h.redact(err))
	h.react(m, chat.Cross)

	line := chat.Quiet("❌ " + reason)
	target := thread
	if target == "" {
		target = m.ChannelID
		line.Reference = &discordgo.MessageReference{MessageID: m.ID, ChannelID: m.ChannelID}
	}
	if _, err := h.API.Send(target, line); err != nil {
		h.Log.Error("the reason could not be posted", "error", h.redact(err))
	}
}

func (h *Handler) say(thread, text string) {
	if _, err := h.API.Send(thread, chat.Quiet(text)); err != nil {
		h.Log.Error("could not post in the thread", "thread", thread, "error", h.redact(err))
	}
}

func (h *Handler) react(m *discordgo.Message, emoji string) {
	if err := h.API.React(m.ChannelID, m.ID, emoji); err != nil {
		h.Log.Warn("could not react", "emoji", emoji, "error", h.redact(err))
	}
}

func (h *Handler) redact(err error) string { return chat.Redact(err, h.Token) }

// Records are kept while their agent lives, and for a week after it goes so a follow-up in
// the thread can still start a fresh session that knows where it came from.
func (h *Handler) Prune(keep time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()

	all, err := h.Store.Load()
	if err != nil {
		h.Log.Error("the state file could not be read to prune it", "error", err)
		return
	}
	for thread, rec := range all {
		if h.Now().Sub(time.Unix(rec.Updated, 0)) < keep {
			continue
		}
		if _, err := herdr.Status(h.Herdr, rec.Agent); !errors.Is(err, herdr.ErrGone) {
			continue
		}
		if err := h.Store.Remove(thread); err != nil {
			h.Log.Error("a finished session could not be forgotten", "thread", thread, "error", err)
			continue
		}
		os.RemoveAll(h.Cfg.SessionDir(thread))
		h.Log.Info("forgot a finished session and its attachments", "thread", thread, "agent", rec.Agent)
	}
}

// The token form of a mention and not Discord's list of mentioned users, which also holds
// the author of any message replied to with the ping left on.
func mentions(content, bot string) bool { return mentionOf(bot).MatchString(content) }

func mentionOf(bot string) *regexp.Regexp {
	return regexp.MustCompile(`<@!?` + regexp.QuoteMeta(bot) + `>`)
}

var unslug = regexp.MustCompile(`[^a-z0-9]+`)

func tabLabel(channel string, now time.Time) string {
	slug := strings.Trim(unslug.ReplaceAllString(strings.ToLower(channel), "-"), "-")
	if len(slug) > 30 {
		slug = strings.TrimRight(slug[:30], "-")
	}
	if slug == "" {
		slug = "discord"
	}
	return slug + "-" + now.Format("1504")
}

func threadName(instruction string) string {
	line, _, _ := strings.Cut(instruction, "\n")
	line = strings.TrimSpace(line)
	if r := []rune(line); len(r) > 90 {
		line = string(r[:90]) + "…"
	}
	if line == "" {
		return "mochi"
	}
	return "mochi: " + line
}
