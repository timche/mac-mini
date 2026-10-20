// Package post is `mochi post`, which a session runs to tell its own Discord thread, in plain
// language, what it found or finished, with images or a recording as the proof.
package post

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/chat"
	"github.com/timche/mac-mini/mochi/internal/mark"
	"github.com/timche/mac-mini/mochi/internal/origin"
	"github.com/timche/mac-mini/mochi/internal/state"
)

// Discord's limit is 2,000 characters, so a longer post goes as several, split between lines.
const chunk = 1900

// Discord's own limits on one message: ten attachments, and a file of at most 100 MB on the
// most boosted server, less on others, which is checked against the server's own limit before
// sending. Checked here so a post fails with a reason rather than Discord's refusal.
const (
	MaxFiles    = 10
	MaxFileSize = 100 << 20
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

// The proof heading of a post's template, by what is attached under it.
const (
	headingImages = "**Screenshots**"
	headingVideos = "**Videos**"
	headingBoth   = "**Screenshots and videos**"
)

var (
	imageExt = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".heic": true, ".bmp": true, ".avif": true, ".tiff": true, ".tif": true, ".svg": true, ".apng": true}
	videoExt = map[string]bool{".mp4": true, ".mov": true, ".webm": true, ".mkv": true, ".m4v": true, ".avi": true, ".ogv": true, ".wmv": true}
)

// CheckProof refuses a proof heading that does not name what is attached, so a post never
// shows videos under **Screenshots**.
func CheckProof(text string, files []string) error {
	var used string
	for _, line := range strings.Split(text, "\n") {
		switch line = strings.TrimSpace(line); line {
		case headingImages, headingVideos, headingBoth:
			used = line
		}
	}
	if used == "" {
		return nil
	}
	images, videos := false, false
	for _, f := range files {
		ext := strings.ToLower(filepath.Ext(f))
		images = images || imageExt[ext]
		videos = videos || videoExt[ext]
	}
	want := ""
	switch {
	case images && videos:
		want = headingBoth
	case images:
		want = headingImages
	case videos:
		want = headingVideos
	default:
		return fmt.Errorf("the post has a %s heading but attaches no image or video: attach them with --file, or leave the heading out", used)
	}
	if used != want {
		return fmt.Errorf("the post's proof heading is %s, but what it attaches calls for %s", used, want)
	}
	return nil
}

// Roots answers the folders a session's attachments may come from: first the checkout it works
// in, a checkout of its server's repository, then Claude Code's scratch folders for it; a post
// attaches nothing from anywhere else.
type Roots func(guild string) ([]string, error)

// A file goes up only from inside one of the session's own folders, by its real path once links
// are followed, and never from a .git: a session cannot post a key, another repository's file
// or git's configuration by naming it.
//
// It answers each file's real path, which is what is opened to send, so what was checked is
// what goes up.
func checkInside(files []string, guild string, roots Roots) ([]string, error) {
	if len(files) == 0 {
		return nil, nil
	}
	if roots == nil {
		return nil, errors.New("there is no checkout to check the files against")
	}
	bases, err := roots(guild)
	if err != nil {
		return nil, err
	}
	var real []string
	for _, b := range bases {
		if r, err := filepath.EvalSymlinks(b); err == nil {
			real = append(real, r)
		}
	}
	resolved := make([]string, 0, len(files))
	for _, file := range files {
		path, err := filepath.EvalSymlinks(file)
		if err != nil {
			return nil, fmt.Errorf("%s is not a file", file)
		}
		if path, err = filepath.Abs(path); err != nil {
			return nil, err
		}
		base, err := inside(path, real)
		if err != nil {
			return nil, fmt.Errorf("%s %v", file, err)
		}
		if err := plainFile(path); err != nil {
			return nil, fmt.Errorf("%s %v", file, err)
		}
		// What git ignores in the checkout is where keys and local settings are kept; a
		// screenshot, a recording or a build there is what a session means to post.
		if len(real) > 0 && base == real[0] && !isMedia(path) && !strings.EqualFold(filepath.Ext(path), ".zip") && origin.Ignored(base, path) {
			return nil, fmt.Errorf("%s is a file git ignores in the checkout, so it is not posted: only images, videos and zips are taken from there", file)
		}
		resolved = append(resolved, path)
	}
	return resolved, nil
}

// Names a secret is kept under, matched on a file's own name in any case.
var (
	secretPrefixes = []string{".env", "id_", "credentials"}
	secretSuffixes = []string{".pem", ".key", ".p12", ".pfx", ".keystore"}
	secretNames    = []string{".npmrc", ".netrc", ".pgpass", ".git-credentials"}
)

// A file with a secret's name, or one linked under another name somewhere else, is not posted.
func plainFile(path string) error {
	name := strings.ToLower(filepath.Base(path))
	for _, p := range secretPrefixes {
		if strings.HasPrefix(name, p) {
			return errors.New("has the name of a file secrets are kept in, so it is not posted")
		}
	}
	for _, s := range secretSuffixes {
		if strings.HasSuffix(name, s) {
			return errors.New("has the name of a file secrets are kept in, so it is not posted")
		}
	}
	for _, n := range secretNames {
		if name == n {
			return errors.New("has the name of a file secrets are kept in, so it is not posted")
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return errors.New("is not a file")
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Nlink > 1 {
		return errors.New("is linked under another name as well, so it is not posted: copy it instead")
	}
	return nil
}

func isMedia(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return imageExt[ext] || videoExt[ext]
}

// The folder of bases a path is inside, or why it is not posted.
func inside(path string, bases []string) (string, error) {
	for _, base := range bases {
		rel, err := filepath.Rel(base, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			// APFS takes .GIT for .git.
			if strings.EqualFold(part, ".git") {
				return "", errors.New("is git's own, so it is not posted")
			}
		}
		return base, nil
	}
	return "", errors.New("is outside this session's checkout and its scratch folder, so it is not posted")
}

// Within what the server lets one file be, so a file too large says so before Discord refuses.
func checkSizes(files []string, limit int64) error {
	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			return fmt.Errorf("%s is not a file", file)
		}
		if info.Size() > limit {
			return fmt.Errorf("%s is %d MB, over the %d MB this server takes for one file: leave it off, and say in the post that it is too large to attach", file, (info.Size()+(1<<20)-1)>>20, limit>>20)
		}
	}
	return nil
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

	// With --done: the session has cleaned up, and its tab may be closed.
	Close bool

	// The session's folders, one of which every attached file has to be inside; nil takes no
	// file.
	Roots Roots
}

var pullRequest = regexp.MustCompile(`\Ahttps://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/pull/([0-9]+)/?\z`)

// ParsePR is a pull request address's "owner/repo" and number.
func ParsePR(url string) (string, int, bool) {
	m := pullRequest.FindStringSubmatch(url)
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.Atoi(m[2])
	return m[1], n, err == nil
}

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
	if err := CheckProof(p.Text, p.Files); err != nil {
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
	guild := chat.GuildOf(api, rec)
	if p.PR != "" {
		if err := checkRepo(p.PR, guild, repos); err != nil {
			return err
		}
	}
	files, err := checkInside(p.Files, guild, p.Roots)
	if err != nil {
		return err
	}
	if len(files) > 0 {
		if err := checkSizes(files, chat.UploadLimit(api, guild)); err != nil {
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
		text = strings.TrimRight(text, " \t\r\n") + "\n\n" + line
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
			for _, file := range files {
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
