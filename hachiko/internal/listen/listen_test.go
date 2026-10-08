package listen

import (
	"bytes"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/harness"
	"github.com/timche/mac-mini/hachiko/internal/logs"
	"github.com/timche/mac-mini/hachiko/internal/self"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
	"github.com/timche/mac-mini/hachiko/internal/totp"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

var base = time.Unix(1700000000, 0)

// The listener against the shapes Discord and herdr both answer in: a thread with messages
// in it, and an on-call agent on its question. Nothing here reaches either.
type listenFixture struct {
	t     *testing.T
	l     *listener
	herdr *harness.Herdr
	log   *bytes.Buffer
	now   time.Time

	// What the bot was asked to do, and what it answers: the messages in the thread, and
	// everything posted or reacted to on the way back.
	messages []discord.Message
	posted   []string
	reacted  []string
	reactErr bool

	// A thread the bot cannot read — no permission on it, or Tim deleted it — and how many
	// times it was actually asked, which is what a backoff is.
	readErr int
	reads   int
}

const (
	timID        = "987654321098765432"
	channelID    = "1234567890123456789"
	threadID     = "222222222222222222"
	diskIncident = "disk-1700000300"
)

// The RFC's own secret, so a code in a test is one the vectors already cover.
var approvalSecret = base32.StdEncoding.WithPadding(base32.NoPadding).
	EncodeToString([]byte("12345678901234567890"))

func newListener(t *testing.T) *listenFixture {
	t.Helper()

	home := t.TempDir()
	cfg := config.Config{
		Home:       home,
		MachineDir: filepath.Join(home, ".mac-mini"),
		StateDir:   filepath.Join(home, "state"),
		Discord:    config.DiscordConfig{ChannelID: channelID, UserID: timID},
	}

	f := &listenFixture{
		t:     t,
		herdr: &harness.Herdr{HasAgent: true, HasWorkspace: true, BlockedPrompt: true},
		log:   &bytes.Buffer{},
		now:   time.Unix(1111111111, 0),
	}

	bot := botAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			f.reads++
			if f.readErr != 0 {
				w.WriteHeader(f.readErr)
				return
			}
			// Newest first, which is what Discord answers with and the opposite of the order a
			// conversation has to be read in.
			newestFirst := make([]discord.Message, 0, len(f.messages))
			for i := len(f.messages) - 1; i >= 0; i-- {
				newestFirst = append(newestFirst, f.messages[i])
			}
			body, _ := json.Marshal(newestFirst)
			w.Write(body)
		case strings.Contains(r.URL.Path, "/reactions/"):
			if f.reactErr {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			f.reacted = append(f.reacted, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		default:
			var sent struct{ Content string }
			json.NewDecoder(r.Body).Decode(&sent)
			f.posted = append(f.posted, sent.Content)
			w.Write([]byte(`{"id":"posted-1"}`))
		}
	})

	now := func() time.Time { return f.now }
	f.l = &listener{
		cfg:    cfg,
		store:  statedir.Store{Dir: cfg.StateDir},
		bot:    bot,
		herdr:  f.herdr.Run,
		now:    now,
		log:    logs.Logger{Out: f.log, Now: now},
		secret: approvalSecret,
		// Taken out, the way the bot's own is: a test of the poll is about what it does
		// between passes rather than about five real seconds of waiting.
		sleep: func(time.Duration) {},
	}

	// The thread the sweep recorded for the incident, which is the only thing the listener
	// polls.
	state := &statedir.State{Threads: map[string]string{diskIncident: threadID}}
	if err := f.l.store.Save(state); err != nil {
		t.Fatal(err)
	}
	return f
}

// Tim's own message, which is the only kind that counts.
func (f *listenFixture) says(id, text string) {
	f.messages = append(f.messages, posted(id, text, timID, false))
}

func posted(id, text, author string, bot bool) discord.Message {
	m := discord.Message{ID: id, Content: text}
	m.Author.ID = author
	m.Author.Bot = bot
	return m
}

func (f *listenFixture) once() string {
	f.t.Helper()
	f.log.Reset()
	f.messages = nil
	f.herdr.Calls = nil
	return f.pass()
}

