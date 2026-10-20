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
	"unicode"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/chat"
	"github.com/timche/mac-mini/mochi/internal/config"
	"github.com/timche/mac-mini/mochi/internal/gather"
	"github.com/timche/mac-mini/mochi/internal/herdr"
	"github.com/timche/mac-mini/mochi/internal/mark"
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

	// Whether a new-session mention is one task, several, or a question for Tim; nil is
	// always one.
	Split func(instruction string, replied *discordgo.Message) (split.Answer, error)

	// The questions asked and not yet answered.
	Pending state.PendingStore

	// Each session's state, and the name of the thread or post mochi made for it.
	Marks mark.Marker

	// Which sessions are waiting on Tim, and whether he has been told since his last word.
	Waits state.WaitStore

	// The pause between reads of an agent's status after its question is cancelled; nil is
	// time.Sleep.
	Nap func(time.Duration)

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
	if h.textAnswer(m, instruction) {
		return
	}
	if !mentioned {
		if strings.Contains(strings.ToLower(m.Content), "mochi") {
			h.Log.Info("a message from Tim names mochi without a mention mochi recognises, so it was ignored", "message", m.ID)
		}
		return
	}

	// 👀 for as long as the mention is being handled, and no reaction after: the reply, the
	// question or the session's name says where it went.
	h.react(m.ChannelID, m.ID)
	defer h.unreact(m.ChannelID, m.ID)

	ch, err := h.API.Channel(m.ChannelID)
	if err != nil {
		h.fail(m, "", "the channel could not be read", err)
		return
	}

	rec, found, err := h.Store.Get(m.ChannelID)
	if err != nil {
		h.fail(m, "", "mochi could not read its own notes", err)
		return
	}

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
	// The gateway may have said while the request was in flight, and it is the newer word.
	if _, known := h.roles[guild]; !known {
		h.setRole(guild, chat.BotRole(roles, h.roleBot))
	}
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
	answer, ok := h.classify(m, instruction, replied)
	if !ok {
		return
	}
	if answer.Decision == split.Ask {
		h.ask(m, instruction, answer)
		return
	}
	h.begin(server, ch, m, instruction, replied, answer, answer.Decision == split.Many)
}

// The sessions themselves, once it is settled whether they are one or several: straight from
// a mention, or from Tim's answer to a question.
func (h *Handler) begin(server config.Server, ch *discordgo.Channel, m *discordgo.Message, instruction string, replied *discordgo.Message, answer split.Answer, many bool) {
	if m.GuildID == "" {
		m.GuildID = ch.GuildID
	}
	name := title(answer, replied, instruction)

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

	// A mention in a channel goes to a post in the server's forum, where new messages do not
	// bury it; one in a thread or a post stays there, as below.
	forum := ""
	if !ch.IsThread() {
		forum = server.Forum
	}

	var alone string
	if many && len(answer.Tasks) > 1 {
		into, why := h.splitInto(ch, len(answer.Tasks))
		if forum != "" {
			into = forum
		}
		if into != "" {
			h.openMany(server, ch, m, replied, instruction, into, forum != "", answer.Tasks, set)
			return
		}
		alone = why
	}
	if forum != "" {
		h.openPost(server, ch, m, opener{mention: m, replied: replied, instruction: instruction, summary: answer.Summary}, set, name, answer.Type)
		return
	}

	// A thread cannot hold a thread, so a mention inside one makes that thread the session's,
	// one Tim is already in.
	thread, opened := ch.ID, !ch.IsThread()
	if opened {
		threadName := mark.ThreadName(mark.Running, name)
		made, err := h.API.StartThread(m.ChannelID, m.ID, threadName)
		if err != nil {
			h.fail(m, "", "a thread could not be opened on this message", err)
			return
		}
		thread = made.ID
		h.Marks.Named(thread, name, threadName)
		h.follow(thread)
	}

	session := state.Session{Thread: thread, Source: ch.ID, Guild: m.GuildID, LastMention: m.ID}
	text := opening(ch, m, thread, instruction, set, h.files(thread, set), "", "")
	if err := h.start(server, &session, name, text); err != nil {
		h.Marks.Failed(thread)
		h.fail(m, thread, err.Error(), nil)
		return
	}

	if opened {
		if _, err := h.announce(thread, opener{mention: m, replied: replied, instruction: instruction, summary: answer.Summary}); err != nil {
			h.Log.Error("could not post in the thread", "thread", thread, "error", h.redact(err))
		}
	} else {
		h.say(thread, "Session started.")
	}
	if alone != "" {
		h.say(thread, alone)
	}
	h.Marks.Started(thread)
	h.heard(thread)
}

