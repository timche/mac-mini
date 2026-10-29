// Package gateway is a bot's connection to Discord's gateway, v10 with JSON encoding: hello,
// heartbeats, identify and resume, and the dispatches a tool here handles, delivered to
// typed handlers. It reconnects by itself, resuming where Discord allows it, and gives up
// only on a close no reconnect gets past.
package gateway

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/url"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/timche/mac-mini/discord"
)

const DefaultURL = "wss://gateway.discord.gg"

const (
	opDispatch       = 0
	opHeartbeat      = 1
	opIdentify       = 2
	opResume         = 6
	opReconnect      = 7
	opInvalidSession = 9
	opHello          = 10
	opHeartbeatAck   = 11
)

// What a client closes with when it means to resume: anything but 1000 or 1001, which end
// the session, and from the range RFC 6455 leaves to applications.
const closeToResume CloseCode = 4900

// How long an identify or a resume has to bring READY or RESUMED before the connection is
// given up on.
const readyTimeout = 2 * time.Minute

// The least wait before a reconnect after a connection that never got as far as READY or
// RESUMED, so a gateway that keeps asking for a reconnect straight after hello is not
// answered in a tight loop.
const unreadyFloor = time.Second

// Each runs in a goroutine of its own, as the gateway's reader may not wait on a handler: a
// heartbeat ack it does not read in time is a connection it takes for dead.
type Handlers struct {
	Ready             func(*discord.Ready)
	MessageCreate     func(*discord.Message)
	InteractionCreate func(*discord.Interaction)
}

type Session struct {
	Token   string
	Intents discord.Intent

	// The gateway Discord named over REST; DefaultURL when empty.
	URL string
	TLS *tls.Config

	Handlers Handlers

	// Runs before every identify, the one gateway call Discord allows a bot once every five
	// seconds across all of its connections.
	BeforeIdentify func()

	Log *slog.Logger

	// The first wait before a reconnect, doubling up to MaxBackoff; and the wait before
	// identifying anew after Discord invalidated the session, which it asks to be random
	// between one and five seconds.
	Backoff     time.Duration
	MaxBackoff  time.Duration
	InvalidWait func() time.Duration

	mu      sync.Mutex
	running bool
	stop    chan struct{}
	done    chan struct{}
	link    *link

	sessionID string
	resumeURL string
	seq       int64

	ready   bool
	lastAck time.Time
	fatal   error
}

func New(token string, intents discord.Intent) *Session {
	return &Session{
		Token:       token,
		Intents:     intents,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		Backoff:     time.Second,
		MaxBackoff:  5 * time.Minute,
		InvalidWait: func() time.Duration { return time.Second + rand.N(4*time.Second) },
	}
}

var (
	ErrAlreadyOpen = errors.New("the gateway session is already open")
	errReconnect   = errors.New("Discord asked for a reconnect")
	errZombie      = errors.New("Discord stopped acknowledging heartbeats")
	errNotReady    = errors.New("Discord did not finish the handshake")
	errStopped     = errors.New("the gateway session was closed")
)

// InvalidSessionError is op 9: Discord dropped the session, and says whether it can still be
// resumed.
type InvalidSessionError struct{ Resumable bool }

func (e *InvalidSessionError) Error() string {
	return fmt.Sprintf("Discord invalidated the gateway session (resumable: %v)", e.Resumable)
}

// Open connects and identifies, and returns once Discord has sent READY, or with the error
// that stopped it — a CloseError when Discord closed the connection. Past that the session
// keeps itself connected until Close, or until a fatal close, which Err then reports.
func (s *Session) Open() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return ErrAlreadyOpen
	}
	s.running, s.fatal = true, nil
	s.sessionID, s.resumeURL, s.seq = "", "", 0
	s.stop, s.done = make(chan struct{}), make(chan struct{})
	s.mu.Unlock()

	first := make(chan error, 1)
	go s.run(first)
	if err := <-first; err != nil {
		<-s.done
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
		return err
	}
	return nil
}

// Close ends the session with a normal close, which Discord takes as the end of it.
func (s *Session) Close() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	close(s.stop)
	l := s.link
	done := s.done
	s.mu.Unlock()
	if l != nil {
		l.closeWith(CloseNormal, "")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	return nil
}