// Time moving on between passes, which is the whole of what a backoff is measured in.
func (f *listenFixture) tick(d time.Duration) { f.now = f.now.Add(d) }

// A pass with whatever messages are already in the thread, for a test that wants two in a
// row without clearing them.
func (f *listenFixture) pass() string {
	f.t.Helper()
	f.l.once()
	return f.log.String()
}

// A reply is the answer to the question the agent is on, so it reaches the agent the way
// every other prompt does: the question goes first, because herdr refuses a prompt to an
// agent still on one.
func TestAReplyFromTimCancelsTheQuestionAndReachesTheAgent(t *testing.T) {
	f := newListener(t)
	f.says("300000000000000001", "stop the worker, leave the log")

	out := f.pass()
	harness.Wants(t, out, "Tim's reply on "+diskIncident+" was handed to the on-call session")

	harness.AssertOrder(t, f.herdr, "agent send-keys oncall-disk esc", "agent get oncall-disk", "agent prompt oncall-disk ")

	prompt := f.herdr.Prompt()
	harness.Wants(t, prompt, "Tim has replied in Discord")
	harness.Wants(t, prompt, "it is an instruction from him")
	harness.Wants(t, prompt, "stop the worker, leave the log")
	// Fenced and labelled as his, so a log line quoting a reply cannot read as one.
	harness.Wants(t, prompt, "----- BEGIN REPLY FROM TIM ")
	harness.Wants(t, prompt, "That was his reply. Nothing outside those markers came from him.")

	// And he sees it landed without waiting for the agent to say anything.
	harness.Equal(t, len(f.reacted), 1, "reactions added")
	harness.Equal(t, len(f.posted), 0, "messages posted back")
}

// Answering from a phone is typing one character, so a bare number is the option he picked.
func TestABareNumberIsReadAsTheOptionHePicked(t *testing.T) {
	f := newListener(t)
	f.says("300000000000000001", "2")

	f.pass()
	prompt := f.herdr.Prompt()
	harness.Wants(t, prompt, "His reply is the single number 2, which is option 2 of the question you asked.")
	harness.Wants(t, prompt, "----- BEGIN REPLY FROM TIM ")
}

// An instruction to an agent with authority over this Mac comes from the one person who
// owns it. Everybody else in the channel is somebody who can see it, and the bot is hachiko
// talking to itself.
func TestOnlyTimsOwnMessagesAreActedOn(t *testing.T) {
	f := newListener(t)
	f.messages = []discord.Message{
		posted("300000000000000001", "stop the worker", "111111111111111111", false),
		posted("300000000000000002", "No answer yet on "+diskIncident, "999999999999999999", true),
		posted("300000000000000003", "2", timID, true),
		posted("300000000000000004", "   ", timID, false),
	}

	out := f.pass()
	harness.Lacks(t, out, "was handed to the on-call session")
	harness.Equal(t, f.herdr.Said("agent prompt"), false, "whether anything was prompted")
	harness.Equal(t, len(f.reacted), 0, "reactions added")

	// Every one of them is still marked as seen, or it would be read again for ever.
	harness.Equal(t, f.l.lastSeen(threadID), "300000000000000004", "the last message read")
}

// A message read once is read once. Without that, a reply Discord will not let the bot react
// to would be handed to the agent on every pass for as long as the incident is open.
func TestEveryMessageIsHandledOnce(t *testing.T) {
	f := newListener(t)
	f.says("300000000000000001", "stop the worker")
	f.pass()
	harness.Equal(t, f.l.lastSeen(threadID), "300000000000000001", "the last message read")

	f.herdr.Calls = nil
	f.messages = nil
	f.pass()
	harness.Equal(t, f.herdr.Said("agent prompt"), false, "whether the same reply was handed over twice")
}

// A thread nothing is open on any more, so the state directory does not grow a file per
// incident for the life of the Mac.
func TestAThreadNothingIsOpenOnIsForgotten(t *testing.T) {
	f := newListener(t)
	f.says("300000000000000001", "stop the worker")
	f.pass()

	if err := f.l.store.Save(&statedir.State{}); err != nil {
		t.Fatal(err)
	}
	f.l.once()
	harness.Equal(t, f.l.lastSeen(threadID), "", "what the listener remembers about a closed thread")
}

