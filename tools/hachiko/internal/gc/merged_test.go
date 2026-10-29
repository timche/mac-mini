package gc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// A repository with a worktree on a branch, both real git: the checkout under the projects
// root and the worktree under the herdr root, which is where herdr puts one. What the merged
// sweep asks about a worktree is what git answers, so there is nothing here to fake.
func (f *fixture) repoWithWorktree(name, branch string) (checkout, dir string) {
	f.t.Helper()

	checkout = f.mkdir(filepath.Join(f.cfg.GCProjectsRoot, "acme", name))
	f.git(checkout, "init", "-b", "main")
	f.write(filepath.Join(checkout, "README.md"), "# "+name+"\n")
	f.git(checkout, "add", "-A")
	f.git(checkout, "commit", "-m", "first")

	dir = filepath.Join(f.cfg.HerdrRoot, name, strings.ReplaceAll(branch, "/", "-"))
	f.mkdir(filepath.Dir(dir))
	f.git(checkout, "worktree", "add", "-b", branch, dir)

	return checkout, dir
}

// A commit in the worktree, which is what a session's change is, and its id, so a pull
// request can be said to carry it.
func (f *fixture) commitIn(dir, file string) string {
	f.t.Helper()

	f.write(filepath.Join(dir, file), "change\n")
	f.git(dir, "add", "-A")
	f.git(dir, "commit", "-m", "change")

	return f.git(dir, "rev-parse", "HEAD")
}

// What GitHub answers about the branch: merged, as the pull request whose head is that
// commit.
func (f *fixture) mergedAs(branch, head string, number int) {
	f.merged[branch] = &PullRequest{
		Number: number,
		URL:    "https://github.com/acme/app/pull/" + strconv.Itoa(number),
		Head:   head,
	}
}

// The clean case every test below starts from: a worktree whose one commit is the merged
// pull request's head, with nothing running in it and nothing uncommitted.
func mergedFixture(t *testing.T) (f *fixture, checkout, dir, branch string) {
	t.Helper()

	f = newFixture(t)
	branch = "feature/widget"
	checkout, dir = f.repoWithWorktree("app", branch)
	f.mergedAs(branch, f.commitIn(dir, "widget.txt"), 42)

	return f, checkout, dir, branch
}

// This repository's own git, which the sweep's own git is not: the commits a test makes must
// not depend on Tim's global config, his signing key least of all.
func (f *fixture) git(dir string, args ...string) string {
	f.t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=gc", "GIT_AUTHOR_EMAIL=gc@example.com",
		"GIT_COMMITTER_NAME=gc", "GIT_COMMITTER_EMAIL=gc@example.com",
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *fixture) branches(checkout string) string {
	f.t.Helper()
	return f.git(checkout, "branch", "--format=%(refname:short)")
}

// The whole of it: the folder goes, and so does the branch — which `git branch -d` would
// refuse, the commit being nowhere in main's history, exactly as a squash merge leaves it.
func TestAMergedWorktreeAndItsBranchGo(t *testing.T) {
	f, checkout, dir, branch := mergedFixture(t)

	out := f.sweep()
	harness.Wants(t, out, "removing the worktree "+dir+" and its branch "+branch+
		", merged as https://github.com/acme/app/pull/42")

	gone(t, dir)
	harness.Equal(t, f.branches(checkout), "main", "the branches left")
	harness.Equal(t, len(f.sent), 0, "messages sent")
}

// What a worktree is for. Nothing landed is nothing to clean up after, and the work in it
// exists nowhere else.
func TestAWorktreeWithNoMergedPullRequestStays(t *testing.T) {
	f, checkout, dir, branch := mergedFixture(t)
	delete(f.merged, branch)

	harness.Equal(t, f.sweep(), "", "the log of a sweep with nothing merged")
	there(t, dir)
	harness.Wants(t, f.branches(checkout), branch)
}

// A pull request GitHub will not answer for is one every worktree in that repository fails
// on every sweep, so a live sweep says nothing at all about it.
func TestAGitHubThatWillNotAnswerLeavesTheWorktreeAndSaysNothing(t *testing.T) {
	f, _, dir, branch := mergedFixture(t)
	f.mergedErr[branch] = "gh: could not determine the current repository"

	harness.Wants(t, f.dryRun(), "GitHub would not say whether "+branch+" has merged")

	harness.Equal(t, f.sweep(), "", "the log of a sweep GitHub refused")
	there(t, dir)
}

// A workspace is the entry in Tim's sidebar, so a worktree with one is a session he still
// has open whatever is running inside it.
func TestAWorktreeHerdrHasOpenStays(t *testing.T) {
	f, _, dir, _ := mergedFixture(t)
	f.herdrHolds[dir] = true

	harness.Wants(t, f.dryRun(), "would stay: herdr has a workspace open on it")

	f.sweep()
	there(t, dir)
}

