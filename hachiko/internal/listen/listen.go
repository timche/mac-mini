// Package listen is `hachiko listen`, which watches the incident threads in Discord for a
// reply from Tim and hands it to the on-call session. What it is for: he wakes up, reads
// one message on his phone, and answers it there. Attaching to herdr from a phone is three
// minutes of work and reading a message is none, so the question the on-call session is
// waiting on is answerable from the thread the alert is in — and answering it stops the
// three-hour clock the same way picking an option in herdr does, because the answer reaches
// the agent and takes it off its question.
//
// It runs as its own LaunchAgent rather than inside the five-minute sweep: a reply is worth
// seconds, not five minutes, and a process that sits in a poll has no business holding the
// lock a sweep needs. It writes nothing the sweep owns.
package listen

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/logs"
	"github.com/timche/mac-mini/hachiko/internal/oncall"
	"github.com/timche/mac-mini/hachiko/internal/self"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
	"github.com/timche/mac-mini/hachiko/internal/totp"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

const (
	listenPoll = 5 * time.Second

	// What the agent costs when the feature is off: one start, one line, and an exit that
	// launchd throttles. Nothing polls and no token is asked for.
	listenIdle = 5 * time.Minute

	// How often the heartbeat the five-minute check reads is written. Not every pass: the
	// poll is five seconds, which would be seventeen thousand writes a day for a reading
	// nothing takes oftener than twice an interval. Half a minute is sync's own number and
	// well inside the ten minutes the check gives it.
	beatInterval = 30 * time.Second
)

// The two shapes a reply can have. A bare number is the option he picked, which is what
// answering from a phone looks like; `approve <code>` is the second factor the never list
// needs, and it is deliberately not a word that could be typed by accident.
var (
	optionReply   = regexp.MustCompile(`\A([1-9][0-9]?)\s*\.?\z`)
	approvalReply = regexp.MustCompile(`(?i)\Aapprove\s+([0-9]{6})\z`)
)

// The outer half: it reads no token itself, it re-enters under `op run` so that the token
// is resolved once per start and lives in one process's environment for as long as that
// process does. One op call per start rather than one per poll, which is what keeps a
// five-second loop off the service account's daily limit.
func Run(cfg config.Config) error {
	now := config.ClockFromEnv()
	log := logs.Logger{Out: os.Stdout, Now: now}
	store := statedir.Store{Dir: cfg.StateDir}

	if !cfg.Discord.On() {
		sayOnce(store, log, "unconfigured",
			"no Discord channel and user are configured, so there is nothing to listen to")
		goIdle(cfg, switchedOff, time.Sleep, now)
		return nil
	}

	// Somebody means replies to work, so from here every way out that is not the poll is a
	// listener which has stopped — and the watch has nothing to measure its ten minutes
	// against until a heartbeat exists. The ones below return before the re-exec, and `op run`
	// itself failing never comes back here at all.
	keepBeat(cfg, now)

	op, err := discord.LookOp()
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if _, err := os.Stat(cfg.DiscordEnvFile); err != nil {
		return fmt.Errorf("%s is missing, so there is no token to listen with", cfg.DiscordEnvFile)
	}

	return discord.ExecOP(op, append(append([]string{"op", "run"}, discord.EnvFiles(cfg)...), "--", self, "--listen-mode"))
}

// The far end of that re-exec. The token is in this process's environment and nowhere
// else; it is never written, never an argument, and taken out of every error that leaves
// here.
func WithToken(cfg config.Config) error {
	now := config.ClockFromEnv()
	log := logs.Logger{Out: os.Stdout, Now: now}

	store := statedir.Store{Dir: cfg.StateDir}

	token := strings.TrimSpace(os.Getenv("HACHIKO_DISCORD_BOT_TOKEN"))
	if token == "" {
		sayOnce(store, log, "no-token",
			"the bot token did not resolve, so replies are off and the webhook is what alerts go to")
		goIdle(cfg, configuredAndBroken, time.Sleep, now)
		return nil
	}

	l := &listener{
		cfg:    cfg,
		store:  store,
		bot:    discord.NewBot(token),
		herdr:  oncall.HerdrCLI,
		now:    now,
		log:    log,
		secret: strings.TrimSpace(os.Getenv("HACHIKO_APPROVAL_TOTP")),
		sleep:  time.Sleep,
	}

	sayOnce(store, log, "listening-"+cfg.Discord.UserID,
		"listening to the incident threads in Discord for a reply from "+cfg.Discord.UserID)
	return l.poll(self.Looking(self.Stat))
}