// An agent that is not on a question takes a prompt as it is: the reply queues behind
// whatever it is doing rather than being refused.
func TestAReplyToAnAgentThatIsWorkingIsQueuedRatherThanInterrupting(t *testing.T) {
	f := newListener(t)
	f.herdr.BlockedPrompt = false
	f.says("300000000000000001", "stop the worker")

	harness.Wants(t, f.pass(), "was handed to the on-call session")
	harness.Equal(t, f.herdr.Said("agent send-keys"), false, "whether a question was cancelled")
	harness.Wants(t, f.herdr.Prompt(), "Tim has replied in Discord")
}

func TestAReplyWithNoSessionLeftSaysSoInTheThread(t *testing.T) {
	f := newListener(t)
	f.herdr.AgentGone = true
	f.says("300000000000000001", "stop the worker")

	harness.Wants(t, f.pass(), "did not reach the on-call session")
	harness.Equal(t, len(f.posted), 1, "messages posted back")
	harness.Wants(t, f.posted[0], "no on-call session open on it any more")
}

// The second factor the never list needs. The code is checked here and the secret stays
// here: the agent is told that the action it registered is approved and never what approved
// it.
func TestAnApprovalCodeApprovesTheActionTheAgentRegistered(t *testing.T) {
	f := newListener(t)
	if err := f.l.store.RequestApproval(diskIncident, "delete the orphaned postgres volume"); err != nil {
		t.Fatal(err)
	}

	f.says("300000000000000001", "approve "+currentCode(t, f))
	harness.Wants(t, f.pass(), "an approved action on "+diskIncident+" was handed to the on-call session")

	prompt := f.herdr.Prompt()
	harness.Wants(t, prompt, "approved one action on "+diskIncident+" with a code from his authenticator")
	harness.Wants(t, prompt, "approved: delete the orphaned postgres volume")
	harness.Wants(t, prompt, "You may now carry out that action and nothing else")
	harness.Lacks(t, prompt, approvalSecret)
	harness.Lacks(t, prompt, currentCode(t, f))

	// One request, one approval: the next thing it wants approved is a request of its own.
	harness.Equal(t, f.l.store.OpenApproval(diskIncident), "", "the request left open after an approval")
}

// A code is good for thirty seconds and would otherwise be good for all of them. A reply
// nobody can take back is one somebody can replay.
func TestAnApprovalCodeIsAcceptedOnce(t *testing.T) {
	f := newListener(t)
	code := currentCode(t, f)

	for i, action := range []string{"first action", "second action"} {
		if err := f.l.store.RequestApproval(diskIncident, action); err != nil {
			t.Fatal(err)
		}
		f.once()
		f.says(fmt.Sprintf("30000000000000001%d", i), "approve "+code)
		f.pass()
	}

	harness.Equal(t, len(f.posted), 1, "messages posted back")
	harness.Wants(t, f.posted[0], "Code not accepted.")
	harness.Wants(t, f.herdr.Prompt(), "")
	harness.Equal(t, f.l.store.OpenApproval(diskIncident), "second action", "the request left open after a replay")
}

// A code with nothing open to approve approves nothing, and a wrong code gets the same
// sentence as a right one with no request behind it: a hint is a hint to whoever is trying
// codes.
func TestACodeWithNoRequestAndAWrongCodeAreBothRefusedWithoutAHint(t *testing.T) {
	f := newListener(t)

	f.says("300000000000000001", "approve "+currentCode(t, f))
	harness.Wants(t, f.pass(), "")
	harness.Equal(t, len(f.posted), 1, "messages posted back with no request open")
	harness.Equal(t, f.posted[0], "Code not accepted.", "what a code with no request is answered with")
	harness.Equal(t, f.herdr.Said("agent prompt"), false, "whether anything was prompted")

	if err := f.l.store.RequestApproval(diskIncident, "delete the volume"); err != nil {
		t.Fatal(err)
	}
	f.once()
	f.says("300000000000000002", "approve 000000")
	f.pass()

	harness.Equal(t, f.posted[len(f.posted)-1], "Code not accepted.", "what a wrong code is answered with")
	harness.Equal(t, f.herdr.Said("agent prompt"), false, "whether a wrong code prompted anything")
	harness.Equal(t, f.l.store.OpenApproval(diskIncident), "delete the volume", "the request left open")
}

