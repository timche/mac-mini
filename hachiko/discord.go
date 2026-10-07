package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"time"
)

const discordAPI = "https://discord.com/api/v10"

// Where newBot points. A variable rather than the constant itself so that a test of the step
// that builds its own bot — the one `op run` re-enters, which is handed a token and a channel
// and nothing else — can be driven against a server of its own. Nothing but a test ever
// changes it, and a test that forgets to is a test that posts to Discord.
var discordBase = discordAPI

// The token reaches one process's environment and one request header. Nothing puts it in
// an argument, where ps would show it to every process on the machine, and nothing writes
// it anywhere — and every error text that leaves here has it taken out first, for the
// same reason the webhook URL does.
type discordBot struct {
	token string
	// Where the API is, so a test drives every call against the shapes Discord answers in
	// rather than against a description of them.
	api    string
	client *http.Client
	now    func() time.Time
	sleep  func(time.Duration)
}

func newBot(token string) discordBot {
	return discordBot{
		token:  token,
		api:    discordBase,
		client: &http.Client{Timeout: 20 * time.Second},
		now:    time.Now,
		sleep:  time.Sleep,
	}
}

type discordMessage struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Author  struct {
		ID  string `json:"id"`
		Bot bool   `json:"bot"`
	} `json:"author"`
}

// How many times a rate limit is waited out before the call is given up on. Discord bans
// a bot that ignores a 429 rather than slowing it down, so the wait is the one it was
// asked for — but a listener that waited for ever on one call would stop reading the rest
// of the threads.
const discordRetries = 3

func (b discordBot) call(method, path string, body any) ([]byte, error) {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return nil, err
		}
	}

	for attempt := 0; ; attempt++ {
		req, err := http.NewRequest(method, b.api+path, bytes.NewReader(payload))
		if err != nil {
			return nil, b.scrub(err)
		}
		req.Header.Set("Authorization", "Bot "+b.token)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := b.client.Do(req)
		if err != nil {
			return nil, b.scrub(err)
		}

		answer, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if readErr != nil {
			return nil, b.scrub(readErr)
		}

		switch {
		case resp.StatusCode == http.StatusTooManyRequests && attempt < discordRetries:
			b.sleep(retryAfter(resp, answer))
			continue
		case resp.StatusCode >= 500 && attempt < discordRetries:
			b.sleep(time.Duration(attempt+1) * time.Second)
			continue
		case resp.StatusCode < 200 || resp.StatusCode > 299:
			return nil, fmt.Errorf("Discord answered %d to %s %s", resp.StatusCode, method, path)
		}

		return answer, nil
	}
}

// Discord says how long to wait in the header and in the body both, and the body is the
// one with sub-second resolution. A cap, because a global limit can be minutes and a
// listener asleep that long has stopped listening.
func retryAfter(resp *http.Response, body []byte) time.Duration {
	wait := time.Second

	if header := resp.Header.Get("Retry-After"); header != "" {
		if seconds, err := strconv.ParseFloat(header, 64); err == nil && seconds > 0 {
			wait = time.Duration(seconds * float64(time.Second))
		}
	}

	var limit struct {
		RetryAfter float64 `json:"retry_after"`
	}
	if json.Unmarshal(body, &limit) == nil && limit.RetryAfter > 0 {
		wait = time.Duration(limit.RetryAfter * float64(time.Second))
	}

	if wait > 30*time.Second {
		wait = 30 * time.Second
	}
	return wait
}

// net/http names the URL it failed on, which is no secret here, but an error that came
// back with a header echoed into it would be — and this is logged where everything else
// is.
func (b discordBot) scrub(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(redact(err.Error(), b.token))
}

func (b discordBot) post(channel, content string) (string, error) {
	answer, err := b.call(http.MethodPost, "/channels/"+channel+"/messages",
		map[string]any{"content": capMessage(content), "allowed_mentions": noMentions()})
	if err != nil {
		return "", err
	}

	var message discordMessage
	if err := json.Unmarshal(answer, &message); err != nil {
		return "", errors.New("Discord answered something that is not a message")
	}
	return message.ID, nil
}

// A thread started from the message rather than a standalone one, so the first line of an
// incident is what the thread is titled by and everything after it is underneath. A day's
// archive, which is longer than any incident here has taken and short enough that the
// channel does not fill with open threads.
func (b discordBot) openThread(channel, message, name string) (string, error) {
	answer, err := b.call(http.MethodPost,
		"/channels/"+channel+"/messages/"+message+"/threads",
		map[string]any{"name": safe(name, discordThreadName), "auto_archive_duration": 1440})
	if err != nil {
		return "", err
	}

	var thread struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(answer, &thread); err != nil || thread.ID == "" {
		return "", errors.New("Discord made a thread it did not name")
	}
	return thread.ID, nil
}

// Discord caps a thread name at a hundred characters, and an incident id and a headline
// are longer than that together.
const discordThreadName = 90

// Oldest first, which is the order a conversation has to be read in and the opposite of
// the order Discord returns by default. `after` is a message id and not a time, so a
// listener that was down for an hour reads what it missed rather than guessing at it.
func (b discordBot) messagesAfter(channel, after string) ([]discordMessage, error) {
	path := "/channels/" + channel + "/messages?limit=100"
	if after != "" {
		path += "&after=" + after
	}

	answer, err := b.call(http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var messages []discordMessage
	if err := json.Unmarshal(answer, &messages); err != nil {
		return nil, errors.New("Discord answered something that is not a list of messages")
	}

	// Sorted rather than reversed. Discord documents newest first for a plain fetch and
	// oldest first for an `after`, and which of the two a given call returns is not something
	// to stake the order of a conversation on — a reversal that guessed wrong handed the
	// agent the messages backwards and remembered the oldest as the newest. The id is the
	// clock: a snowflake is a timestamp, so sorting by it numerically is sorting by when.
	sort.Slice(messages, func(i, j int) bool {
		return snowflake(messages[i].ID) < snowflake(messages[j].ID)
	})
	return messages, nil
}

// An id that is not a number sorts before every real one, which puts anything Discord
// answered with that this does not understand at the front rather than at the end, where it
// would be mistaken for the newest thing said.
func snowflake(id string) uint64 {
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// So that Tim can see his reply landed without waiting for the agent to say anything.
func (b discordBot) react(channel, message, emoji string) error {
	_, err := b.call(http.MethodPut,
		"/channels/"+channel+"/messages/"+message+"/reactions/"+emoji+"/@me", nil)
	return err
}
