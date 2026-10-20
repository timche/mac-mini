// Package leaks is the secret scan a post goes through before it is sent: betterleaks, the
// scanner the account's own pre-commit hook runs, over the post's text and its attachments.
package leaks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/timche/mac-mini/mochi/internal/op"
)

const timeout = 60 * time.Second

// Runner is one betterleaks run: its arguments, what it reads on stdin, and what it printed and
// exited with.
type Runner func(ctx context.Context, args []string, stdin io.Reader) (stdout []byte, exit int, err error)

// The variables that would point betterleaks at a configuration the session chose.
var configVars = []string{"BETTERLEAKS_CONFIG", "GITLEAKS_CONFIG", "BETTERLEAKS_CONFIG_TOML", "GITLEAKS_CONFIG_TOML"}

// Where Homebrew installs it, and nowhere a session's PATH could point instead.
var Locations = []string{"/opt/homebrew/bin/betterleaks", "/usr/local/bin/betterleaks"}

func binary() (string, error) {
	for _, path := range Locations {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return path, nil
		}
	}
	return "", fmt.Errorf("betterleaks is not in %s", strings.Join(Locations, " or "))
}

func CLI(ctx context.Context, args []string, stdin io.Reader) ([]byte, int, error) {
	bin, err := binary()
	if err != nil {
		return nil, 0, err
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = scrub(op.Scrub(os.Environ()))
	cmd.Stdin = stdin
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case ctx.Err() != nil:
		return nil, 0, fmt.Errorf("it took longer than %s", timeout)
	case errors.As(err, &exit):
		return stdout.Bytes(), exit.ExitCode(), nil
	case err != nil:
		return nil, 0, err
	}
	return stdout.Bytes(), 0, nil
}

func scrub(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		keep := true
		for _, v := range configVars {
			keep = keep && name != v
		}
		if keep {
			out = append(out, kv)
		}
	}
	return out
}

// Finding is what a post is refused with: the rule that matched and where, never the secret.
type Finding struct {
	RuleID string `json:"RuleID"`
	File   string `json:"File"`
}

// Scanner scans with a configuration of the project's own when it has one.
type Scanner struct {
	Run    Runner
	Config string
}

func (s Scanner) args(command string, extra ...string) []string {
	args := []string{command, "--no-banner", "--redact", "--report-format", "json", "--report-path", "-"}
	if s.Config != "" {
		args = append(args, "--config", s.Config)
	}
	return append(args, extra...)
}

// Text scans a post's text.
func (s Scanner) Text(text string) ([]Finding, error) {
	return s.scan(s.args("stdin"), strings.NewReader(text))
}

// File scans one attachment, into the archives it holds.
func (s Scanner) File(path string) ([]Finding, error) {
	return s.scan(s.args("dir", path), nil)
}

// Fail closed: anything but a clean run or one with findings is an error.
func (s Scanner) scan(args []string, stdin io.Reader) ([]Finding, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, exit, err := s.Run(ctx, args, stdin)
	switch {
	case err != nil:
		return nil, err
	case exit != 0 && exit != 1:
		return nil, fmt.Errorf("betterleaks exited %d", exit)
	}
	var found []Finding
	if err := json.Unmarshal(bytes.TrimSpace(out), &found); err != nil {
		return nil, errors.New("betterleaks answered something that is not a report")
	}
	if exit == 1 && len(found) == 0 {
		return nil, errors.New("betterleaks reported a leak but named none")
	}
	return found, nil
}