// Asked once per repository and no more, however many of its worktrees turn up.
func TestAHerdrThatWillNotAnswerRemovesNoneOfTheRepositorysWorktrees(t *testing.T) {
	f, checkout, dir, _ := mergedFixture(t)
	f.git(checkout, "worktree", "add", "-b", "other",
		filepath.Join(f.cfg.HerdrRoot, "app", "other"))
	f.herdrErr = "no server is running"

	out := f.sweep()
	harness.Wants(t, out, "worktrees it has open, so none of them went: no server is running")
	there(t, dir)

	harness.Equal(t, len(f.asked), 0, "pull requests read")
	harness.Equal(t, len(f.listed), 1, "times herdr was asked")
	harness.Equal(t, len(lines(out)), 1, "lines in the log")
}

// Uncommitted and untracked both, which is work the pull request does not carry and the
// folder is the only copy of.
func TestAWorktreeWithWorkInItStays(t *testing.T) {
	for _, change := range []string{"widget.txt", "notes.txt"} {
		t.Run(change, func(t *testing.T) {
			f, _, dir, _ := mergedFixture(t)
			f.write(filepath.Join(dir, change), "half of something\n")

			harness.Wants(t, f.dryRun(), "would stay: it has changes that were never committed")

			f.sweep()
			there(t, dir)
		})
	}
}

// A commit made after the merge. The folder is where it lives, there having been no push.
func TestAWorktreeWithCommitsThePullRequestDoesNotStays(t *testing.T) {
	f, _, dir, _ := mergedFixture(t)
	f.commitIn(dir, "more.txt")

	harness.Wants(t, f.dryRun(), "would stay: it has commits that https://github.com/acme/app/pull/42 does not")

	f.sweep()
	there(t, dir)
}

// A head this repository has never fetched is one nothing can be said to descend from, so
// the worktree stays — the safe way round for an answer nobody can check.
func TestAWorktreeWhoseMergedHeadIsUnknownHereStays(t *testing.T) {
	f, _, dir, branch := mergedFixture(t)
	f.mergedAs(branch, "0000000000000000000000000000000000000001", 42)

	f.sweep()
	there(t, dir)
}

func TestAProcessSittingInAMergedWorktreeKeepsIt(t *testing.T) {
	f, _, dir, _ := mergedFixture(t)
	f.sitting(4242, accountUID, filepath.Join(dir, "packages", "app"), "/usr/bin/node")

	harness.Wants(t, f.dryRun(), "would stay: process 4242 is sitting in it")

	f.sweep()
	there(t, dir)
}

// The same records the scratch sweep reads for which sessions are live, read here for where
// they are.
func TestASessionRunningInAMergedWorktreeKeepsIt(t *testing.T) {
	f, _, dir, _ := mergedFixture(t)

	f.write(filepath.Join(f.home, ".claude", "sessions", "5151.json"),
		`{"pid":5151,"sessionId":"11111111-2222-3333-4444-555555555555","cwd":"`+dir+`"}`)
	f.alive[5151] = true

	harness.Wants(t, f.dryRun(), "would stay: a Claude Code session is running in it")

	f.sweep()
	there(t, dir)
}

// A session whose record is there and whose process is not is a session that ended.
func TestASessionRecordOfADeadProcessKeepsNothing(t *testing.T) {
	f, _, dir, _ := mergedFixture(t)

	f.write(filepath.Join(f.home, ".claude", "sessions", "5151.json"),
		`{"pid":5151,"sessionId":"11111111-2222-3333-4444-555555555555","cwd":"`+dir+`"}`)

	f.sweep()
	gone(t, dir)
}

func TestADryRunRemovesNothing(t *testing.T) {
	f, checkout, dir, branch := mergedFixture(t)

	out := f.dryRun()
	harness.Wants(t, out, "would remove the worktree "+dir+" and its branch "+branch+
		", merged as https://github.com/acme/app/pull/42")

	there(t, dir)
	harness.Wants(t, f.branches(checkout), branch)
	gone(t, f.cfg.GCStateDir)
}

// The one pass here that leaves the machine, so it runs on a clock of its own rather than
// six times an hour.
func TestPullRequestsAreReadOnceAnHour(t *testing.T) {
	f, _, _, branch := mergedFixture(t)
	delete(f.merged, branch)

	f.sweep()
	harness.Equal(t, len(f.asked), 1, "pull requests read on the first sweep")

	f.at(600).sweep()
	harness.Equal(t, len(f.asked), 1, "pull requests read ten minutes later")

	harness.Wants(t, f.at(600).dryRun(), "no merged worktree would go for another 50 minutes")

	f.at(3600).sweep()
	harness.Equal(t, len(f.asked), 2, "pull requests read an hour later")
}

func TestNoGHReadsNoPullRequest(t *testing.T) {
	f, _, dir, _ := mergedFixture(t)
	f.ghUp = false

	harness.Equal(t, f.sweep(), "", "the log of a sweep with no gh")
	there(t, dir)
	harness.Equal(t, len(f.asked), 0, "pull requests read")

	harness.Wants(t, f.dryRun(), "there is no git and gh to read pull requests with")
}

