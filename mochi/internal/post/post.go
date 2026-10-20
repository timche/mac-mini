// Package post is `mochi post`, which a session runs to tell its own Discord thread, in plain
// language, what it found or finished, with images or a recording as the proof.
package post

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/chat"
	"github.com/timche/mac-mini/mochi/internal/mark"
	"github.com/timche/mac-mini/mochi/internal/state"
)

// Discord's limit is 2,000 characters, so a longer post goes as several, split between lines.
const chunk = 1900

// Discord's own limits on one message: ten attachments, and on a server without boosts ten
// megabytes a file. Checked here so a post fails with a reason rather than Discord's refusal.
const (
	MaxFiles    = 10
	MaxFileSize = 10 << 20
)

// A post is read in Discord by moderators, so it carries no link and no file path: anything
// worth showing is an attachment. Refused here rather than trusted to the orders.
//
// Links are anything with a scheme, mailto: and file:, www., and a bare domain on a TLD Discord
// turns into a link by itself; paths are ~/…, anything starting at /, and a relative path that
// ends in a file name. "3/4", "and/or", "a/b", "e.g." and "v1.2" are none of those.
var (
	links = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://\S*`),
		regexp.MustCompile(`(?i)\b(?:mailto|file):\S+`),
		regexp.MustCompile(`(?i)\bwww\.\S+`),
	}
	domain = regexp.MustCompile(`(?i)\b(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+(?:com|org|net|io|dev|gg|app|co|me|xyz|ai|so|sh|tv|ly|to)\b(?:/\S*)?`)

	// Naming the product and FACEIT is how a moderator would say it, so these two, and their
	// subdomains, may stand in a post as names; with a path after them they are links again.
	named = []string{"repeek.gg", "faceit.com"}
	paths = []*regexp.Regexp{
		regexp.MustCompile(`(?:^|[\s"'(\x60])(~/\S*)`),
		regexp.MustCompile(`(?:^|[\s"'(\x60])(/[A-Za-z0-9._~-]+\S*)`),
		regexp.MustCompile(`(?:^|[\s"'(\x60])([A-Za-z0-9._-]+(?:/[A-Za-z0-9._-]+)+\.[A-Za-z][A-Za-z0-9]{0,5})\b`),
	}
)

func CheckText(text string) error {
	for _, re := range links {
		if found := re.FindString(text); found != "" {
			return refusedLink(found)
		}
	}
	for _, found := range domain.FindAllString(text, -1) {
		if !isNamed(found) {
			return refusedLink(found)
		}
	}
	for _, re := range paths {
		if found := re.FindStringSubmatch(text); found != nil {
			return fmt.Errorf("a post is read in Discord by moderators, so it carries no file paths: say it in words and attach images instead (found %s)", found[1])
		}
	}
	return nil
}

func refusedLink(found string) error {
	return fmt.Errorf("a post is read in Discord by moderators, so it carries no links: say it in words and attach images instead (found %s)", found)
}

func isNamed(host string) bool {
	host = strings.ToLower(host)
	for _, name := range named {
		if host == name || strings.HasSuffix(host, "."+name) {
			return true
		}
	}
	return false
}

func CheckFiles(files []string) error {
	if len(files) > MaxFiles {
		return fmt.Errorf("a post takes at most %d files, not %d", MaxFiles, len(files))
	}
	for _, file := range files {
		info, err := os.Stat(file)
		switch {
		case err != nil || !info.Mode().IsRegular():
			return fmt.Errorf("%s is not a file", file)
		case info.Size() > MaxFileSize:
			return fmt.Errorf("%s is over Discord's %d MB for one file", file, MaxFileSize>>20)
		}
	}
	return nil
}

