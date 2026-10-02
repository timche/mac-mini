package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fakeBotToken = "MTIzNDU2.NOT-A-REAL-BOT-TOKEN"

// Off is the default and the thing that must not break: with no channel and no user, the
// webhook path is exactly what it was, nothing asks for a token, and nothing opens a
// thread.
func TestWithNoChannelAndUserTheBotIsOffAndTheWebhookPathIsUntouched(t *testing.T) {
	cfg := Config{EnvFile: "/somewhere/hachiko.env.op", DiscordEnvFile: "/somewhere/hachiko.discord.env.op"}

	equal(t, cfg.Discord.On(), false, "whether the bot is on with nothing configured")
	equal(t, strings.Join(opArgs(cfg, "/cache/hachiko", Outgoing{}), " "),
		"op run --env-file /somewhere/hachiko.env.op -- /cache/hachiko --send",
		"what op is asked to run with the bot off")

	// Half-configured is off: a channel with no account to take replies from would be a
	// listener that obeys anybody in it, and an account with no channel has nowhere to read.
	for _, half := range []DiscordConfig{{ChannelID: "123"}, {UserID: "456"}} {
		cfg.Discord = half
		equal(t, cfg.Discord.On(), false, "whether half a configuration turns the bot on")
	}
}

// The second env file is the one that carries the token, and it is passed only when the
// feature is on: `op run` refuses a reference it cannot resolve, so a bot token named
// before the field exists would stop every alert rather than leaving one feature off.
func TestTheTokensEnvFileIsPassedOnlyWhenTheBotIsConfigured(t *testing.T) {
	dir := t.TempDir()
	discordEnv := filepath.Join(dir, "hachiko.discord.env.op")
	writeFile(t, discordEnv, "HACHIKO_DISCORD_BOT_TOKEN=op://dev/hachiko-discord/bot token\n")

	cfg := Config{EnvFile: "/somewhere/hachiko.env.op", DiscordEnvFile: discordEnv}
	equal(t, len(envFiles(cfg)), 2, "env files with the bot off")

	cfg.Discord = DiscordConfig{ChannelID: "123", UserID: "456"}
	equal(t, strings.Join(envFiles(cfg), " "),
		"--env-file /somewhere/hachiko.env.op --env-file "+discordEnv,
		"env files with the bot on")

	// And a Mac where the link is not there yet keeps alerting rather than failing on it.
	cfg.DiscordEnvFile = filepath.Join(dir, "missing.env.op")
	equal(t, len(envFiles(cfg)), 2, "env files when the second one is not there")
}

// Both are ids in a request path, so anything that is not a snowflake is nothing.
func TestTheConfigFileTakesOnlySnowflakes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "discord")

	writeFile(t, path, `# the channel hachiko alerts to
channel = 1234567890123456789
user   =   987654321098765432
`)
	cfg := readDiscordConfig(path)
	equal(t, cfg.ChannelID, "1234567890123456789", "the channel id")
	equal(t, cfg.UserID, "987654321098765432", "the user id")
	equal(t, cfg.On(), true, "whether the bot is on")

	writeFile(t, path, "channel =\nuser =\n")
	equal(t, readDiscordConfig(path).On(), false, "whether an empty file turns the bot on")

	for _, bad := range []string{
		"channel = ../../etc/passwd\nuser = 1\n",
		"channel = 1/messages\nuser = 1\n",
		"channel = <#1234>\nuser = 1\n",
	} {
		writeFile(t, path, bad)
		equal(t, readDiscordConfig(path).ChannelID, "", "the channel read from "+bad)
	}

	equal(t, readDiscordConfig(filepath.Join(dir, "nothing-here")).On(), false,
		"whether a missing file turns the bot on")
}

// One incident, one thread: the first message opens it and everything after goes inside, so
// a reminder three hours later is under the alert it is about.
func TestTheFirstMessageOpensAThreadAndTheRestGoIntoIt(t *testing.T) {
	bot, calls := fakeBot(t, map[string]string{
		"POST /channels/chan/messages":               `{"id":"msg-1"}`,
		"POST /channels/chan/messages/msg-1/threads": `{"id":"thread-1"}`,
		"POST /channels/thread-1/messages":           `{"id":"msg-2"}`,
	})

	thread, err := sendThroughBot(bot, "chan", Outgoing{Text: "the disk is filling", OpenThread: "disk-42"})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, thread, "thread-1", "the thread the first message opened")

	again, err := sendThroughBot(bot, "chan", Outgoing{Text: "still filling", Thread: "thread-1"})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, again, "", "a thread opened by a later message")
	equal(t, strings.Join(*calls, " | "),
		"POST /channels/chan/messages | POST /channels/chan/messages/msg-1/threads | POST /channels/thread-1/messages",
		"what the bot was asked to do")
}

