// Package herdrtab names this session's herdr tab after the title Claude Code gives the
// session, and follows that title as it moves.
//
// Claude Code writes the title into the transcript as an `ai-title` line early in the first
// turn, and writes another when the session's subject moves, so the last one is current.
// Asking the model to rename its own tab was followed only some of the time; this needs
// nothing from it.
//
// A tab is this hook's to name while it carries herdr's default label, which is its number,
// or the label this hook last gave it. Anything else was named by hand, by the lead skill or
// by mochi, and is left alone until a clear puts the number back. herdr keeps no record of
// who set a label, so the one this set is kept per tab beside the binary's cache.
//
// It runs on every tool call, so what costs nothing comes first: the title is read before
// herdr is asked anything, and a title this has already given the tab ends the run without a
// single herdr call. Silent on stdout, since what a UserPromptSubmit or SessionStart hook
// prints lands in the session's context, and every way it can fail is a line on stderr.
package herdrtab

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The whole run, shared by every call it makes, since what is waiting on it is a session
// between one tool call and the next.
const budget = 5 * time.Second

// Records of tabs that were closed without a clear, which nothing else deletes.
const keepRecords = 14 * 24 * time.Hour

// The title is a line near the end of a transcript that grows all session long, and this
// runs after every tool call.
const tailBytes = 1 << 20

// A sidebar entry shows about this much of a label.
const maxWords = 4

type event struct {
	EventName      string `json:"hook_event_name"`
	Source         string `json:"source"`
	TranscriptPath string `json:"transcript_path"`
}

type namer struct {
	ctx       context.Context
	tab       string
	workspace string
}

// Observe reads the hook event and names the session's tab after the session's title. A tab
// is the one thing it needs and cannot be told: HERDR_TAB_ID is set by the herdr that
// started this terminal, so no tab means no herdr, or a terminal herdr does not own.
func Observe(in io.Reader) {
	defer func() {
		if problem := recover(); problem != nil {
			warn("%v", problem)
		}
	}()

	tab := os.Getenv("HERDR_TAB_ID")
	// A mochi thread runs in a tab mochi made and names for the thread, which is not a
	// name a session may take over.
	if os.Getenv("HERDR_ENV") != "1" || tab == "" || os.Getenv("MOCHI_THREAD") != "" {
		return
	}

	body, err := io.ReadAll(in)
	if err != nil {
		return
	}
	var ev event
	if json.Unmarshal(body, &ev) != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	n := &namer{ctx: ctx, tab: tab, workspace: os.Getenv("HERDR_WORKSPACE_ID")}

	switch ev.EventName {
	case "SessionStart":
		// A clear starts another task, so a name given for the last one outlives what
		// it described; every other source carries on the task that earned it.
		if ev.Source == "clear" {
			n.reset()
		}
	case "UserPromptSubmit", "PostToolUse", "Stop":
		n.name(ev.TranscriptPath)
	}
}

func (n *namer) name(transcript string) {
	wanted := kebab(latestTitle(transcript))
	if wanted == "" {
		return
	}
	// Before herdr is asked what the tab is called, since a session whose title has not
	// moved since the last tool call is the common case.
	ours, known := recall(n.tab)
	if known && ours == wanted {
		return
	}

	label, err := n.label()
	if err != nil {
		warn("%v", err)
		return
	}
	if !digits(label) && label != ours {
		return
	}
	if label != wanted {
		if err := n.rename(wanted); err != nil {
			warn("%v", err)
			return
		}
	}
	remember(n.tab, wanted)
}

// herdr has no unset, and renaming to an empty string leaves the tab visibly blank, so the
// default label has to be rebuilt: it is the tab's 1-based position in its workspace, which
// is the order `tab list` answers in.
func (n *namer) reset() {
	if n.workspace == "" {
		return
	}
	position, err := n.position()
	if err != nil {
		warn("%v", err)
		return
	}
	if err := n.rename(strconv.Itoa(position)); err != nil {
		warn("%v", err)
		return
	}
	forget(n.tab)
}

