package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Discord takes 2,000 characters. What is over that is detail, and the on-call tab
// has all of it.
const (
	discordLimit = 2000
	messageLimit = 1900
	messageKeep  = 1860
)

// Where a message goes beyond the channel: into the thread an incident already has, or
// into one this message is about to open. Both are ids and names rather than secrets, so
// they travel as arguments; the message itself does not, since it carries paths and
// command lines chosen by whatever filled the disk.
type Outgoing struct {
	Text       string
	Thread     string
	OpenThread string
}

// The one step that sees the webhook URL or the bot token, and the only reason `op run`
// is ever called: every run counts against the service account's daily limit, so it
// happens when there is something to send and not before.
//
// The secret arrives in this process's environment and leaves it in a request body or one
// header. Nothing puts it in an argument, where ps would show it to every process on the
// machine, and nothing writes it anywhere. The thread the child opened comes back on its
// stdout, which is the one thing it prints.
// One --env-file per file, and the second only when the feature that needs it is on: `op
// run` refuses a reference it cannot resolve, and a bot token named before the field exists
// would take every alert down with it.
func envFiles(cfg Config) []string {
	files := []string{"--env-file", cfg.EnvFile}

	if cfg.Discord.On() {
		if _, err := os.Stat(cfg.DiscordEnvFile); err == nil {
			files = append(files, "--env-file", cfg.DiscordEnvFile)
		}
	}
	return files
}

func lookOp() (string, error) {
	op, err := exec.LookPath("op")
	if err != nil {
		return "", errors.New("no op on PATH, so no secret of hachiko's can be resolved")
	}
	return op, nil
}

// Replacing this process rather than starting another: what comes back is a long-running
// listener, and a parent whose only job was to wait for it would be a second process in
// every listing and a second thing for launchd to lose track of.
func execOP(op string, args []string) error {
	return syscall.Exec(op, args, os.Environ())
}

func sendThroughOP(cfg Config, out Outgoing) (string, error) {
	op, err := lookOp()
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
	return strings.TrimSpace(stdout.String()), nil
}

// The reference and never the value: what op resolves arrives in the child's
// environment, and nothing about the webhook or the token is in a command line ps shows
// to every process on the machine.
func opArgs(cfg Config, self string, out Outgoing) []string {
	args := append([]string{"op", "run"}, envFiles(cfg)...)
	args = append(args, "--", self, "--send")

	if cfg.Discord.On() {
		args = append(args, "--channel", cfg.Discord.ChannelID)
	}
	if out.Thread != "" {
		args = append(args, "--thread", out.Thread)
	}
	if out.OpenThread != "" {
		args = append(args, "--open-thread", out.OpenThread)
	}
	return args
}

// The far end of that re-exec, reached only under `op run`. The bot when there is a token
// and a channel to use it on, and the webhook otherwise — which is the whole of how this
// stays off until Tim has configured it, and how it keeps working on a Mac whose Discord
// application somebody deleted.
func sendMode(stdin io.Reader, out Outgoing, channel string) (string, error) {
	message, err := io.ReadAll(stdin)
	if err != nil {
		return "", err
	}
	if len(bytes.TrimSpace(message)) == 0 {
		return "", errors.New("nothing to send")
	}
	out.Text = string(message)

	// Trimmed, because a 1Password field holding a trailing newline is a URL net/http
	// refuses, a header value it rejects outright, and an error message carrying a form of
	// it this would not recognise.
	if token := strings.TrimSpace(os.Getenv("HACHIKO_DISCORD_BOT_TOKEN")); token != "" && channel != "" {
		return sendThroughBot(newBot(token), channel, out)
	}

	webhook := strings.TrimSpace(os.Getenv("HACHIKO_DISCORD_URL"))
	if webhook == "" {
		return "", errors.New("HACHIKO_DISCORD_URL is empty, so the op:// reference did not resolve")
	}
	return "", postDiscord(&http.Client{Timeout: 20 * time.Second}, webhook, out.Text)
}

// One incident, one thread: the first message opens it and every message after it goes
// inside, so a reminder three hours later is under the alert it is about rather than
// somewhere further down a channel. A thread that could not be opened is not a message
// that failed — the message is already posted, and the next one goes to the channel.
func sendThroughBot(bot discordBot, channel string, out Outgoing) (string, error) {
	where := channel
	if out.Thread != "" {
		where = out.Thread
	}

	posted, err := bot.post(where, out.Text)
	if err != nil {
		return "", err
	}
	if out.OpenThread == "" {
		return "", nil
	}

	thread, err := bot.openThread(channel, posted, out.OpenThread)
	if err != nil {
		return "", err
	}
	return thread, nil
}

func postDiscord(client *http.Client, webhook, message string) error {
	body, err := json.Marshal(map[string]string{"content": capMessage(message)})
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, webhook, bytes.NewReader(body))
	if err != nil {
		return errors.New(redact(err.Error(), webhook))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return errors.New(redact(err.Error(), webhook))
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("the webhook answered %d", resp.StatusCode)
	}
	return nil
}

func capMessage(message string) string {
	message = strings.TrimRight(message, "\n")
	if len(message) <= messageLimit {
		return message
	}
	return message[:messageKeep] + "\n[truncated — the rest is in the on-call tab in herdr]"
}

// net/http names the URL it failed on, and an alert that could not be sent is logged
// where everything else is — so every spelling of it a message could carry goes. A
// *url.Error prints the target through %q, which escapes a newline or a byte outside
// ASCII and so spells the same URL differently; net/url hands back the percent-escaped
// forms. Longest first, so a prefix of one does not break the match for another.
func redact(text, webhook string) string {
	webhook = strings.TrimSpace(webhook)
	if webhook == "" {
		return text
	}

	quoted := strconv.Quote(webhook)
	forms := []string{
		webhook,
		quoted[1 : len(quoted)-1],
		url.QueryEscape(webhook),
		url.PathEscape(webhook),
	}
	sort.Slice(forms, func(i, j int) bool { return len(forms[i]) > len(forms[j]) })

	for _, form := range forms {
		if form != "" {
			text = strings.ReplaceAll(text, form, "the webhook")
		}
	}
	return text
}
