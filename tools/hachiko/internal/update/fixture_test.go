package update

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/discord"
)

// A whole update against a table of command lines and the answers to them, which is how
// every rule here is checked without moving a version on anybody's machine: the steps, what
// counts as having moved, which git commands the lockfiles are committed with, when the
// proxy daemon and tailscaled are touched, and what is said about each.
type box struct {
	t   *testing.T
	cfg config.Config
	dir string
	now time.Time

	// Keyed by the whole command line, which is what makes a test fail when a step starts
	// running something other than what it was written for. A command with no answer worked
	// and said nothing, so a test sets up only the readings it is about.
	//
	// A queue rather than one answer, because every version here is read twice — once before
	// a step and once after it — and something moving is the two readings differing. The
	// last answer in the queue stands for every call after it.
	answers map[string][]Output
	calls   []string
	run     []Command

	sent    []discord.Outgoing
	spawned [][]string
	sendErr error

	log bytes.Buffer
}

const (
	home = "/Users/account"
	repo = "/Users/account/.mac-mini"
)

func newBox(t *testing.T) *box {
	t.Helper()
	dir := t.TempDir()

	return &box{
		t:   t,
		dir: dir,
		now: time.Date(2026, 10, 18, 4, 5, 0, 0, time.UTC),
		cfg: config.Config{
			Home:           home,
			MachineDir:     repo,
			RootHelper:     "/usr/local/libexec/claude-root",
			UpdateStateDir: filepath.Join(dir, "state"),
			UpdateLock:     filepath.Join(dir, "lock"),
			StateDir:       filepath.Join(dir, "watch"),
			UpdateHour:     4,
			Host:           "mac-mini",
			Discord:        config.DiscordConfig{ChannelID: "incidents", UserID: "tim", UpdatesID: "updates"},
		},
		answers: map[string][]Output{},
	}
}

func (b *box) answer(line string, out Output) { b.answers[line] = []Output{out} }

// What the same command answers from its next call onward, which is how a version that
// moved is written down.
func (b *box) then(line string, out Output) { b.answers[line] = append(b.answers[line], out) }

// A command that worked and printed this on stdout, which is what every reading is.
func (b *box) says(line, stdout string) {
	b.answer(line, Output{OK: true, Stdout: stdout, Text: stdout})
}

func (b *box) saysNext(line, stdout string) {
	b.then(line, Output{OK: true, Stdout: stdout, Text: stdout})
}

func (b *box) fails(line, text string) { b.answer(line, Output{Text: text}) }

// One Homebrew package at one version before the upgrade and another after it, with
// Homebrew's other answer left empty so that nothing else looks as though it moved.
func (b *box) moveBrew(name, from, to string) {
	b.says("brew list --versions", name+" "+from+"\n")
	b.saysNext("brew list --versions", name+" "+to+"\n")
	b.says("brew list --cask --versions", "")
}

// What `herdr status server` says the running server started from, in the shape herdr prints
// it: a line per reading, and the version among them.
func (b *box) herdrServerOn(version string) {
	b.says("herdr status server", "status: running\nversion: "+version+
		"\nsocket: "+home+"/.config/herdr/herdr.sock\n")
}

func (b *box) moveHerdr(from, to string) {
	b.says("herdr --version", "herdr "+from)
	b.saysNext("herdr --version", "herdr "+to)
}

// What `pip show` says a package in one of mise's pythons is at, before an upgrade and after.
func (b *box) movePip(exe, pkg, from, to string) {
	b.says(exe+" -m pip show "+pkg, "Name: "+pkg+"\nVersion: "+from+"\n")
	b.saysNext(exe+" -m pip show "+pkg, "Name: "+pkg+"\nVersion: "+to+"\n")
}

// One tool outdated before the upgrade and current after it, which is a tool that moved.
func (b *box) moveTool(line string) {
	b.says(miseOutdatedGlobal, line)
	b.saysNext(miseOutdatedGlobal, "")
}

func (b *box) deps() Deps {
	return Deps{
		Now:   func() time.Time { return b.now },
		Log:   &b.log,
		Sleep: func(time.Duration) {},
		Run: func(cmd Command) Output {
			b.calls = append(b.calls, cmd.line())
			b.run = append(b.run, cmd)

			queue := b.answers[cmd.line()]
			if len(queue) == 0 {
				return Output{OK: true}
			}
			if len(queue) > 1 {
				b.answers[cmd.line()] = queue[1:]
			}
			return queue[0]
		},
		Send: func(out discord.Outgoing) (string, error) {
			b.sent = append(b.sent, out)
			if b.sendErr != nil {
				return "", b.sendErr
			}
			return "thread-" + out.OpenThread, nil
		},
		Spawn: func(args ...string) error {
			b.spawned = append(b.spawned, args)
			return nil
		},
	}
}

