package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

const fakeWebhook = "https://discord.invalid/api/webhooks/123/NOT-A-REAL-TOKEN"

// ps shows every process's arguments to everybody on the machine, so what op is asked
// to run names the reference and never the value.
func TestTheWebhookIsNeverInACommandLine(t *testing.T) {
	cfg := Config{EnvFile: "/somewhere/hachiko.env.op"}
	args := opArgs(cfg, "/cache/hachiko", Outgoing{})

	for _, arg := range args {
		if strings.Contains(arg, "discord") || strings.Contains(arg, "HACHIKO_DISCORD_URL") {
			t.Errorf("the webhook reached a command line: %v", args)
		}
	}
	equal(t, strings.Join(args, " "),
		"op run --env-file /somewhere/hachiko.env.op -- /cache/hachiko --send",
		"what op is asked to run")
}

func TestPostDiscordSendsTheMessageAsContent(t *testing.T) {
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		equal(t, r.Header.Get("Content-Type"), "application/json", "the content type")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	if err := postDiscord(server.Client(), server.URL, "the disk is filling"); err != nil {
		t.Fatal(err)
	}
	equal(t, body, `{"content":"the disk is filling"}`, "the request body")
}

func TestPostDiscordReportsTheStatusWithoutTheWebhook(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	err := postDiscord(server.Client(), server.URL, "the disk is filling")
	if err == nil {
		t.Fatal("a 500 was not reported")
	}
	equal(t, err.Error(), "the webhook answered 500", "the error")
}

// net/http names the URL it failed on, and that message goes into a log the next
// session reads.
func TestAFailedRequestDoesNotPutTheWebhookInTheError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL + "/api/webhooks/123/NOT-A-REAL-TOKEN"
	server.Close()

	err := postDiscord(server.Client(), url, "the disk is filling")
	if err == nil {
		t.Fatal("a request to a closed server did not fail")
	}
	lacks(t, err.Error(), "NOT-A-REAL-TOKEN")
	wants(t, err.Error(), "the webhook")
}

func TestRedactTakesTheWebhookOutOfAnyText(t *testing.T) {
	text := `Post "` + fakeWebhook + `": dial tcp: connection refused`
	got := redact(text, fakeWebhook)

	lacks(t, got, "NOT-A-REAL-TOKEN")
	lacks(t, got, "discord.invalid")
	wants(t, got, "connection refused")
}

// A *url.Error prints its target through %q, which escapes a newline and any byte
// outside ASCII — so the URL in the message is spelled differently from the one in the
// environment, and a 1Password field with a trailing newline is enough to cause it.
func TestRedactTakesEverySpellingOfTheWebhookOut(t *testing.T) {
	const token = "NOT-A-REAL-TOKEN"
	stored := "https://discord.invalid/api/webhooks/123/" + token + "-ü\n"
	webhook := strings.TrimSpace(stored)

	for _, text := range []string{
		`Post ` + strconv.Quote(webhook) + `: dial tcp: connection refused`,
		`Post ` + strconv.Quote(stored) + `: net/url: invalid control character in URL`,
		"requested " + url.QueryEscape(webhook) + " and got nothing",
		"requested " + url.PathEscape(webhook) + " and got nothing",
		"plain " + webhook + " and nothing else",
	} {
		got := redact(text, stored)
		lacks(t, got, token)
		lacks(t, got, "discord.invalid")
		wants(t, got, "the webhook")
	}
}

// A field whose value ends in a newline is a URL net/http refuses outright, which would
// turn a working webhook into an alert nobody receives.
func TestSendModeTrimsTheResolvedReference(t *testing.T) {
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got = string(raw)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	t.Setenv("HACHIKO_DISCORD_URL", server.URL+"\n")
	if _, err := sendMode(strings.NewReader("the disk is filling"), Outgoing{}, ""); err != nil {
		t.Fatalf("a reference with a trailing newline was not sent: %v", err)
	}
	equal(t, got, `{"content":"the disk is filling"}`, "the request body")
}

// Discord takes 2,000 characters. What is over that is detail, and the on-call tab has
// all of it.
func TestALongMessageIsCappedAndSaysWhereTheRestIs(t *testing.T) {
	got := capMessage(strings.Repeat("x", 5000))

	if len(got) >= discordLimit {
		t.Errorf("the message is %d characters, which Discord refuses", len(got))
	}
	wants(t, got, "[truncated — the rest is in the on-call tab in herdr]")

	short := "one line"
	equal(t, capMessage(short), short, "a short message")
}

func TestSendModeRefusesWithNothingToSendAndWithNoReferenceResolved(t *testing.T) {
	t.Setenv("HACHIKO_DISCORD_URL", "")
	_, err := sendMode(strings.NewReader("something"), Outgoing{}, "")
	if err == nil {
		t.Fatal("an unresolved reference was not reported")
	}
	wants(t, err.Error(), "did not resolve")

	t.Setenv("HACHIKO_DISCORD_URL", fakeWebhook)
	_, err = sendMode(strings.NewReader("   \n"), Outgoing{}, "")
	if err == nil {
		t.Fatal("an empty message was not reported")
	}
	wants(t, err.Error(), "nothing to send")
}
