// Package projectdocs tells Claude, at session start, that this project has working docs
// and what is in them.
//
// The global CLAUDE.md already says where a project's docs are, but a rule is only as good
// as it is remembered, and starting work without the docs means relitigating a decision
// they already record. Printing them is the difference between an instruction and a
// mechanism: stdout from a SessionStart hook is injected into the session context whether
// or not anything reads the rule.
//
// Only $PROJECT_DOCS_DIR/shared, for what applies to every project, and
// $PROJECT_DOCS_DIR/<project>, one folder per project in the separate docs repository. A
// checkout's own docs/ is the project's published documentation, not its working docs, so
// it is never looked at.
//
// Says nothing about the project when it has no docs, so the cost on a project without
// them is the shared listing and one exit.
//
// The path and the file list, and nothing beyond them. What a project's docs are, how they
// are structured and what they are good for differ per project and are its own README's to
// say; how the folder syncs is the same everywhere and is the global CLAUDE.md's. A hook
// that repeated either would be asserting something it cannot know is still true.
//
// No instruction to read them either. That is in the global CLAUDE.md, and a sentence
// saying it again on every session start costs tokens forever to restate something already
// in context.
package projectdocs

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/timche/mac-mini/claude-hooks/internal/collate"
)

// Print writes the listing and answers nothing: a hook that fails has a session waiting on
// it, so everything unreadable here — a docs root that is not there, a directory the
// account cannot open, a git that will not answer — ends the listing where it got to
// rather than reporting itself.
//
// Paths are joined by hand and never cleaned, because they are printed: a
// $PROJECT_DOCS_DIR with a trailing slash is echoed back as the session was configured.
func Print(w io.Writer) {
	root := os.Getenv("PROJECT_DOCS_DIR")
	if root == "" {
		root = os.Getenv("HOME") + "/projects/docs"
	}

	// shared/ holds what applies across projects, such as how the one Cloudflare account
	// is set up, so it is listed in every session, in a repository or not. A session that
	// never sees it would redo a convention another project already settled.
	shared := root + "/shared"
	if isDir(shared) {
		fmt.Fprintf(w, "Shared docs for every project: %s\n\n", shared)
		list(w, shared)
		fmt.Fprintln(w)
	}

	dir := os.Getenv("CLAUDE_PROJECT_DIR")
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return
		}
		dir = cwd
	}

	if _, err := git(dir, "rev-parse", "--git-dir"); err != nil {
		return
	}

	project := name(dir)
	if project == "" {
		return
	}

	docs := root + "/" + project
	if !isDir(docs) {
		return
	}
	fmt.Fprintf(w, "Working docs for %s: %s\n\n", project, docs)
	list(w, docs)
}

// The remote name, not the directory: a worktree is named for its branch, and every branch
// of a repository shares one docs folder. Falls back to the main worktree's directory for
// a repo with no remote, which is what --git-common-dir points at.
func name(dir string) string {
	if url, err := git(dir, "remote", "get-url", "origin"); err == nil {
		if project := base(url, ".git"); project != "" {
			return project
		}
	}
	common, err := git(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || common == "" {
		return ""
	}
	return filepath.Base(filepath.Dir(common))
}

// Top-level files by name, subdirectories as a name and a count. Listing every file put
// fifty lines into the context of every session on the largest project, most of them a
// folder of numbered plans that no session reads all of. The shape is what is worth
// injecting; the contents are one ls away.
func list(w io.Writer, dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	var files, dirs []string
	for _, entry := range entries {
		switch {
		case entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".md"):
			files = append(files, entry.Name())
		case entry.IsDir() && entry.Name() != ".git":
			dirs = append(dirs, entry.Name())
		}
	}
	sortNames(files)
	sortNames(dirs)

	for _, file := range files {
		fmt.Fprintf(w, "  %s\n", file)
	}
	for _, sub := range dirs {
		fmt.Fprintf(w, "  %s/ (%d)\n", sub, count(filepath.Join(dir, sub)))
	}
}

func sortNames(names []string) {
	sort.Slice(names, func(i, j int) bool { return collate.Compare(names[i], names[j]) < 0 })
}

// Every depth below the subdirectory, since the count stands for what is in it, and a
// directory that cannot be read counts as the files that could.
func count(dir string) int {
	found := 0
	filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".md") {
			found++
		}
		return nil
	})
	return found
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// basename(1), down to its edges, because the project name came from it: trailing slashes
// go, and a suffix that is the whole of what is left stays, so a remote at `.git` is named
// `.git` rather than nothing at all.
func base(path, suffix string) string {
	trimmed := strings.TrimRight(path, "/")
	if trimmed == "" {
		if path == "" {
			return ""
		}
		return "/"
	}
	if i := strings.LastIndex(trimmed, "/"); i >= 0 {
		trimmed = trimmed[i+1:]
	}
	if trimmed != suffix && strings.HasSuffix(trimmed, suffix) {
		return strings.TrimSuffix(trimmed, suffix)
	}
	return trimmed
}

// git is the one command run here, and it is run rather than reimplemented because what a
// worktree's origin and common directory are is git's answer to give. Its output comes
// back as a shell would have taken it, the trailing newlines gone.
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}