// Nothing to check a code against is not a code accepted.
func TestAnApprovalWithNoSecretConfiguredIsRefused(t *testing.T) {
	f := newListener(t)
	f.l.secret = ""
	if err := f.l.store.RequestApproval(diskIncident, "delete the volume"); err != nil {
		t.Fatal(err)
	}

	f.says("300000000000000001", "approve "+currentCode(t, f))
	harness.Wants(t, f.pass(), "there is nothing to check it against")
	harness.Equal(t, f.posted[len(f.posted)-1], "Code not accepted.", "what a code with no secret is answered with")
	harness.Equal(t, f.herdr.Said("agent prompt"), false, "whether anything was prompted")
}

// A bot without permission to react still has to say the reply landed.
func TestAReplyIsAcknowledgedInTheThreadWhenItCannotBeReactedTo(t *testing.T) {
	f := newListener(t)
	f.reactErr = true
	f.says("300000000000000001", "stop the worker")

	f.pass()
	harness.Equal(t, len(f.posted), 1, "messages posted back")
	harness.Equal(t, f.posted[0], "Passed to the agent.", "what was posted instead of a reaction")
}

// The action an approval is bound to arrives as a file, for the same reason a report does:
// it names paths and commands chosen by whatever filled the disk, and an argument is
// readable by every process on the Mac.
func TestAnApprovalRequestOnlyTakesAnIncidentIdAndIsClipped(t *testing.T) {
	dir := t.TempDir()
	store := statedir.Store{Dir: dir}

	for _, bad := range []string{"../../etc/passwd", "disk", "disk-", "disk-1/x", "", "Disk-1"} {
		if err := store.RequestApproval(bad, "delete the volume"); err == nil {
			t.Errorf("%q was accepted as an incident id", bad)
		}
	}
	if err := store.RequestApproval(diskIncident, "   "); err == nil {
		t.Error("a request with no action was accepted")
	}
	harness.Equal(t, store.OpenApproval("../../etc/passwd"), "", "what a path reads back as")

	if err := store.RequestApproval(diskIncident, strings.Repeat("x", wording.ReplyLimit+50)); err != nil {
		t.Fatal(err)
	}
	if len(store.OpenApproval(diskIncident)) > wording.ReplyLimit+10 {
		t.Error("an action was not clipped")
	}
}

// Six digits over three thirty-second steps is about one chance in a thousand a try, and a
// reply every few seconds from an account somebody has taken over is hours rather than years
// to a hit — which is no protection at all when what is being protected is deleting a
// repository. So the request goes after three, and the session has to ask again in the thread
// where Tim can see it.
func TestThreeWrongCodesCancelTheRequest(t *testing.T) {
	f := newListener(t)
	if err := f.l.store.RequestApproval(diskIncident, "delete the orphaned postgres volume"); err != nil {
		t.Fatal(err)
	}

	for try := 1; try < approvalTries; try++ {
		f.once()
		f.says(fmt.Sprintf("30000000000000010%d", try), "approve 000000")
		out := f.pass()

		harness.Wants(t, out, fmt.Sprintf("not accepted (%d of %d)", try, approvalTries))
		harness.Equal(t, f.posted[len(f.posted)-1], "Code not accepted.", "what a wrong code is answered with")
		harness.Equal(t, f.l.store.OpenApproval(diskIncident), "delete the orphaned postgres volume",
			"the request still open")
	}

	f.once()
	f.says("300000000000000199", "approve 000000")
	out := f.pass()

	harness.Wants(t, out, "3 codes for "+diskIncident+" were not accepted, so the request to be allowed that action is cancelled")
	harness.Wants(t, f.posted[len(f.posted)-1], "That was the third try, so the request is cancelled")
	harness.Equal(t, f.l.store.OpenApproval(diskIncident), "", "the request left open after three wrong codes")

	// And the right code is no good either, because there is nothing left for it to approve.
	f.once()
	f.says("300000000000000200", "approve "+currentCode(t, f))
	f.pass()
	harness.Equal(t, f.herdr.Said("agent prompt"), false, "whether a code approved anything after the request went")
}