// The poll, which is the job. It returns only when the wrapper has built a new binary
// behind this process: the agent has no interval of its own, so nothing would otherwise run
// an edit to the listener until the Mac restarted, and a reply read by code from this
// morning is a reply handed to the session by rules that have since changed.
//
// The question is asked between passes and never inside one. A pass that has taken a reply
// off Discord has already marked it seen, so a process that stopped halfway through one
// would drop the reply it was in the middle of handing over and nothing would read it again.
// At five seconds a pass, that costs the new binary one pass of latency.
func (l *listener) poll(binary *self.Watch) error {
	for {
		l.beat()
		l.once()

		if binary.Replaced() {
			return self.ErrReplaced
		}
		l.sleep(listenPoll)
	}
}

// The heartbeat, and the only thing on this Mac that could tell a listener which has stopped
// from one which is answering: it polls every five seconds and reports nothing it manages,
// so what nothing can report about itself is not running at all.
//
// Written while it polls, and once by a configured listener that cannot poll at all — never
// by one nobody configured, which is what makes a missing heartbeat mean the feature is off
// rather than the listener dead: a listener with nothing configured says nothing and polls
// nothing for the life of the Mac, and counting that as a stoppage would alert on every
// machine here that has never turned replies on.
func (l *listener) beat() {
	now := l.now()
	if !l.lastBeat.IsZero() && now.Sub(l.lastBeat) < beatInterval {
		return
	}
	l.lastBeat = now

	writeBeat(l.cfg, now)
}

func writeBeat(cfg config.Config, at time.Time) {
	path := cfg.ListenStamp()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	os.WriteFile(path, []byte(strconv.FormatInt(at.Unix(), 10)+"\n"), 0o644)
}

// The heartbeat a configured listener that cannot run leaves for the watch to find too old,
// and the one case the check could not otherwise catch: a listener configured on a Mac where
// it has never once polled has nothing to go stale, so it reads as the feature being off for
// the life of the Mac.
//
// Only where there is none. launchd starts this agent again every five minutes for as long as
// it keeps exiting, and a heartbeat rewritten on each of those starts never reaches the ten
// minutes the watch gives it — the alert this exists to raise would be the one alert that can
// never fire.
func keepBeat(cfg config.Config, now func() time.Time) {
	if _, err := os.Lstat(cfg.ListenStamp()); err == nil {
		return
	}
	writeBeat(cfg, now())
}

// The two reasons the listener idles instead of polling, which are not the same thing to
// anything watching it.
const (
	// Nothing in the config file: nobody has asked for replies from Discord, and a listener
	// that is not meant to be running is not one that stopped. It takes its heartbeat with it
	// so the check reads the feature as off — and so an alert the check already had open on
	// it has something that clears it, instead of standing for the life of the Mac with
	// nothing left to answer it.
	switchedOff = true

	// A channel and a user are set, so somebody does mean replies to work, and only the token
	// did not resolve — revoked, or a vault that would not answer. That is a listener which
	// has stopped, so the heartbeat is left exactly where it is to go stale and be reported,
	// and written where there is none at all. The message the check sends points at this log,
	// where the line just written says the token is what failed.
	configuredAndBroken = false
)

func goIdle(cfg config.Config, off bool, sleep func(time.Duration), now func() time.Time) {
	if off {
		os.Remove(cfg.ListenStamp())
	} else {
		keepBeat(cfg, now)
	}
	sleep(listenIdle)
}

// launchd restarts this agent every five minutes for as long as nothing is configured, and a
// line on every start is a line every five minutes for the life of the Mac about something
// that is not wrong. So the state it last said is written down, and it says nothing again
// until that changes — which is also how turning the feature on gets a line of its own.
func sayOnce(store statedir.Store, log logs.Logger, state, message string) {
	path := filepath.Join(store.Dir, "listen", "said")

	if was, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(was)) == state {
		return
	}
	log.Say("%s", message)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
		os.WriteFile(path, []byte(state), 0o644)
	}
}

