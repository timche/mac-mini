package sync

import (
	"fmt"
	"strings"
)

// After three paths the rest is a count: a subject naming forty files is a subject nobody
// reads to the end of, and `git log --stat` has the whole list anyway.
const named = 3

// Sentence case, so it reads like a hand-written subject in the log.
func commitSubject(files []string) string {
	for i, file := range files {
		files[i] = oneLine(file)
	}

	if len(files) <= named {
		return "Update " + strings.Join(files, ", ")
	}
	return fmt.Sprintf("Update %s and %d more", strings.Join(files[:named], ", "), len(files)-named)
}

// A path is a string of bytes macOS makes no promises about, and `-z` hands it over as it
// is rather than in git's quoted spelling — so a newline in one would otherwise be a commit
// subject that is two lines, and the second of them a body.
func oneLine(path string) string {
	var out strings.Builder
	out.Grow(len(path))

	for _, r := range path {
		if r == '\t' || (r >= 0x20 && r != 0x7f) {
			out.WriteRune(r)
		} else {
			out.WriteByte(' ')
		}
	}
	return out.String()
}