// The count is there to stop somebody guessing at one approval, not to lock the session out
// of asking for the next thing: a different action starts again, and so does a good code.
func TestTheWrongCodeCountIsPerActionAndClearedByAGoodOne(t *testing.T) {
	f := newListener(t)

	tryWrong := func(id, action string) string {
		t.Helper()
		if err := f.l.store.RequestApproval(diskIncident, action); err != nil {
			t.Fatal(err)
		}
		f.once()
		f.says(id, "approve 000000")
		return f.pass()
	}

	tryWrong("300000000000000101", "delete the volume")
	harness.Wants(t, tryWrong("300000000000000102", "stop the worker"), fmt.Sprintf("not accepted (1 of %d)", approvalTries))

	if err := f.l.store.RequestApproval(diskIncident, "stop the worker"); err != nil {
		t.Fatal(err)
	}
	f.once()
	f.says("300000000000000103", "approve "+currentCode(t, f))
	harness.Wants(t, f.pass(), "an approved action on "+diskIncident)

	harness.Wants(t, tryWrong("300000000000000104", "stop the worker"), fmt.Sprintf("not accepted (1 of %d)", approvalTries))
}

// A thread that will not answer — a bot without permission on it, a thread Tim deleted —
// asked again every five seconds was twelve identical lines a minute in the log for as long as
// the incident stayed open, which buries the lines that matter.
func TestAThreadThatWillNotAnswerIsBackedOffAndSaidOnce(t *testing.T) {
	f := newListener(t)
	f.readErr = http.StatusForbidden

	harness.Wants(t, f.once(), "could not be read, so it is tried again in")
	harness.Equal(t, f.reads, 1, "reads attempted")

	// Said once, and not asked again until the backoff is up.
	for i := 0; i < 4; i++ {
		harness.Equal(t, f.once(), "", "the log while the thread is backed off")
	}
	harness.Equal(t, f.reads, 1, "reads attempted while backed off")

	// The backoff doubles from one poll, so a little later it is tried again — and still says
	// nothing, because nothing about it has changed.
	f.tick(listenPoll * 2)
	harness.Equal(t, f.once(), "", "the log on the retry")
	harness.Equal(t, f.reads, 2, "reads attempted after the backoff")

	// One line when it comes back, which is the other state change worth one.
	f.readErr = 0
	f.tick(listenPoll * 4)
	harness.Wants(t, f.once(), "is readable again")
	harness.Equal(t, f.reads, 3, "reads attempted once it answered")

	// And nothing more about it after that.
	f.tick(listenPoll)
	harness.Equal(t, f.once(), "", "the log once it is readable again")
}

// launchd restarts the agent every five minutes for as long as nothing is configured, and a
// line on every start is a line every five minutes for the life of the Mac about something
// that is not wrong.
func TestAnUnconfiguredListenerSaysSoOnceAndAgainWhenItChanges(t *testing.T) {
	store := statedir.Store{Dir: filepath.Join(t.TempDir(), "state")}
	out := &bytes.Buffer{}
	log := logs.Logger{Out: out, Now: func() time.Time { return base }}

	for i := 0; i < 5; i++ {
		sayOnce(store, log, "unconfigured", "nothing is configured")
	}
	harness.Equal(t, strings.Count(out.String(), "nothing is configured"), 1,
		"times an unconfigured listener said so")

	// Turning it on is a state change, and that gets a line of its own.
	sayOnce(store, log, "listening-987", "listening for a reply")
	sayOnce(store, log, "listening-987", "listening for a reply")
	harness.Equal(t, strings.Count(out.String(), "listening for a reply"), 1,
		"times it said it had started listening")

	// And so is turning it off again.
	sayOnce(store, log, "unconfigured", "nothing is configured")
	harness.Equal(t, strings.Count(out.String(), "nothing is configured"), 2,
		"times it said so after the configuration changed back")
}