func (b *box) update(dry bool) *updater {
	b.t.Helper()

	u := &updater{cfg: b.cfg, deps: b.deps(), store: Store{Dir: b.cfg.UpdateStateDir},
		hand: b.hand(), dry: dry}
	if err := u.run(); err != nil {
		b.t.Fatalf("the update failed: %v", err)
	}
	return u
}

func (b *box) hand() Handoff {
	return Handoff{Dir: b.cfg.UpdateStateDir, WatchDir: b.cfg.StateDir}
}

// What the update left for the five-minute check, which is the whole of what it says about a
// failed step now that it posts nothing about one.
func (b *box) failing() []Handed { return b.hand().Failing() }

// And the check's half of it: the incident it raised a step as and the thread of the post it
// opened, which is where the line saying the step works again has to go.
func (b *box) raised(step, incident, thread string) {
	b.t.Helper()

	raised := b.hand().Raised()
	raised[step] = Raised{Incident: incident, Thread: thread}
	if err := b.hand().SetRaised(raised); err != nil {
		b.t.Fatal(err)
	}
}

func (b *box) ran(line string) bool {
	for _, call := range b.calls {
		if call == line {
			return true
		}
	}
	return false
}

// Every command whose line starts with this, which is how the git calls are read back
// without writing each of them out.
func (b *box) matching(prefix string) []string {
	var found []string
	for _, call := range b.calls {
		if strings.HasPrefix(call, prefix) {
			found = append(found, call)
		}
	}
	return found
}

func (b *box) summary() string {
	for _, out := range b.sent {
		if out.Channel == b.cfg.Discord.UpdatesID {
			return out.Text
		}
	}
	return ""
}

func (b *box) incidents() []discord.Outgoing {
	var found []discord.Outgoing
	for _, out := range b.sent {
		if out.Channel == "" {
			found = append(found, out)
		}
	}
	return found
}

// The readings a night where everything is current answers with, so a test that is about
// one step says only that step's answers. The three `rev-parse --verify` calls answer the
// way git does when the ref is not there, since a default of "it worked" would be a tree
// permanently mid-merge.
func (b *box) current() {
	b.says("mise --version", "2026.10.11 macos-arm64 (2026-10-15)")
	b.says("herdr --version", "herdr 0.9.5")
	b.says("softwareupdate --list", "No new software available.")
	b.says(appleInstallCall, nothingToInstall)

	for _, ref := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD"} {
		b.fails("git -C "+repo+" rev-parse -q --verify "+ref, "")
	}
}

// What the root helper says when it has put an update in, one line per update the way it
// writes them — and what it says when there is nothing waiting that it may install.
func (b *box) installedApple(lines ...string) {
	b.says(appleInstallCall, strings.Join(lines, "\n")+"\n")
}

const nothingToInstall = "nothing to install: every Apple update waiting either restarts the Mac or is macOS itself\n"

// What `mise outdated` answers for one tool, in mise's own table.
func outdatedLine(name, requested, current, latest string) string {
	return strings.Join([]string{name, requested, current, latest, "~/.config/mise/config.toml"}, " ") + "\n"
}

const (
	miseOutdatedGlobal = "mise outdated -C " + home + " --no-header"
	miseOutdatedRepo   = "mise outdated -C " + repo + " --local --no-header"
	miseUpGlobal       = "mise up -C " + home + " --yes"
	miseUpRepo         = "mise up -C " + repo + " --local --yes"
	gitStatus          = "git -C " + repo + " --no-optional-locks status --porcelain=v1 -- " + lockPaths
	gitBranch          = "git -C " + repo + " rev-parse --abbrev-ref HEAD"
	gitDefault         = "git -C " + repo + " symbolic-ref --short refs/remotes/origin/HEAD"
	gitAdd             = "git -C " + repo + " add -A -- " + lockPaths
	gitPush            = "git -C " + repo + " push origin main"
	appleInstallCall   = "sudo -n /usr/local/libexec/claude-root install-apple-updates"
	lockPaths          = "mise.lock home/.config/mise/mise.lock home/.config/mise/locks"
)