type listener struct {
	cfg   config.Config
	store statedir.Store
	bot   discord.Bot
	herdr oncall.Runner
	now   func() time.Time
	log   interface{ Say(string, ...any) }
	sleep func(d time.Duration)

	// The otpauth URI or base32 as 1Password handed it over, decoded only when a code
	// actually arrives. Never given to the agent, never logged, and never in an argument.
	secret string

	lastBeat time.Time

	// A thread that will not answer — a bot without permission on it, a thread Tim deleted —
	// asked again every five seconds was twelve identical lines a minute in the log, for as
	// long as the incident stayed open. In memory rather than in a file: the process lives as
	// long as the feature is on, and a backoff that survived a restart would be one nothing
	// ever clears.
	trouble map[string]*threadTrouble
}

// How long a thread that failed is left alone, and whether the log has already said so.
type threadTrouble struct {
	fails   int
	nextTry time.Time
	said    bool
}

// Doubling from one poll, to five minutes. Long enough that a permission somebody fixes is
// picked up within the five minutes, short enough that nothing waits an hour for a reply.
const listenBackoffMax = 5 * time.Minute

func (l *listener) troubleWith(thread string) *threadTrouble {
	if l.trouble == nil {
		l.trouble = map[string]*threadTrouble{}
	}
	if l.trouble[thread] == nil {
		l.trouble[thread] = &threadTrouble{}
	}
	return l.trouble[thread]
}

func (l *listener) failed(incident, thread string, err error) {
	t := l.troubleWith(thread)
	t.fails++

	wait := listenPoll << min(t.fails-1, 10)
	if wait > listenBackoffMax {
		wait = listenBackoffMax
	}
	t.nextTry = l.now().Add(wait)

	// Once per state change. A line every five seconds buries the lines that matter, which is
	// the same rule the rest of this log is written to.
	if !t.said {
		t.said = true
		l.log.Say("the thread for %s could not be read, so it is tried again in %s and not said again until it answers: %v",
			incident, wait, err)
	}
}

func (l *listener) answered(incident, thread string) {
	t := l.troubleWith(thread)
	if t.said {
		l.log.Say("the thread for %s is readable again", incident)
	}
	t.fails, t.said, t.nextTry = 0, false, time.Time{}
}

// One pass over the threads the open incidents have. The threads come from the state the
// sweep writes, read without the lock: a sweep holds that lock across a herdr call and an
// `op run`, and a loop that waited for it would be a loop that misses replies for minutes.
func (l *listener) once() {
	state, err := l.store.Load()
	if err != nil && !errors.Is(err, statedir.ErrCorrupt) {
		return
	}

	for _, incident := range statedir.SortedKeys(state.Threads) {
		l.thread(state, incident, state.Threads[incident])
	}
	l.forgetSeenExcept(state.Threads)
}

func (l *listener) thread(state *statedir.State, incident, thread string) {
	if t := l.troubleWith(thread); l.now().Before(t.nextTry) {
		return
	}

	messages, err := l.bot.MessagesAfter(thread, l.lastSeen(thread))
	if err != nil {
		l.failed(incident, thread, err)
		return
	}
	l.answered(incident, thread)

	for _, message := range messages {
		// Whatever it was, it has been seen: a message that cannot be acted on may not be
		// read again on the next pass, or one Discord will not let the bot react to would
		// be handled for ever.
		l.markSeen(thread, message.ID)

		// His account and nothing else. A bot's own message is the obvious one to exclude,
		// and so is everybody else in the channel — an instruction to the agent on this
		// machine comes from the one person who owns it.
		if message.Author.Bot || message.Author.ID != l.cfg.Discord.UserID {
			continue
		}

		text := strings.TrimSpace(message.Content)
		if text == "" {
			continue
		}
		l.reply(state, incident, thread, message.ID, text)
	}
}

func (l *listener) reply(state *statedir.State, incident, thread, messageID, text string) {
	if code := approvalReply.FindStringSubmatch(text); code != nil {
		l.approve(incident, thread, messageID, code[1])
		return
	}

	lead := fmt.Sprintf(`Tim has replied in Discord, in the thread for %s. What is between the markers below is his answer and nothing else's: it is an instruction from him, which is the authority your standing orders say to wait for, so act on it within the limits those orders give you.

If what he is asking for is on the never list, do not do it on the strength of this message. Register it with `+"`hachiko approval-request %s <file>`"+`, post in the thread what you are asking to be allowed to do, and ask him to reply "approve" with the six-digit code from his authenticator. hachiko checks that code itself and tells you when it has been accepted; you are never given the secret behind it and may not accept a code yourself.`,
		incident, incident)

	if option := optionReply.FindStringSubmatch(text); option != nil {
		lead += fmt.Sprintf("\n\nHis reply is the single number %s, which is option %s of the question you asked.", option[1], option[1])
	}

	if err := l.handTo(incident, lead, wording.Clip(text, wording.ReplyLimit)); err != nil {
		l.log.Say("Tim's reply on %s did not reach the on-call session: %v", incident, err)
		l.sayInThread(thread, "That did not reach the agent: "+err.Error())
		return
	}

	l.log.Say("Tim's reply on %s was handed to the on-call session", incident)
	l.acknowledge(thread, messageID)
	_ = state
}