// One session in a post of its own in the forum, and a reply to Tim saying where it went.
func (h *Handler) openPost(server config.Server, ch *discordgo.Channel, m *discordgo.Message, o opener, set *gather.Set, name, typ string) {
	instruction := o.instruction
	post, err := h.post(server.Forum, name, typ, o)
	if err != nil {
		h.fail(m, "", "a post could not be opened for this", err)
		return
	}
	thread := post.ID

	session := state.Session{Thread: thread, Source: ch.ID, Guild: m.GuildID, LastMention: m.ID}
	text := opening(ch, m, thread, instruction, set, h.files(thread, set), "", "")
	err = h.start(server, &session, name, text)
	h.moved(m, name, thread)
	if err != nil {
		h.Marks.Failed(thread)
		h.fail(m, thread, err.Error(), nil)
		return
	}
	h.Marks.Started(thread)
	h.heard(thread)
}

// Tim's message goes to the session with what everybody else wrote in the thread since his
// last mention, and nothing of theirs reaches it otherwise. A session waiting on a question
// has it cancelled and takes his message as the answer, since he answers wherever he is.
func (h *Handler) relay(server config.Server, ch *discordgo.Channel, rec state.Session, m *discordgo.Message, instruction string) {
	status, err := herdr.Status(h.Herdr, rec.Agent)
	if errors.Is(err, herdr.ErrGone) {
		h.restart(server, ch, rec, m, instruction)
		return
	} else if err != nil {
		h.fail(m, rec.Thread, "the session could not be reached", err)
		return
	}

	others, err := gather.Since(h.API, rec.Thread, rec.LastMention, m, h.bot)
	if err != nil {
		h.fail(m, rec.Thread, "the thread could not be read", err)
		return
	}

	files := h.files(rec.Thread, others)
	deliver := func(answering bool) error {
		// Pasted whole: Claude Code shows a long paste as a placeholder but hands the model all
		// of it, and a follow-up has no slash command that a placeholder would hide.
		text := followUp(m, instruction, others, files, answering)
		if answering {
			h.Log.Info("a follow-up answers the question a session was waiting on", "workspace", rec.Workspace, "tab", rec.Tab)
			return herdr.Answer(h.Herdr, rec.Agent, text, h.nap)
		}
		return herdr.Prompt(h.Herdr, rec.Agent, text)
	}
	answering := status == herdr.Blocked
	err = deliver(answering)
	// It asked between the status read and the prompt.
	if errors.Is(err, herdr.ErrBlocked) && !answering {
		err = deliver(true)
	}
	switch {
	case errors.Is(err, herdr.ErrStillBlocked):
		h.fail(m, rec.Thread, "the session is waiting on a question and would not take this as the answer", err)
		return
	case err != nil:
		h.fail(m, rec.Thread, "the message did not reach the session", err)
		return
	}

	rec.LastMention, rec.Updated = m.ID, h.Now().Unix()
	if rec.Guild == "" {
		rec.Guild = m.GuildID
	}
	if err := h.Store.Put(rec); err != nil {
		h.Log.Error("the relay was delivered but not recorded", "thread", rec.Thread, "error", h.redact(err))
	}
	h.Marks.Started(rec.Thread)
	h.heard(rec.Thread)
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
	text := opening(ch, m, rec.Thread, instruction, set, h.files(rec.Thread, set), note, "")
	if err := h.start(server, &rec, mark.Title(ch.Name), text); err != nil {
		h.Marks.Failed(rec.Thread)
		h.fail(m, rec.Thread, err.Error(), nil)
		return
	}

	h.say(rec.Thread, "The previous session ended, so a new one started.")
	h.Marks.Started(rec.Thread)
	h.heard(rec.Thread)
}