// Post is one `mochi post`.
type Post struct {
	Text  string
	Files []string

	// A state the post puts the session in: ❓ for a result or a product question that needs
	// an answer from Tim or the team,
	// ✅ for a task done with nothing left for him; empty changes nothing.
	Settles string

	// A pull request, the one link a post may carry, which mochi adds as a line of its own.
	PR string

	// The kind of work a forum post is refiled as; a post with a type needs no text.
	Type string
}

var pullRequest = regexp.MustCompile(`\Ahttps://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/pull/([0-9]+)/?\z`)

// The line a pull request gets: its number and its link, as `#<number> (<url>)`.
func PRLine(url string) (string, error) {
	m := pullRequest.FindStringSubmatch(url)
	if m == nil {
		return "", fmt.Errorf("--pr takes a GitHub pull request's address, https://github.com/<owner>/<repo>/pull/<number>, not %s", url)
	}
	return "#" + m[2] + " (" + url + ")", nil
}

// Repos answers which "owner/repo" a server's sessions work in, so a pull request of any
// other repository cannot be posted as one of theirs.
type Repos func(guild string) (string, error)

func checkRepo(url, guild string, repos Repos) error {
	if repos == nil {
		return errors.New("there is no repository to check the pull request against")
	}
	want, err := repos(guild)
	if err != nil {
		return err
	}
	if got := pullRequest.FindStringSubmatch(url)[1]; !strings.EqualFold(got, want) {
		return fmt.Errorf("the pull request is in %s, not %s, which is this session's repository", got, want)
	}
	return nil
}

func (p Post) Check() error {
	if err := CheckText(p.Text); err != nil {
		return err
	}
	if p.PR != "" {
		if _, err := PRLine(p.PR); err != nil {
			return err
		}
	}
	return CheckFiles(p.Files)
}

// What the session was working on shows the post's state once it is up. A pull request's
// line is added after the text, with embeds suppressed so Discord shows no preview of it.
func Send(api chat.API, store state.Store, marks *mark.Marker, repos Repos, thread string, p Post) error {
	rec, err := store.Session(thread)
	if err != nil {
		return err
	}
	if err := p.Check(); err != nil {
		return err
	}
	if p.PR != "" {
		if err := checkRepo(p.PR, chat.GuildOf(api, rec), repos); err != nil {
			return err
		}
	}
	if p.Type != "" {
		if marks == nil {
			return errors.New("there is nothing to change the post's type with")
		}
		if err := marks.Typed(rec.Thread, p.Type); err != nil {
			return err
		}
	}
	text := p.Text
	if p.PR != "" {
		line, _ := PRLine(p.PR)
		text = strings.TrimRight(text, "\n") + "\n" + line
	}
	if strings.TrimSpace(text) == "" && len(p.Files) == 0 {
		if p.Settles != "" && marks != nil {
			marks.Settled(rec.Thread, p.Settles)
		}
		return nil
	}

	parts := split(text)
	for i, part := range parts {
		msg := chat.Quiet(part)
		if p.PR != "" {
			msg.Flags |= discordgo.MessageFlagsSuppressEmbeds
		}
		if i == len(parts)-1 {
			for _, file := range p.Files {
				f, err := os.Open(file)
				if err != nil {
					return err
				}
				defer f.Close()
				msg.Files = append(msg.Files, &discordgo.File{Name: filepath.Base(file), Reader: f})
			}
		}
		if _, err := api.Send(rec.Thread, msg); err != nil {
			return err
		}
	}
	if p.Settles != "" && marks != nil {
		marks.Settled(rec.Thread, p.Settles)
	}
	return nil
}

func split(text string) []string {
	var parts []string
	for len(text) > chunk {
		cut := strings.LastIndex(text[:chunk], "\n")
		if cut <= 0 {
			cut = chunk
			for cut > 0 && !utf8Start(text[cut]) {
				cut--
			}
		}
		parts = append(parts, strings.TrimRight(text[:cut], "\n"))
		text = strings.TrimLeft(text[cut:], "\n")
	}
	return append(parts, strings.TrimRight(text, "\n"))
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }
