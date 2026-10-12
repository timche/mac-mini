// Package discord is the only way anything hachiko has to say reaches Tim, and the only
// thing that ever holds the bot token. Everything goes through the bot, which posts and
// reads a thread back both — the second half being what makes answering an alert from
// Discord possible.
//
// The secret is resolved by re-entering this binary under `op run`, which is the whole of
// why that happens at all: it arrives in one child's environment and leaves it in one
// header, never in an argument ps would show to the machine and never written anywhere.
package discord

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/logs"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// A sweep that never returns is one holding the lock that keeps the next twelve from
// running, and this one resolves a reference over the network before it posts anything.
const opTimeout = 90 * time.Second

// Discord takes 2,000 characters. What is over that is detail, and the on-call tab
// has all of it.
const (
	Limit        = 2000
	messageLimit = 1900
	messageKeep  = 1860
)

// There is nowhere else for a message to go, so a channel nobody has configured is a send
// that failed rather than one that quietly did nothing.
const noChannel = "no Discord channel is configured in ~/.config/hachiko/discord, so there is nowhere to send this"

// Where a message goes beyond the channel: into the thread an incident already has, or
// into one this message is about to open. Both are ids and names rather than secrets, so
// they travel as arguments; the message itself does not, since it carries paths and
// command lines chosen by whatever filled the disk.
type Outgoing struct {
	Text       string
	Thread     string
	OpenThread string

	// Whether this is the message that says the incident is over, which on a forum is the
	// one that retags the post and closes it. A word rather than anything of the incident's,
	// so it travels as an argument with the rest.
	Outcome bool
}

// The two tags a forum of Tim's is set up with, matched by name on the channel Discord
// describes rather than by an id written down here. A forum missing one of them is a forum
// that simply does not get that label: the alert is the point, and Tim owns the forum.
const (
	tagOpen     = "Open"
	tagResolved = "Resolved"
)

func EnvFiles(cfg config.Config) []string {
	return []string{"--env-file", cfg.EnvFile}
}

func LookOp() (string, error) {
	op, err := exec.LookPath("op")
	if err != nil {
		return "", errors.New("no op on PATH, so no secret of hachiko's can be resolved")
	}
	return op, nil
}

// Replacing this process rather than starting another: what comes back is a long-running
// listener, and a parent whose only job was to wait for it would be a second process in
// every listing and a second thing for launchd to lose track of.
func ExecOP(op string, args []string) error {
	return syscall.Exec(op, args, os.Environ())
}

