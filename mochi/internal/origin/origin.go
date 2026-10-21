// Package origin is which GitHub repository a mapped checkout is, read from its origin remote,
// so a session can only link a pull request of the project it works in.
package origin

import (
	"context"
	"errors"
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

	github = regexp.MustCompile(`\A(?:https://github\.com/|ssh://github\.com/|git@github\.com:)([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+?)(?:\.git)?/?\z`)
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
		return "", fmt.Errorf("the origin of %s is not a GitHub remote", path)
	}
	cache[path] = repo
	return repo, nil
}

// A remote can carry credentials, https://user:token@host/…, which no match needs. Errors name
// the checkout and never the remote, so even credentials too malformed to strip cannot leak.
var userinfo = regexp.MustCompile(`\A([A-Za-z][A-Za-z0-9+.-]*://)[^/@]*@`)

var ErrNotGitHub = errors.New("not a GitHub remote")

func Parse(remote string) (string, error) {
	remote = userinfo.ReplaceAllString(remote, "$1")
	m := github.FindStringSubmatch(remote)
	if m == nil {
		return "", ErrNotGitHub
	}
	return m[1], nil
}

// Toplevel is the root of the checkout a path is in, a worktree's own root for a path in one.
func Toplevel(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "--show-toplevel")
	cmd.Env = op.Scrub(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s is in no checkout", path)
	}
	return strings.TrimSpace(string(out)), nil
}

// Match is the root of the checkout dir is in and the index of the checkout in repos with the
// same origin, so a session anywhere, a worktree included, is known by what it is a checkout of
// rather than where it is.
func Match(dir string, repos []string) (string, int, error) {
	root, err := Toplevel(dir)
	if err != nil {
		return "", 0, err
	}
	got, err := Of(root)
	if err != nil {
		return "", 0, err
	}
	for i, repo := range repos {
		if want, err := Of(repo); err == nil && strings.EqualFold(got, want) {
			return root, i, nil
		}
	}
	return "", 0, fmt.Errorf("%s is a checkout of %s, which no server is mapped to", root, got)
}

// Ignored says whether git ignores a path in the checkout at root.
func Ignored(root, path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "check-ignore", "-q", "--", path)
	cmd.Env = op.Scrub(os.Environ())
	return cmd.Run() == nil
}
