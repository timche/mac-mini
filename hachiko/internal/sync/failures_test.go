package sync

import (
	"strings"
	"testing"
	"time"
)

// A push that will not land is a pass a minute, and sixty notifications an hour is a
// notification Tim turns off — and then the next one is one he never sees.
func TestAFailureThatPersistsIsOneMessage(t *testing.T) {
	f := newFixture(t)
	f.retry.Attempts = 1
	f.tree.unpushed = []string{"deadbee mine"}
	f.tree.oldest = base
	f.tree.pushRefused = 9
	f.tree.pushText = "fatal: Could not read from remote repository"

	for i := 0; i < 4; i++ {
		f.at(int64(i) * 60).pass(pushWhenDue)
	}

	if len(f.sent) != 1 {
		t.Fatalf("%d messages, want 1: %v", len(f.sent), f.sent)
	}
	if !strings.Contains(f.sent[0].Text, "could not be pushed") {
		t.Errorf("message: %s", f.sent[0].Text)
	}
	if f.sent[0].OpenThread == "" {
		t.Error("the first message of a failure opened no thread")
	}
}

// A send that failed is a failure nobody has heard about, so the next pass says it again
// rather than treating the record as a message that went out.
func TestAMessageThatDidNotSendIsLeftToTheNextPass(t *testing.T) {
	f := newFixture(t)
	f.retry.Attempts = 1
	f.tree.unpushed = []string{"deadbee mine"}
	f.tree.oldest = base
	f.tree.pushRefused = 9
	f.tree.pushText = "fatal: Could not read from remote repository"

	f.sendErr = "the webhook answered 500"
	_, log := f.pass(pushWhenDue)

	if !strings.Contains(log, "left to the next pass") {
		t.Errorf("log: %s", log)
	}
	if rec := f.state().Posted[failedPush+" "+f.repo.Path]; rec.Sent {
		t.Error("a send that failed was recorded as one that went out")
	}

	f.sendErr = ""
	f.at(60).pass(pushWhenDue)

	if len(f.sent) != 1 {
		t.Fatalf("%d messages: %v", len(f.sent), f.sent)
	}
}

// One line in the same thread when it stops, so the message that said something was wrong
// is the one that says it is over.
func TestAFailureThatClearsIsSaidInTheSameThread(t *testing.T) {
	f := newFixture(t)
	f.retry.Attempts = 1
	f.tree.unpushed = []string{"deadbee mine"}
	f.tree.oldest = base
	f.tree.pushRefused = 1
	f.tree.pushText = "fatal: Could not read from remote repository"

	f.pass(pushWhenDue)
	thread := f.sent[0].OpenThread
	if thread == "" {
		t.Fatal("no thread was opened")
	}

	f.at(600).pass(pushWhenDue)

	if len(f.sent) != 2 {
		t.Fatalf("%d messages: %v", len(f.sent), f.sent)
	}
	cleared := f.sent[1]
	if cleared.Thread != "thread-1" {
		t.Errorf("the clear went to %q rather than into the thread", cleared.Thread)
	}
	if !strings.Contains(cleared.Text, "pushing again") {
		t.Errorf("message: %s", cleared.Text)
	}
	if !strings.Contains(cleared.Text, "10 minutes") {
		t.Errorf("the clear did not say how long it had been failing: %s", cleared.Text)
	}
	if len(f.state().Posted) != 0 {
		t.Errorf("the record outlived the failure: %+v", f.state().Posted)
	}
}

// Nobody was ever told, so there is nothing to tell them is over.
func TestAFailureNobodyHeardAboutClearsSilently(t *testing.T) {
	f := newFixture(t)
	f.retry.Attempts = 1
	f.tree.unpushed = []string{"deadbee mine"}
	f.tree.oldest = base
	f.tree.pushRefused = 1
	f.tree.pushText = "fatal: Could not read from remote repository"
	f.sendErr = "the webhook answered 500"

	f.pass(pushWhenDue)

	f.sendErr = ""
	f.at(600).pass(pushWhenDue)

	if len(f.sent) != 0 {
		t.Errorf("it said something was over that it never said was wrong: %v", f.sent)
	}
	if len(f.state().Posted) != 0 {
		t.Errorf("posted: %+v", f.state().Posted)
	}
}

