package listen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/timche/mac-mini/mochi/internal/event"
)

// Short enough that Claude Code takes it as typed and runs /lead, rather than as a paste.
// Measured without the brief's path, which is as long as the cache directory it is under.
func shortLine(t *testing.T, prompt string) {
	t.Helper()
	if strings.ContainsAny(prompt, "\n\r") || len(briefPath.ReplaceAllString(prompt, "")) > 400 {
		t.Errorf("the pasted prompt is not one short line (%d bytes):\n%s", len(prompt), prompt)
	}
}

func TestASessionIsPastedOneLineThatNamesItsBrief(t *testing.T) {
	f := newFixture(t)
	qol := f.api.Post(channel, 1, maker, "QOL list")
	m := f.mention(channel, 2, tim, "research", qol)

	f.h.Handle(m)

	raw := f.herdr.raw["mochi-"+m.ID]
	if len(raw) != 1 {
		t.Fatalf("pasted %d prompts", len(raw))
	}
	path := filepath.Join(f.cfg.SessionDir(m.ID), "brief.md")
	want := "/lead Tim mentioned mochi in Discord. Your brief is " + path + "; read all of it before anything else. It holds his instruction and the fenced Discord context, and says which skill to load first."
	if raw[0] != want {
		t.Errorf("pasted:\n%s\nwant:\n%s", raw[0], want)
	}
	shortLine(t, raw[0])

	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("brief: %v, %v", info, err)
	}
	body, _ := os.ReadFile(path)
	for _, want := range []string{"----- BEGIN INSTRUCTION FROM TIM", "----- BEGIN DISCORD CONTEXT", "QOL list"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the brief lacks %q", want)
		}
	}
}

func TestEverySplitSessionHasABriefOfItsOwn(t *testing.T) {
	f := newFixture(t)
	f.h.Split = splitsInto(abc, nil, nil)
	f.h.Handle(f.mention(channel, 1, tim, "research a, b and c separately", nil))

	for i := range abc {
		thread := chanID(i)
		raw := f.herdr.raw["mochi-"+thread]
		if len(raw) != 1 || !strings.Contains(raw[0], filepath.Join(f.cfg.SessionDir(thread), "brief.md")) {
			t.Errorf("session %d pasted %q", i+1, raw)
			continue
		}
		shortLine(t, raw[0])
		body, _ := os.ReadFile(filepath.Join(f.cfg.SessionDir(thread), "brief.md"))
		if !strings.Contains(string(body), "TASK "+string(rune('1'+i))+" of 3") {
			t.Errorf("session %d's brief is not its own", i+1)
		}
	}
}

// A follow-up is pasted whole, fenced, with no file to read and no command in front.
func TestAFollowUpIsPastedWhole(t *testing.T) {
	f := newFixture(t)
	first := f.mention(channel, 1, tim, "start", nil)
	f.h.Handle(first)
	f.api.Post(first.ID, 2, maker, strings.Repeat("a long message ", 500))
	follow := f.mention(first.ID, 3, tim, "also b)", nil)

	f.h.Handle(follow)

	raw := f.herdr.raw["mochi-"+first.ID]
	if len(raw) != 2 || !strings.HasPrefix(raw[1], event.FollowedPrefix+" (") || !event.FromMochi(raw[1]) {
		t.Fatalf("pasted %.200q", raw)
	}
	if !strings.Contains(between(t, raw[1], "INSTRUCTION FROM TIM"), "also b)") || !strings.Contains(between(t, raw[1], "DISCORD CONTEXT"), "a long message") {
		t.Errorf("the follow-up is not all there:\n%.400s", raw[1])
	}
	if matches, _ := filepath.Glob(filepath.Join(f.cfg.SessionDir(first.ID), "relay-*")); len(matches) != 0 {
		t.Errorf("a relay file was written: %v", matches)
	}
}

func TestARestartedSessionGetsAFreshBrief(t *testing.T) {
	f := newFixture(t)
	first := f.mention(channel, 1, tim, "start", nil)
	f.h.Handle(first)
	delete(f.herdr.agents, "mochi-"+first.ID)
	f.h.Handle(f.mention(first.ID, 2, tim, "again", nil))

	raw := f.herdr.raw["mochi-"+first.ID]
	if len(raw) != 2 || !strings.HasPrefix(raw[1], "/lead ") {
		t.Fatalf("pasted %q", raw)
	}
	shortLine(t, raw[1])
	body, _ := os.ReadFile(filepath.Join(f.cfg.SessionDir(first.ID), "brief.md"))
	if !strings.Contains(string(body), "has gone, so this is a new one") {
		t.Error("the brief is not the restart's")
	}
}
