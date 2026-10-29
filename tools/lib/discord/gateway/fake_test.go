package gateway

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/timche/mac-mini/discord"
)

// A gateway on the other end of a real TLS websocket: it upgrades every connection, hands
// each to the test, and speaks frames the way a server does — unmasked, and checking that
// every frame the client sends is masked.
type fakeGateway struct {
	t     *testing.T
	ts    *httptest.Server
	conns chan *serverConn

	// What the handshake looked like, for the checks on it; and an accept key to answer with
	// in place of the right one.
	mu         sync.Mutex
	requests   []*http.Request
	badAccept  bool
	extensions string
}

func newFakeGateway(t *testing.T) *fakeGateway {
	t.Helper()
	g := &fakeGateway{t: t, conns: make(chan *serverConn, 8)}
	g.ts = httptest.NewTLSServer(http.HandlerFunc(g.upgrade))
	t.Cleanup(g.ts.Close)
	return g
}

func (g *fakeGateway) url() string { return "wss://" + g.ts.Listener.Addr().String() }

func (g *fakeGateway) tlsConfig() *tls.Config {
	pool := x509.NewCertPool()
	pool.AddCert(g.ts.Certificate())
	return &tls.Config{RootCAs: pool}
}

func (g *fakeGateway) upgrade(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	g.requests = append(g.requests, r)
	bad, ext := g.badAccept, g.extensions
	g.mu.Unlock()

	if r.Header.Get("Upgrade") != "websocket" || r.Header.Get("Sec-WebSocket-Version") != "13" {
		http.Error(w, "not a websocket", http.StatusBadRequest)
		return
	}
	key := accept(r.Header.Get("Sec-WebSocket-Key"))
	if bad {
		key = accept("something else")
	}
	nc, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		g.t.Error(err)
		return
	}
	rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + key + "\r\n")
	if ext != "" {
		rw.WriteString("Sec-WebSocket-Extensions: " + ext + "\r\n")
	}
	rw.WriteString("\r\n")
	rw.Flush()
	g.conns <- &serverConn{t: g.t, nc: nc, br: rw.Reader, path: r.URL.Path, query: r.URL.RawQuery}
}

// next is the next connection the client made.
func (g *fakeGateway) next() *serverConn {
	g.t.Helper()
	select {
	case c := <-g.conns:
		g.t.Cleanup(func() { c.nc.Close() })
		return c
	case <-time.After(5 * time.Second):
		g.t.Fatal("the client did not connect")
		return nil
	}
}

// none says the client did not connect again within a while.
func (g *fakeGateway) none(within time.Duration) {
	g.t.Helper()
	select {
	case c := <-g.conns:
		c.nc.Close()
		g.t.Errorf("the client connected again, to %s", c.path)
	case <-time.After(within):
	}
}

type serverConn struct {
	t           *testing.T
	nc          net.Conn
	br          *bufio.Reader
	path, query string
	wmu         sync.Mutex
}

type frame struct {
	fin     bool
	op      byte
	payload []byte
}

func (c *serverConn) readFrame() (frame, error) {
	c.nc.SetReadDeadline(time.Now().Add(5 * time.Second))
	var head [2]byte
	if _, err := io.ReadFull(c.br, head[:]); err != nil {
		return frame{}, err
	}
	if head[1]&0x80 == 0 {
		c.t.Error("a client frame was not masked")
	}
	size := uint64(head[1] & 0x7F)
	switch size {
	case 126:
		var ext [2]byte
		io.ReadFull(c.br, ext[:])
		size = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		io.ReadFull(c.br, ext[:])
		size = binary.BigEndian.Uint64(ext[:])
	}
	var mask [4]byte
	io.ReadFull(c.br, mask[:])
	payload := make([]byte, size)
	if _, err := io.ReadFull(c.br, payload); err != nil {
		return frame{}, err
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return frame{fin: head[0]&0x80 != 0, op: head[0] & 0x0F, payload: payload}, nil
}

func (c *serverConn) writeFrame(fin bool, op byte, payload []byte) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	b0 := op
	if fin {
		b0 |= 0x80
	}
	out := []byte{b0}
	switch n := len(payload); {
	case n <= 125:
		out = append(out, byte(n))
	case n <= 0xFFFF:
		out = append(out, 126)
		out = binary.BigEndian.AppendUint16(out, uint16(n))
	default:
		out = append(out, 127)
		out = binary.BigEndian.AppendUint64(out, uint64(n))
	}
	c.nc.Write(append(out, payload...))
}

