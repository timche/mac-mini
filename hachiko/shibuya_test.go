package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEverySweepTellsShibuyaWhatItRead(t *testing.T) {
	f := newFixture(t)
	f.freeGB = 787
	f.grow("tmp/worker.log", 3*mb)

	equal(t, f.sweep(), "", "a quiet check's log")

	equal(t, len(f.checkins), 1, "check-ins")
	equal(t, f.checkins[0].Failed, false, "whether the sweep reported a fault")
	equal(t, f.checkins[0].FreeGB, 787.0, "the free space reported")
	equal(t, f.checkins[0].OpenIncidents, 0, "the open incidents reported")
	equal(t, f.checkins[0].HotProcesses, 0, "the hot processes reported")
}

// An incident being worked is part of what the Mac is doing, so a recovery message says
// what it came back to rather than only that it came back.
func TestAnOpenIncidentIsCounted(t *testing.T) {
	f := newFixture(t)
	f.grow("tmp/worker.log", 3*mb)
	f.at(0).sweep()

	f.grow("tmp/worker.log", 4*mb)
	f.at(300).sweep()

	equal(t, f.checkins[len(f.checkins)-1].OpenIncidents, 1, "the open incidents reported")
}

// The three faults of the watch rather than of the Mac. Each of them is a sweep that ran
// and came back knowing less than it should, which is exactly what nothing on this machine
// can report about itself.
func TestAWalkThatRanOutOfSecondsIsAFailedSweep(t *testing.T) {
	f := newFixture(t)
	f.cutShort = true

	f.sweep()

	equal(t, f.checkins[0].Failed, true, "whether the sweep reported a fault")
	wants(t, f.checkins[0].Reason, "saw only part of the disk")
}

func TestNoProcessSampleIsAFailedSweep(t *testing.T) {
	f := newFixture(t)
	f.procsErr = errors.New("ps: no such process")

	f.sweep()

	equal(t, f.checkins[0].Failed, true, "whether the sweep reported a fault")
	wants(t, f.checkins[0].Reason, "no process sample")
}

