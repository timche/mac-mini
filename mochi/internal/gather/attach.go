package gather

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/chat"
)

const (
	MB = 1 << 20

	// Enough for any screenshot or short recording a moderator attaches, and a bound on what
	// one session can put on a disk nobody is watching.
	PerFile      = 25 * MB
	SessionTotal = 100 * MB
)

// Attachments are downloaded rather than linked: Claude reads an image from a path, and the
// URLs Discord hands out expire.
type Downloader struct {
	API     chat.API
	Dir     string
	PerFile int64
	Total   int64
}

func SessionFiles(api chat.API, dir string) Downloader {
	return Downloader{API: api, Dir: dir, PerFile: PerFile, Total: SessionTotal}
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func (d Downloader) Fetch(msgs []*discordgo.Message) Files {
	files := Files{}
	if err := os.MkdirAll(d.Dir, 0o700); err != nil {
		for _, m := range msgs {
			for _, a := range m.Attachments {
				files[a.ID] = "skipped " + a.Filename + ": the session folder could not be made"
			}
		}
		return files
	}

	used := folderSize(d.Dir)
	for _, m := range msgs {
		for _, a := range m.Attachments {
			files[a.ID] = d.one(m, a, &used)
		}
	}
	return files
}

func (d Downloader) one(m *discordgo.Message, a *discordgo.MessageAttachment, used *int64) string {
	name := unsafeName.ReplaceAllString(filepath.Base(a.Filename), "_")
	path := filepath.Join(d.Dir, m.ID+"-"+a.ID+"-"+name)
	describe := func(size int64) string {
		return fmt.Sprintf("%s (%s, %s)", path, kind(a), human(size))
	}

	if info, err := os.Stat(path); err == nil {
		return describe(info.Size())
	}

	size := int64(a.Size)
	switch {
	case size > d.PerFile:
		return fmt.Sprintf("skipped %s (%s): over the %s limit for one file", a.Filename, human(size), human(d.PerFile))
	case *used+size > d.Total:
		return fmt.Sprintf("skipped %s (%s): the session already holds %s of its %s", a.Filename, human(size), human(*used), human(d.Total))
	}

	written, err := d.save(a.URL, path, min(d.PerFile, d.Total-*used))
	if err != nil {
		return fmt.Sprintf("skipped %s: %v", a.Filename, err)
	}
	*used += written
	return describe(written)
}

// Into a temporary name and then renamed, so a download cut short is never mistaken for a
// file already there. The limit is enforced on the bytes as well as on the size Discord
// claimed.
func (d Downloader) save(url, path string, limit int64) (int64, error) {
	body, err := d.API.Fetch(url)
	if err != nil {
		return 0, err
	}
	defer body.Close()

	tmp := path + ".part"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, io.LimitReader(body, limit+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > limit {
		err = errors.New("it was larger than Discord said, past the limit")
	}
	if err != nil {
		os.Remove(tmp)
		return 0, err
	}
	return n, os.Rename(tmp, path)
}

func folderSize(dir string) int64 {
	var total int64
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
	}
	return total
}

func kind(a *discordgo.MessageAttachment) string {
	if a.ContentType != "" {
		return a.ContentType
	}
	return "unknown type"
}

func human(n int64) string {
	switch {
	case n >= MB:
		return fmt.Sprintf("%.1f MB", float64(n)/MB)
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}
