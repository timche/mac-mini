// Package listen is `mochi listen`: the gateway connection, and what a mention from Tim
// does — a new Claude Code session in herdr, or his follow-up relayed to one already
// running.
package listen

import (
	"encoding/json"
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
	"github.com/timche/mac-mini/mochi/internal/split"
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

	// Whether a new-session mention is one task or several; nil is always one.
	Split func(instruction string, replied *discordgo.Message) ([]split.Task, error)

	// One mention at a time: each is a handful of Discord calls and a herdr start, and two
	// at once would race on the state file and on the agent name.
	mu      sync.Mutex
	bot     string
	mention *regexp.Regexp

	// The bot's managed role in each guild, by guild, once known; "" for a guild that has
	// none. Kept current from the gateway, and read from Discord when a message needs it
	// before the gateway has said. A lock of its own, since a mention holds mu for as long as
	// a classifier call and a herdr start take, and every gateway event would queue behind it.
	// Always taken after mu, never before.
	roleMu  sync.Mutex
	roleBot string
	roles   map[string]string
}

func (h *Handler) SetBot(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.bot = id
	h.mention = regexp.MustCompile(`<@!?` + regexp.QuoteMeta(id) + `>`)

	h.roleMu.Lock()
	defer h.roleMu.Unlock()
	h.roleBot = id
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
	if !ok || h.mention == nil {
		return
	}
	instruction, mentioned := h.mentioned(m)
	if !mentioned {
		if strings.Contains(strings.ToLower(m.Content), "mochi") {
			h.Log.Info("a message from Tim names mochi without a mention mochi recognises, so it was ignored", "message", m.ID)
		}
		return
	}

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

// The token form of a mention and not Discord's lists of mentioned users and roles, which
// also hold the author of any message replied to with the ping left on. Either the bot
// itself or its own managed role, which Discord's autocomplete offers beside it; no other
// role. The instruction is the message with every such mention taken out.
func (h *Handler) mentioned(m *discordgo.Message) (string, bool) {
	content := m.Content
	found := h.mention.MatchString(content)
	content = h.mention.ReplaceAllString(content, "")

	if strings.Contains(content, "<@&") {
		if role := h.botRole(m.GuildID); role != "" && strings.Contains(content, "<@&"+role+">") {
			found = true
			content = strings.ReplaceAll(content, "<@&"+role+">", "")
		}
	}
	return strings.TrimSpace(content), found
}

// The request is made without the role lock held, so gateway events go on being applied
// while it is in flight.
func (h *Handler) botRole(guild string) string {
	h.roleMu.Lock()
	role, known := h.roles[guild]
	h.roleMu.Unlock()
	if known {
		return role
	}

	roles, err := h.API.Roles(guild)
	if err != nil {
		h.Log.Warn("the guild's roles could not be read, so a role mention of mochi is not recognised", "guild", guild, "error", h.redact(err))
		return ""
	}
	h.roleMu.Lock()
	defer h.roleMu.Unlock()
	h.setRole(guild, chat.BotRole(roles, h.roleBot))
	return h.roles[guild]
}

// Called with roleMu held.
func (h *Handler) setRole(guild, role string) {
	if h.roles == nil {
		h.roles = map[string]string{}
	}
	h.roles[guild] = role
}

// What the gateway says about roles: a guild's whole list when it becomes available, and
// each role created, changed or deleted afterwards. discordgo's typed events drop the
// tags, so these are read from the raw payload.
func (h *Handler) Gateway(event string, data []byte) {
	switch event {
	case "GUILD_CREATE", "GUILD_ROLE_CREATE", "GUILD_ROLE_UPDATE", "GUILD_ROLE_DELETE":
	default:
		return
	}

	h.roleMu.Lock()
	defer h.roleMu.Unlock()
	if h.roleBot == "" {
		return
	}

	switch event {
	case "GUILD_CREATE":
		var g struct {
			ID    string      `json:"id"`
			Roles []chat.Role `json:"roles"`
		}
		if json.Unmarshal(data, &g) == nil && g.ID != "" {
			h.setRole(g.ID, chat.BotRole(g.Roles, h.roleBot))
		}
	case "GUILD_ROLE_CREATE", "GUILD_ROLE_UPDATE":
		var e struct {
			GuildID string    `json:"guild_id"`
			Role    chat.Role `json:"role"`
		}
		if json.Unmarshal(data, &e) != nil || e.GuildID == "" {
			return
		}
		switch {
		case e.Role.BotID() == h.roleBot:
			h.setRole(e.GuildID, e.Role.ID)
		case h.roles[e.GuildID] == e.Role.ID:
			h.setRole(e.GuildID, "")
		}
	case "GUILD_ROLE_DELETE":
		var e struct {
			GuildID string `json:"guild_id"`
			RoleID  string `json:"role_id"`
		}
		if json.Unmarshal(data, &e) == nil && h.roles[e.GuildID] == e.RoleID {
			h.setRole(e.GuildID, "")
		}
	}
}

func (h *Handler) open(server config.Server, ch *discordgo.Channel, m *discordgo.Message, instruction string) {
	replied := h.replied(m)
	tasks, ok := h.tasks(m, instruction, replied)
	if !ok {
		return
	}
	name := title(tasks, replied, instruction)

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

	var alone string
	if len(tasks) > 1 {
		into, why := h.splitInto(ch, len(tasks))
		if into != "" {
			h.openMany(server, ch, m, instruction, into, tasks, set)
			return
		}
		alone = why
	}

	// A thread cannot hold a thread, so a mention inside one makes that thread the session's.
	thread := ch.ID
	if !ch.IsThread() {
		opened, err := h.API.StartThread(m.ChannelID, m.ID, threadName(name))
		if err != nil {
			h.fail(m, "", "a thread could not be opened on this message", err)
			return
		}
		thread = opened.ID
	}

	session := state.Session{Thread: thread, Source: ch.ID, Guild: m.GuildID, LastMention: m.ID}
	text := opening(ch, m, instruction, set, h.files(thread, set), "", "")
	if err := h.start(server, &session, name, text); err != nil {
		h.fail(m, thread, err.Error(), nil)
		return
	}

	h.say(thread, fmt.Sprintf("session started in %s: %s", server.Workspace, session.Tab))
	if alone != "" {
		h.say(thread, alone)
	}
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
	text := opening(ch, m, instruction, set, h.files(rec.Thread, set), note, "")
	if err := h.start(server, &rec, strings.TrimPrefix(ch.Name, "mochi: "), text); err != nil {
		h.fail(m, rec.Thread, err.Error(), nil)
		return
	}

	h.say(rec.Thread, fmt.Sprintf("the previous session is gone, so a new one started in %s: %s", server.Workspace, rec.Tab))
	h.react(m, chat.Check)
}

// The record is written as soon as the agent exists, so a follow-up finds it even when the
// first prompt did not land.
func (h *Handler) start(server config.Server, rec *state.Session, name, text string) error {
	rec.Agent = "mochi-" + rec.Thread
	rec.Workspace = server.Workspace
	rec.Tab = tabLabel(name, h.Now())
	rec.Updated = h.Now().Unix()

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
	return gather.SessionFiles(h.API, h.Cfg.SessionDir(thread)).Fetch(set.Messages())
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

func threadName(name string) string {
	line, _, _ := strings.Cut(name, "\n")
	line = strings.TrimSpace(line)
	if r := []rune(line); len(r) > 90 {
		line = string(r[:90]) + "…"
	}
	if line == "" {
		return "mochi"
	}
	return "mochi: " + line
}
