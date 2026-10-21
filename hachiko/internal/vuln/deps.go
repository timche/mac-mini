package vuln

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// Command is one reading of what is installed. Dir because every mise call has to name the
// directory it is about — mise decides which tools are active from it, and a LaunchAgent
// starts in / — and a timeout because this runs inside the five-minute check: a tool waiting
// on a prompt would hold the sweep's lock and with it the Mac's only monitor.
type Command struct {
	Name    string
	Args    []string
	Dir     string
	Timeout time.Duration

	// Variables added to this process's environment for this one command, rather than an
	// environment of its own: what needs it is `HOMEBREW_NO_AUTO_UPDATE`, and a brew that
	// inherited nothing would have no PATH, no HOME and no prefix.
	Env []string
}

func (c Command) line() string { return strings.TrimSpace(c.Name + " " + strings.Join(c.Args, " ")) }

// Output is what came of it, and it carries the exit status rather than only whether there
// was one: `brew vulns` exits 1 when it has found something, so a reading read as a failure
// for being non-zero would be a Mac that reports nothing exactly when it has something to
// report. OK is the weaker claim that the command ran at all.
type Output struct {
	OK     bool
	Code   int
	Stdout string
	Text   string
}

// Deps is everything the check learns about this Mac, asks the advisory sources, or hands
// to the update. One function each, so every rule below is driven in a test by a table of
// command lines, recorded responses and files in a temporary directory — nothing here
// reaches the network or this machine's own tooling when it is being tested.
type Deps struct {
	Now func() time.Time
	Log io.Writer

	// NVD asks unauthenticated callers for no more than five requests in thirty seconds, so
	// its pages are read with a gap between them. A function rather than time.Sleep, so a test
	// walks that loop without waiting out the gaps.
	Sleep func(time.Duration)

	Run func(cmd Command) Output

	// The files read off this checkout rather than out of a tool: the two compiled tools'
	// `go.mod`, and the lockfile that says which npm packages the Worker is built from.
	ReadFile func(path string) ([]byte, error)

	// One GET and one POST, which is the whole of what the sources take. None of them is
	// behind a credential, so none of this goes through `op run`: a check twice an hour
	// against the service account's day would be the most expensive thing hachiko does.
	Get  func(url string) ([]byte, error)
	Post func(url string, body []byte) ([]byte, error)
}

// How long one request may take, and how long the whole pass may. The check runs inside the
// five-minute sweep and under its lock, so a source that has stopped answering may not cost
// the Mac its disk watch: every request is under one deadline for the pass, and a pass that
// runs out says which sources it never reached rather than reporting them clear.
//
// Thirty seconds for a request, measured against NVD, which is the slow one: a page of its
// CVEs with every description in it takes six seconds on a good day, and a timeout of fifteen
// reported NVD unreachable on a Mac where it had answered. Three minutes for the pass, which
// is what the four feeds and fifty OSV records really take together — and it is inside the
// sweep's own lock, so a pass this long means the next five-minute check finds the lock held
// and does nothing, eight times a day. shibuya waits a quarter of an hour for a check-in, so
// one skipped check costs nothing.
const (
	requestTimeout = 30 * time.Second
	passTimeout    = 3 * time.Minute
)

func realDeps(cfg config.Config) Deps {
	client := &http.Client{Timeout: requestTimeout}
	deadline := time.Now().Add(passTimeout)

	return Deps{
		Now:      config.ClockFromEnv(),
		Log:      os.Stdout,
		Sleep:    time.Sleep,
		Run:      run,
		ReadFile: os.ReadFile,
		Get:      func(url string) ([]byte, error) { return fetch(client, deadline, url, nil) },
		Post:     func(url string, body []byte) ([]byte, error) { return fetch(client, deadline, url, body) },
	}
}

// A body makes it a POST, which is the one source that takes one. The pass deadline is in
// the context rather than in the client, so a dozen sources that each answer slowly cannot
// add up to a sweep that never finishes.
func fetch(client *http.Client, deadline time.Time, url string, body []byte) ([]byte, error) {
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	method, reader := http.MethodGet, io.Reader(nil)
	if body != nil {
		method, reader = http.MethodPost, bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// GitHub asks every caller for its own Accept header and the others want plain JSON. Sent
	// per host rather than to all of them, because NVD sits behind a CDN that answers a 406 to
	// an Accept it does not recognise — which is what it did to GitHub's.
	accept := "application/json"
	if req.URL.Host == "api.github.com" {
		accept = "application/vnd.github+json"
	}
	req.Header.Set("Accept", accept)

	// What identifies this Mac's requests in a rate-limit answer that names nobody otherwise.
	req.Header.Set("User-Agent", "hachiko")

	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	// Capped, because what comes back is chosen by the source rather than by this Mac: a feed
	// that answers with a gigabyte would be read into the memory of a process the disk watch
	// is running inside.
	read, err := io.ReadAll(io.LimitReader(res.Body, responseLimit))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, &httpError{URL: url, Status: res.StatusCode, Body: string(read)}
	}
	return read, nil
}

// Eight megabytes, which is twice the largest answer any of these feeds has given — a day of
// NVD with every description in it — and far short of anything that would trouble a Mac.
const responseLimit = 8 << 20

// A source that answered with something other than 200, which is mostly how a rate limit
// arrives: GitHub gives an unauthenticated caller sixty requests an hour across every call,
// and says so with a 403 and a body explaining it.
type httpError struct {
	URL    string
	Status int
	Body   string
}

// The status and the first line of what it said, which is what names a rate limit; the rest
// of the body is an HTML page as often as not.
func (e *httpError) Error() string {
	return fmt.Sprintf("it answered %d: %s", e.Status, wording.Clip(oneLine(e.Body), wording.OutputLimit))
}

func run(cmd Command) Output {
	ctx, cancel := context.WithTimeout(context.Background(), cmd.Timeout)
	defer cancel()

	exe := exec.CommandContext(ctx, cmd.Name, cmd.Args...)
	exe.Dir = cmd.Dir
	if len(cmd.Env) > 0 {
		exe.Env = append(os.Environ(), cmd.Env...)
	}

	var stdout bytes.Buffer
	var both lockedBuffer
	exe.Stdout = io.MultiWriter(&stdout, &both)
	exe.Stderr = &both

	err := exe.Run()
	text := strings.TrimSpace(both.String())
	if err != nil && text == "" {
		text = err.Error()
	}

	// Started and finished, whatever it exited with. A command that could not be started at
	// all — no such tool on PATH, a timeout — is the one that is not OK, because then there is
	// no reading rather than a reading of nothing.
	out := Output{OK: exe.ProcessState != nil, Stdout: stdout.String(), Text: text}
	if exe.ProcessState != nil {
		out.Code = exe.ProcessState.ExitCode()
	}
	if ctx.Err() != nil {
		out.OK = false
	}
	return out
}

// exec copies stdout and stderr on a goroutine each and both reach this one, which a plain
// bytes.Buffer written from two at once does not survive.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