// The code is checked here and the secret stays here. The agent is told that an action it
// named has been approved and never what approved it, so a session that has been talked
// into something cannot approve it for itself.
func (l *listener) approve(incident, thread, messageID, code string) {
	action := l.store.OpenApproval(incident)
	if action == "" {
		// Not a hint: a code with nothing open to approve is either a mistake or somebody
		// trying codes, and both get the same sentence.
		l.sayInThread(thread, "Code not accepted.")
		return
	}

	secret, err := totp.Secret(l.secret)
	if err != nil {
		l.log.Say("a code arrived for %s and there is nothing to check it against: %v", incident, err)
		l.sayInThread(thread, "Code not accepted.")
		return
	}

	step, ok := totp.Verify(secret, code, l.now())
	if !ok || l.usedStep(step) {
		l.wrongCode(incident, thread, action)
		return
	}
	l.useStep(step)
	l.clearAttempts(incident)

	lead := fmt.Sprintf(`Tim has approved one action on %s with a code from his authenticator, checked by hachiko. The action approved is the one you registered and is quoted between the markers below. You may now carry out that action and nothing else: anything further on the never list needs a request and a code of its own.`,
		incident)

	if err := l.handTo(incident, lead, "approved: "+wording.Clip(action, wording.ReplyLimit)); err != nil {
		l.log.Say("the approval on %s did not reach the on-call session: %v", incident, err)
		l.sayInThread(thread, "The code was accepted but did not reach the agent: "+err.Error())
		return
	}

	l.store.CloseApproval(incident)
	l.log.Say("an approved action on %s was handed to the on-call session", incident)
	l.acknowledge(thread, messageID)
}

// Three. A code is six digits over three thirty-second steps, so a reply every few seconds
// from an account somebody has taken over is about one chance in a thousand per try and
// hours rather than years to a hit — which is no protection at all when the thing being
// protected is deleting a repository. So the request itself goes after three wrong codes and
// the session has to register it again, which it cannot do without saying in the thread what
// it is asking for, where Tim can see it.
const approvalTries = 3

func (l *listener) wrongCode(incident, thread, action string) {
	tries := l.recordAttempt(incident, action)

	if tries >= approvalTries {
		l.store.CloseApproval(incident)
		l.clearAttempts(incident)
		l.log.Say("%d codes for %s were not accepted, so the request to be allowed that action is cancelled",
			tries, incident)
		l.sayInThread(thread, "Code not accepted. That was the third try, so the request is cancelled and the agent has to ask again.")
		return
	}

	l.log.Say("a code for %s was not accepted (%d of %d)", incident, tries, approvalTries)
	l.sayInThread(thread, "Code not accepted.")
}

// Counted against the action it is for, so registering a different action starts again: the
// count is there to stop somebody guessing at one approval, not to lock the session out of
// asking for the next thing.
func (l listener) attemptsPath(incident string) string {
	return filepath.Join(l.cfg.StateDir, "listen", "tries-"+incident)
}

func (l listener) recordAttempt(incident, action string) int {
	tries, was := 0, ""
	if file, err := os.ReadFile(l.attemptsPath(incident)); err == nil {
		count, rest, _ := strings.Cut(strings.TrimSpace(string(file)), "\n")
		tries, _ = strconv.Atoi(count)
		was = rest
	}
	if was != action {
		tries = 0
	}
	tries++

	if err := os.MkdirAll(filepath.Dir(l.attemptsPath(incident)), 0o755); err == nil {
		os.WriteFile(l.attemptsPath(incident), []byte(fmt.Sprintf("%d\n%s", tries, action)), 0o644)
	}
	return tries
}

func (l listener) clearAttempts(incident string) { os.Remove(l.attemptsPath(incident)) }