func currentCode(t *testing.T, f *listenFixture) string {
	t.Helper()

	secret, err := totp.Secret(approvalSecret)
	if err != nil {
		t.Fatal(err)
	}
	return totp.At(secret, f.l.now().Unix()/30)
}

// A bot pointed at a server of this test's own, so a listener that polls reaches no
// Discord. The token is a shape rather than a secret, and the sleep is taken out: what a
// test of the listener is about is what it does with the messages.
func botAgainst(t *testing.T, handler http.HandlerFunc) discord.Bot {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return discord.Bot{
		Token:  "MTIzNDU2.NOT-A-REAL-BOT-TOKEN",
		API:    server.URL,
		Client: server.Client(),
		Now:    time.Now,
		Sleep:  func(time.Duration) {},
	}
}

// The listener has no interval of its own, so without this nothing would run an edit to it
// until the Mac restarted. Its agent restarts on any exit, so what matters is where the
// exit falls: a pass that has taken a reply off Discord has already marked it seen, and one
// abandoned halfway through would drop the reply it was in the middle of handing over.
func TestAListenerExitsBetweenPassesForABinaryTheWrapperHasReplaced(t *testing.T) {
	f := newListener(t)
	f.says("300000000000000001", "2")

	passes, looks := 0, 0
	f.l.sleep = func(time.Duration) { passes++ }

	// The build lands while the listener is in a pass, which is where a real one always
	// lands: the wrapper moves it in as soon as its sources have changed.
	err := f.l.poll(self.Looking(func() (self.ID, bool) {
		if looks++; looks > 2 {
			return self.ID{Inode: 2}, true
		}
		return self.ID{Inode: 1}, true
	}))

	harness.Equal(t, errors.Is(err, self.ErrReplaced), true, "why the listener exited")

	// One wait, so two passes: the one that handed the reply over and the one after it that
	// found the binary moved. The reply had already reached the agent when it exited.
	harness.Equal(t, passes, 1, "passes the listener waited between")
	harness.Wants(t, f.log.String(), "was handed to the on-call session")
}

// What the five-minute check reads to tell a listener that has stopped from one that is
// answering, since the listener reports nothing it manages itself.
func TestAPollingListenerWritesAHeartbeatWhereTheCheckReadsIt(t *testing.T) {
	f := newListener(t)

	f.l.beat()

	body, err := os.ReadFile(f.l.cfg.ListenStamp())
	if err != nil {
		t.Fatalf("a polling listener wrote no heartbeat: %v", err)
	}
	harness.Equal(t, strings.TrimSpace(string(body)), "1111111111", "the heartbeat")
}

// The poll is five seconds, and a heartbeat per pass would be seventeen thousand writes a
// day for a reading nothing takes oftener than twice an interval.
func TestTheHeartbeatIsWrittenTwiceAMinuteAndNotTwiceAPass(t *testing.T) {
	f := newListener(t)

	f.l.beat()
	first := beatAt(t, f)

	f.tick(10 * time.Second)
	f.l.beat()
	harness.Equal(t, beatAt(t, f), first, "the heartbeat ten seconds later")

	f.tick(25 * time.Second)
	f.l.beat()
	harness.Equal(t, beatAt(t, f) != first, true, "whether the heartbeat moved after half a minute")
}

// An unconfigured listener idles and exits, which from outside looks exactly like one that
// has hung — so the check reads a missing heartbeat as the feature being off, and a listener
// nobody asked for has to take its own with it.
func TestAListenerNobodyConfiguredTakesItsHeartbeatWithIt(t *testing.T) {
	f := newListener(t)
	f.l.beat()

	goIdle(f.l.cfg, switchedOff, func(time.Duration) {}, f.l.now)

	if _, err := os.Stat(f.l.cfg.ListenStamp()); err == nil {
		t.Error("a listener nobody configured left a heartbeat behind")
	}
}

