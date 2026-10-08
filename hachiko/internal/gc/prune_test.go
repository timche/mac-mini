package gc

import (
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// The first prune of a day reclaims everything the ones after it would have, and a prune is
// the longest-running thing this sweep does — so the sweep that runs six times an hour prunes
// once, and the record of when it did is in its own state.
func TestDockerIsPrunedOnceADayRatherThanSixTimesAnHour(t *testing.T) {
	f := newFixture(t)
	f.reclaims("1.2GB", "300MB")

	harness.Wants(t, f.sweep(),
		"pruned what docker had not touched for 168h: build cache 1.2GB, dangling images 300MB")
	harness.Equal(t, len(f.dockerPruned), 2, "the commands the first sweep ran")
	harness.Equal(t, f.state().Pruned, base.Unix(), "the time it wrote down")

	f.at(600)
	harness.Equal(t, f.sweep(), "", "the log of the sweep ten minutes after it")
	harness.Equal(t, len(f.dockerPruned), 2, "the commands after a sweep ten minutes later")

	f.at(86400 + 600)
	f.sweep()
	harness.Equal(t, len(f.dockerPruned), 4, "the commands after a day has passed")
}

// The two commands and nothing else. A `docker system prune` would be one subprocess instead
// of two and would take volumes and networks with it — and a volume here is somebody's
// database, swept only by the rule that can tell a removed worktree's from a checkout's. No
// `-a` either, so a base image a project still names stays however long since anybody built.
func TestOnlyTheBuildCacheAndDanglingImagesArePruned(t *testing.T) {
	f := newFixture(t)
	f.reclaims("1.2GB", "300MB")
	f.sweep()

	harness.Equal(t, f.dockerPruned[0], "builder until=168h", "what the first command pruned")
	harness.Equal(t, f.dockerPruned[1], "image until=168h", "what the second command pruned")

	harness.Equal(t, len(f.removed), 0, "the volumes a prune removed")
	harness.Equal(t, len(f.downs), 0, "the compose projects a prune took down")
}

func TestADryRunSaysWhatItWouldPruneAndPrunesNothing(t *testing.T) {
	f := newFixture(t)
	f.reclaims("1.2GB", "300MB")

	log := f.dryRun()

	harness.Wants(t, log, "would run docker builder prune --force --filter until=168h")
	harness.Wants(t, log, "would run docker image prune --force --filter until=168h")
	harness.Equal(t, len(f.dockerPruned), 0, "the commands a dry run ran")
	harness.Equal(t, f.state().Pruned, int64(0), "the time a dry run wrote down")
}

// A session asking what the sweep would do is owed the reason as much as the command: a dry
// run that said nothing about the prune would read as a sweep that does not have one.
func TestADryRunSaysWhenTheNextPruneIsDue(t *testing.T) {
	f := newFixture(t)
	f.sweep()

	f.at(3600)
	harness.Wants(t, f.dryRun(), "docker was pruned 1 hour ago, so nothing would be pruned for another 23 hours")
}

// A Mac with OrbStack stopped has no cache to prune and no fault to report, and the day is
// not spent either: the prune happens as soon as there is a daemon to do it.
func TestNothingIsPrunedOrSaidWithDockerDown(t *testing.T) {
	f := newFixture(t)
	f.dockerUp = false
	f.reclaims("1.2GB", "300MB")

	harness.Equal(t, f.sweep(), "", "the log of a sweep with docker down")
	harness.Equal(t, len(f.dockerPruned), 0, "the commands it ran")
	harness.Equal(t, f.state().Pruned, int64(0), "the time docker being down wrote down")
}

// A prune that got nothing back did nothing, and this log is one line per thing done — which
// is what all but the first prune of a week finds, every day, for ever.
func TestAPruneThatReclaimedNothingSaysNothing(t *testing.T) {
	f := newFixture(t)

	harness.Equal(t, f.sweep(), "", "the log of a sweep whose prune got nothing back")
	harness.Equal(t, len(f.dockerPruned), 2, "the commands it still ran")
	harness.Equal(t, f.state().Pruned, base.Unix(), "the time it still wrote down")
}

// Through the same failure model as a compose project that will not go down: one message
// while it persists, and a line in the same thread when it stops. The difference is the day
// between prunes — a sweep that did not prune is not a prune that has started working.
func TestAPruneThatFailsIsOneMessageUntilAPruneWorks(t *testing.T) {
	f := newFixture(t)
	f.reclaims("1.2GB", "300MB")
	f.dockerPruneErr["builder"] = "Error response from daemon: a prune is already running"

	log := f.sweep()

	harness.Wants(t, log, "docker would not prune its build cache")
	// The images are the other half and pruned anyway: one of the two failing is not both.
	harness.Wants(t, log, "dangling images 300MB")

	harness.Wants(t, f.lastSent(), "Docker would not prune what nothing is using")
	harness.Wants(t, f.lastSent(), "build cache")
	harness.Wants(t, f.lastSent(), "a prune is already running")
	harness.Wants(t, f.lastSent(), "docker builder prune --force --filter until=168h")

	said := len(f.sent)
	f.at(600)
	f.sweep()

	harness.Equal(t, len(f.sent), said, "the messages a sweep that did not prune sent")
	harness.Equal(t, len(f.state().Posted), 1, "the failures still open ten minutes later")

	delete(f.dockerPruneErr, "builder")
	f.at(87000)
	f.sweep()

	harness.Wants(t, f.lastSent(), "Docker prunes what nothing is using again")
	harness.Equal(t, len(f.state().Posted), 0, "the failures still open after a prune worked")
}

// In the state file before the commands run rather than at the end of the sweep: the two
// prunes are the longest-running thing here, and a sweep killed or timed out between them is
// exactly the sweep that never reaches that save — and would prune again ten minutes later.
func TestTheTimeOfAPruneIsSavedBeforeThePruneRuns(t *testing.T) {
	f := newFixture(t)

	written := int64(-1)
	f.whilePruning = func() { written = f.state().Pruned }

	f.sweep()

	harness.Equal(t, written, base.Unix(), "the time the state file held while the prune was running")
}

// `docker image prune` prints "Total reclaimed space:" and buildkit's own prune prints
// "Total:" and a tab. Which of them a docker version prints is not something to depend on, so
// both are read — and a prune that printed neither is said to have printed nothing.
func TestEitherSpellingOfWhatDockerReclaimedIsRead(t *testing.T) {
	f := newFixture(t)
	f.dockerPruneOut["builder"] = "Total:\t2.3GB\n"
	f.dockerPruneOut["image"] = "Deleted Images:\ndeleted: sha256:abc\n\nTotal reclaimed space: 512MB\n"

	harness.Wants(t, f.sweep(), "build cache 2.3GB, dangling images 512MB")

	unreadable := newFixture(t)
	unreadable.dockerPruneOut["builder"] = "something else entirely\n"
	unreadable.dockerPruneOut["image"] = "Total reclaimed space: 1MB\n"

	harness.Wants(t, unreadable.sweep(), "build cache an amount it did not print")
}
