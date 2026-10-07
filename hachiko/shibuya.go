package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
)

// The check-in with shibuya, which is the half of the watch that is not on this Mac.
// Nothing here watches hachiko: a LaunchAgent that never loaded, a sweep hung on a stale
// mount and a Mac that lost power are the same silence, and a watch cannot report its own.
// So every sweep says it ran, and a quarter of an hour without one is shibuya's message to
// send rather than this process's.
//
// One attempt and ten seconds. A sweep runs every five minutes and holds a lock the next
// twelve wait on, so a retry here would cost more than the ping is worth — the next sweep is
// the retry, and the grace at the other end is three of them.
const checkinTimeout = 10 * time.Second

// Checkin is what a sweep has to say about itself: the reading when it worked, and the
// reason when it ran and did not finish its job.
type Checkin struct {
	Failed bool
	Reason string

	FreeGB        float64
	OpenIncidents int
	HotProcesses  int

	// What shibuya calls this Mac in the message it writes when the check-ins stop.
	Display string
}

// The state the log is keyed on, so a Mac with no token configured says so once rather than
// every five minutes for the life of the machine.
const (
	checkinSent        = "sent"
	checkinFailed      = "failed"
	checkinUnset       = "unconfigured"
	checkinNoToken     = "no-token"
	checkinTokenPublic = "token-readable"
)

type switchClient struct {
	cfg     config.Config
	client  *http.Client
	readURL func(path string) (string, error)
	readKey func(path string) (string, error)
}

func newSwitch(cfg config.Config) switchClient {
	return switchClient{
		cfg:     cfg,
		client:  &http.Client{Timeout: checkinTimeout},
		readURL: switchURL,
		readKey: switchToken,
	}
}

// What the sweep records and the log is keyed on: the state this check-in ended in, and the
// line to say when that state is new.
func (s switchClient) send(in Checkin) (string, string) {
	target, err := s.readURL(s.cfg.SwitchConfig)
	if err != nil {
		return checkinUnset, fmt.Sprintf("there is no shibuya to check in with, so nothing is watching hachiko: %v", err)
	}

	token, err := s.readKey(s.cfg.SwitchToken)
	switch {
	case errors.Is(err, errTokenPublic):
		return checkinTokenPublic, fmt.Sprintf("%v", err)
	case err != nil:
		return checkinNoToken, fmt.Sprintf("there is no ping token, so nothing is watching hachiko: %v", err)
	}

	if err := s.post(target, token, in); err != nil {
		// The token is redacted the way the webhook is: net/http names the URL it failed on,
		// and a header value has no business in a log either way.
		return checkinFailed, fmt.Sprintf("shibuya did not take this check-in, so it may say this Mac is offline: %v",
			redactSecret(err.Error(), token, "the ping token"))
	}
	return checkinSent, ""
}

func (s switchClient) post(target, token string, in Checkin) error {
	path, body := "/ping", map[string]any{
		"host":           switchHost(s.cfg.Host),
		"free_gb":        in.FreeGB,
		"open_incidents": in.OpenIncidents,
		"hot_processes":  in.HotProcesses,
		"version":        buildVersion(),
	}
	// Left out rather than sent empty: shibuya reads an absent display as "use the slug",
	// and a key whose value cleaned away to nothing says the same thing less clearly.
	if display := safe(in.Display, displayLimit); display != "" {
		body["display"] = display
	}
	if in.Failed {
		path = "/fail"
		body["reason"] = safe(in.Reason, reasonLimit)
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(target, "/")+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}

	// Built in this process and sent in one header. Never an argument, where ps shows it to
	// every process on the machine, and never written anywhere.
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("shibuya answered %d", resp.StatusCode)
	}
	return nil
}

// A reason is a line about a sweep and goes into a Discord post, which shibuya cuts to 300
// characters of its own — so it is cut here too, and three of them are the ellipsis `safe`
// adds, so what travels is what arrives rather than three characters more.
const reasonLimit = 297

// shibuya cuts a display name to forty characters, so this cuts it to thirty-seven and the
// three `safe` adds are the ellipsis: what travels is what arrives, the same arithmetic the
// reason above is cut by.
const displayLimit = 37

var errTokenPublic = errors.New("token is readable by more than this account")

// The URL is not a secret and is in the checkout, one line and nothing else — a comment
// and the address shibuya answers on. A file that is missing is a Mac with this switched
// off rather than a Mac with a broken one.
func switchURL(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%s is not there", path)
	}

	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parsed, err := url.Parse(line)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return "", fmt.Errorf("%s does not hold an https URL", path)
		}
		return line, nil
	}
	return "", fmt.Errorf("%s names no URL", path)
}

// The one secret on this side, and the one this repo generates rather than resolves: it is
// in a file because hachiko reads it twice an hour and every `op run` counts against the
// service account's daily limit, and what it can do if it leaks is fake a check-in.
//
// Which is why a token anything but this account can read is refused rather than used: a
// file at 0644 in a home directory every session shares is a token every session has, and
// the fix is one chmod rather than a rotation.
func switchToken(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%s is not there", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s is mode %04o rather than 0600, so it was not used: %w",
			path, info.Mode().Perm(), errTokenPublic)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%s could not be read", path)
	}

	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	return token, nil
}

// shibuya names a host in [a-z0-9-]{1,32}, and macOS hands back "Tims-Mac-mini.fritz.box":
// capitals, and a search domain the router chose that changes with the network.
func switchHost(hostname string) string {
	label, _, _ := strings.Cut(strings.ToLower(hostname), ".")
	slug := strings.Trim(strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, label), "-")
	if len(slug) > 32 {
		slug = strings.Trim(slug[:32], "-")
	}
	if slug == "" {
		return "mac"
	}
	return slug
}

// Which commit of this repository the running binary was built from, which is the one thing
// shibuya can say about a Mac it cannot reach. go stamps it for a build inside a work tree
// and stamps nothing when there is no repository, so a binary built from a tarball says
// nothing rather than lying.
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}

	revision, modified := "", false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}

	if revision == "" {
		return ""
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if modified {
		return revision + "-dirty"
	}
	return revision
}