// Straight to the agent, through the same cancel-then-prompt the sweep uses: herdr refuses
// a prompt to an agent on a question, and this is the answer to that question. Nothing
// records this esc against the wait, and nothing needs to: no sweep reads an agent leaving
// `blocked` as an answer any more, whoever took the question away.
func (l *listener) handTo(incident, lead, data string) error {
	kind := statedir.KindOf(incident)

	o := oncall.Oncaller{Cfg: l.cfg, Herdr: l.herdr, Now: l.now, Log: l.log}

	status, err := o.Status(kind)
	if err != nil {
		return err
	}
	switch status {
	case oncall.StatusGone:
		return errors.New("there is no on-call session open on it any more")
	case oncall.StatusBlocked:
		_, err := o.InterruptWith(kind, lead, "REPLY FROM TIM", data)
		return err
	}

	// Working or finished, so there is no question in the way: the prompt queues behind
	// whatever it is doing.
	return o.PromptWith(kind, lead, "REPLY FROM TIM", data)
}

// A tick on his own message, which is the shortest way to say it landed, and a sentence in
// the thread if the bot has no permission to react.
func (l *listener) acknowledge(thread, messageID string) {
	if err := l.bot.React(thread, messageID, "%E2%9C%85"); err != nil {
		l.sayInThread(thread, "Passed to the agent.")
	}
}

func (l *listener) sayInThread(thread, text string) {
	if _, err := l.bot.Post(thread, text); err != nil {
		l.log.Say("nothing could be posted back to the thread: %v", err)
	}
}

// The listener's own bookkeeping, in files of its own: the sweep owns state.json, and a
// five-second loop writing it would be a five-second loop taking its lock.
func (l listener) seenPath(thread string) string {
	return filepath.Join(l.cfg.StateDir, "listen", "seen-"+thread)
}

func (l listener) lastSeen(thread string) string {
	id, err := os.ReadFile(l.seenPath(thread))
	if err != nil {
		return ""
	}
	return config.DiscordID(string(id))
}

// The highest id seen and not the last one handled: `after` means later than this, so a mark
// that went backwards — which is what one message arriving out of order did — asked for the
// same messages again on the next pass and handed every one of them to the agent a second
// time.
func (l listener) markSeen(thread, messageID string) {
	if config.DiscordID(messageID) == "" || discord.Snowflake(messageID) <= discord.Snowflake(l.lastSeen(thread)) {
		return
	}
	if err := os.MkdirAll(filepath.Dir(l.seenPath(thread)), 0o755); err != nil {
		return
	}
	os.WriteFile(l.seenPath(thread), []byte(messageID), 0o644)
}

// A thread nothing is open on any more, so a state directory does not grow a file per
// incident for the life of the Mac.
func (l listener) forgetSeenExcept(threads map[string]string) {
	keep := map[string]bool{}
	for _, thread := range threads {
		keep["seen-"+thread] = true
	}

	entries, err := os.ReadDir(filepath.Join(l.cfg.StateDir, "listen"))
	if err != nil {
		return
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "seen-") && !keep[entry.Name()] {
			os.Remove(filepath.Join(l.cfg.StateDir, "listen", entry.Name()))
		}
	}
}

// A code is good for its thirty seconds and would otherwise be good for all of them, so
// the step it belonged to is remembered and refused the second time. The last few are
// enough: a step older than the window cannot verify again anyway.
const usedStepsKept = 16

func (l listener) usedStepsPath() string {
	return filepath.Join(l.cfg.StateDir, "listen", "used-steps")
}

func (l listener) usedSteps() []string {
	file, err := os.ReadFile(l.usedStepsPath())
	if err != nil {
		return nil
	}
	var steps []string
	for _, line := range strings.Split(string(file), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			steps = append(steps, line)
		}
	}
	return steps
}

func (l listener) usedStep(step int64) bool {
	return statedir.Contains(l.usedSteps(), strconv.FormatInt(step, 10))
}

func (l listener) useStep(step int64) {
	steps := append(l.usedSteps(), strconv.FormatInt(step, 10))
	sort.Strings(steps)
	if len(steps) > usedStepsKept {
		steps = steps[len(steps)-usedStepsKept:]
	}

	if err := os.MkdirAll(filepath.Dir(l.usedStepsPath()), 0o755); err != nil {
		return
	}
	os.WriteFile(l.usedStepsPath(), []byte(strings.Join(steps, "\n")+"\n"), 0o644)
}