func TestAnUnreadableStateIsAFailedSweep(t *testing.T) {
	f := newFixture(t)
	if err := os.MkdirAll(f.cfg.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.cfg.StateDir, "state.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	f.sweep()

	equal(t, f.checkins[0].Failed, true, "whether the sweep reported a fault")
	wants(t, f.checkins[0].Reason, "the state file could not be read")
}

// Two faults in one sweep are one check-in, because they are one sweep.
func TestTwoFaultsAreOneReason(t *testing.T) {
	f := newFixture(t)
	f.cutShort = true
	f.procsErr = errors.New("ps: no such process")

	f.sweep()

	equal(t, len(f.checkins), 1, "check-ins")
	wants(t, f.checkins[0].Reason, "saw only part of the disk")
	wants(t, f.checkins[0].Reason, "no process sample")
}

// A dry run changes nothing anywhere, and a check-in is a change at the other end: a Mac
// told it is being watched on a run that watched nothing.
func TestADryRunChecksInWithNothing(t *testing.T) {
	f := newFixture(t)
	f.grow("tmp/worker.log", 3*mb)

	out := f.dryRun()

	equal(t, len(f.checkins), 0, "check-ins")
	wants(t, out, "would check in with shibuya")
}

func TestADryRunSaysWhatItWouldReportAsAFault(t *testing.T) {
	f := newFixture(t)
	f.cutShort = true

	out := f.dryRun()

	equal(t, len(f.checkins), 0, "check-ins")
	wants(t, out, "would tell shibuya this sweep did not finish its job")
}

// This runs every five minutes for ever. Twelve lines an hour about a token that is still
// missing would bury the lines that matter, so the state is what decides whether anything
// is said at all.
func TestTheSwitchIsSaidOncePerStateChange(t *testing.T) {
	f := newFixture(t)
	f.checkinState = checkinNoToken
	f.checkinSay = "there is no ping token, so nothing is watching hachiko"

	wants(t, f.sweep(), "there is no ping token")
	equal(t, f.sweep(), "", "the second check's log")

	f.checkinState, f.checkinSay = checkinSent, ""
	wants(t, f.sweep(), "shibuya is hearing from this Mac again")
	equal(t, f.sweep(), "", "the check after that one")
}

// The first sweep on a fresh Mac is a state change too, and the one thing it should not do
// is announce that a switch nothing has ever used is working.
func TestAFirstCheckInSaysNothing(t *testing.T) {
	f := newFixture(t)

	equal(t, f.sweep(), "", "the first check's log")
	equal(t, f.state().Switch, checkinSent, "the remembered switch state")
}

// Everything below is the client rather than the sweep: the two files it reads, the one
// request it makes, and what it does with a token it should not use.

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
func switchFiles(t *testing.T, url, token string, mode os.FileMode) Config {
	t.Helper()
	dir := t.TempDir()

	cfg := Config{
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
func switchAt(t *testing.T, server *switchServer, token string, mode os.FileMode) switchClient {
	t.Helper()

	client := newSwitch(switchFiles(t, "https://shibuya.test", token, mode))
	client.readURL = func(string) (string, error) { return server.server.URL, nil }
	return client
}

func TestTheCheckInCarriesTheHostTheReadingAndTheToken(t *testing.T) {
	server := newSwitchServer(t)

	state, line := switchAt(t, server, "deadbeef\n", 0o600).
		send(Checkin{FreeGB: 787, OpenIncidents: 0, HotProcesses: 2})

	equal(t, state, checkinSent, "the switch state")
	equal(t, line, "", "the line logged")
	equal(t, len(server.paths), 1, "requests")
	equal(t, server.paths[0], "/ping", "the path")
	// Trimmed, because a token file somebody rotated by hand holds a newline and the Worker
	// compares what it is sent byte for byte.
	equal(t, server.auth[0], "Bearer deadbeef", "the header")
	equal(t, server.bodies[0]["host"], "mac-mini", "the host")
	equal(t, server.bodies[0]["free_gb"], 787.0, "the free space")
	equal(t, server.bodies[0]["hot_processes"], 2.0, "the hot processes")
}

func TestAFailedSweepGoesToFailWithItsReason(t *testing.T) {
	server := newSwitchServer(t)
	client := switchAt(t, server, "deadbeef", 0o600)
	client.readURL = func(string) (string, error) { return server.server.URL + "/", nil }

	state, _ := client.send(Checkin{Failed: true, Reason: "there was no process sample"})

	equal(t, state, checkinSent, "the switch state")
	equal(t, server.paths[0], "/fail", "the path")
	equal(t, server.bodies[0]["reason"], "there was no process sample", "the reason")
}

// Off until Tim has configured it, exactly as the Discord bot is: a Mac with no config file
// is one with this switched off rather than one with a broken switch.
func TestNoConfigIsOneLineAndNoRequest(t *testing.T) {
	server := newSwitchServer(t)
	cfg := switchFiles(t, "", "deadbeef", 0o600)

	state, line := newSwitch(cfg).send(Checkin{})

	equal(t, state, checkinUnset, "the switch state")
	wants(t, line, "there is no shibuya to check in with")
	equal(t, len(server.paths), 0, "requests")
}

func TestAConfigThatIsNotAnHTTPSURLIsRefused(t *testing.T) {
	cfg := switchFiles(t, "http://shibuya.timche.dev", "deadbeef", 0o600)

	state, line := newSwitch(cfg).send(Checkin{})

	equal(t, state, checkinUnset, "the switch state")
	wants(t, line, "does not hold an https URL")
}

func TestNoTokenIsOneLineAndNoRequest(t *testing.T) {
	server := newSwitchServer(t)

	state, line := switchAt(t, server, "", 0o600).send(Checkin{})

	equal(t, state, checkinNoToken, "the switch state")
	wants(t, line, "there is no ping token")
	equal(t, len(server.paths), 0, "requests")
}

// A token every session on this Mac can read is a token every session has, and the fix is
// one chmod rather than a rotation — so it is refused and named rather than used.
func TestATokenAnyoneCanReadIsRefused(t *testing.T) {
	server := newSwitchServer(t)

	state, line := switchAt(t, server, "deadbeef", 0o644).send(Checkin{})

	equal(t, state, checkinTokenPublic, "the switch state")
	wants(t, line, "mode 0644 rather than 0600")
	equal(t, len(server.paths), 0, "requests")
}

func TestAWorkerThatRefusesTheCheckInSaysSoWithoutTheToken(t *testing.T) {
	server := newSwitchServer(t)
	server.status = http.StatusUnauthorized

	state, line := switchAt(t, server, "deadbeef", 0o600).send(Checkin{})

	equal(t, state, checkinFailed, "the switch state")
	wants(t, line, "shibuya answered 401")
	lacks(t, line, "deadbeef")
}

// The token is in one header and never in an error text, but net/http names what it failed
// on and nothing that leaves here may carry a secret on the strength of that.
func TestTheTokenIsTakenOutOfWhateverFails(t *testing.T) {
	cfg := switchFiles(t, "https://shibuya.invalid", "deadbeef", 0o600)

	client := newSwitch(cfg)
	client.client = &http.Client{Transport: failingTransport{token: "deadbeef"}}

	state, line := client.send(Checkin{})

	equal(t, state, checkinFailed, "the switch state")
	lacks(t, line, "deadbeef")
	wants(t, line, "the ping token")
}

type failingTransport struct{ token string }

func (f failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial failed with " + f.token)
}

// A sweep holds a lock the next twelve wait on, so the one thing this may not do is sit in
// a connection. Ten seconds on the Mac, and the same arithmetic with a tenth of a second
// here.
func TestACheckInThatHangsGivesUp(t *testing.T) {
	equal(t, checkinTimeout, 10*time.Second, "the check-in timeout")

	server := newSwitchServer(t)
	server.delay = 2 * time.Second

	client := switchAt(t, server, "deadbeef", 0o600)
	client.client = &http.Client{Timeout: 100 * time.Millisecond}

	start := time.Now()
	state, line := client.send(Checkin{})
	took := time.Since(start)

	equal(t, state, checkinFailed, "the switch state")
	wants(t, line, "shibuya did not take this check-in")
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

	client.send(Checkin{Failed: true, Reason: "ps failed\nand said " + long})

	reason, _ := server.bodies[0]["reason"].(string)
	lacks(t, reason, "\n")
	if len(reason) > 300 {
		t.Fatalf("the reason was %d characters, which is more than the 300 shibuya keeps", len(reason))
	}
}
