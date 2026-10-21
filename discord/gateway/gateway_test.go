package gateway

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/discord"
)

func TestHelloIdentifyReadyAndHeartbeats(t *testing.T) {
	h := newHarness(t)
	h.open()
	c := h.g.next()
	if c.path != "/" || !hasQuery(c) {
		t.Errorf("connected to %s?%s", c.path, c.query)
	}
	c.hello(40 * time.Millisecond)

	id := c.expect(opIdentify)
	var identify struct {
		Token      string            `json:"token"`
		Intents    int               `json:"intents"`
		Properties map[string]string `json:"properties"`
		Compress   *bool             `json:"compress"`
	}
	json.Unmarshal(id.D, &identify)
	if identify.Token != token || identify.Intents != 1|1<<9|1<<15 || identify.Properties["os"] == "" || identify.Compress != nil {
		t.Errorf("identify = %s", id.D)
	}

	c.ready("s1", h.g.url()+"/resume", 1)
	if err := h.openErr(t); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-h.readies:
		if r.User.Username != "mochi" || len(r.Guilds) != 1 || r.SessionID != "s1" {
			t.Errorf("ready = %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("READY was not delivered")
	}

	// Each beat carries the last sequence, and each ack is noted.
	for range 3 {
		beat := c.expect(opHeartbeat)
		if string(beat.D) != "1" {
			t.Errorf("heartbeat d = %s", beat.D)
		}
		c.send(opHeartbeatAck, nil, 0, "")
	}
	time.Sleep(5 * time.Millisecond)
	ready, ack := h.s.Status()
	if !ready || time.Since(ack) > time.Second {
		t.Errorf("status = %v, %v", ready, ack)
	}
	if h.identified() != 1 {
		t.Errorf("%d identifies", h.identified())
	}
	if err := h.s.Err(); err != nil {
		t.Errorf("err = %v", err)
	}
}

// Discord may ask for a beat at any time, which is answered at once.
func TestAHeartbeatRequestIsAnsweredAtOnce(t *testing.T) {
	h := newHarness(t)
	c := h.connected(t, time.Hour)
	c.send(opHeartbeat, nil, 0, "")
	done := make(chan struct{})
	go func() { c.expect(opHeartbeat); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("no heartbeat")
	}
}

// A beat due while the last one was never acknowledged is a connection that died without
// saying so: closed with a code that keeps the session, and resumed at the address READY
// gave, from the last sequence seen.
func TestAMissedAckReconnectsAndResumes(t *testing.T) {
	h := newHarness(t)
	c := h.connected(t, 30*time.Millisecond)
	c.send(opDispatch, map[string]any{"id": "1400000000000000001", "content": "hi"}, 2, "MESSAGE_CREATE")
	h.message(t)

	c.expect(opHeartbeat) // and no ack
	if code := c.expectClose(); code == CloseNormal || code == CloseGoingAway {
		t.Errorf("closed with %d, which ends the session", code)
	}

	r := h.g.next()
	if r.path != "/resume" || !hasQuery(r) {
		t.Errorf("reconnected to %s?%s", r.path, r.query)
	}
	r.hello(time.Hour)
	resume := r.expect(opResume)
	var d struct {
		Token     string `json:"token"`
		SessionID string `json:"session_id"`
		Seq       int64  `json:"seq"`
	}
	json.Unmarshal(resume.D, &d)
	if d.Token != token || d.SessionID != "s1" || d.Seq != 2 {
		t.Errorf("resume = %s", resume.D)
	}

	// What Discord replays before RESUMED is delivered like anything else.
	r.send(opDispatch, map[string]any{"id": "1400000000000000002", "content": "missed"}, 3, "MESSAGE_CREATE")
	r.send(opDispatch, nil, 4, "RESUMED")
	if m := h.message(t); m.Content != "missed" {
		t.Errorf("replayed %+v", m)
	}
	waitReady(t, h)
	if h.identified() != 1 {
		t.Errorf("%d identifies, want the one", h.identified())
	}
}

func waitReady(t *testing.T, h *harness) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ready, _ := h.s.Status(); ready {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the session did not become ready again")
}

func TestReconnectRequestResumes(t *testing.T) {
	h := newHarness(t)
	c := h.connected(t, time.Hour)
	c.send(opReconnect, nil, 0, "")
	if code := c.expectClose(); code == CloseNormal || code == CloseGoingAway {
		t.Errorf("closed with %d", code)
	}
	r := h.g.next()
	r.hello(time.Hour)
	r.expect(opResume)
	r.send(opDispatch, nil, 2, "RESUMED")
	waitReady(t, h)
}

func TestAResumableInvalidSessionResumes(t *testing.T) {
	h := newHarness(t)
	c := h.connected(t, time.Hour)
	c.send(opInvalidSession, true, 0, "")
	r := h.g.next()
	if r.path != "/resume" {
		t.Errorf("reconnected to %s", r.path)
	}
	r.hello(time.Hour)
	r.expect(opResume)
	r.send(opDispatch, nil, 2, "RESUMED")
	waitReady(t, h)
}

// A session Discord will not resume is identified anew, at the gateway's own address.
func TestAnUnresumableInvalidSessionIdentifiesAgain(t *testing.T) {
	h := newHarness(t)
	c := h.connected(t, time.Hour)
	c.send(opInvalidSession, false, 0, "")
	r := h.g.next()
	if r.path != "/" {
		t.Errorf("reconnected to %s", r.path)
	}
	r.hello(time.Hour)
	r.expect(opIdentify)
	r.ready("s2", h.g.url()+"/resume", 1)
	waitReady(t, h)
	if h.identified() != 2 {
		t.Errorf("%d identifies", h.identified())
	}
}

// 4004 is a token Discord refuses: Open says so in a type, and nothing tries again.
func TestAuthenticationFailedIsFatalOnOpen(t *testing.T) {
	h := newHarness(t)
	h.open()
	c := h.g.next()
	c.hello(time.Hour)
	c.expect(opIdentify)
	c.closeWith(CloseAuthenticationFailed)

	err := h.openErr(t)
	var closed *CloseError
	if !errors.As(err, &closed) || closed.Code != CloseAuthenticationFailed || !closed.Code.Fatal() {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("error text = %s", err)
	}
	if code := c.expectClose(); code != CloseAuthenticationFailed {
		t.Errorf("echoed %d", code)
	}
	h.g.none(100 * time.Millisecond)
}

func TestAFatalCloseLaterStopsTheSession(t *testing.T) {
	for _, code := range []CloseCode{CloseAuthenticationFailed, CloseInvalidShard, CloseShardingRequired, CloseInvalidAPIVersion, CloseInvalidIntents, CloseDisallowedIntents} {
		t.Run(strconv.Itoa(int(code)), func(t *testing.T) {
			h := newHarness(t)
			c := h.connected(t, time.Hour)
			c.closeWith(code)
			h.g.none(100 * time.Millisecond)
			var closed *CloseError
			if err := h.s.Err(); !errors.As(err, &closed) || closed.Code != code {
				t.Errorf("err = %v", err)
			}
			if ready, _ := h.s.Status(); ready {
				t.Error("still ready")
			}
		})
	}
}

// Any other close is a reason to come back: resumed where Discord allows it, identified anew
// where it does not.
func TestACloseWithACodeReconnects(t *testing.T) {
	for code, resumed := range map[CloseCode]bool{
		CloseUnknownError:    true,
		CloseRateLimited:     true,
		CloseGoingAway:       true,
		CloseInvalidSeq:      false,
		CloseSessionTimedOut: false,
	} {
		t.Run(strconv.Itoa(int(code)), func(t *testing.T) {
			h := newHarness(t)
			c := h.connected(t, time.Hour)
			c.closeWith(code)
			if echoed := c.expectClose(); echoed != code {
				t.Errorf("echoed %d", echoed)
			}
			r := h.g.next()
			r.hello(time.Hour)
			if resumed {
				r.expect(opResume)
				r.send(opDispatch, nil, 2, "RESUMED")
			} else {
				r.expect(opIdentify)
				r.ready("s2", h.g.url()+"/resume", 1)
			}
			waitReady(t, h)
		})
	}
}

// A message in fragments, with a ping between two of them: the ping is answered with its own
// payload, and the fragments make one READY.
func TestFragmentsAndAServerPing(t *testing.T) {
	h := newHarness(t)
	h.open()
	c := h.g.next()
	c.hello(time.Hour)
	c.expect(opIdentify)

	body, _ := json.Marshal(map[string]any{"op": 0, "s": 1, "t": "READY", "d": map[string]any{
		"session_id": "s1", "resume_gateway_url": h.g.url(), "user": map[string]any{"username": strings.Repeat("m", 300)},
	}})
	c.writeFrame(false, opText, body[:100])
	c.writeFrame(true, opPing, []byte("are you there"))
	c.writeFrame(false, opContinuation, body[100:200])
	c.writeFrame(true, opContinuation, body[200:])

	for {
		f, err := c.readFrame()
		if err != nil {
			t.Fatal(err)
		}
		if f.op == opPong {
			if string(f.payload) != "are you there" {
				t.Errorf("pong = %q", f.payload)
			}
			break
		}
	}
	if err := h.openErr(t); err != nil {
		t.Fatal(err)
	}
	if r := <-h.readies; len(r.User.Username) != 300 {
		t.Errorf("user = %q", r.User.Username)
	}
}

// A message larger than 64 KiB arrives with a 64-bit length.
func TestALargeMessage(t *testing.T) {
	h := newHarness(t)
	c := h.connected(t, time.Hour)
	content := strings.Repeat("x", 70_000)
	c.send(opDispatch, map[string]any{"id": "1", "content": content}, 2, "MESSAGE_CREATE")
	if m := h.message(t); m.Content != content {
		t.Errorf("content of %d bytes", len(m.Content))
	}
}

// A frame that claims a terabyte is refused on its header, before a byte of it is read.
func TestAFrameOverTheLimitIsRefused(t *testing.T) {
	h := newHarness(t)
	c := h.connected(t, time.Hour)
	c.nc.Write(binary.BigEndian.AppendUint64([]byte{0x81, 127}, 1<<40))
	if code := c.expectClose(); code != CloseProtocolError {
		t.Errorf("closed with %d", code)
	}
}

// Fragments that are each small but together past the limit are refused too.
func TestAMessageOverTheLimitIsRefused(t *testing.T) {
	h := newHarness(t)
	c := h.connected(t, time.Hour)
	chunk := make([]byte, 1<<20)
	c.writeFrame(false, opText, chunk)
	for range maxMessage >> 20 {
		c.writeFrame(false, opContinuation, chunk)
	}
	if code := c.expectClose(); code != CloseProtocolError {
		t.Errorf("closed with %d", code)
	}
}

func TestAProtocolErrorClosesWith1002(t *testing.T) {
	h := newHarness(t)
	c := h.connected(t, time.Hour)
	c.writeFrame(true, 0x3, []byte("reserved opcode"))
	if code := c.expectClose(); code != CloseProtocolError {
		t.Errorf("closed with %d", code)
	}
	r := h.g.next() // and comes back
	r.hello(time.Hour)
	r.expect(opResume)
}

func TestAHandshakeWithTheWrongAcceptIsRefused(t *testing.T) {
	h := newHarness(t)
	h.g.badAccept = true
	h.open()
	<-h.g.conns
	if err := h.openErr(t); err == nil || !strings.Contains(err.Error(), "accept") {
		t.Errorf("err = %v", err)
	}
}

func TestACompressionExtensionIsRefused(t *testing.T) {
	h := newHarness(t)
	h.g.extensions = "permessage-deflate"
	h.open()
	<-h.g.conns
	if err := h.openErr(t); err == nil {
		t.Error("an extension was accepted")
	}
	h.g.mu.Lock()
	defer h.g.mu.Unlock()
	if ext := h.g.requests[0].Header.Get("Sec-WebSocket-Extensions"); ext != "" {
		t.Errorf("the client asked for %s", ext)
	}
}

func TestOnlyWSSIsDialled(t *testing.T) {
	s := New(token, intents)
	s.URL = "ws://127.0.0.1:1"
	if err := s.Open(); err == nil || !strings.Contains(err.Error(), "wss") {
		t.Errorf("err = %v", err)
	}
}

// Close is the end: a normal close, which Discord takes as the end of the session, and no
// reconnect.
func TestCloseEndsTheSession(t *testing.T) {
	h := newHarness(t)
	c := h.connected(t, time.Hour)
	h.s.Close()
	if code := c.expectClose(); code != CloseNormal {
		t.Errorf("closed with %d", code)
	}
	h.g.none(100 * time.Millisecond)
	if ready, _ := h.s.Status(); ready {
		t.Error("still ready")
	}

	// And it can be opened again.
	h.open()
	again := h.g.next()
	again.hello(time.Hour)
	again.expect(opIdentify)
	again.ready("s2", h.g.url()+"/resume", 1)
	if err := h.openErr(t); err != nil {
		t.Fatal(err)
	}
}

func TestEventsAreTyped(t *testing.T) {
	h := newHarness(t)
	roles := make(chan string, 4)
	h.s.Handlers.GuildCreate = func(g *discord.Guild) { roles <- g.ID + " " + g.Roles[0].BotID() }
	h.s.Handlers.GuildRoleUpdate = func(r *discord.GuildRole) { roles <- r.GuildID + " " + r.Role.BotID() }
	h.s.Handlers.GuildRoleDelete = func(r *discord.GuildRoleDelete) { roles <- r.GuildID + " -" + r.RoleID }
	h.s.Handlers.InteractionCreate = func(i *discord.Interaction) { roles <- i.Data.CustomID + " " + i.Member.User.ID }
	c := h.connected(t, time.Hour)

	c.send(opDispatch, map[string]any{"id": "g1", "roles": []map[string]any{{"id": "r1", "tags": map[string]any{"bot_id": "b1"}}}}, 2, "GUILD_CREATE")
	c.send(opDispatch, map[string]any{"guild_id": "g1", "role": map[string]any{"id": "r2", "tags": map[string]any{"bot_id": "b2"}}}, 3, "GUILD_ROLE_UPDATE")
	c.send(opDispatch, map[string]any{"guild_id": "g1", "role_id": "r2"}, 4, "GUILD_ROLE_DELETE")
	c.send(opDispatch, map[string]any{"id": "i1", "type": 3, "token": "t", "data": map[string]any{"custom_id": "mochi:x:one"}, "member": map[string]any{"user": map[string]any{"id": "u1"}}}, 5, "INTERACTION_CREATE")

	got := map[string]bool{}
	for range 4 {
		select {
		case r := <-roles:
			got[r] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("got %v", got)
		}
	}
	for _, want := range []string{"g1 b1", "g1 b2", "g1 -r2", "mochi:x:one u1"} {
		if !got[want] {
			t.Errorf("missing %q in %v", want, got)
		}
	}
}
