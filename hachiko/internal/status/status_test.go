package status

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/harness"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
)

// The five sections and the one line above them, in the order somebody reads them in: what is
// running, then how much room is left, then what the watch has open about it.
func TestTheScreenIsFiveSectionsUnderOneLineAboutTheMac(t *testing.T) {
	f := newFixture(t)
	f.repo("projects/docs", RepoState{Read: true})
	f.log("hachiko.log", 1200)

	report := f.report()

	harness.Wants(t, report, "hachiko on mac-mini, Thu 8 Oct 12:40\n")

	at := 0
	for _, section := range []string{"Agents", "Disk", "Incidents", "Sync", "Logs"} {
		found := strings.Index(report, "\n"+section+"\n")
		if found < at {
			t.Fatalf("%s is out of order or missing in:\n%s", section, report)
		}
		at = found
	}
}

// Written out in one go, so nobody is ever looking at half a screen because something
// further down could not be read.
func TestTheWholeScreenGoesOutAtOnce(t *testing.T) {
	f := newFixture(t)

	s := screen{cfg: f.cfg, deps: f.deps()}
	if err := s.write(); err != nil {
		t.Fatal(err)
	}

	harness.Equal(t, f.out.String(), s.report(), "what was written against what was built")
}

// Both readings about one agent, because they answer different questions: launchd says
// whether the agent is there at all, and the stamp says whether it is doing its job.
func TestAnAgentIsWhatLaunchdHasAndWhatItsStampSays(t *testing.T) {
	f := newFixture(t)

	harness.Wants(t, f.row("Agents", "sync"), "loaded, pid 2345")
	harness.Wants(t, f.row("Agents", "sync"), "last heartbeat less than a minute ago")
	harness.Wants(t, f.row("Agents", "sync"), "stale after 10 minutes")

	// An agent on an interval, between runs: loaded and running nothing, which is what it
	// looks like for all but a second of every ten minutes.
	harness.Wants(t, f.row("Agents", "the worktree sweep"), "loaded  ")
	harness.Lacks(t, f.row("Agents", "the worktree sweep"), "pid")
	harness.Wants(t, f.row("Agents", "the worktree sweep"), "last sweep 4 minutes ago")
}

func TestAnAgentLaunchdDoesNotHaveIsSaidToBeNotLoaded(t *testing.T) {
	f := newFixture(t)
	delete(f.jobs, "io.github.timche.hachiko-gc")

	harness.Wants(t, f.row("Agents", "the worktree sweep"), "not loaded")
}

// The same threshold the watch would have alerted on, so the screen and the alert cannot
// disagree about one agent.
func TestAStampOlderThanTheWatchsThresholdIsSaidToBeStale(t *testing.T) {
	f := newFixture(t)
	f.stamps[statedir.AgentGC] = f.now.Add(-2 * time.Hour)

	harness.Wants(t, f.row("Agents", "the worktree sweep"), "last sweep 2 hours ago")

	f.colour = true
	harness.Wants(t, f.row("Agents", "the worktree sweep"), "\x1b[33mlast sweep 2 hours ago\x1b[0m")
}

// Answering from Discord ships off, so a listener with no heartbeat is a feature nobody
// turned on — and a threshold printed against it would read as a deadline on something
// switched off.
func TestAListenerNobodyConfiguredIsOffRatherThanLate(t *testing.T) {
	f := newFixture(t)
	delete(f.stamps, statedir.AgentListen)

	row := f.row("Agents", "the Discord listener")

	harness.Wants(t, row, "off, nothing configured")
	harness.Lacks(t, row, "stale after")
}

// The watch is the one agent with no threshold here, because nothing on this Mac can tell
// whether the thing doing the checking is running.
func TestTheWatchItselfIsOnTheScreenAndWatchedFromOffTheMac(t *testing.T) {
	f := newFixture(t)

	harness.Wants(t, f.row("Agents", "the watch"), "last check 2 minutes ago")
	harness.Wants(t, f.row("Agents", "the watch"), "watched by shibuya, off this Mac")
}

func TestTheDiskIsFreeSpaceAgainstTheWatchsOwnMarks(t *testing.T) {
	f := newFixture(t)

	harness.Wants(t, f.section("Disk"), "free            400 GB")
	harness.Wants(t, f.section("Disk"), "low below       100 GB")
	harness.Wants(t, f.section("Disk"), "critical below  20 GB")
}

