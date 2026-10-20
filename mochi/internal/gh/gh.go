// Package gh is the GitHub reading mochi does for a session's pull request, through the gh
// CLI and the account's own login to it, behind one Runner so a test drives it against the
// shapes the API answers in.
package gh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/timche/mac-mini/mochi/internal/op"
)

const timeout = 30 * time.Second

type Runner func(args ...string) ([]byte, error)

// gh keeps its login in its own config under HOME, which launchd gives the agent, so it needs
// nothing of the listener's environment; the bot token is taken out of what it inherits.
func CLI(args ...string) ([]byte, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, errors.New("no gh on PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Env = op.Scrub(os.Environ())
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		line, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n")
		return nil, fmt.Errorf("gh %s failed: %v %s", strings.Join(args[:min(2, len(args))], " "), err, line)
	}
	return stdout.Bytes(), nil
}

// PR is what mochi watches a pull request for.
type PR struct {
	Open, Merged bool
	Head         string
}

func Pull(run Runner, repo string, number int) (PR, error) {
	out, err := run("api", fmt.Sprintf("repos/%s/pulls/%d", repo, number))
	if err != nil {
		return PR{}, err
	}
	var r struct {
		State  string `json:"state"`
		Merged bool   `json:"merged"`
		Head   struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := json.Unmarshal(out, &r); err != nil || r.State == "" {
		return PR{}, fmt.Errorf("GitHub answered something that is not a pull request for %s#%d", repo, number)
	}
	return PR{Open: r.State == "open", Merged: r.Merged, Head: r.Head.SHA}, nil
}

// The conclusions that are a check failing rather than passing, being skipped or still going.
var failing = map[string]bool{"failure": true, "timed_out": true, "startup_failure": true}

// Failed is the names of a commit's check runs that have finished and failed, sorted.
func Failed(run Runner, repo, sha string) ([]string, error) {
	out, err := run("api", fmt.Sprintf("repos/%s/commits/%s/check-runs?per_page=100", repo, sha))
	if err != nil {
		return nil, err
	}
	var r struct {
		CheckRuns []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"check_runs"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, fmt.Errorf("GitHub answered something that is not a list of checks for %s", sha)
	}
	var names []string
	for _, c := range r.CheckRuns {
		if c.Status == "completed" && failing[c.Conclusion] {
			names = append(names, c.Name)
		}
	}
	sort.Strings(names)
	return names, nil
}
