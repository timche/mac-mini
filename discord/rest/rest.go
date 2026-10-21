// Package rest is a client for Discord's REST API v10: the requests, the rate limits Discord
// asks every client to keep, and errors that never carry the token.
package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/timche/mac-mini/discord"
)

const API = "https://discord.com/api/v10"

// Discord asks for a User-Agent naming the library and a URL for it.
const userAgent = "DiscordBot (https://github.com/timche/mac-mini, 1)"

// The token reaches one request header and nothing else, and every error text that leaves
// here has it taken out first, since what calls this logs and posts errors.
type Client struct {
	Token string

	// Where the API is, and the clock and sleep the rate limits are kept on, so a test drives
	// every call against a server of its own.
	API   string
	HTTP  *http.Client
	Now   func() time.Time
	Sleep func(time.Duration)

	mu      sync.Mutex
	routes  map[string]string // route → Discord's bucket hash
	buckets map[string]*bucket
	global  time.Time // nothing is sent before this
}

type bucket struct {
	remaining int
	reset     time.Time
}

func New(token string) *Client {
	return &Client{
		Token: token,
		API:   API,
		HTTP:  &http.Client{Timeout: 20 * time.Second},
		Now:   time.Now,
		Sleep: time.Sleep,
	}
}

// Error is Discord's answer to a request it refused, with its JSON error code when it gave
// one.
type Error struct {
	Method  string
	Path    string
	Status  int
	Code    int
	Message string
}

func (e *Error) Error() string {
	text := fmt.Sprintf("Discord answered %d to %s %s", e.Status, e.Method, e.Path)
	if e.Message != "" {
		text += fmt.Sprintf(": %s (%d)", e.Message, e.Code)
	}
	return text
}

// JSON error codes a caller tells apart.
const (
	CodeUnknownChannel     = 10003
	CodeMissingAccess      = 50001
	CodeMissingPermissions = 50013
)

// RateLimitError is a request given up on because Discord said to wait: a 429, or a bucket
// already spent, on a request made with NoWait, or a 429 that kept coming back.
type RateLimitError struct {
	Method     string
	Path       string
	RetryAfter time.Duration
	Global     bool
}

func (e *RateLimitError) Error() string {
	scope := "route"
	if e.Global {
		scope = "global"
	}
	return fmt.Sprintf("Discord's %s rate limit on %s %s, for another %s", scope, e.Method, e.Path, e.RetryAfter.Round(time.Millisecond))
}

// Option changes how one request is made.
type Option func(*options)

type options struct{ noWait bool }

// NoWait gives a request up with a RateLimitError where it would otherwise wait out a rate
// limit, for a limit long enough that the caller would rather schedule the retry itself.
func NoWait() Option { return func(o *options) { o.noWait = true } }

// How many 429s a request waits out before it is given up on, and how many 502s it is sent
// again after: Discord answers both while it is struggling, and neither for ever.
const (
	limitRetries   = 5
	gatewayRetries = 3
)

type request struct {
	method, path string
	contentType  string
	body         []byte
}

func (c *Client) jsonRequest(method, path string, body any) (request, error) {
	r := request{method: method, path: path}
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return r, err
		}
		r.body, r.contentType = payload, "application/json"
	}
	return r, nil
}

// do sends a request and decodes Discord's answer into out, when out is not nil.
func (c *Client) do(r request, out any, opts ...Option) error {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	route := routeOf(r.method, r.path)
	shown := shownPath(r.path)

	for attempt, bad := 0, 0; ; {
		if wait, global := c.wait(route); wait > 0 {
			if o.noWait {
				return &RateLimitError{Method: r.method, Path: shown, RetryAfter: wait, Global: global}
			}
			c.Sleep(wait)
			continue
		}

		resp, body, err := c.send(r)
		if err != nil {
			return c.scrub(err)
		}
		c.observe(route, resp)

		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			wait, global := c.limited(resp, body)
			if o.noWait || attempt >= limitRetries {
				return &RateLimitError{Method: r.method, Path: shown, RetryAfter: wait, Global: global}
			}
			attempt++
			c.Sleep(wait)
			continue
		case resp.StatusCode == http.StatusBadGateway && bad < gatewayRetries:
			bad++
			continue
		case resp.StatusCode < 200 || resp.StatusCode > 299:
			e := &Error{Method: r.method, Path: shown, Status: resp.StatusCode}
			var answer struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			}
			if json.Unmarshal(body, &answer) == nil {
				e.Code, e.Message = answer.Code, c.redact(answer.Message)
			}
			return e
		}

		if out == nil || len(body) == 0 {
			return nil
		}
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("Discord's answer to %s %s did not parse: %w", r.method, shown, err)
		}
		return nil
	}
}

