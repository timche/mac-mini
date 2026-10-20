// Package post is `mochi post`, which a session runs to put findings, a plan, a PR link or a
// screenshot into its own Discord thread.
package post

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/chat"
	"github.com/timche/mac-mini/mochi/internal/state"
)

// Discord's limit is 2,000 characters, so a longer post goes as several, split between lines.
const chunk = 1900

// The thread is the one the session's tab was started with, and it has to be one mochi
// opened a session for: the variable alone would let a session post anywhere the bot can.
func Send(api chat.API, store state.Store, thread, file, text string) error {
	if thread == "" {
		return errors.New("mochi post runs only inside a session mochi started, and MOCHI_THREAD is not set")
	}
	rec, ok, err := store.Get(thread)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("MOCHI_THREAD names no thread mochi started a session for")
	}

	parts := split(text)
	for i, part := range parts {
		msg := chat.Quiet(part)
		if i == len(parts)-1 && file != "" {
			f, err := os.Open(file)
			if err != nil {
				return err
			}
			defer f.Close()
			msg.Files = []*discordgo.File{{Name: filepath.Base(file), Reader: f}}
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
