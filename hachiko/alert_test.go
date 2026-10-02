package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const fakeWebhook = "https://discord.invalid/api/webhooks/123/NOT-A-REAL-TOKEN"

// ps shows every process's arguments to everybody on the machine, so what op is asked
// to run names the reference and never the value.
func TestTheWebhookIsNeverInACommandLine(t *testing.T) {
	cfg := Config{EnvFile: "/somewhere/hachiko.env.op"}
	args := opArgs(cfg, "/cache/hachiko")

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
	err := sendMode(strings.NewReader("something"))
	if err == nil {
		t.Fatal("an unresolved reference was not reported")
	}
	wants(t, err.Error(), "did not resolve")

	t.Setenv("HACHIKO_DISCORD_URL", fakeWebhook)
	err = sendMode(strings.NewReader("   \n"))
	if err == nil {
		t.Fatal("an empty message was not reported")
	}
	wants(t, err.Error(), "nothing to send")
}