// Status is whether the connection has finished its handshake, and when Discord last
// acknowledged a heartbeat.
func (s *Session) Status() (ready bool, lastAck time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready, s.lastAck
}

// Err is the close the session gave up on, or nil while it is still trying.
func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fatal
}

func (s *Session) stopping() bool {
	select {
	case <-s.stop:
		return true
	default:
		return false
	}
}

func (s *Session) run(first chan<- error) {
	defer close(s.done)
	backoff := s.Backoff
	for {
		ready := false
		err := s.connection(func() {
			ready = true
			backoff = s.Backoff
			if first != nil {
				first <- nil
				first = nil
			}
		})
		if first != nil {
			first <- err
			return
		}
		if s.stopping() {
			return
		}

		var closed *CloseError
		var invalid *InvalidSessionError
		wait := backoff
		switch {
		case errors.As(err, &closed) && closed.Code.Fatal():
			s.mu.Lock()
			s.fatal = err
			s.mu.Unlock()
			s.Log.Error("Discord closed the gateway for good", "error", s.scrub(err))
			return
		case errors.As(err, &invalid) && !invalid.Resumable:
			s.forget()
			wait = s.InvalidWait()
		case errors.Is(err, errReconnect):
			wait = 0
		default:
			if errors.As(err, &closed) && !closed.Code.Resumable() {
				s.forget()
			}
			backoff = min(backoff*2, s.MaxBackoff)
		}
		if !ready {
			wait = max(wait, unreadyFloor)
		}
		s.Log.Warn("the gateway connection ended, so it is opened again", "error", s.scrub(err), "wait", wait.String(), "resume", s.resumable())

		select {
		case <-s.stop:
			return
		case <-time.After(wait):
		}
	}
}

func (s *Session) forget() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID, s.resumeURL, s.seq = "", "", 0
}

func (s *Session) resumable() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID != "" && s.resumeURL != ""
}

// A connection, with what its heartbeat has seen.
type link struct {
	*conn
	acked  atomic.Bool
	zombie atomic.Bool
}

type payload struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
	S  int64           `json:"s"`
	T  string          `json:"t"`
}

// connection is one websocket's life: hello, identify or resume, then dispatches until it
// ends, with why it ended.
func (s *Session) connection(onReady func()) error {
	s.mu.Lock()
	resume := s.sessionID != "" && s.resumeURL != ""
	addr := s.URL
	if resume {
		addr = s.resumeURL
	}
	s.mu.Unlock()
	if addr == "" {
		addr = DefaultURL
	}
	u, err := url.Parse(addr)
	if err != nil {
		return err
	}
	u.RawQuery = "v=10&encoding=json"

	c, err := dial(u.String(), s.TLS)
	if err != nil {
		return err
	}
	l := &link{conn: c}
	l.acked.Store(true)
	s.mu.Lock()
	if s.stopping() {
		s.mu.Unlock()
		l.closeWith(CloseNormal, "")
		return errStopped
	}
	s.link = l
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.link, s.ready = nil, false
		s.mu.Unlock()
		l.nc.Close()
	}()

	l.nc.SetReadDeadline(time.Now().Add(handshakeTimeout))
	interval, err := hello(l)
	if err != nil {
		return err
	}
	l.nc.SetReadDeadline(time.Time{})

	if resume {
		s.mu.Lock()
		d := map[string]any{"token": s.Token, "session_id": s.sessionID, "seq": s.seq}
		s.mu.Unlock()
		err = l.send(opResume, d)
	} else {
		if s.BeforeIdentify != nil {
			s.BeforeIdentify()
		}
		err = l.send(opIdentify, map[string]any{
			"token":   s.Token,
			"intents": s.Intents,
			"properties": map[string]string{
				"os":      runtime.GOOS,
				"browser": "mac-mini",
				"device":  "mac-mini",
			},
		})
	}
	if err != nil {
		return err
	}

	stopBeat := make(chan struct{})
	defer close(stopBeat)
	go s.heartbeat(l, interval, stopBeat)

	var ready atomic.Bool
	var late atomic.Bool
	timer := time.AfterFunc(readyTimeout, func() {
		if !ready.Load() {
			late.Store(true)
			l.closeWith(closeToResume, "")
		}
	})
	defer timer.Stop()
	becameReady := func() {
		ready.Store(true)
		s.mu.Lock()
		s.ready, s.lastAck = true, time.Now()
		s.mu.Unlock()
		onReady()
	}

	for {
		data, err := l.readMessage()
		switch {
		case err != nil && l.zombie.Load():
			return errZombie
		case err != nil && late.Load():
			return errNotReady
		case err != nil:
			return err
		}
		var p payload
		if err := json.Unmarshal(data, &p); err != nil {
			l.closeWith(CloseDecodeError, "")
			return fmt.Errorf("a gateway message did not parse: %w", err)
		}

		switch p.Op {
		case opDispatch:
			if p.S > 0 {
				s.mu.Lock()
				s.seq = p.S
				s.mu.Unlock()
			}
			switch p.T {
			case "READY":
				var r discord.Ready
				if err := json.Unmarshal(p.D, &r); err != nil {
					l.closeWith(CloseDecodeError, "")
					return fmt.Errorf("READY did not parse: %w", err)
				}
				s.mu.Lock()
				s.sessionID, s.resumeURL = r.SessionID, r.ResumeGatewayURL
				s.mu.Unlock()
				becameReady()
				if s.Handlers.Ready != nil {
					go s.Handlers.Ready(&r)
				}
			case "RESUMED":
				becameReady()
			default:
				s.dispatch(p.T, p.D)
			}
		case opHeartbeat:
			if err := s.beat(l); err != nil {
				return err
			}
		case opHeartbeatAck:
			l.acked.Store(true)
			s.mu.Lock()
			s.lastAck = time.Now()
			s.mu.Unlock()
		case opReconnect:
			l.closeWith(closeToResume, "")
			return errReconnect
		case opInvalidSession:
			var resumable bool
			json.Unmarshal(p.D, &resumable)
			l.closeWith(closeToResume, "")
			return &InvalidSessionError{Resumable: resumable}
		}
	}
}

