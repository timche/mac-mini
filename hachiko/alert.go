package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Discord takes 2,000 characters. What is over that is detail, and the on-call tab
// has all of it.
const (
	discordLimit = 2000
	messageLimit = 1900
	messageKeep  = 1860
)

// The one step that sees the webhook URL, and the only reason `op run` is ever
// called: every run counts against the service account's daily limit, so it happens
// when there is something to send and not before.
//
// The URL arrives in this process's environment and leaves it in a request body.
// Nothing puts it in an argument, where ps would show it to every process on the
// machine, and nothing writes it anywhere.
func sendThroughOP(cfg Config, message string) error {
	op, err := exec.LookPath("op")
	if err != nil {
		return errors.New("no op on PATH, so the alert was not sent")
	}

	if _, err := os.Stat(cfg.EnvFile); err != nil {
		return fmt.Errorf("%s is missing, so the alert was not sent", cfg.EnvFile)
	}

	// The binary rather than the wrapper: the wrapper's job is to decide which
	// binary runs, and this one is already running.
	self, err := os.Executable()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()

	args := opArgs(cfg, self)
	cmd := exec.CommandContext(ctx, op, args[1:]...)
	cmd.Stdin = strings.NewReader(message)

	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			detail = err.Error()
		}
		return errors.New(detail)
	}
	return nil
}

// The reference and never the value: what op resolves arrives in the child's
// environment, and nothing about the webhook is in a command line ps shows to every
// process on the machine.
func opArgs(cfg Config, self string) []string {
	return []string{"op", "run", "--env-file", cfg.EnvFile, "--", self, "--send"}
}

// The far end of that re-exec, reached only under `op run`.
func sendMode(stdin io.Reader) error {
	url := os.Getenv("HACHIKO_DISCORD_URL")
	if url == "" {
		return errors.New("HACHIKO_DISCORD_URL is empty, so the op:// reference did not resolve")
	}

	message, err := io.ReadAll(stdin)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(message)) == 0 {
		return errors.New("nothing to send")
	}

	return postDiscord(&http.Client{Timeout: 20 * time.Second}, url, string(message))
}

func postDiscord(client *http.Client, url, message string) error {
	body, err := json.Marshal(map[string]string{"content": capMessage(message)})
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return errors.New(redact(err.Error(), url))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return errors.New(redact(err.Error(), url))
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
// where everything else is.
func redact(text, url string) string {
	if url == "" {
		return text
	}
	return strings.ReplaceAll(text, url, "the webhook")
}