func (c *Client) send(r request) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(context.Background(), r.method, c.API+r.path, bytes.NewReader(r.body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bot "+c.Token)
	req.Header.Set("User-Agent", userAgent)
	if r.contentType != "" {
		req.Header.Set("Content-Type", r.contentType)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, nil, err
	}
	return resp, body, nil
}

// How long a request on this route has to wait first, and whether for the global limit.
func (c *Client) wait(route string) (time.Duration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.Now()
	if now.Before(c.global) {
		return c.global.Sub(now), true
	}
	b := c.buckets[c.routes[route]]
	if b == nil || b.remaining > 0 || !now.Before(b.reset) {
		if b != nil && b.remaining > 0 {
			// Spent locally until Discord's answer says otherwise, so requests sent at once
			// on one route do not all take the last one.
			b.remaining--
		}
		return 0, false
	}
	return b.reset.Sub(now), false
}

// What every answer says about the bucket its route is in.
func (c *Client) observe(route string, resp *http.Response) {
	hash := resp.Header.Get("X-RateLimit-Bucket")
	remaining, errR := strconv.Atoi(resp.Header.Get("X-RateLimit-Remaining"))
	after, errA := strconv.ParseFloat(resp.Header.Get("X-RateLimit-Reset-After"), 64)
	if hash == "" || errR != nil || errA != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.routes == nil {
		c.routes, c.buckets = map[string]string{}, map[string]*bucket{}
	}
	c.routes[route] = hash
	c.buckets[hash] = &bucket{remaining: remaining, reset: c.Now().Add(seconds(after))}
}

// A 429's wait, from the body, which has sub-second resolution, or the Retry-After header.
// A global one holds every request back, not only this route's.
func (c *Client) limited(resp *http.Response, body []byte) (time.Duration, bool) {
	var answer struct {
		RetryAfter float64 `json:"retry_after"`
		Global     bool    `json:"global"`
	}
	json.Unmarshal(body, &answer)
	wait := seconds(answer.RetryAfter)
	if wait <= 0 {
		if header, err := strconv.ParseFloat(resp.Header.Get("Retry-After"), 64); err == nil {
			wait = seconds(header)
		}
	}
	if wait <= 0 {
		wait = time.Second
	}
	global := answer.Global || resp.Header.Get("X-RateLimit-Global") == "true"
	if global {
		c.mu.Lock()
		c.global = c.Now().Add(wait)
		c.mu.Unlock()
	}
	return wait, global
}

func seconds(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

var (
	snowflake = regexp.MustCompile(`^[0-9]{15,21}$`)

	// The segment after an interaction's id is its token, which is no business of a log.
	interactionToken = regexp.MustCompile(`^(/interactions/[^/]+/)[^/]+`)
)

// A rate limit bucket is per route, where a route is the path with every id but the major
// one — the channel, the guild, the interaction — taken out; and a reaction's emoji with it.
func routeOf(method, path string) string {
	path, _, _ = strings.Cut(path, "?")
	parts := strings.Split(path, "/")
	for i := 1; i < len(parts); i++ {
		switch prev := parts[i-1]; {
		case prev == "reactions":
			parts[i] = "{emoji}"
		case i >= 2 && parts[i-2] == "interactions":
			parts[i] = "{token}"
		case prev == "channels" || prev == "guilds" || prev == "interactions":
		case snowflake.MatchString(parts[i]):
			parts[i] = "{id}"
		}
	}
	return method + " " + strings.Join(parts, "/")
}

func shownPath(path string) string {
	path, _, _ = strings.Cut(path, "?")
	return interactionToken.ReplaceAllString(path, "$1<token>")
}

func (c *Client) redact(text string) string { return discord.Redact(text, c.Token) }

// net/http names the URL it failed on, which holds no secret here, but an error that came
// back with a header echoed into it would.
func (c *Client) scrub(err error) error { return errors.New(c.redact(err.Error())) }

// A message with files is multipart: its JSON as payload_json, then each file as files[n].
func multipartBody(payload any, files []*discord.File) (request, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	body, err := json.Marshal(payload)
	if err != nil {
		return request{}, err
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="payload_json"`)
	h.Set("Content-Type", "application/json")
	part, err := w.CreatePart(h)
	if err != nil {
		return request{}, err
	}
	part.Write(body)

	for i, f := range files {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="files[%d]"; filename="%s"`, i, quoteEscaper.Replace(f.Name)))
		contentType := f.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		h.Set("Content-Type", contentType)
		part, err := w.CreatePart(h)
		if err != nil {
			return request{}, err
		}
		if _, err := io.Copy(part, f.Reader); err != nil {
			return request{}, err
		}
	}
	if err := w.Close(); err != nil {
		return request{}, err
	}
	return request{contentType: w.FormDataContentType(), body: buf.Bytes()}, nil
}

var quoteEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`)