// Discord bans a bot that ignores a 429 rather than slowing it down, so the wait is the one
// it asked for — and it is the body's, which has sub-second resolution.
func TestARateLimitIsWaitedOutForAsLongAsDiscordAsked(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		if hits == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"retry_after":0.75,"global":false}`))
			return
		}
		w.Write([]byte(`{"id":"msg-1"}`))
	}))
	defer server.Close()

	var slept []time.Duration
	bot := discordBot{
		token:  fakeBotToken,
		client: server.Client(),
		now:    func() time.Time { return base },
		sleep:  func(d time.Duration) { slept = append(slept, d) },
	}

	id, err := botAt(bot, server.URL).post("chan", "the disk is filling")
	if err != nil {
		t.Fatal(err)
	}
	equal(t, id, "msg-1", "the message id once the limit was waited out")
	equal(t, hits, 2, "requests made")
	equal(t, len(slept), 1, "waits")
	if len(slept) == 1 {
		equal(t, slept[0], 750*time.Millisecond, "how long it waited")
	}
}

// A global limit can be minutes, and a listener asleep that long has stopped listening.
func TestARateLimitWaitIsCapped(t *testing.T) {
	resp := &http.Response{Header: http.Header{"Retry-After": []string{"600"}}}
	equal(t, retryAfter(resp, nil), 30*time.Second, "the longest it waits")

	equal(t, retryAfter(&http.Response{Header: http.Header{}}, nil), time.Second,
		"the wait when Discord said nothing")
}

// The token is in one header and nothing else. An error text that carried it would be in
// the log every session reads.
func TestAFailedBotCallDoesNotPutTheTokenInTheError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	bot := botAt(newBot(fakeBotToken), server.URL)
	server.Close()

	_, err := bot.post("chan", "the disk is filling")
	if err == nil {
		t.Fatal("a request to a closed server did not fail")
	}
	lacks(t, err.Error(), "NOT-A-REAL-BOT-TOKEN")

	// And the redaction is the webhook's, so every spelling of it goes the same way.
	lacks(t, redact("dial failed for Bot "+fakeBotToken, fakeBotToken), "NOT-A-REAL-BOT-TOKEN")
}

func TestTheTokenIsNeverInACommandLine(t *testing.T) {
	cfg := Config{
		EnvFile:        "/somewhere/hachiko.env.op",
		DiscordEnvFile: "/somewhere/hachiko.discord.env.op",
		Discord:        DiscordConfig{ChannelID: "123", UserID: "456"},
	}

	for _, arg := range opArgs(cfg, "/cache/hachiko", Outgoing{Thread: "thread-1"}) {
		lacks(t, arg, "NOT-A-REAL")
		if strings.Contains(arg, "HACHIKO_DISCORD_BOT_TOKEN") && !strings.HasSuffix(arg, ".env.op") {
			t.Errorf("the token reached a command line: %s", arg)
		}
	}
}

// Oldest first, because a conversation has to be read in the order it happened and Discord
// returns the opposite.
func TestMessagesComeBackOldestFirst(t *testing.T) {
	bot, calls := fakeBot(t, map[string]string{
		"GET /channels/thread-1/messages?limit=100&after=msg-1": `[
			{"id":"msg-3","content":"3","author":{"id":"tim"}},
			{"id":"msg-2","content":"2","author":{"id":"tim"}}]`,
	})

	messages, err := bot.messagesAfter("thread-1", "msg-1")
	if err != nil {
		t.Fatal(err)
	}
	equal(t, len(messages), 2, "messages read")
	equal(t, messages[0].ID, "msg-2", "the first message read")
	equal(t, messages[1].ID, "msg-3", "the second message read")
	wants(t, strings.Join(*calls, " "), "after=msg-1")
}

// A thread name Discord refuses is a thread that is never made, and an incident id with a
// headline after it is longer than the hundred characters it takes.
func TestAThreadNameIsClippedToWhatDiscordTakes(t *testing.T) {
	var sent string
	bot, _ := fakeBotWith(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/threads") {
			raw, _ := io.ReadAll(r.Body)
			sent = string(raw)
			w.Write([]byte(`{"id":"thread-1"}`))
			return
		}
		w.Write([]byte(`{"id":"msg-1"}`))
	})

	long := strings.Repeat("x", 300)
	if _, err := bot.openThread("chan", "msg-1", long); err != nil {
		t.Fatal(err)
	}

	var body struct{ Name string }
	if err := json.Unmarshal([]byte(sent), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Name) > 100 {
		t.Errorf("the thread name is %d characters, which Discord refuses", len(body.Name))
	}
}

// The bot and the webhook, chosen by whether there is a token and a channel for it: that is
// the whole of how this stays off until Tim has configured it, and how it keeps working on
// a Mac whose Discord application somebody deleted.
func TestSendModeUsesTheBotOnlyWithATokenAndAChannel(t *testing.T) {
	var reached string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = r.URL.Path
		w.Write([]byte(`{"id":"msg-1"}`))
	}))
	defer server.Close()

	t.Setenv("HACHIKO_DISCORD_URL", server.URL+"/api/webhooks/1/x")
	t.Setenv("HACHIKO_DISCORD_BOT_TOKEN", "")

	if _, err := sendMode(strings.NewReader("the disk is filling"), Outgoing{}, "chan"); err != nil {
		t.Fatal(err)
	}
	equal(t, reached, "/api/webhooks/1/x", "where a message went with no token")

	// With a token but no channel there is nothing to post into, so the webhook again.
	t.Setenv("HACHIKO_DISCORD_BOT_TOKEN", fakeBotToken)
	if _, err := sendMode(strings.NewReader("the disk is filling"), Outgoing{}, ""); err != nil {
		t.Fatal(err)
	}
	equal(t, reached, "/api/webhooks/1/x", "where a message went with no channel")
}

func fakeBot(t *testing.T, answers map[string]string) (discordBot, *[]string) {
	t.Helper()

	var calls []string
	bot, _ := fakeBotWith(t, func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}
		calls = append(calls, key)

		answer, ok := answers[key]
		if !ok {
			t.Errorf("the bot asked for something the fake does not answer: %s", key)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(answer))
	})
	return bot, &calls
}

func fakeBotWith(t *testing.T, handler http.HandlerFunc) (discordBot, *httptest.Server) {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	bot := discordBot{
		token:  fakeBotToken,
		api:    server.URL,
		client: server.Client(),
		now:    func() time.Time { return base },
		sleep:  func(time.Duration) {},
	}
	return bot, server
}

func botAt(bot discordBot, api string) discordBot {
	bot.api = api
	return bot
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