func TestFreeSpaceUnderTheLowMarkIsColouredWhereAnybodyIsLooking(t *testing.T) {
	f := newFixture(t)
	f.freeKB = 40 * config.GiB
	f.colour = true

	harness.Wants(t, f.section("Disk"), "\x1b[33m40 GB\x1b[0m")
}

// A reading it could not take is said in its own section and nothing else: this reports
// problems rather than being one.
func TestASectionThatCouldNotBeReadSaysSoInItself(t *testing.T) {
	f := newFixture(t)
	f.freeErr = errors.New("statfs: no such file or directory")
	f.reposErr = errors.New("/Users/someone/.config/hachiko/sync could not be read")

	harness.Wants(t, f.section("Disk"), "could not be read: statfs")
	harness.Wants(t, f.section("Sync"), "~/.config/hachiko/sync could not be read")
}

func TestAQuietMacHasNothingOpen(t *testing.T) {
	harness.Wants(t, newFixture(t).section("Incidents"), "nothing open")
}

func TestAnIncidentWhoseAgentOwesItsFirstReportSaysSo(t *testing.T) {
	f := newFixture(t)
	f.state.Pending = map[string]statedir.Pending{
		"disk-1700000300": {OpenedAt: f.ago(3 * time.Minute), Tab: "oncall-disk"},
	}

	row := f.row("Incidents", "disk-1700000300")

	harness.Wants(t, row, "opened 3 minutes ago")
	harness.Wants(t, row, "no report from the agent yet")
	harness.Wants(t, row, "an on-call session is in oncall-disk")
}

// The one thing the screen is most often opened to answer: how long a question has been in
// front of him, which is the clock the handover runs on.
func TestAnIncidentWaitingOnAnAnswerSaysSinceWhen(t *testing.T) {
	f := newFixture(t)
	f.state.Waiting = map[string]statedir.Waiting{
		"disk": {
			Incident: "disk-1700000300",
			Tab:      "oncall-disk",
			Opened:   f.ago(2 * time.Hour),
			Since:    f.ago(105 * time.Minute),
		},
	}

	row := f.row("Incidents", "disk-1700000300")

	harness.Wants(t, row, "opened 2 hours ago")
	harness.Wants(t, row, "a question has been in front of you for 1 hour 45 minutes")
	harness.Wants(t, row, "an on-call session is in oncall-disk")
}

func TestAnIncidentNoSessionCouldBeOpenedForSaysThatToo(t *testing.T) {
	f := newFixture(t)
	f.state.Waiting = map[string]statedir.Waiting{
		"cpu": {Incident: "cpu-1700000800", Opened: f.ago(time.Hour)},
	}

	harness.Wants(t, f.row("Incidents", "cpu-1700000800"), "no on-call session is attached")
}

// The three things the watch says once and remembers having said. None of them is an
// incident, and all three are open until something clears them, so a screen that left them
// out would be a screen that says nothing is wrong under a ⚠️ nobody has answered.
func TestTheAlertsTheWatchHasOpenAreOnTheScreen(t *testing.T) {
	f := newFixture(t)
	f.state.SetAgent(statedir.AgentGC, statedir.AgentLiveness{Stale: f.ago(70 * time.Minute)})
	f.state.SyncBehind = map[string]statedir.Behind{
		"late " + f.cfg.Home + "/projects/docs": {Since: f.ago(2 * time.Hour), Said: true},
		"dirty " + f.cfg.Home + "/elsewhere":    {Since: f.ago(time.Hour)},
	}
	f.state.CapTrouble = []string{f.cfg.Home + "/Library/Logs/herdr.log"}

	section := f.section("Incidents")

	harness.Wants(t, section, "reported as not running 1 hour 10 minutes ago")
	harness.Wants(t, section, "~/projects/docs")
	harness.Wants(t, section, "reported as late 2 hours ago")
	harness.Wants(t, section, "~/Library/Logs/herdr.log")
	harness.Wants(t, section, "reported as a log that could not be capped")

	// One nobody was ever told about is not an open alert: the watch's own rule is that a
	// dirty tree is only news once the debounce cannot be what is holding it.
	harness.Lacks(t, section, "~/elsewhere")
}

func TestAStateFileThatWouldNotParseIsSaidRatherThanReadAsQuiet(t *testing.T) {
	f := newFixture(t)
	f.stateErr = statedir.ErrCorrupt

	harness.Wants(t, f.section("Incidents"), "could not be read, so nothing below is what it knows")
}

