package rest

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/timche/mac-mini/discord"
)

const token = "MTIz.secret-bot-token"

// A clock that only moves when the client sleeps, so every wait is exact and instant.
type clock struct {
	mu    sync.Mutex
	now   time.Time
	slept []time.Duration
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Sleep(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.slept = append(c.slept, d)
	c.now = c.now.Add(d)
}

type hit struct {
	Method, Path, Query, Auth, ContentType string
	Body                                   []byte
}

type server struct {
	t      *testing.T
	mu     sync.Mutex
	hits   []hit
	answer func(n int, w http.ResponseWriter, r *http.Request)
}

func newServer(t *testing.T, answer func(n int, w http.ResponseWriter, r *http.Request)) (*server, *Client, *clock) {
	t.Helper()
	s := &server{t: t, answer: answer}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.hits = append(s.hits, hit{r.Method, r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Get("Authorization"), r.Header.Get("Content-Type"), body})
		n := len(s.hits)
		s.mu.Unlock()
		s.answer(n, w, r)
	}))
	t.Cleanup(ts.Close)
	clk := &clock{now: time.Unix(1792500000, 0)}
	c := New(token)
	c.API, c.HTTP, c.Now, c.Sleep = ts.URL, ts.Client(), clk.Now, clk.Sleep
	return s, c, clk
}

func ok(body string) func(int, http.ResponseWriter, *http.Request) {
	return func(_ int, w http.ResponseWriter, _ *http.Request) { io.WriteString(w, body) }
}

func TestARequestCarriesTheTokenAndDecodesTheAnswer(t *testing.T) {
	s, c, _ := newServer(t, ok(`{"id":"1400000000000000001","channel_id":"1400000000000000002","content":"hi","timestamp":"2026-10-20T12:00:00.123000+00:00","author":{"id":"9","username":"tim","global_name":"Tim"},"type":19,"message_reference":{"message_id":"5"}}`))
	m, err := c.Message("1400000000000000002", "1400000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if m.Content != "hi" || m.Author.DisplayName() != "Tim" || m.Type != discord.MessageTypeReply || m.MessageReference.MessageID != "5" || m.Timestamp.Nanosecond() != 123000000 {
		t.Errorf("message = %+v", m)
	}
	h := s.hits[0]
	if h.Method != "GET" || h.Path != "/channels/1400000000000000002/messages/1400000000000000001" || h.Auth != "Bot "+token {
		t.Errorf("hit = %+v", h)
	}
}

