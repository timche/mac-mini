package listen

import (
	"reflect"
	"testing"

	"github.com/timche/mac-mini/mochi/internal/mark"
	"github.com/timche/mac-mini/mochi/internal/split"
)

func (f *fixture) shows(id string, want ...string) {
	f.t.Helper()
	if got := f.api.Showing(id); !reflect.DeepEqual(got, want) {
		f.t.Errorf("%s shows %v, want %v", id, got, want)
	}
}

// 👀 while it is picked up, then ⏳ in its place once the session runs; the thread is named
// with the state in front of its title.
func TestAMentionGoesFromSeenToRunning(t *testing.T) {
	f := newFixture(t)
	m := f.mention(channel, 1, tim, "research", nil)

	f.h.Handle(m)

	f.shows(m.ID, mark.Running)
	if !f.api.ReactedWith(m.ID, mark.Seen) || !reflect.DeepEqual(f.api.Unreactions, []string{m.ID + " " + mark.Seen}) {
		t.Errorf("reactions %v, taken back %v", f.api.Reactions, f.api.Unreactions)
	}
	if f.api.Threads[0] != m.ID+" ⏳ research" {
		t.Errorf("thread = %q", f.api.Threads[0])
	}

	// The session hands Tim a result, and later says it is done.
	f.h.Marks.Settled(m.ID, mark.Asking)
	f.shows(m.ID, mark.Asking)
	if f.api.Channels[m.ID].Name != "❓ research" {
		t.Errorf("thread = %q", f.api.Channels[m.ID].Name)
	}
	f.h.Marks.Settled(m.ID, mark.Finished)
	f.shows(m.ID, mark.Finished)
}

func TestAQuestionShowsAskingUntilItIsAnswered(t *testing.T) {
	f, m, _ := askFixture(t, abc)
	f.shows(m.ID, mark.Asking)

	press(f, tim, buttonsOf(t, f.api.Sent[0].Msg.Components)[1].CustomID)
	f.shows(m.ID, mark.Running)
}

func TestAFailureShowsOnlyACross(t *testing.T) {
	f := newFixture(t)
	f.herdr.startFails = true
	m := f.mention(channel, 1, tim, "research", nil)

	f.h.Handle(m)

	f.shows(m.ID, mark.Failed)
	if f.api.Channels[m.ID].Name != "❌ research" {
		t.Errorf("thread = %q", f.api.Channels[m.ID].Name)
	}
}

// Several sessions: each thread's first post shows its own state, and Tim's mention ✅ only
// once all of them are done.
func TestASplitMentionFinishesWithItsLastSession(t *testing.T) {
	f := newFixture(t)
	f.h.Split = splitsInto(abc, nil, nil)
	m := f.mention(channel, 1, tim, "research a, b and c separately", nil)

	f.h.Handle(m)

	f.shows(m.ID, mark.Running)
	var firsts []string
	for i := range abc {
		thread := chanID(i)
		first := f.record(thread).Origin.Message
		f.shows(first, mark.Running)
		firsts = append(firsts, first)
	}

	f.h.Marks.Settled(chanID(0), mark.Finished)
	f.h.Marks.Settled(chanID(1), mark.Finished)
	f.shows(firsts[0], mark.Finished)
	f.shows(m.ID, mark.Running)
	f.h.Marks.Settled(chanID(2), mark.Finished)
	f.shows(m.ID, mark.Finished)
}

func TestASplitWithAFailedStartIsACross(t *testing.T) {
	f := newFixture(t)
	f.h.Split = answers(split.Answer{Decision: split.Many, Tasks: abc}, nil, nil)
	f.herdr.failFrom = 3
	m := f.mention(channel, 1, tim, "research a, b and c separately", nil)

	f.h.Handle(m)
	f.shows(m.ID, mark.Failed)
}

// A follow-up: ⏳ on it and, again, on the session's first message, until the session posts.
func TestAFollowUpPutsTheSessionBackToRunning(t *testing.T) {
	f := newFixture(t)
	first := f.mention(channel, 1, tim, "start", nil)
	f.h.Handle(first)
	f.h.Marks.Settled(first.ID, mark.Asking)
	f.shows(first.ID, mark.Asking)

	follow := f.mention(first.ID, 2, tim, "merge it", nil)
	f.h.Handle(follow)

	f.shows(follow.ID, mark.Running)
	f.shows(first.ID, mark.Running)

	f.h.Marks.Settled(first.ID, mark.Finished)
	f.shows(follow.ID, mark.Finished)
	f.shows(first.ID, mark.Finished)
}