func TestARepositorySyncIsKeepingUpstreamReadsAsGitHasItNow(t *testing.T) {
	f := newFixture(t)
	f.repo("projects/docs", RepoState{Read: true})
	f.repo("projects/other", RepoState{
		Read: true, Dirty: true, Unpushed: 2, Oldest: f.now.Add(-12 * time.Minute),
	})

	harness.Wants(t, f.row("Sync", "~/projects/docs"), "clean  nothing unpushed")
	harness.Wants(t, f.row("Sync", "~/projects/other"), "dirty  2 commits unpushed, oldest 12 minutes old")
}

// Late by the watch's own rule — the delay the repository was given plus the slack the watch
// allows on top of it — so one commit cannot be late on the screen and waiting in the alert.
func TestACommitPastThePushDelayAndTheWatchsSlackIsColoured(t *testing.T) {
	f := newFixture(t)
	f.colour = true
	f.repo("projects/docs", RepoState{Read: true, Unpushed: 1, Oldest: f.now.Add(-20 * time.Minute)})

	harness.Lacks(t, f.row("Sync", "~/projects/docs"), "\x1b[33m")

	f.trees[f.repos[0].Path] = RepoState{Read: true, Unpushed: 1, Oldest: f.now.Add(-2 * time.Hour)}
	harness.Wants(t, f.row("Sync", "~/projects/docs"), "\x1b[33m1 commit unpushed, oldest 2 hours old\x1b[0m")
}

// Nothing resumes a paused repository by itself, so a screen that left it out would say a
// repository is fine while nothing is syncing it.
func TestARepositoryPausedOnAConflictSaysSoAndWhen(t *testing.T) {
	f := newFixture(t)
	f.repo("projects/docs", RepoState{Read: true, Paused: f.now.Add(-90 * time.Minute)})

	harness.Wants(t, f.row("Sync", "~/projects/docs"), "paused on a conflict for 1 hour 30 minutes")
}

func TestARepositoryGitWouldNotAnswerAboutIsNotCalledClean(t *testing.T) {
	f := newFixture(t)
	f.repo("projects/docs", RepoState{Trouble: "git would not say what is in it"})

	row := f.row("Sync", "~/projects/docs")

	harness.Wants(t, row, "git would not say what is in it")
	harness.Lacks(t, row, "clean")
}

func TestALogIsItsSizeAgainstTheCapTheWatchKeepsItUnder(t *testing.T) {
	f := newFixture(t)
	f.log("hachiko.log", 1200)
	f.log("herdr.log", 11*1024)
	f.logs = append(f.logs, Log{Path: f.cfg.Home + "/Library/Logs/ssh-agent.log"})

	harness.Wants(t, f.row("Logs", "~/Library/Logs/hachiko.log"), "1 MB of 10 MB")
	harness.Wants(t, f.row("Logs", "~/Library/Logs/herdr.log"), "11 MB of 10 MB")
	harness.Wants(t, f.row("Logs", "~/Library/Logs/ssh-agent.log"), "nothing has been written to it")

	f.colour = true
	harness.Wants(t, f.row("Logs", "~/Library/Logs/herdr.log"), "\x1b[33m11 MB of 10 MB\x1b[0m")
	harness.Lacks(t, f.row("Logs", "~/Library/Logs/hachiko.log"), "\x1b[33m")
}

// A pipe gets no colour at all, because the first thing anybody does with this output is grep
// it or paste it into a message.
func TestNothingIsColouredWhereNobodyIsLooking(t *testing.T) {
	f := newFixture(t)
	f.freeKB = 1 * config.GiB
	f.stamps[statedir.AgentGC] = f.now.Add(-2 * time.Hour)

	harness.Lacks(t, f.report(), "\x1b")
}

// The columns are what makes one screen readable rather than five paragraphs, and an escape
// code is bytes a width must not count: a row with a warning in it may not be the one row out
// of line.
func TestTheColumnsLineUpWithAColouredCellAmongThem(t *testing.T) {
	f := newFixture(t)
	f.colour = true
	f.stamps[statedir.AgentSync] = f.now.Add(-2 * time.Hour)

	widths := map[int]int{}
	for _, line := range strings.Split(f.section("Agents"), "\n") {
		plain := strings.NewReplacer("\x1b[33m", "", "\x1b[0m", "").Replace(line)
		widths[strings.Index(plain, "loaded")]++
	}

	harness.Equal(t, len(widths), 1, "the column every agent's launchd reading starts at")

	// And no line ends in the padding before a column nothing was said in.
	for _, line := range strings.Split(f.report(), "\n") {
		if strings.TrimRight(line, " ") != line {
			t.Errorf("a line ends in padding: %s", quoted(line))
		}
	}
}
