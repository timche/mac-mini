package listen

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/mochi/internal/chat"
)

const (
	botRole   = "1562103296259522733"
	otherRole = "1562103296259522799"
)

func role(id, bot string) chat.Role {
	r := chat.Role{ID: id}
	if bot != "" {
		r.Tags = &struct {
			BotID string `json:"bot_id"`
		}{BotID: bot}
	}
	return r
}

func withRoles(f *fixture) {
	f.api.GuildRoles = map[string][]chat.Role{guild: {
		role(guild, ""),
		role(otherRole, "1400000000000000099"),
		role(botRole, bot.ID),
	}}
}

// What Tim actually sent: Discord's autocomplete offered mochi's managed role beside mochi,
// and he picked the role.
func TestAMentionOfMochisOwnRoleStartsASession(t *testing.T) {
	f := newFixture(t)
	withRoles(f)
	m := f.api.Post(channel, 1, tim, "<@&"+botRole+"> have a look at this")

	f.h.Handle(m)

	prompt := onlyPrompt(t, f.herdr, "mochi-"+m.ID)
	if got := strings.TrimSpace(between(t, prompt, "INSTRUCTION FROM TIM")); got != "have a look at this" {
		t.Errorf("instruction = %q", got)
	}
	if !f.api.ReactedWith(m.ID, "⏳") {
		t.Errorf("reactions = %v", f.api.Reactions)
	}

	// Read once and kept, not on every message.
	f.h.Handle(f.api.Post(channel, 2, tim, "<@&"+botRole+"> and this"))
	if f.api.RoleReads != 1 {
		t.Errorf("the roles were read %d times", f.api.RoleReads)
	}
}

func TestAnyOtherRoleMentionStartsNothing(t *testing.T) {
	f := newFixture(t)
	withRoles(f)
	for n, content := range []string{
		"<@&" + otherRole + "> have a look at this",
		"<@&" + guild + "> everyone look",
	} {
		f.h.Handle(f.api.Post(channel, n+1, tim, content))
	}
	if len(f.herdr.calls) != 0 || len(f.api.Reactions) != 0 {
		t.Errorf("herdr %v, reactions %v", f.herdr.calls, f.api.Reactions)
	}
}

func TestEveryFormOfMentionIsTakenOutOfTheInstruction(t *testing.T) {
	f := newFixture(t)
	withRoles(f)
	m := f.api.Post(channel, 1, tim, "<@&"+botRole+"> <@!"+bot.ID+"> look at <@&"+otherRole+"> please <@"+bot.ID+">")

	f.h.Handle(m)

	got := strings.TrimSpace(between(t, onlyPrompt(t, f.herdr, "mochi-"+m.ID), "INSTRUCTION FROM TIM"))
	if got != "look at <@&"+otherRole+"> please" {
		t.Errorf("instruction = %q", got)
	}
}

// The gateway's own account of a guild's roles, so the first role mention needs no request.
func TestTheGatewayKeepsTheRoleCurrent(t *testing.T) {
	f := newFixture(t)

	f.h.Gateway("GUILD_CREATE", []byte(`{"id":"`+guild+`","roles":[{"id":"`+botRole+`","tags":{"bot_id":"`+bot.ID+`"}},{"id":"`+otherRole+`"}]}`))
	m := f.api.Post(channel, 1, tim, "<@&"+botRole+"> look")
	f.h.Handle(m)
	if f.api.RoleReads != 0 || len(f.herdr.prompts["mochi-"+m.ID]) != 1 {
		t.Errorf("reads %d, prompts %v", f.api.RoleReads, f.herdr.prompts)
	}

	f.h.Gateway("GUILD_ROLE_DELETE", []byte(`{"guild_id":"`+guild+`","role_id":"`+botRole+`"}`))
	gone := f.api.Post(channel, 2, tim, "<@&"+botRole+"> look again")
	f.h.Handle(gone)
	if len(f.herdr.prompts["mochi-"+gone.ID]) != 0 {
		t.Error("a deleted role still triggers")
	}

	f.h.Gateway("GUILD_ROLE_CREATE", []byte(`{"guild_id":"`+guild+`","role":{"id":"`+otherRole+`","tags":{"bot_id":"`+bot.ID+`"}}}`))
	again := f.api.Post(channel, 3, tim, "<@&"+otherRole+"> the new role")
	f.h.Handle(again)
	if len(f.herdr.prompts["mochi-"+again.ID]) != 1 {
		t.Error("a new managed role was not taken up")
	}
}

// The miss that started this: a message that names mochi but mentions it in no form mochi
// knows is logged, so the next one is visible.
func TestNamingMochiWithoutAMentionIsLogged(t *testing.T) {
	f := newFixture(t)
	var logged strings.Builder
	f.h.Log = slog.New(slog.NewTextHandler(&logged, nil))

	f.h.Handle(f.api.Post(channel, 1, tim, "hey Mochi, have a look"))
	f.h.Handle(f.api.Post(channel, 2, tim, "an ordinary message"))

	if strings.Count(logged.String(), "names mochi without a mention") != 1 {
		t.Errorf("log:\n%s", logged.String())
	}
}

// A mention holds the handler's lock for as long as a classifier call and a herdr start
// take; a role event in the meantime is applied at once rather than queued behind it.
func TestARoleEventIsAppliedWhileAMentionIsInProgress(t *testing.T) {
	f := newFixture(t)
	f.herdr.started, f.herdr.release = make(chan struct{}), make(chan struct{})

	handled := make(chan struct{})
	go func() {
		f.h.Handle(f.api.Post(channel, 1, tim, "<@"+bot.ID+"> research"))
		close(handled)
	}()
	<-f.herdr.started

	applied := make(chan struct{})
	go func() {
		f.h.Gateway("GUILD_ROLE_CREATE", []byte(`{"guild_id":"`+guild+`","role":{"id":"`+botRole+`","tags":{"bot_id":"`+bot.ID+`"}}}`))
		close(applied)
	}()
	select {
	case <-applied:
	case <-time.After(2 * time.Second):
		t.Fatal("the role event waited for the mention")
	}
	if got := f.h.botRole(guild); got != botRole || f.api.RoleReads != 0 {
		t.Errorf("role = %q after %d reads", got, f.api.RoleReads)
	}

	close(f.herdr.release)
	<-handled
}

// Everything else the gateway sends is not looked at, let alone locked for.
func TestOtherGatewayEventsAreIgnored(t *testing.T) {
	f := newFixture(t)
	f.h.Lock()
	defer f.h.Unlock()

	done := make(chan struct{})
	go func() {
		f.h.Gateway("MESSAGE_CREATE", []byte(`{}`))
		f.h.Gateway("GUILD_CREATE", []byte(`{"id":"`+guild+`","roles":[]}`))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a gateway event waited for the handler's lock")
	}
}

// A role event that lands while the roles are being fetched is newer than the fetch, and the
// fetch may not overwrite it.
func TestAFetchDoesNotOverwriteANewerGatewayUpdate(t *testing.T) {
	f := newFixture(t)
	withRoles(f)
	f.api.DuringRoles = func() {
		f.h.Gateway("GUILD_ROLE_CREATE", []byte(`{"guild_id":"`+guild+`","role":{"id":"`+otherRole+`","tags":{"bot_id":"`+bot.ID+`"}}}`))
	}

	if got := f.h.botRole(guild); got != otherRole {
		t.Errorf("role = %q, want the gateway's %q", got, otherRole)
	}
}
