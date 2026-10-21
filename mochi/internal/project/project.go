// Package project is what a repository says about mochi's sessions in it, kept in `.mochi/` at
// its root and read from the server's mapped checkout at each use, so a change applies once it
// is merged there and needs no restart.
package project

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/timche/mac-mini/mochi/internal/fence"
)

const (
	Dir              = ".mochi"
	ConfigFile       = Dir + "/config.json"
	InstructionsFile = Dir + "/instructions.md"

	// The most of the instructions an opening carries.
	MaxInstructions = 16 << 10

	maxConfig = 16 << 10
)

// ErrMissing is a repository without the file: every feature it would turn on is off.
var ErrMissing = errors.New("not in the repository")

type Settings struct {
	// The forum a session started from a channel gets a post in; with none, a thread on Tim's
	// message.
	Forum string `json:"forum"`

	// The channel `mochi announce` posts to, for any session in a checkout of the repository.
	Announce string `json:"announce"`

	// Domains a post may link to over https, each with its subdomains: where the team finds the
	// product, such as its store listings.
	Links []string `json:"links"`
}

var (
	snowflake = regexp.MustCompile(`\A[0-9]{17,20}\z`)
	host      = regexp.MustCompile(`\A(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z][a-z0-9-]*[a-z0-9]\z`)
)

// Check holds the settings to what a Discord request path and a link check can take, and puts
// every domain in lower case so a match needs no folding.
func (s *Settings) Check() error {
	if s.Forum != "" && !snowflake.MatchString(s.Forum) {
		return errors.New("forum is not a Discord id")
	}
	if s.Announce != "" && !snowflake.MatchString(s.Announce) {
		return errors.New("announce is not a Discord id")
	}
	for i, link := range s.Links {
		if !host.MatchString(strings.ToLower(link)) {
			return fmt.Errorf("links has %q, which is not a domain", link)
		}
		s.Links[i] = strings.ToLower(link)
	}
	return nil
}

// Load is the repository's `.mochi/config.json`. Strict, because every value ends up in a
// Discord request path or decides what a post may link to: a key mochi does not know is a typo.
func Load(repo string) (Settings, error) {
	var s Settings
	body, err := read(repo, ConfigFile, maxConfig)
	if err != nil {
		return s, err
	}
	if len(body) > maxConfig {
		return s, fmt.Errorf("%s: over %d KB", ConfigFile, maxConfig>>10)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return Settings{}, fmt.Errorf("%s: %w", ConfigFile, err)
	}
	if dec.More() {
		return Settings{}, fmt.Errorf("%s: more than one JSON object", ConfigFile)
	}
	if err := s.Check(); err != nil {
		return Settings{}, fmt.Errorf("%s: %w", ConfigFile, err)
	}
	return s, nil
}

// Instructions is the repository's `.mochi/instructions.md`, for the sessions mochi starts in
// it: cut at a line past MaxInstructions with a note saying so, and cleaned of what a terminal
// would act on.
func Instructions(repo string) (string, error) {
	return Text(repo, InstructionsFile)
}

// Text is a file of the repository's own text read the way Instructions reads one.
func Text(repo, file string) (string, error) {
	body, err := read(repo, file, MaxInstructions)
	if err != nil {
		return "", err
	}
	text := strings.ToValidUTF8(string(body), "")
	if len(body) > MaxInstructions {
		text = strings.ToValidUTF8(string(body[:MaxInstructions]), "")
		text = text[:strings.LastIndex(text, "\n")+1] + fmt.Sprintf("\n(%s goes on past %d KB; read the rest in the file)", file, MaxInstructions>>10)
	}
	return strings.TrimSpace(fence.Clean(text)), nil
}

// read is up to max+1 bytes of file in repo: a regular file, inside the repository once links
// are followed and outside git's own files, since a link could otherwise hand mochi anything
// this account can read.
func read(repo, file string, max int) ([]byte, error) {
	root, err := filepath.EvalSymlinks(repo)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, ErrMissing)
	}
	// Missing is only a path with nothing at it: a link that leads nowhere is somebody's
	// mistake, and would otherwise turn the fallback on.
	at := root
	for _, part := range strings.Split(file, "/") {
		at = filepath.Join(at, part)
		info, err := os.Lstat(at)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%s: %w", file, ErrMissing)
		} else if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			if _, err := os.Stat(at); err != nil {
				return nil, fmt.Errorf("%s is a link that leads nowhere", file)
			}
		}
	}
	path, err := filepath.EvalSymlinks(at)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("%s leads out of the repository", file)
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if strings.EqualFold(part, ".git") {
			return nil, fmt.Errorf("%s leads into git's own files", file)
		}
	}
	// Before the open, which would wait forever on a fifo.
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a file", file)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return body, nil
}