// HERDR_BIN_PATH is the herdr that started this terminal, which is the one holding the
// socket the tab ids belong to; the PATH is for a terminal that was handed no path.
func (n *namer) herdr(args ...string) (json.RawMessage, error) {
	bin := os.Getenv("HERDR_BIN_PATH")
	if info, err := os.Stat(bin); err != nil || info.IsDir() {
		bin = "herdr"
	}

	out, err := exec.CommandContext(n.ctx, bin, args...).Output()
	called := strings.Join(args, " ")

	// A refusal is JSON on stdout and a status of 1 together, and what it says is the
	// only account of why.
	var answer struct {
		Result json.RawMessage `json:"result"`
		Error  struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(out, &answer) == nil && answer.Error.Code != "" {
		return nil, fmt.Errorf("herdr %s: %s", called, answer.Error.Message)
	}
	if err != nil {
		return nil, fmt.Errorf("herdr %s: %w", called, err)
	}
	if len(answer.Result) == 0 {
		return nil, fmt.Errorf("herdr %s answered nothing", called)
	}
	return answer.Result, nil
}

func (n *namer) label() (string, error) {
	out, err := n.herdr("tab", "get", n.tab)
	if err != nil {
		return "", err
	}
	var answer struct {
		Tab struct {
			Label string `json:"label"`
		} `json:"tab"`
	}
	if json.Unmarshal(out, &answer) != nil || answer.Tab.Label == "" {
		return "", fmt.Errorf("herdr says nothing about what %s is labelled", n.tab)
	}
	return answer.Tab.Label, nil
}

func (n *namer) position() (int, error) {
	out, err := n.herdr("tab", "list")
	if err != nil {
		return 0, err
	}
	var answer struct {
		Tabs []struct {
			ID        string `json:"tab_id"`
			Workspace string `json:"workspace_id"`
		} `json:"tabs"`
	}
	if json.Unmarshal(out, &answer) != nil {
		return 0, fmt.Errorf("herdr tab list is not a list of tabs")
	}
	position := 0
	for _, listed := range answer.Tabs {
		if listed.Workspace != n.workspace {
			continue
		}
		position++
		if listed.ID == n.tab {
			return position, nil
		}
	}
	return 0, fmt.Errorf("herdr does not have %s in workspace %s", n.tab, n.workspace)
}

func (n *namer) rename(label string) error {
	_, err := n.herdr("tab", "rename", n.tab, label)
	return err
}

// Only the tail of the transcript is read, and the line is found by looking for the type
// before any of it is parsed: this runs after every tool call, and a transcript carries a
// session's every message.
func latestTitle(path string) string {
	if path == "" {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()

	end, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return ""
	}
	if end > tailBytes {
		if _, err := file.Seek(end-tailBytes, io.SeekStart); err != nil {
			return ""
		}
	} else if _, err := file.Seek(0, io.SeekStart); err != nil {
		return ""
	}
	body, err := io.ReadAll(file)
	if err != nil {
		return ""
	}

	lines := strings.Split(string(body), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if !strings.Contains(lines[i], `"ai-title"`) {
			continue
		}
		var entry struct {
			Type  string `json:"type"`
			Title string `json:"aiTitle"`
		}
		// The first line of the tail is half a line, and so is any line a transcript
		// was caught mid-write on.
		if json.Unmarshal([]byte(lines[i]), &entry) != nil {
			continue
		}
		if entry.Type == "ai-title" && entry.Title != "" {
			return entry.Title
		}
	}
	return ""
}

// Go's standard library has no NFKD, so a letter outside ASCII goes rather than folding onto
// the letter it is built from. A title Claude Code writes is English, and what survives is
// still a label that tells the tab apart from the others.
func kebab(title string) string {
	var ascii strings.Builder
	for _, r := range strings.ToLower(title) {
		switch {
		// A possessive or a contraction is one word: "don't" is "dont" and not
		// "don-t".
		case r == '\'' || r == '’':
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.':
			ascii.WriteRune(r)
		default:
			ascii.WriteByte(' ')
		}
	}

	var words []string
	for _, word := range strings.Fields(ascii.String()) {
		// A dot inside a word is a version or a file extension; one at either end is
		// the punctuation a sentence was written with.
		if word = strings.Trim(word, "."); word == "" {
			continue
		}
		if words = append(words, word); len(words) == maxWords {
			break
		}
	}
	return strings.Join(words, "-")
}

// herdr's own label for a tab nobody has named, which is the tab's number.
func digits(label string) bool {
	for _, r := range label {
		if r < '0' || r > '9' {
			return false
		}
	}
	return label != ""
}

// Beside the binary's own cache, so a session trying a change out with
// CLAUDE_HOOKS_CACHE_DIR keeps its records out of the ones every other session is using.
func recordPath(tab string) string {
	cache := os.Getenv("CLAUDE_HOOKS_CACHE_DIR")
	if cache == "" {
		cache = filepath.Join(os.Getenv("HOME"), "Library", "Caches", "claude-hooks")
	}
	// A tab id carries a colon, and this is a file named after one.
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '_'
	}, tab)
	return filepath.Join(cache, "herdr-tab", name)
}

func recall(tab string) (string, bool) {
	body, err := os.ReadFile(recordPath(tab))
	if err != nil {
		return "", false
	}
	return string(body), true
}

func remember(tab, label string) {
	path := recordPath(tab)
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	sweep(filepath.Dir(path))
	os.WriteFile(path, []byte(label), 0o600)
}

func forget(tab string) {
	os.Remove(recordPath(tab))
}

// A tab that was closed rather than cleared leaves a record nothing else deletes.
func sweep(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err == nil && time.Since(info.ModTime()) > keepRecords {
			os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}

func warn(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "claude-hooks herdr-tab: "+format+"\n", args...)
}