// Claude Code puts its worktrees inside the repository they branched from, and this
// repository is one of the two places that happens on this Mac.
func TestAWorktreeInClaudeCodesOwnRootGoes(t *testing.T) {
	for _, where := range []string{"projects", "machine"} {
		t.Run(where, func(t *testing.T) {
			f := newFixture(t)

			checkout := f.cfg.MachineDir
			if where == "projects" {
				checkout = filepath.Join(f.cfg.GCProjectsRoot, "acme", "app")
			}

			f.mkdir(checkout)
			f.git(checkout, "init", "-b", "main")
			f.write(filepath.Join(checkout, "README.md"), "# app\n")
			f.git(checkout, "add", "-A")
			f.git(checkout, "commit", "-m", "first")

			dir := filepath.Join(checkout, ".claude", "worktrees", "agent-a1b2")
			f.git(checkout, "worktree", "add", "-b", "worktree-agent-a1b2", dir)
			f.mergedAs("worktree-agent-a1b2", f.commitIn(dir, "fix.txt"), 7)

			f.sweep()
			gone(t, dir)
		})
	}
}

// A repository is not a worktree of anything, so nothing in these roots that turns out to be
// one is asked about or removed: a checkout is nobody's to take away.
func TestACheckoutInAWorktreeRootIsNeverRemoved(t *testing.T) {
	f := newFixture(t)

	own := f.mkdir(filepath.Join(f.cfg.HerdrRoot, "app", "main"))
	f.git(own, "init", "-b", "main")
	f.write(filepath.Join(own, "README.md"), "# app\n")
	f.git(own, "add", "-A")
	f.git(own, "commit", "-m", "first")

	f.sweep()
	there(t, own)
	harness.Equal(t, len(f.asked), 0, "pull requests read")
}

// A worktree on no branch has no pull request to have merged, so GitHub is never asked.
func TestADetachedWorktreeIsNeverAsked(t *testing.T) {
	f, _, dir, _ := mergedFixture(t)
	f.git(dir, "checkout", "--detach")

	f.sweep()
	there(t, dir)
	harness.Equal(t, len(f.asked), 0, "pull requests read")
}

// A removal that fails is work the next sweep will fail at in exactly the same way, which is
// what reaches Discord.
func TestAWorktreeThatWillNotGoIsOneMessage(t *testing.T) {
	f, checkout, dir, _ := mergedFixture(t)
	f.git(checkout, "worktree", "lock", dir)

	out := f.sweep()
	harness.Wants(t, out, "the merged worktree "+dir+" would not go")
	there(t, dir)

	harness.Wants(t, f.lastSent(), "⚠️ A merged pull request's worktree would not go")
	harness.Wants(t, f.lastSent(), "**Worktree:** `"+dir+"`")
	harness.Wants(t, f.lastSent(), "**Pull request:** https://github.com/acme/app/pull/42")
	harness.Wants(t, f.lastSent(), "**Needs you:** the worktree above is a merged pull request's")

	// The hour between reads would otherwise read as the removal having started working, the
	// failure model taking a sweep that did not fail at a thing as that thing being over.
	f.at(600).sweep()
	harness.Equal(t, len(f.sent), 1, "messages sent by the sweep ten minutes later")
}

func TestAWorktreeThatGoesLaterClearsItsMessage(t *testing.T) {
	f, checkout, dir, _ := mergedFixture(t)
	f.git(checkout, "worktree", "lock", dir)

	f.sweep()
	f.git(checkout, "worktree", "unlock", dir)

	f.at(3600).sweep()
	gone(t, dir)
	harness.Wants(t, f.lastSent(), "🟢 The merged pull request's worktree is gone now")
}

// Removing the worktree is what hands the folder to the passes that follow, so the compose
// project a session left running in it goes down in the very same sweep.
func TestAComposeProjectOfAMergedWorktreeGoesDownInTheSameSweep(t *testing.T) {
	f, _, dir, _ := mergedFixture(t)
	f.containers = []Container{{Project: "app-widget", WorkingDir: dir}}
	f.volumes = []Volume{{Name: "app-widget_postgres-data", Project: "app-widget"}}

	out := f.sweep()
	gone(t, dir)

	harness.Wants(t, out, "removing compose project app-widget, whose worktree "+dir+" is gone")
	harness.Equal(t, strings.Join(f.downs, " "), "app-widget", "projects taken down")
	harness.Equal(t, strings.Join(f.removed, " "), "app-widget_postgres-data", "volumes removed")
}

// The stamp the next sweep reads to know the hour has not passed.
func TestTheTimeOfAReadIsWrittenBeforeTheWorkStarts(t *testing.T) {
	f, _, _, _ := mergedFixture(t)

	f.at(90).sweep()
	harness.Equal(t, f.state().Merged, base.Unix()+90, "the time pull requests were read")
}

// Nothing on a Mac where neither root exists, which is every machine but this one.
func TestASweepWithNoWorktreeRootsIsQuiet(t *testing.T) {
	f := newFixture(t)

	harness.Equal(t, f.sweep(), "", "the log of a sweep with no worktrees")
	harness.Equal(t, len(f.asked), 0, "pull requests read")
	harness.Equal(t, f.cfg.GCMergedEvery, time.Hour, "how often pull requests are read")
}