// The record is written as soon as the agent exists, so a follow-up finds it even when the
// first prompt did not land.
func (h *Handler) start(server config.Server, rec *state.Session, name, text string) error {
	rec.Agent = "mochi-" + rec.Thread
	rec.Workspace = server.Workspace
	rec.Updated = h.Now().Unix()

	workspace, err := herdr.Workspace(h.Herdr, server.Workspace, server.Repo)
	if err != nil {
		return h.startFailed("the herdr workspace could not be found or made", notStarted, err)
	}
	taken, err := herdr.Tabs(h.Herdr, workspace)
	if err != nil {
		h.Log.Warn("the workspace's tabs could not be listed, so the label may repeat one", "error", h.redact(err))
	}
	rec.Tab = tabLabel(name, taken)
	pane, err := herdr.Tab(h.Herdr, workspace, rec.Tab, server.Repo, ThreadVar+"="+rec.Thread)
	if err != nil {
		return h.startFailed("a herdr tab could not be opened", notStarted, err)
	}
	if err := herdr.Start(h.Herdr, rec.Agent, pane); err != nil {
		return h.startFailed("Claude Code did not start in "+rec.Workspace+": "+rec.Tab, notStarted, err)
	}
	if err := h.Store.Put(*rec); err != nil {
		h.Log.Error("the session started but was not recorded, so a follow-up will start another", "thread", rec.Thread, "error", h.redact(err))
	}
	// Pasted whole: Claude Code shows a long paste as a placeholder but hands the model all of
	// it. A slash command inside a paste never runs, so the skills the session needs are named in
	// its first line for the model to load.
	if err := herdr.Prompt(h.Herdr, rec.Agent, text); err != nil {
		return h.startFailed("Claude Code started in "+rec.Workspace+": "+rec.Tab+" but the prompt did not reach it", notBriefed, err)
	}
	return nil
}

func (h *Handler) nap(d time.Duration) {
	if h.Nap != nil {
		h.Nap(d)
		return
	}
	time.Sleep(d)
}

// What Discord is told when a session does not start, which names none of the tools behind
// it; the log has the detail.
const (
	notStarted = "the session could not be started"
	notBriefed = "the session started but did not get its instructions"
)

func (h *Handler) startFailed(detail, public string, err error) error {
	h.Log.Error(detail, "error", h.redact(err))
	return errors.New(public)
}

func (h *Handler) files(thread string, set *gather.Set) gather.Files {
	return gather.SessionFiles(h.API, h.Cfg.SessionDir(thread)).Fetch(set.Messages())
}

// A ❌ and one line saying why, in the session's thread when there is one and as a reply to
// Tim's message when there is not.
func (h *Handler) fail(m *discordgo.Message, thread, reason string, err error) {
	h.Log.Error(reason, "message", m.ID, "error", h.redact(err))

	line := chat.Quiet("❌ " + sentence(reason))
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
	if _, err := h.API.Send(thread, chat.Quiet(sentence(text))); err != nil {
		h.Log.Error("could not post in the thread", "thread", thread, "error", h.redact(err))
	}
}

// Everything mochi says in Discord reads as a sentence: a capital first, a stop at the end.
// The reasons are written lower-case, since the log carries them too.
func sentence(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return text
	}
	r := []rune(text)
	r[0] = unicode.ToUpper(r[0])
	if !strings.ContainsRune(".!?…", r[len(r)-1]) {
		r = append(r, '.')
	}
	return string(r)
}

func (h *Handler) react(channel, message string) {
	if err := h.API.React(channel, message, chat.Eyes); err != nil {
		h.Log.Warn("could not react", "message", message, "error", h.redact(err))
	}
}

func (h *Handler) unreact(channel, message string) {
	if err := h.API.Unreact(channel, message, chat.Eyes); err != nil {
		h.Log.Warn("a reaction could not be taken back", "message", message, "error", h.redact(err))
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
		delete(all, thread)
		h.Log.Info("forgot a finished session and its attachments", "thread", thread, "agent", rec.Agent)
	}
	h.Marks.Prune(func(thread string) bool { _, ok := all[thread]; return ok })
}

var unslug = regexp.MustCompile(`[^a-z0-9]+`)

// The session's title as a herdr label, kebab-case and at most 30 characters, with a short
// suffix when a tab in the workspace already has it. Sessions keep it: a tab with MOCHI_THREAD
// set is not renamed.
func tabLabel(name string, taken []string) string {
	slug := strings.Trim(unslug.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if slug == "" {
		slug = "discord"
	}
	used := map[string]bool{}
	for _, t := range taken {
		used[t] = true
	}
	for n := 1; ; n++ {
		suffix := ""
		if n > 1 {
			suffix = fmt.Sprintf("-%d", n)
		}
		label := slug
		if len(label) > 30-len(suffix) {
			label = strings.TrimRight(label[:30-len(suffix)], "-")
		}
		if label += suffix; !used[label] {
			return label
		}
	}
}