func (c *serverConn) send(op int, d any, seq int64, event string) {
	body := map[string]any{"op": op, "d": d}
	if event != "" {
		body["t"], body["s"] = event, seq
	}
	raw, _ := json.Marshal(body)
	c.writeFrame(true, opText, raw)
}

func (c *serverConn) hello(interval time.Duration) {
	c.send(opHello, map[string]int{"heartbeat_interval": int(interval / time.Millisecond)}, 0, "")
}

func (c *serverConn) closeWith(code CloseCode) {
	c.writeFrame(true, opClose, binary.BigEndian.AppendUint16(nil, uint16(code)))
}

type sent struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
}

// expect reads until a gateway payload with this op, answering nothing and skipping
// heartbeats unless a heartbeat is what it waits for.
func (c *serverConn) expect(op int) sent {
	c.t.Helper()
	for {
		f, err := c.readFrame()
		if err != nil {
			c.t.Fatalf("waiting for op %d: %v", op, err)
		}
		if f.op != opText {
			continue
		}
		var p sent
		if err := json.Unmarshal(f.payload, &p); err != nil {
			c.t.Fatalf("the client sent %q", f.payload)
		}
		if p.Op == op {
			return p
		}
		if p.Op != opHeartbeat {
			c.t.Fatalf("waiting for op %d, the client sent %s", op, f.payload)
		}
	}
}

// expectClose reads until the client's close frame, and returns its code.
func (c *serverConn) expectClose() CloseCode {
	c.t.Helper()
	for {
		f, err := c.readFrame()
		if err != nil {
			c.t.Fatalf("waiting for a close: %v", err)
		}
		if f.op == opClose {
			if len(f.payload) < 2 {
				return CloseNoStatus
			}
			return CloseCode(binary.BigEndian.Uint16(f.payload))
		}
	}
}

func (c *serverConn) ready(session string, resumeURL string, seq int64) {
	c.send(opDispatch, map[string]any{
		"session_id":         session,
		"resume_gateway_url": resumeURL,
		"user":               map[string]any{"id": "1562103296259522700", "username": "mochi", "bot": true},
		"guilds":             []map[string]any{{"id": "1220378778665095291", "unavailable": true}},
	}, seq, "READY")
}

const token = "MTIz.secret-bot-token"

const intents = discord.IntentsGuildMessages | discord.IntentsMessageContent

// A session against the fake, with waits short enough for a test and every handler
// reporting on a channel.
type harness struct {
	s        *Session
	g        *fakeGateway
	readies  chan *discord.Ready
	messages chan *discord.Message
	opened   chan error

	mu         sync.Mutex
	identifies int
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		g:        newFakeGateway(t),
		readies:  make(chan *discord.Ready, 4),
		messages: make(chan *discord.Message, 16),
		opened:   make(chan error, 1),
	}
	h.s = New(token, intents)
	h.s.URL, h.s.TLS = h.g.url(), h.g.tlsConfig()
	h.s.Backoff, h.s.MaxBackoff = 5*time.Millisecond, 20*time.Millisecond
	h.s.InvalidWait = func() time.Duration { return 5 * time.Millisecond }
	h.s.BeforeIdentify = func() {
		h.mu.Lock()
		h.identifies++
		h.mu.Unlock()
	}
	h.s.Handlers.Ready = func(r *discord.Ready) { h.readies <- r }
	h.s.Handlers.MessageCreate = func(m *discord.Message) { h.messages <- m }
	t.Cleanup(func() { h.s.Close() })
	return h
}

func (h *harness) open() { go func() { h.opened <- h.s.Open() }() }

func (h *harness) openErr(t *testing.T) error {
	t.Helper()
	select {
	case err := <-h.opened:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Open did not return")
		return nil
	}
}

// connected opens the session through a whole identify, READY at seq 1, with heartbeats
// every interval.
func (h *harness) connected(t *testing.T, interval time.Duration) *serverConn {
	t.Helper()
	h.open()
	c := h.g.next()
	c.hello(interval)
	c.expect(opIdentify)
	c.ready("s1", h.g.url()+"/resume", 1)
	if err := h.openErr(t); err != nil {
		t.Fatal(err)
	}
	return c
}

func (h *harness) message(t *testing.T) *discord.Message {
	t.Helper()
	select {
	case m := <-h.messages:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no message was delivered")
		return nil
	}
}

func (h *harness) identified() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.identifies
}

func hasQuery(c *serverConn) bool {
	return strings.Contains(c.query, "v=10") && strings.Contains(c.query, "encoding=json")
}