// Each kind is its own message, because each one is a different thing for Tim to do.
func TestTheThreeKindsAreThreeMessages(t *testing.T) {
	for _, c := range []struct {
		kind, says, can string
	}{
		{failedPush, "could not be pushed", "git push"},
		{failedConflict, "rebase conflicted", "Pulling there is paused"},
		{failedCommit, "could not be committed", "git status"},
	} {
		f := Failure{
			Kind:        c.kind,
			Subject:     "/Users/x/.mac-mini",
			Remote:      "origin",
			Detail:      "CONFLICT (content): Merge conflict in README.md",
			Unpushed:    []string{"deadbee Update a.md"},
			Uncommitted: []string{" M b.md"},
		}

		message := f.message("mac-mini", base, base.Add(90*time.Minute))

		if !strings.Contains(message, c.says) {
			t.Errorf("%s: %s", c.kind, message)
		}
		if !strings.Contains(message, c.can) {
			t.Errorf("%s does not say what Tim can do: %s", c.kind, message)
		}
		if !strings.Contains(message, "1 hour 30 minutes") {
			t.Errorf("%s does not say how long: %s", c.kind, message)
		}
		if !strings.Contains(message, "mac-mini") {
			t.Errorf("%s names no machine: %s", c.kind, message)
		}
	}
}

// A path was chosen by whatever wrote in it, so none of these messages puts one in a command
// Tim is told to paste: a semicolon in a path is a second command in a line he ran.
func TestNoMessageOffersACommandWithAPathInIt(t *testing.T) {
	for _, kind := range []string{failedPush, failedConflict, failedCommit} {
		f := Failure{Kind: kind, Subject: "/tmp/x; rm -rf /", Remote: "origin", Detail: "no"}

		if action := f.action(); strings.Contains(action, "/tmp/x") {
			t.Errorf("%s: %s", kind, action)
		}
	}
}

// The listing is a glance at what is stuck, and the whole of it is in the repository.
func TestTheListingsAreCapped(t *testing.T) {
	var many []string
	for i := 0; i < 25; i++ {
		many = append(many, "deadbee one")
	}

	if got := listed(many); !strings.Contains(got, "and 15 more") {
		t.Errorf("got %s", got)
	}
	if listed(nil) != "" {
		t.Error("an empty listing is a field with nothing in it, so it is left out")
	}
}

// git's own output goes in an inline code span, and a newline ends one whatever the fence is.
func TestGitsOutputIsOneLineInTheMessage(t *testing.T) {
	f := Failure{Kind: failedPush, Subject: "/tmp/x", Remote: "origin",
		Detail: "hint: Updates were rejected\nhint: because the remote has work"}

	message := f.message("mac-mini", base, base)
	for _, line := range strings.Split(message, "\n") {
		if strings.Count(line, "`")%2 != 0 {
			t.Fatalf("a line leaves a code span open: %q", line)
		}
	}
}

// Every message there is, end to end, for reading rather than for parsing.
func TestMessages(t *testing.T) {
	f := Failure{
		Kind:        failedConflict,
		Subject:     "/Users/timche/.mac-mini",
		Remote:      "origin",
		Detail:      "CONFLICT (content): Merge conflict in README.md\nerror: could not apply 4fb3f17",
		Unpushed:    []string{"4fb3f17 Update README.md", "fb68178 Update test/assert.sh"},
		Uncommitted: []string{" M README.md"},
	}

	t.Log("\n" + f.message("mac-mini", base, base.Add(2*time.Hour)))

	for _, kind := range []string{failedPush, failedCommit, failedSetup} {
		f.Kind = kind
		t.Log("\n" + f.message("mac-mini", base, base.Add(5*time.Minute)))
	}
}
