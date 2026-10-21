package gateway

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// A client-only RFC 6455 websocket: TLS, the HTTP upgrade, and frames. No extensions, so no
// compression and every reserved bit is an error.

const (
	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA
)

// A guild's GUILD_CREATE is the largest thing Discord sends, and nothing a bot in a few
// servers receives comes close to this.
const maxMessage = 64 << 20

const handshakeTimeout = 30 * time.Second

var errProtocol = errors.New("the gateway broke the websocket protocol")

type conn struct {
	nc net.Conn
	br *bufio.Reader

	wmu       sync.Mutex
	closeSent bool
}

func dial(raw string, cfg *tls.Config) (*conn, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "wss" {
		return nil, fmt.Errorf("the gateway's address is %s, not wss", u.Scheme)
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "443")
	}
	cfg = cfg.Clone()
	if cfg == nil {
		cfg = &tls.Config{}
	}
	if cfg.ServerName == "" {
		cfg.ServerName = u.Hostname()
	}
	cfg.NextProtos = []string{"http/1.1"}

	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: handshakeTimeout}, Config: cfg}
	nc, err := d.Dial("tcp", host)
	if err != nil {
		return nil, err
	}
	nc.SetDeadline(time.Now().Add(handshakeTimeout))
	c := &conn{nc: nc, br: bufio.NewReaderSize(nc, 64<<10)}
	if err := c.upgrade(u); err != nil {
		nc.Close()
		return nil, err
	}
	nc.SetDeadline(time.Time{})
	return c, nil
}

const acceptGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

func accept(key string) string {
	sum := sha1.Sum([]byte(key + acceptGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func (c *conn) upgrade(u *url.URL) error {
	nonce := make([]byte, 16)
	rand.Read(nonce)
	key := base64.StdEncoding.EncodeToString(nonce)

	target := *u
	target.Scheme = "https"
	req := &http.Request{
		Method:     http.MethodGet,
		URL:        &target,
		Host:       u.Host,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header: http.Header{
			"Upgrade":               {"websocket"},
			"Connection":            {"Upgrade"},
			"Sec-WebSocket-Key":     {key},
			"Sec-WebSocket-Version": {"13"},
			"User-Agent":            {"DiscordBot (https://github.com/timche/mac-mini, 1)"},
		},
	}
	if err := req.Write(c.nc); err != nil {
		return err
	}
	resp, err := http.ReadResponse(c.br, req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	switch {
	case resp.StatusCode != http.StatusSwitchingProtocols:
		return fmt.Errorf("the gateway answered the upgrade with %s", resp.Status)
	case !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket"):
		return errors.New("the gateway did not upgrade to a websocket")
	case !headerHas(resp.Header, "Connection", "upgrade"):
		return errors.New("the gateway did not upgrade the connection")
	case resp.Header.Get("Sec-WebSocket-Accept") != accept(key):
		return errors.New("the gateway's websocket accept does not match the key")
	case resp.Header.Get("Sec-WebSocket-Extensions") != "":
		return errors.New("the gateway chose a websocket extension nobody asked for")
	}
	return nil
}

func headerHas(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for _, t := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(t), token) {
				return true
			}
		}
	}
	return false
}

// readMessage is the next whole text or binary message, its fragments joined. A ping is
// answered and a pong dropped on the way; a close is answered and returned as a CloseError.
func (c *conn) readMessage() ([]byte, error) {
	var msg []byte
	fragmented := false
	for {
		fin, op, payload, err := c.readFrame()
		if err != nil {
			return nil, err
		}
		switch op {
		case opPing:
			if err := c.write(opPong, payload); err != nil {
				return nil, err
			}
		case opPong:
		case opClose:
			ce := parseClose(payload)
			c.closeWith(ce.Code, "")
			return nil, ce
		case opText, opBinary:
			if fragmented {
				return nil, c.fail()
			}
			if fin {
				return payload, nil
			}
			msg, fragmented = payload, true
		case opContinuation:
			if !fragmented || len(msg)+len(payload) > maxMessage {
				return nil, c.fail()
			}
			msg = append(msg, payload...)
			if fin {
				return msg, nil
			}
		default:
			return nil, c.fail()
		}
	}
}

func (c *conn) readFrame() (fin bool, op byte, payload []byte, err error) {
	var head [2]byte
	if _, err := io.ReadFull(c.br, head[:]); err != nil {
		return false, 0, nil, err
	}
	fin, op = head[0]&0x80 != 0, head[0]&0x0F
	masked, size := head[1]&0x80 != 0, uint64(head[1]&0x7F)
	// Reserved bits mean an extension, and none was agreed; a server never masks.
	if head[0]&0x70 != 0 || masked {
		return false, 0, nil, c.fail()
	}
	switch size {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return false, 0, nil, err
		}
		size = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return false, 0, nil, err
		}
		size = binary.BigEndian.Uint64(ext[:])
	}
	if op >= opClose && (!fin || size > 125) {
		return false, 0, nil, c.fail()
	}
	if size > maxMessage {
		return false, 0, nil, c.fail()
	}
	payload = make([]byte, size)
	if _, err := io.ReadFull(c.br, payload); err != nil {
		return false, 0, nil, err
	}
	return fin, op, payload, nil
}

// Every frame a client sends is masked with a key of its own, unfragmented.
func (c *conn) write(op byte, payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closeSent {
		return net.ErrClosed
	}
	if op == opClose {
		c.closeSent = true
	}

	frame := make([]byte, 0, 14+len(payload))
	frame = append(frame, 0x80|op)
	switch n := len(payload); {
	case n <= 125:
		frame = append(frame, 0x80|byte(n))
	case n <= 0xFFFF:
		frame = append(frame, 0x80|126)
		frame = binary.BigEndian.AppendUint16(frame, uint16(n))
	default:
		frame = append(frame, 0x80|127)
		frame = binary.BigEndian.AppendUint64(frame, uint64(n))
	}
	var mask [4]byte
	rand.Read(mask[:])
	frame = append(frame, mask[:]...)
	for i, b := range payload {
		frame = append(frame, b^mask[i%4])
	}

	c.nc.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := c.nc.Write(frame)
	return err
}

// closeWith sends a close frame, unless one went already, and drops the connection.
func (c *conn) closeWith(code CloseCode, reason string) {
	var payload []byte
	// 1005 says a close frame carried no code, and may never be sent as one.
	if code != CloseNoStatus {
		payload = append(binary.BigEndian.AppendUint16(nil, uint16(code)), reason...)
	}
	c.write(opClose, payload)
	c.nc.Close()
}

func (c *conn) fail() error {
	c.closeWith(CloseProtocolError, "")
	return errProtocol
}

func parseClose(payload []byte) *CloseError {
	if len(payload) < 2 {
		return &CloseError{Code: CloseNoStatus}
	}
	return &CloseError{Code: CloseCode(binary.BigEndian.Uint16(payload)), Reason: string(payload[2:])}
}
