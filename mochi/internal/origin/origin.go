// Package origin is which GitHub repository a mapped checkout is, read from its origin remote,
// so a session can only link a pull request of the project it works in.
package origin

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/timche/mac-mini/mochi/internal/op"
)

var (
	mu    sync.Mutex
	cache = map[string]string{}

	github = regexp.MustCompile(`\A(?:https://github\.com/|ssh://git@github\.com/|git@github\.com:)([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+?)(?:\.git)?/?\z`)
)

// "owner/repo" for the checkout at path. Kept once read: a remote does not move under a session.
func Of(path string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	if repo, ok := cache[path]; ok {
		return repo, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", path, "remote", "get-url", "origin")
	cmd.Env = op.Scrub(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("the origin of %s could not be read: %v", path, err)
	}
	repo, err := Parse(strings.TrimSpace(string(out)))
	if err != nil {
		return "", err
	}
	cache[path] = repo
	return repo, nil
}

func Parse(remote string) (string, error) {
	m := github.FindStringSubmatch(remote)
	if m == nil {
		return "", fmt.Errorf("%s is not a GitHub remote", remote)
	}
	return m[1], nil
}