func hello(l *link) (time.Duration, error) {
	data, err := l.readMessage()
	if err != nil {
		return 0, err
	}
	var p payload
	var h struct {
		HeartbeatInterval int `json:"heartbeat_interval"`
	}
	if json.Unmarshal(data, &p) != nil || p.Op != opHello || json.Unmarshal(p.D, &h) != nil || h.HeartbeatInterval <= 0 {
		l.closeWith(CloseDecodeError, "")
		return 0, errors.New("the gateway did not open with a hello")
	}
	return time.Duration(h.HeartbeatInterval) * time.Millisecond, nil
}

// The first beat comes at a random point of the first interval, as Discord asks, so clients
// that connected together do not beat together. A beat sent while the last one is still
// unacknowledged is a connection that died without saying so: it is closed, to be resumed.
func (s *Session) heartbeat(l *link, interval time.Duration, stop <-chan struct{}) {
	wait := time.Duration(rand.Float64() * float64(interval))
	for {
		select {
		case <-stop:
			return
		case <-time.After(wait):
		}
		wait = interval
		if !l.acked.Swap(false) {
			l.zombie.Store(true)
			l.closeWith(closeToResume, "")
			return
		}
		if s.beat(l) != nil {
			return
		}
	}
}

func (s *Session) beat(l *link) error {
	s.mu.Lock()
	var seq any
	if s.seq > 0 {
		seq = s.seq
	}
	s.mu.Unlock()
	return l.send(opHeartbeat, seq)
}

func (l *link) send(op int, d any) error {
	body, err := json.Marshal(map[string]any{"op": op, "d": d})
	if err != nil {
		return err
	}
	return l.write(opText, body)
}

func (s *Session) dispatch(event string, data json.RawMessage) {
	h := s.Handlers
	switch event {
	case "MESSAGE_CREATE":
		deliver(s, h.MessageCreate, data)
	case "INTERACTION_CREATE":
		deliver(s, h.InteractionCreate, data)
	}
}

// deliver decodes an event and hands it to its handler, when there is one, in a goroutine of
// its own.
func deliver[T any](s *Session, handler func(*T), data json.RawMessage) {
	if handler == nil {
		return
	}
	event := new(T)
	if err := json.Unmarshal(data, event); err != nil {
		s.Log.Warn("a gateway event did not parse, so it was dropped", "error", err)
		return
	}
	go handler(event)
}

func (s *Session) scrub(err error) string {
	return discord.Redact(err.Error(), s.Token)
}
