package shibuya

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// The client itself: the two files it reads, the one request it makes, and what it does
// with a token it should not use. What a whole sweep tells it is asserted where the sweep
// is.

type switchServer struct {
	paths   []string
	auth    []string
	bodies  []map[string]any
	status  int
	delay   time.Duration
	server  *httptest.Server
	tokenAt string
}

func newSwitchServer(t *testing.T) *switchServer {
	t.Helper()
	s := &switchServer{status: http.StatusOK}

	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.delay > 0 {
			time.Sleep(s.delay)
		}

		s.paths = append(s.paths, r.URL.Path)
		s.auth = append(s.auth, r.Header.Get("Authorization"))

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		s.bodies = append(s.bodies, body)

		w.WriteHeader(s.status)
	}))
	t.Cleanup(s.server.Close)

	return s
}

// The config and the token as they are on the Mac: a URL in a file that is in the checkout,
// and a token in a file at 0600 that is not.
func switchFiles(t *testing.T, url, token string, mode os.FileMode) config.Config {
	t.Helper()
	dir := t.TempDir()

	cfg := config.Config{
		Host:         "mac-mini",
		SwitchConfig: filepath.Join(dir, "shibuya"),
		SwitchToken:  filepath.Join(dir, "shibuya-token"),
	}

	if url != "" {
		body := "# shibuya, the dead man's switch\n" + url + "\n"
		if err := os.WriteFile(cfg.SwitchConfig, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if token != "" {
		if err := os.WriteFile(cfg.SwitchToken, []byte(token), mode); err != nil {
			t.Fatal(err)
		}
	}
	return cfg
}

// An httptest server answers on http, and the config file deliberately takes https alone,
// so the URL reaches the client through the seam the config is read by.
func switchAt(t *testing.T, server *switchServer, token string, mode os.FileMode) Client {
	t.Helper()

	client := New(switchFiles(t, "https://shibuya.test", token, mode))
	client.readURL = func(string) (string, error) { return server.server.URL, nil }
	return client
}

func TestTheCheckInCarriesTheHostTheReadingAndTheToken(t *testing.T) {
	server := newSwitchServer(t)

	state, line := switchAt(t, server, "deadbeef\n", 0o600).
		Send(Checkin{FreeGB: 787, OpenIncidents: 0, HotProcesses: 2, Display: "Mac mini"})

	harness.Equal(t, state, Sent, "the switch state")
	harness.Equal(t, line, "", "the line logged")
	harness.Equal(t, len(server.paths), 1, "requests")
	harness.Equal(t, server.paths[0], "/ping", "the path")
	// Trimmed, because a token file somebody rotated by hand holds a newline and the Worker
	// compares what it is sent byte for byte.
	harness.Equal(t, server.auth[0], "Bearer deadbeef", "the header")
	harness.Equal(t, server.bodies[0]["host"], "mac-mini", "the host")
	harness.Equal(t, server.bodies[0]["free_gb"], 787.0, "the free space")
	harness.Equal(t, server.bodies[0]["hot_processes"], 2.0, "the hot processes")
	// shibuya writes the message about a Mac it cannot reach, so what to call this one has to
	// travel with the check-in; without it shibuya falls back to the host slug.
	harness.Equal(t, server.bodies[0]["display"], "Mac mini", "the display name")
}

// A display name that is not there, or that cleans away to nothing, is left out of the body
// altogether: shibuya reads an absent one as "use the slug", and a key holding an empty
// string says the same thing less clearly.
func TestACheckInWithNoDisplayNameOmitsTheField(t *testing.T) {
	server := newSwitchServer(t)
	client := switchAt(t, server, "deadbeef", 0o600)

	for _, display := range []string{"", "   ", "\x00\x01"} {
		client.Send(Checkin{FreeGB: 787, Display: display})

		body := server.bodies[len(server.bodies)-1]
		if _, ok := body["display"]; ok {
			harness.Equal(t, body["display"], nil, "the display sent for "+strconv.Quote(display))
		}
	}
}

// What travels is what arrives: shibuya cuts a display name to forty characters, so hachiko
// does too rather than sending one it knows will be cut.
func TestALongDisplayNameIsCutToWhatShibuyaKeeps(t *testing.T) {
	server := newSwitchServer(t)

	switchAt(t, server, "deadbeef", 0o600).
		Send(Checkin{Display: strings.Repeat("m", 60)})

	display, _ := server.bodies[0]["display"].(string)
	if len(display) > 40 {
		t.Errorf("a display name of %d characters: %q", len(display), display)
	}
}

func TestAFailedSweepGoesToFailWithItsReason(t *testing.T) {
	server := newSwitchServer(t)
	client := switchAt(t, server, "deadbeef", 0o600)
	client.readURL = func(string) (string, error) { return server.server.URL + "/", nil }

	state, _ := client.Send(Checkin{Failed: true, Reason: "there was no process sample"})

	harness.Equal(t, state, Sent, "the switch state")
	harness.Equal(t, server.paths[0], "/fail", "the path")
	harness.Equal(t, server.bodies[0]["reason"], "there was no process sample", "the reason")
}

// Off until Tim has configured it, exactly as the Discord bot is: a Mac with no config file
// is one with this switched off rather than one with a broken switch.
func TestNoConfigIsOneLineAndNoRequest(t *testing.T) {
	server := newSwitchServer(t)
	cfg := switchFiles(t, "", "deadbeef", 0o600)

	state, line := New(cfg).Send(Checkin{})

	harness.Equal(t, state, Unset, "the switch state")
	harness.Wants(t, line, "there is no shibuya to check in with")
	harness.Equal(t, len(server.paths), 0, "requests")
}

func TestAConfigThatIsNotAnHTTPSURLIsRefused(t *testing.T) {
	cfg := switchFiles(t, "http://shibuya.timche.dev", "deadbeef", 0o600)

	state, line := New(cfg).Send(Checkin{})

	harness.Equal(t, state, Unset, "the switch state")
	harness.Wants(t, line, "does not hold an https URL")
}

func TestNoTokenIsOneLineAndNoRequest(t *testing.T) {
	server := newSwitchServer(t)

	state, line := switchAt(t, server, "", 0o600).Send(Checkin{})

	harness.Equal(t, state, NoToken, "the switch state")
	harness.Wants(t, line, "there is no ping token")
	harness.Equal(t, len(server.paths), 0, "requests")
}

// A token every session on this Mac can read is a token every session has, and the fix is
// one chmod rather than a rotation — so it is refused and named rather than used.
func TestATokenAnyoneCanReadIsRefused(t *testing.T) {
	server := newSwitchServer(t)

	state, line := switchAt(t, server, "deadbeef", 0o644).Send(Checkin{})

	harness.Equal(t, state, TokenPublic, "the switch state")
	harness.Wants(t, line, "mode 0644 rather than 0600")
	harness.Equal(t, len(server.paths), 0, "requests")
}

func TestAWorkerThatRefusesTheCheckInSaysSoWithoutTheToken(t *testing.T) {
	server := newSwitchServer(t)
	server.status = http.StatusUnauthorized

	state, line := switchAt(t, server, "deadbeef", 0o600).Send(Checkin{})

	harness.Equal(t, state, Failed, "the switch state")
	harness.Wants(t, line, "shibuya answered 401")
	harness.Lacks(t, line, "deadbeef")
}

// The token is in one header and never in an error text, but net/http names what it failed
// on and nothing that leaves here may carry a secret on the strength of that.
func TestTheTokenIsTakenOutOfWhateverFails(t *testing.T) {
	cfg := switchFiles(t, "https://shibuya.invalid", "deadbeef", 0o600)

	client := New(cfg)
	client.client = &http.Client{Transport: failingTransport{token: "deadbeef"}}

	state, line := client.Send(Checkin{})

	harness.Equal(t, state, Failed, "the switch state")
	harness.Lacks(t, line, "deadbeef")
	harness.Wants(t, line, "the ping token")
}

type failingTransport struct{ token string }

func (f failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial failed with " + f.token)
}

// A sweep holds a lock the next twelve wait on, so the one thing this may not do is sit in
// a connection. Ten seconds on the Mac, and the same arithmetic with a tenth of a second
// here.
func TestACheckInThatHangsGivesUp(t *testing.T) {
	harness.Equal(t, checkinTimeout, 10*time.Second, "the check-in timeout")

	server := newSwitchServer(t)
	server.delay = 2 * time.Second

	client := switchAt(t, server, "deadbeef", 0o600)
	client.client = &http.Client{Timeout: 100 * time.Millisecond}

	start := time.Now()
	state, line := client.Send(Checkin{})
	took := time.Since(start)

	harness.Equal(t, state, Failed, "the switch state")
	harness.Wants(t, line, "shibuya did not take this check-in")
	if took > time.Second {
		t.Fatalf("a check-in that hung took %s, which is a sweep waiting on it", took)
	}
}

// A reason is a line about a sweep and ends up in a Discord post, which shibuya caps itself
// — so what travels is what arrives, and a path chosen by whatever filled the disk is one
// line rather than a conversation.
func TestAReasonIsOneLineAndClipped(t *testing.T) {
	server := newSwitchServer(t)
	client := switchAt(t, server, "deadbeef", 0o600)

	long := ""
	for len(long) < 600 {
		long += "very-long-path/"
	}

	client.Send(Checkin{Failed: true, Reason: "ps failed\nand said " + long})

	reason, _ := server.bodies[0]["reason"].(string)
	harness.Lacks(t, reason, "\n")
	if len(reason) > 300 {
		t.Fatalf("the reason was %d characters, which is more than the 300 shibuya keeps", len(reason))
	}
}

func TestTheSwitchHostIsTheNameShibuyaAccepts(t *testing.T) {
	for in, want := range map[string]string{
		"Tims-Mac-mini.fritz.box": "tims-mac-mini",
		"Tims-Mac-mini.local":     "tims-mac-mini",
		"mac-mini":                "mac-mini",
		"Tim's Mac_mini":          "tim-s-mac-mini",
		strings.Repeat("a", 40):   strings.Repeat("a", 32),
		"...":                     "mac",
	} {
		if got := switchHost(in); got != want {
			t.Errorf("switchHost(%q) = %q, want %q", in, got, want)
		}
	}
}
