package watch

import (
	"fmt"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
	"github.com/timche/mac-mini/hachiko/internal/usage"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

const (
	labelUsed   = "Used"
	labelResets = "Resets"
	labelWasAt  = "Was at"

	// The same time in the two tenses it is read in: ahead of the window starting again, and
	// after it has.
	labelReset = "Reset"
)

// window is one of the account's two usage limits with this check's words on it: the key the
// reading and the state both call it, and the two words a message spells it with.
type usageWindow struct {
	key string

	// What a lead calls the limit — "5-hour" — and what a field calls the span of it.
	short string
	span  string
}

func usageWindows() []usageWindow {
	return []usageWindow{
		{key: usage.FiveHour, short: "5-hour", span: "5-hour window"},
		{key: usage.SevenDay, short: "7-day", span: "7-day window"},
	}
}

// The one thing the watch looks at that is nothing to do with this machine: every Claude Code
// session on this Mac spends the same account's 5-hour and 7-day limits, so what is left of
// them is the ceiling on how much work can be going at once — ahead of the CPU and the disk,
// which is what everybody expects to run out first.
//
// Not an incident and no on-call session, for the reason the liveness check is neither: the
// answer is already written in the message and it is Tim's, which is to hold work back. A
// session opened over it would spend the very thing that is running out.
//
// One warning per window per period, and a 🟢 when that window starts again. The reading
// comes from `claude-hooks statusline`, so it only moves while somebody is running a session
// — which the warning is asking him to stop doing — and that is why what ends a warning is
// the reset time it was sent about passing rather than a fresh reading saying so.
func (s sweeper) usage(state *statedir.State, now time.Time) {
	reading, read := s.deps.UsageReading()

	for _, w := range usageWindows() {
		if said, known := state.Usage[w.key]; known {
			// The window that warning was about is over, whatever the file says now.
			if !time.Unix(said.Resets, 0).After(now) {
				s.sayUsageReset(state, w, said, now)
			}
			// Said once either way: a window the warning was about is still the window it
			// was about, and the one that has just started cannot be nearly spent yet.
			continue
		}

		if !read {
			continue
		}
		if spent, ok := reading.Window(w.key); ok && spent.Spent(s.cfg.UsageWarnPercent, now) {
			s.sayUsageSpent(state, w, spent, now)
		}
	}
}

func (s sweeper) sayUsageSpent(state *statedir.State, w usageWindow, spent usage.Window, now time.Time) {
	left := spent.Resets.Sub(now)

	if s.dry {
		s.say("would report that %s of the %s usage limit is gone, with %s to go",
			wording.Percent(spent.Used), w.short, hmStr(left))
		return
	}
	s.say("%s of the %s usage limit is gone, and it resets in %s",
		wording.Percent(spent.Used), w.short, hmStr(left))

	message := wording.Lead(wording.MarkerDegraded,
		fmt.Sprintf("%s of the account's %s Claude usage is gone", wording.Percent(spent.Used), w.short)).
		Field(labelUsed, fmt.Sprintf("%s of the %s", wording.Percent(spent.Used), w.span)).
		Field(labelResets, fmt.Sprintf("%s, in %s", wording.TimePhrase(spent.Resets, now),
			wording.DurationPhrase(left))).
		Field(wording.LabelWhy, "Every Claude Code session on this Mac spends the same account's "+
			"limits, so what is left is what all of them have between them. A session that runs out "+
			"stops in the middle of whatever it was doing.").
		Can("**Needs you:** hold new work back until it resets — nothing here can raise the limit, "+
			"and the sessions already running will spend what is left.").
		About("", s.cfg.Host).
		String()

	thread, err := s.deps.Send(discord.Outgoing{Text: message, OpenThread: usageThread(w)})
	if err != nil {
		s.say("the message about the %s usage limit did not send and is left to the next check: %v",
			w.short, err)
		return
	}
	state.SetUsage(w.key, statedir.UsageSaid{
		Said:   now.Unix(),
		Used:   spent.Used,
		Resets: spent.Resets.Unix(),
		Thread: thread,
	})
}

func (s sweeper) sayUsageReset(state *statedir.State, w usageWindow, said statedir.UsageSaid, now time.Time) {
	if s.dry {
		s.say("would report that the %s usage limit has reset", w.short)
		return
	}
	s.say("the %s usage limit has reset", w.short)

	message := wording.Lead(wording.MarkerRecovered,
		fmt.Sprintf("The account's %s Claude usage has reset", w.short)).
		Field(labelWasAt, wording.Percent(said.Used)+" of the "+w.span).
		Field(labelReset, wording.TimePhrase(time.Unix(said.Resets, 0), now)).
		About("", s.cfg.Host).
		String()

	if _, err := s.deps.Send(discord.Outgoing{Text: message, Thread: said.Thread, Outcome: true}); err != nil {
		s.say("the message saying the %s usage limit has reset did not send and is left to the next check: %v",
			w.short, err)
		return
	}
	state.ForgetUsage(w.key)
}

// The key the post is opened under, which is a name of hachiko's own and not the window's
// own spelling: `five_hour` is Claude Code's field name and this is a thread about a limit.
func usageThread(w usageWindow) string { return "usage-" + w.key }
