package gather

import (
	"strings"
	"testing"

	"github.com/timche/mac-mini/mochi/internal/chat/chattest"
)

func TestOnlyTheMostRecentLaterRepliesAreTheFocus(t *testing.T) {
	f := server()
	target := f.Post(channel, 1, maker, "QOL list")
	for n := 2; n <= 81; n++ {
		reply(f.Post(channel, n, other, "+1 "+chattest.ID(n)), target)
	}
	mention := reply(f.Post(channel, 82, tim, "<@"+mochi.ID+"> research"), target)

	set, err := Reply(f, mention)
	if err != nil {
		t.Fatal(err)
	}
	focus, surrounding := sections(t, Render(set, nil))

	if got := strings.Count(focus, "+1 "); got != FocusReplies {
		t.Errorf("%d later replies in the focus", got)
	}
	if !strings.Contains(focus, "+1 "+chattest.ID(81)) || strings.Contains(focus, "+1 "+chattest.ID(31)) {
		t.Error("the focus is not the most recent replies")
	}
	if strings.Count(surrounding, "+1 ") != 30 {
		t.Errorf("%d older replies kept as surrounding, want 30", strings.Count(surrounding, "+1 "))
	}
}