func TestEveryEndpointIsTheOneDiscordDocuments(t *testing.T) {
	s, c, _ := newServer(t, ok(`{}`))
	const ch, msg, user = "1400000000000000002", "1400000000000000001", "94004892942741504"
	archived := true
	tags := []string{"7"}
	content := "closed"

	c.Gateway()
	c.Me()
	c.Guild("1220378778665095291")
	c.Channel(ch)
	c.EditChannel(ch, &discord.ChannelEdit{Archived: &archived, AppliedTags: &tags})
	c.Messages(ch, 50, "", "", msg)
	c.Send(ch, &discord.MessageSend{Content: "x"})
	c.EditMessage(&discord.MessageEdit{ID: msg, Channel: ch, Content: &content})
	c.ReactionAdd(ch, msg, "👀")
	c.ReactionRemove(ch, msg, "👀")
	c.StartThread(ch, msg, &discord.ThreadStart{Name: "t", AutoArchiveDuration: 1440})
	c.NewThread(ch, &discord.ThreadStart{Name: "t", Type: discord.ChannelTypeGuildPublicThread})
	c.ForumPost(ch, &discord.ThreadStart{Name: "p", AppliedTags: tags}, &discord.MessageSend{Content: "first"})
	c.AddThreadMember(ch, user)
	c.Respond(&discord.Interaction{ID: "55", Token: "interaction-token"}, &discord.InteractionResponse{Type: discord.InteractionResponseDeferredMessageUpdate})
	none, flags := []discord.MessageAttachment{}, discord.MessageFlags(0)
	c.EditMessage(&discord.MessageEdit{ID: msg, Channel: ch, Content: &content, Flags: &flags, Attachments: &none})
	c.DeleteMessage(ch, msg)

	want := []string{
		"GET /gateway",
		"GET /users/@me",
		"GET /guilds/1220378778665095291",
		"GET /channels/" + ch,
		"PATCH /channels/" + ch + ` {"archived":true,"applied_tags":["7"]}`,
		"GET /channels/" + ch + "/messages?around=" + msg + "&limit=50",
		"POST /channels/" + ch + `/messages {"content":"x"}`,
		"PATCH /channels/" + ch + "/messages/" + msg + ` {"content":"closed"}`,
		"PUT /channels/" + ch + "/messages/" + msg + "/reactions/%F0%9F%91%80/@me",
		"DELETE /channels/" + ch + "/messages/" + msg + "/reactions/%F0%9F%91%80/@me",
		"POST /channels/" + ch + "/messages/" + msg + `/threads {"name":"t","auto_archive_duration":1440}`,
		"POST /channels/" + ch + `/threads {"name":"t","type":11}`,
		"POST /channels/" + ch + `/threads {"name":"p","applied_tags":["7"],"message":{"content":"first"}}`,
		"PUT /channels/" + ch + "/thread-members/" + user,
		`POST /interactions/55/interaction-token/callback {"type":6}`,
		"PATCH /channels/" + ch + "/messages/" + msg + ` {"content":"closed","attachments":[],"flags":0}`,
		"DELETE /channels/" + ch + "/messages/" + msg,
	}
	if len(s.hits) != len(want) {
		t.Fatalf("%d requests, want %d", len(s.hits), len(want))
	}
	for i, h := range s.hits {
		got := h.Method + " " + h.Path
		if h.Query != "" {
			got += "?" + h.Query
		}
		if len(h.Body) > 0 {
			got += " " + string(h.Body)
		}
		if got != want[i] {
			t.Errorf("request %d\n got %s\nwant %s", i, got, want[i])
		}
	}
}

// Nothing a message mochi posts quotes may ping anybody, so an empty parse list is sent as
// one rather than left out.
func TestAllowedMentionsAreAlwaysSent(t *testing.T) {
	s, c, _ := newServer(t, ok(`{}`))
	c.Send("1", &discord.MessageSend{Content: "@everyone", AllowedMentions: &discord.MessageAllowedMentions{}})
	c.Send("1", &discord.MessageSend{Content: "@everyone", AllowedMentions: &discord.MessageAllowedMentions{Users: []string{"94004892942741504"}}, Reference: &discord.MessageReference{MessageID: "3", ChannelID: "1"}, Flags: discord.MessageFlagsSuppressEmbeds})
	want := []string{
		`{"content":"@everyone","allowed_mentions":{"parse":[],"replied_user":false}}`,
		`{"content":"@everyone","allowed_mentions":{"parse":[],"users":["94004892942741504"],"replied_user":false},"message_reference":{"message_id":"3","channel_id":"1"},"flags":4}`,
	}
	for i, h := range s.hits {
		if string(h.Body) != want[i] {
			t.Errorf("body %d = %s", i, h.Body)
		}
	}
}

func TestButtonsAreSentAsComponents(t *testing.T) {
	s, c, _ := newServer(t, ok(`{}`))
	c.Send("1", &discord.MessageSend{Components: []discord.MessageComponent{discord.ActionsRow{Components: []discord.MessageComponent{
		discord.Button{Label: "1 session", Style: discord.SecondaryButton, CustomID: "mochi:n:one", Disabled: true},
	}}}})
	want := `{"components":[{"type":1,"components":[{"type":2,"label":"1 session","style":2,"custom_id":"mochi:n:one","disabled":true}]}]}`
	if got := string(s.hits[0].Body); got != want {
		t.Errorf("body = %s", got)
	}
}

