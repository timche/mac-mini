// Package post is `mochi post`, which a session runs to tell its own Discord thread, in plain
// language, what it found or finished, with images or a recording as the proof.
package post

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/chat"
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
var (
	link = regexp.MustCompile(`(?i)\bhttps?://\S+|\bwww\.\S+|\bdiscord\.gg/\S+|\bdiscord(?:app)?\.com/invite/\S+`)
	path = regexp.MustCompile(`(?:^|[\s"'(\x60])(~?/[^\s/]+/\S*)`)
)

func CheckText(text string) error {
	if found := link.FindString(text); found != "" {
		return fmt.Errorf("a post is read in Discord by moderators, so it carries no links: say it in words and attach images instead (found %s)", found)
	}
	if found := path.FindStringSubmatch(text); found != nil {
		return fmt.Errorf("a post is read in Discord by moderators, so it carries no file paths: say it in words and attach images instead (found %s)", found[1])
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

func Send(api chat.API, store state.Store, thread string, files []string, text string) error {
	rec, err := store.Session(thread)
	if err != nil {
		return err
	}
	if err := CheckFiles(files); err != nil {
		return err
	}
	if err := CheckText(text); err != nil {
		return err
	}

	parts := split(text)
	for i, part := range parts {
		msg := chat.Quiet(part)
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
