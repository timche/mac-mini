package listen

import (
	"log/slog"
	"strings"
	"testing"
)

// The managed role that stands for mochi, which Discord's autocomplete offers beside mochi
// itself.
const botRole = "1562103296259522733"

func TestARoleMentionOfMochiStartsNothing(t *testing.T) {
	f := newFixture(t)

	f.h.Handle(f.api.Post(channel, 1, tim, "<@&"+botRole+"> have a look at this"))

	if len(f.herdr.calls) != 0 || len(f.api.Reactions) != 0 || len(f.api.Sent) != 0 {
		t.Errorf("herdr %v, reactions %v, sent %v", f.herdr.calls, f.api.Reactions, f.api.Sent)
	}
}

func TestARoleMentionBesideMochisIsLeftInTheInstruction(t *testing.T) {
	f := newFixture(t)
	m := f.api.Post(channel, 1, tim, "<@!"+bot.ID+"> look at <@&"+botRole+"> please <@"+bot.ID+">")

	f.h.Handle(m)

	got := strings.TrimSpace(between(t, onlyPrompt(t, f.herdr, "mochi-"+m.ID), "INSTRUCTION FROM TIM"))
	if got != "look at <@&"+botRole+"> please" {
		t.Errorf("instruction = %q", got)
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