func TestAnUploadIsMultipartWithPayloadJSON(t *testing.T) {
	s, c, _ := newServer(t, ok(`{"id":"1"}`))
	_, err := c.Send("1", &discord.MessageSend{
		Content: "shots",
		Files: []*discord.File{
			{Name: "a.png", ContentType: "image/png", Reader: strings.NewReader("PNG")},
			{Name: `b "quoted".txt`, Reader: strings.NewReader("text")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	h := s.hits[0]
	media, params, err := mime.ParseMediaType(h.ContentType)
	if err != nil || media != "multipart/form-data" {
		t.Fatalf("content type %q", h.ContentType)
	}
	r := multipart.NewReader(strings.NewReader(string(h.Body)), params["boundary"])
	var parts []string
	for {
		p, err := r.NextPart()
		if err != nil {
			break
		}
		body, _ := io.ReadAll(p)
		parts = append(parts, p.FormName()+"|"+p.FileName()+"|"+p.Header.Get("Content-Type")+"|"+string(body))
	}
	want := []string{
		`payload_json||application/json|{"content":"shots"}`,
		"files[0]|a.png|image/png|PNG",
		`files[1]|b "quoted".txt|application/octet-stream|text`,
	}
	if strings.Join(parts, "\n") != strings.Join(want, "\n") {
		t.Errorf("parts:\n%s", strings.Join(parts, "\n"))
	}
}

// A name with a line break in it would write a header of its own into the part.
func TestAFileNameCannotInjectAHeader(t *testing.T) {
	s, c, _ := newServer(t, ok(`{"id":"1"}`))
	c.Send("1", &discord.MessageSend{Files: []*discord.File{{Name: "a.png\r\nX-Evil: 1\r\n\x00x", Reader: strings.NewReader("PNG")}}})
	h := s.hits[0]
	_, params, _ := mime.ParseMediaType(h.ContentType)
	r := multipart.NewReader(strings.NewReader(string(h.Body)), params["boundary"])
	r.NextPart()
	p, err := r.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if p.FileName() != "a.pngX-Evil: 1x" || p.Header.Get("Content-Type") != "application/octet-stream" || len(p.Header) != 2 {
		t.Errorf("file name %q, header %v", p.FileName(), p.Header)
	}
}

func TestAForumPostWithFilesNestsTheMessageInPayloadJSON(t *testing.T) {
	s, c, _ := newServer(t, ok(`{"id":"1"}`))
	c.ForumPost("1", &discord.ThreadStart{Name: "p"}, &discord.MessageSend{Content: "first", Files: []*discord.File{{Name: "a.png", Reader: strings.NewReader("PNG")}}})
	if !strings.Contains(string(s.hits[0].Body), `{"name":"p","message":{"content":"first"}}`) {
		t.Errorf("body = %s", s.hits[0].Body)
	}
}

func TestInteractionCallbacks(t *testing.T) {
	s, c, _ := newServer(t, ok(``))
	i := &discord.Interaction{ID: "55", Token: "tok"}
	if err := c.Respond(i, &discord.InteractionResponse{Type: discord.InteractionResponseDeferredMessageUpdate}); err != nil {
		t.Fatal(err)
	}
	c.Respond(i, &discord.InteractionResponse{
		Type: discord.InteractionResponseChannelMessageWithSource,
		Data: &discord.InteractionResponseData{Content: "Only the person who asked can choose.", Flags: discord.MessageFlagsEphemeral, AllowedMentions: &discord.MessageAllowedMentions{}},
	})
	want := []string{
		`{"type":6}`,
		`{"type":4,"data":{"content":"Only the person who asked can choose.","flags":64,"allowed_mentions":{"parse":[],"replied_user":false}}}`,
	}
	for n, h := range s.hits {
		if h.Path != "/interactions/55/tok/callback" || string(h.Body) != want[n] {
			t.Errorf("%s %s", h.Path, h.Body)
		}
	}
}

func limits(w http.ResponseWriter, bucket string, remaining int, after string) {
	w.Header().Set("X-RateLimit-Bucket", bucket)
	w.Header().Set("X-RateLimit-Remaining", itoa(remaining))
	w.Header().Set("X-RateLimit-Reset-After", after)
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// The bucket an answer says is spent holds back the next request on that route until it
// resets, and no other route.
func TestASpentBucketIsWaitedOut(t *testing.T) {
	s, c, clk := newServer(t, func(n int, w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/channels/1/") {
			limits(w, "abc", 0, "1.5")
		}
		io.WriteString(w, `{}`)
	})
	c.Message("1", "1400000000000000001")
	c.Channel("2")
	if len(clk.slept) != 0 {
		t.Errorf("another route waited: %v", clk.slept)
	}
	c.Message("1", "1400000000000000009")
	if len(clk.slept) != 1 || clk.slept[0] != 1500*time.Millisecond {
		t.Errorf("slept %v", clk.slept)
	}
	if len(s.hits) != 3 {
		t.Errorf("%d requests", len(s.hits))
	}
}

func TestAGlobal429HoldsBackEveryRoute(t *testing.T) {
	_, c, clk := newServer(t, func(n int, w http.ResponseWriter, r *http.Request) {
		if n == 1 {
			w.Header().Set("X-RateLimit-Global", "true")
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"message":"You are being rate limited.","retry_after":2.5,"global":true}`)
			return
		}
		io.WriteString(w, `{}`)
	})
	if _, err := c.Channel("1"); err != nil {
		t.Fatal(err)
	}
	if len(clk.slept) != 1 || clk.slept[0] != 2500*time.Millisecond {
		t.Errorf("slept %v", clk.slept)
	}
	// Once the clock is past it, nothing waits again.
	c.Guild("2")
	if len(clk.slept) != 1 {
		t.Errorf("slept %v", clk.slept)
	}
}

func TestRetryAfterHeaderIsUsedWhenTheBodyHasNone(t *testing.T) {
	_, c, clk := newServer(t, func(n int, w http.ResponseWriter, r *http.Request) {
		if n == 1 {
			w.Header().Set("Retry-After", "4")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		io.WriteString(w, `{}`)
	})
	c.Channel("1")
	if len(clk.slept) != 1 || clk.slept[0] != 4*time.Second {
		t.Errorf("slept %v", clk.slept)
	}
}

// A rename limit is ten minutes: the caller would rather schedule it than sleep in the call.
func TestNoWaitGivesUpRatherThanWaiting(t *testing.T) {
	s, c, clk := newServer(t, func(n int, w http.ResponseWriter, r *http.Request) {
		switch n {
		case 1:
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"retry_after":540.2,"global":false}`)
		case 2:
			limits(w, "rename", 0, "600")
			io.WriteString(w, `{}`)
		}
	})
	_, err := c.EditChannel("1", &discord.ChannelEdit{Name: "x"}, NoWait())
	var limited *RateLimitError
	if !errors.As(err, &limited) || limited.Global || limited.RetryAfter != 540200*time.Millisecond {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.EditChannel("1", &discord.ChannelEdit{Name: "y"}, NoWait()); err != nil {
		t.Fatal(err)
	}
	_, err = c.EditChannel("1", &discord.ChannelEdit{Name: "z"}, NoWait())
	if !errors.As(err, &limited) || limited.RetryAfter != 600*time.Second {
		t.Fatalf("err = %v", err)
	}
	if len(clk.slept) != 0 || len(s.hits) != 2 {
		t.Errorf("slept %v, %d requests", clk.slept, len(s.hits))
	}
}

// A wait past two minutes is given up on rather than slept through, and so is the next
// request held back by the same global limit.
func TestALongLimitIsGivenUpOnRatherThanWaited(t *testing.T) {
	s, c, clk := newServer(t, func(n int, w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"retry_after":3600,"global":true}`)
	})
	var limited *RateLimitError
	if _, err := c.Channel("1"); !errors.As(err, &limited) || limited.RetryAfter != time.Hour || !limited.Global {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.Guild("2"); !errors.As(err, &limited) || !limited.Global {
		t.Fatalf("err = %v", err)
	}
	if len(clk.slept) != 0 || len(s.hits) != 1 {
		t.Errorf("slept %v, %d requests", clk.slept, len(s.hits))
	}
}

func TestA429ThatKeepsComingIsGivenUpOn(t *testing.T) {
	s, c, _ := newServer(t, func(n int, w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"retry_after":1}`)
	})
	_, err := c.Channel("1")
	var limited *RateLimitError
	if !errors.As(err, &limited) || len(s.hits) != limitRetries+1 {
		t.Errorf("err = %v after %d requests", err, len(s.hits))
	}
}

func TestA502IsSentAgain(t *testing.T) {
	s, c, _ := newServer(t, func(n int, w http.ResponseWriter, r *http.Request) {
		if n < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		io.WriteString(w, `{"id":"1"}`)
	})
	if ch, err := c.Channel("1"); err != nil || ch.ID != "1" || len(s.hits) != 3 {
		t.Errorf("%+v, %v, %d requests", ch, err, len(s.hits))
	}
}

func TestErrorsAreTypedAndCarryNoToken(t *testing.T) {
	_, c, _ := newServer(t, func(n int, w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"message":"Unknown Channel `+token+`","code":10003}`)
	})
	err := c.Respond(&discord.Interaction{ID: "55", Token: "interaction-secret"}, &discord.InteractionResponse{Type: 6})
	var rest *Error
	if !errors.As(err, &rest) || rest.Status != 404 || rest.Code != CodeUnknownChannel {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "interaction-secret") {
		t.Errorf("error text = %s", err)
	}
}

func TestATransportErrorCarriesNoToken(t *testing.T) {
	c := New(token)
	c.API = "http://127.0.0.1:1/" + token
	_, err := c.Channel("1")
	if err == nil || strings.Contains(err.Error(), token) {
		t.Errorf("err = %v", err)
	}
}

// A refused connection names the URL, and an interaction's URL holds its token.
func TestATransportErrorCarriesNoInteractionToken(t *testing.T) {
	c := New(token)
	c.API = "http://127.0.0.1:1"
	err := c.Respond(&discord.Interaction{ID: "55", Token: "interaction-secret"}, &discord.InteractionResponse{Type: 6})
	if err == nil || strings.Contains(err.Error(), "interaction-secret") || !strings.Contains(err.Error(), "/interactions/55/<token>/callback") {
		t.Errorf("err = %v", err)
	}
}

func TestAnUploadGetsTimeForItsSize(t *testing.T) {
	for size, want := range map[int]time.Duration{
		0:         20 * time.Second,
		400:       20 * time.Second,
		10 << 20:  40 * time.Second,
		100 << 20: 220 * time.Second,
		1 << 30:   5 * time.Minute,
	} {
		if got := timeoutFor(size); got != want {
			t.Errorf("%d bytes: %s, want %s", size, got, want)
		}
	}
	if New(token).HTTP.Timeout != 0 {
		t.Error("a flat client timeout would still cut an upload off")
	}
}

// The deadline is the request's: a server that never answers is given up on.
func TestARequestThatHangsIsGivenUpOn(t *testing.T) {
	release := make(chan struct{})
	_, c, _ := newServer(t, func(n int, w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer close(release)
	c.Timeout = func(int) time.Duration { return 50 * time.Millisecond }
	start := time.Now()
	if _, err := c.Channel("1"); err == nil || time.Since(start) > 5*time.Second {
		t.Errorf("err = %v after %s", err, time.Since(start))
	}
}

func TestRoutesKeepOnlyTheMajorID(t *testing.T) {
	for path, want := range map[string]string{
		"/channels/1400000000000000002/messages/1400000000000000001":                      "GET /channels/1400000000000000002/messages/{id}",
		"/channels/1400000000000000002/messages/1400000000000000001/reactions/%F0%9F/@me": "GET /channels/1400000000000000002/messages/{id}/reactions/{emoji}/@me",
		"/interactions/1400000000000000001/secret/callback":                               "GET /interactions/1400000000000000001/{token}/callback",
		"/channels/1400000000000000002/messages?limit=50&before=1400000000000000001":      "GET /channels/1400000000000000002/messages",
	} {
		if got := routeOf("GET", path); got != want {
			t.Errorf("%s → %s", path, got)
		}
	}
}
