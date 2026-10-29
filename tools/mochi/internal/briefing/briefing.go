// Package briefing is the standing orders every session mochi starts is given at the top of its
// brief: the mochi plugin's briefing.md, read from this checkout at each opening, so an edit
// reaches the next session with no restart. A session cannot work without them, so a file that
// is missing, is not a file, is empty or is too large is an error rather than a brief without.
package briefing

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/timche/mac-mini/mochi/internal/fence"
)

// Past this the file is not the orders any more but something written over them, and a cut
// would drop whichever orders came last.
const Max = 32 << 10

func Read(path string) (string, error) {
	if path == "" {
		return "", errors.New("no briefing is configured")
	}
	// Non-blocking, since a plain open of a fifo waits for a writer forever; the type is then
	// checked on what was opened.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a file", path)
	}
	body, err := io.ReadAll(io.LimitReader(f, Max+1))
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	if len(body) > Max {
		return "", fmt.Errorf("%s is past %d KB", path, Max>>10)
	}
	text := strings.TrimSpace(fence.Clean(strings.ToValidUTF8(string(body), "")))
	if text == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	return text, nil
}