// The other idle path is not the same thing at all: a channel and a user are set, so
// somebody means replies to work and only the token did not resolve. That is a listener
// which has stopped, and giving up the heartbeat would be the one case the check promises to
// catch going quietly unreported for the life of the Mac.
func TestAListenerWhoseTokenDidNotResolveKeepsItsHeartbeatToGoStale(t *testing.T) {
	f := newListener(t)
	f.l.beat()

	goIdle(f.l.cfg, configuredAndBroken, func(time.Duration) {}, f.l.now)

	if _, err := os.Stat(f.l.cfg.ListenStamp()); err != nil {
		t.Errorf("a listener that is configured and broken gave up its heartbeat: %v", err)
	}
	harness.Equal(t, beatAt(t, f), "1111111111", "the heartbeat left to go stale")
}

// The same idle path on a Mac where the listener has never once polled, which is where the
// check used to go silent for good: a stoppage it cannot measure against anything is a
// stoppage it never reports.
func TestAConfiguredListenerThatNeverRanWritesAHeartbeatToGoStale(t *testing.T) {
	f := newListener(t)

	goIdle(f.l.cfg, configuredAndBroken, func(time.Duration) {}, f.l.now)

	harness.Equal(t, beatAt(t, f), "1111111111", "the heartbeat a listener that never polled left")
}

// The two ways the outer half gives up before the re-exec that would have resolved the token:
// no `op` to resolve it with, and no env file naming the reference. Both of them were a
// listener that wrote nothing at all, and the check reads nothing at all as the feature being
// off.
func TestAConfiguredListenerLeavesAHeartbeatWhenItCannotStartAtAll(t *testing.T) {
	for _, one := range []struct {
		what   string
		opOnly bool
	}{
		{what: "with no op to resolve the token with"},
		{what: "with no env file naming the token", opOnly: true},
	} {
		t.Run(one.what, func(t *testing.T) {
			cfg := configuredListener(t, one.opOnly)

			if err := Run(cfg); err == nil {
				t.Fatal("a listener that cannot start said nothing was wrong")
			}

			body, err := os.ReadFile(cfg.ListenStamp())
			if err != nil {
				t.Fatalf("a configured listener that could not start wrote no heartbeat: %v", err)
			}
			harness.Equal(t, strings.TrimSpace(string(body)), "1700000000", "the heartbeat it left")
		})
	}
}

// launchd starts this agent again every five minutes for as long as it keeps exiting. A
// heartbeat rewritten on each of those starts never reaches the ten minutes the watch gives
// it, so the one alert this exists to raise would be the one that can never fire.
func TestARestartedListenerDoesNotRefreshTheHeartbeatItIsWaitingToGoStale(t *testing.T) {
	cfg := configuredListener(t, false)

	if err := Run(cfg); err == nil {
		t.Fatal("a listener that cannot start said nothing was wrong")
	}

	t.Setenv("HACHIKO_NOW", "1700000600")
	if err := Run(cfg); err == nil {
		t.Fatal("a listener that cannot start said nothing was wrong")
	}

	body, err := os.ReadFile(cfg.ListenStamp())
	if err != nil {
		t.Fatal(err)
	}
	harness.Equal(t, strings.TrimSpace(string(body)), "1700000000", "the heartbeat ten minutes later")
}

// A channel and a user in the config, which is the whole of what turns replies on, and a PATH
// with nothing on it — or with an `op` that is never reached, since the env file it would
// resolve from is missing.
func configuredListener(t *testing.T, withOp bool) config.Config {
	t.Helper()

	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if withOp {
		harness.WriteFile(t, filepath.Join(bin, "op"), "#!/bin/sh\nexit 0\n")
		if err := os.Chmod(filepath.Join(bin, "op"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv("PATH", bin)
	t.Setenv("HACHIKO_NOW", "1700000000")

	return config.Config{
		Home:           home,
		StateDir:       filepath.Join(home, "state"),
		DiscordEnvFile: filepath.Join(home, "missing.env.op"),
		Discord:        config.DiscordConfig{ChannelID: channelID, UserID: timID},
	}
}

func beatAt(t *testing.T, f *listenFixture) string {
	t.Helper()

	body, err := os.ReadFile(f.l.cfg.ListenStamp())
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(body))
}