// The one step that reaches the bot token, and the only reason `op run` is ever called:
// every run counts against the service account's daily limit, so it happens when there is
// something to send and not before. The thread the child opened comes back on its stdout,
// which is the one thing it prints.
func SendThroughOP(cfg config.Config, out Outgoing) (string, error) {
	if cfg.Discord.ChannelID == "" {
		return "", errors.New(noChannel)
	}

	op, err := LookOp()
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(cfg.EnvFile); err != nil {
		return "", fmt.Errorf("%s is missing, so the alert was not sent", cfg.EnvFile)
	}

	// The binary rather than the wrapper: the wrapper's job is to decide which
	// binary runs, and this one is already running.
	self, err := os.Executable()
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()

	args := opArgs(cfg, self, out)
	cmd := exec.CommandContext(ctx, op, args[1:]...)
	cmd.Stdin = strings.NewReader(out.Text)

	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", errors.New(detail)
	}

	// A send that worked but had something to say — a thread it could not open — is a line
	// the child wrote and the log would otherwise never see, since only a failure reads this
	// buffer.
	if note := strings.TrimSpace(stderr.String()); note != "" {
		fmt.Fprintln(os.Stderr, note)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// The reference and never the value: what op resolves arrives in the child's
// environment, and nothing about the token is in a command line ps shows to every process
// on the machine.
func opArgs(cfg config.Config, self string, out Outgoing) []string {
	args := append([]string{"op", "run"}, EnvFiles(cfg)...)
	args = append(args, "--", self, "--send", "--channel", cfg.Discord.ChannelID)

	if out.Thread != "" {
		args = append(args, "--thread", out.Thread)
	}
	if out.OpenThread != "" {
		args = append(args, "--open-thread", out.OpenThread)
	}
	if out.Outcome {
		args = append(args, "--outcome")
	}
	return args
}

// The far end of that re-exec, reached only under `op run`.
func SendMode(stdin io.Reader, out Outgoing, channel string) (string, error) {
	message, err := io.ReadAll(stdin)
	if err != nil {
		return "", err
	}
	if len(bytes.TrimSpace(message)) == 0 {
		return "", errors.New("nothing to send")
	}
	out.Text = string(message)

	if channel == "" {
		return "", errors.New(noChannel)
	}

	// Trimmed, because a 1Password field holding a trailing newline is a header value
	// net/http rejects outright, and an error message carrying a form of it this would not
	// recognise.
	token := strings.TrimSpace(os.Getenv("HACHIKO_DISCORD_BOT_TOKEN"))
	if token == "" {
		return "", errors.New("HACHIKO_DISCORD_BOT_TOKEN is empty, so the op:// reference did not resolve")
	}
	return sendThroughBot(NewBot(token), channel, out)
}

// One incident, one thread: the first message opens it and every message after it goes
// inside, so a reminder three hours later is under the alert it is about rather than
// somewhere further down a channel. On a forum that thread is a post and the message that
// opens it is the post's own, which is one call rather than two; on a text channel the
// message is posted and the thread is started from it. A thread that could not be opened is
// not a message that failed — the message is already posted, and the next one goes to the
// channel.
func sendThroughBot(bot Bot, channel string, out Outgoing) (string, error) {
	if out.Thread != "" {
		if _, err := bot.Post(out.Thread, out.Text); err != nil {
			return "", err
		}
		if out.Outcome {
			closePost(bot, out.Thread)
		}
		return "", nil
	}

	// Which kind of channel this is, asked of Discord rather than taken from the config,
	// because a forum refuses a plain message and a text channel has no posts to open: the
	// one thing Tim would have to get right in a file is the one thing Discord will answer
	// for. A type that could not be read is treated as a text channel, and a forum that then
	// refuses the message is a send that failed, which the next check tries again.
	if forum, err := bot.Channel(channel); err == nil && forum.Forum() {
		return bot.OpenPost(channel, ThreadName(out.Text), out.Text, openTags(forum))
	}

	posted, err := bot.Post(channel, out.Text)
	if err != nil {
		return "", err
	}
	if out.OpenThread == "" {
		return "", nil
	}

	// A thread that could not be opened is not a message that failed: the message is posted,
	// and saying otherwise would send it a second time on the next check. The rest of this
	// incident goes to the channel instead, which is a thread missing rather than an alert
	// missing.
	// Named by the message's lead, so a thread in the sidebar says what fired rather than
	// giving an id back to the one person who was never going to type it. The id is in the
	// message's own subtext line, and the thread is found by the id Discord answers with
	// rather than by its name.
	thread, err := bot.OpenThread(channel, posted, ThreadName(out.Text))
	if err != nil {
		logs.Logger{Out: os.Stderr, Now: config.ClockFromEnv()}.Say(
			"the message was posted but no thread could be opened on it, so the rest of this incident goes to the channel: %v", err)
		return "", nil
	}
	return thread, nil
}

func openTags(forum Channel) []string {
	if open := forum.Tag(tagOpen); open != "" {
		return []string{open}
	}
	return nil
}

// The post retagged and archived once the outcome is in it, so the forum reads as a list of
// what is still running rather than everything that ever did. A forum post's own channel
// object is where its tags are, and the forum it hangs under is where the names they mean
// are, which is why both are asked for.
//
// Reported and never returned: the message is already posted, and an outcome that failed on
// its label would be `hachiko notify` telling the session its report did not go out. A text
// channel's thread falls out here on its own, having no forum above it.
func closePost(bot Bot, post string) {
	thread, err := bot.Channel(post)
	if err != nil {
		say("the outcome was posted but the post's tags could not be read, so it stays open: %v", err)
		return
	}

	if thread.ParentID == "" {
		return
	}

	forum, err := bot.Channel(thread.ParentID)
	if err != nil {
		say("the outcome was posted but the forum's tags could not be read, so the post stays open: %v", err)
		return
	}
	if !forum.Forum() {
		return
	}

	tags := []string{}
	for _, tag := range thread.AppliedTags {
		if tag != forum.Tag(tagOpen) {
			tags = append(tags, tag)
		}
	}
	if resolved := forum.Tag(tagResolved); resolved != "" && !statedir.Contains(tags, resolved) {
		tags = append(tags, resolved)
	}

	if err := bot.ClosePost(post, tags); err != nil {
		say("the outcome was posted but the post could not be closed: %v", err)
	}
}

// stderr, because stdout is where the id of the post goes.
func say(format string, args ...any) {
	logs.Logger{Out: os.Stderr, Now: config.ClockFromEnv()}.Say(format, args...)
}

// Discord caps a thread's name at 100 characters.
//
// A title is plain text and Discord renders no markdown in one, so the escaping the lead
// line carries — which is what keeps a file called `**x**` from styling a message — would be
// a backslash here. It comes back out before the length is cut, since cutting first could
// leave a backslash with nothing left after it to escape.
func ThreadName(message string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(message), "\n")
	name := wording.Safe(wording.PlainTitle(first), 96)
	if strings.TrimSpace(name) == "" {
		return "hachiko"
	}
	return name
}

// Discord resolves @everyone, @here and a role mention in a message body unless the body
// says otherwise, and every alert here quotes paths, command lines and log lines chosen by
// whatever filled the disk — so a worker writing "@everyone" into the log it is flooding
// would page the whole server from inside hachiko's own alert. `parse` with nothing in it
// allows no mention of any kind.
//
// Set here rather than at each call site, so nothing that posts can forget it.
func noMentions() map[string]any { return map[string]any{"parse": []string{}} }

const truncatedTail = "[truncated — the rest is in the on-call tab in herdr]"

// Discord takes 2,000 characters, and a message here is built of whole lines — so the cut
// falls on a line boundary. A cut in the middle of a line leaves its inline code span open,
// and Discord then renders everything after it as code, the tail saying it was truncated
// included; it also loses the `-#` subtext without saying so. Cutting whole lines off the
// end drops the subtext first, which is the one line Tim never acts on, and leaves every
// line that is left exactly as it was written.
//
// A single line too long to keep whole — a session's own report rather than anything hachiko
// writes — is cut and its backticks taken out, which is the one way to end a partial line
// with nothing left open.
func capMessage(message string) string {
	message = strings.TrimRight(message, "\n")
	if len(message) <= messageLimit {
		return message
	}

	var kept []string
	room := messageKeep

	for _, line := range strings.Split(message, "\n") {
		if len(line)+1 > room {
			if len(kept) == 0 {
				// ToValidUTF8 because a byte count cuts a path's multi-byte character in
				// half as readily as it cuts anything else.
				kept = append(kept, strings.ToValidUTF8(strings.ReplaceAll(line[:room], "`", ""), ""))
			}
			break
		}
		kept = append(kept, line)
		room -= len(line) + 1
	}

	return strings.TrimRight(strings.Join(kept, "\n"), "\n") + "\n" + truncatedTail
}

func redact(text, token string) string { return logs.Redact(text, token, "the bot token") }
