package gc

import (
	"strings"
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// The case this rule exists for. `repeek-main_postgres-data` is the main checkout's
// database, sitting there with no container in front of it because nobody has started it
// today — a dangling volume of a project named exactly like a worktree's. It is never
// touched, because no container of that project was ever seen inside a worktree root.
func TestAMainCheckoutsIdleDatabaseIsNeverRemoved(t *testing.T) {
	f := newFixture(t)

	f.containers = []Container{
		{Project: "repeek-main", WorkingDir: f.mkdir(f.home + "/projects/repeek")},
	}
	f.volumes = []Volume{
		{Name: "repeek-main_postgres-data", Project: "repeek-main"},
	}

	harness.Equal(t, f.sweep(), "", "the log of a sweep with nothing to do")
	harness.Equal(t, len(f.removed), 0, "volumes removed")
	harness.Equal(t, len(f.state().Projects), 0, "projects recorded")
}

// The same shape, in a worktree: seen running there once, and removed once that worktree
// has gone.
func TestAVolumeOfARemovedWorktreesProjectGoes(t *testing.T) {
	f := newFixture(t)
	worktree := f.herdrWorktree("repeek", "fix-ui", true)

	f.containers = []Container{{Project: "repeek-fix-ui", WorkingDir: worktree}}
	f.volumes = []Volume{{Name: "repeek-fix-ui_postgres-data", Project: "repeek-fix-ui", InUse: true}}

	f.at(0).sweep()
	harness.Equal(t, len(f.removed), 0, "volumes removed while the worktree is there")
	harness.Equal(t, f.state().Projects["repeek-fix-ui"].Worktree, worktree, "the worktree recorded")

	// The worktree goes, and with it the containers that named it. The volume is all that is
	// left, and the record is the only thing that says where it came from.
	remove(t, worktree)
	f.containers = nil
	f.volumes[0].InUse = false

	out := f.at(600).sweep()
	harness.Wants(t, out, "removing volume repeek-fix-ui_postgres-data of compose project repeek-fix-ui")
	harness.Equal(t, strings.Join(f.removed, ","), "repeek-fix-ui_postgres-data", "volumes removed")

	// Nothing of that project is left to find, so there is nothing for the record to
	// authorise ever again.
	harness.Equal(t, len(f.state().Projects), 0, "projects recorded")
}

// The branch outlives its worktree: checked out in the main checkout afterwards, it is the
// same project and the same volumes, and the database is that checkout's now.
func TestABranchCheckedOutInTheMainCheckoutKeepsItsVolumes(t *testing.T) {
	f := newFixture(t)
	worktree := f.herdrWorktree("repeek", "fix-ui", true)

	f.containers = []Container{{Project: "repeek-fix-ui", WorkingDir: worktree}}
	f.volumes = []Volume{{Name: "repeek-fix-ui_postgres-data", Project: "repeek-fix-ui", InUse: true}}
	f.at(0).sweep()

	remove(t, worktree)
	f.containers = []Container{{Project: "repeek-fix-ui", WorkingDir: f.mkdir(f.home + "/projects/repeek")}}
	f.at(600).sweep()
	harness.Equal(t, len(f.state().Projects), 0, "projects recorded once the checkout ran it")

	f.containers = nil
	f.volumes[0].InUse = false
	f.at(1200).sweep()
	harness.Equal(t, len(f.removed), 0, "volumes removed after the checkout stopped")
}

func TestAVolumeInUseAndAnAnonymousOneAreNeverRemoved(t *testing.T) {
	f := recordedThenRemoved(t)
	f.volumes = []Volume{
		{Name: "app-gone_data", Project: "app-gone", InUse: true},
		{Name: "app-gone_cache", Project: "app-gone", Anonymous: true},
		{Name: strings.Repeat("ab", 32), Project: "app-gone", Anonymous: true},
	}

	f.at(600).sweep()
	harness.Equal(t, len(f.removed), 0, "volumes removed")

	// Something of the project is still there, so the record stays and the next sweep can
	// still act on it.
	harness.Equal(t, len(f.state().Projects), 1, "projects recorded")
}

func TestAVolumeOfAWorktreeThatIsStillThereIsNeverRemoved(t *testing.T) {
	f := newFixture(t)
	worktree := f.herdrWorktree("app", "live", true)

	f.containers = []Container{{Project: "app-live", WorkingDir: worktree}}
	f.volumes = []Volume{{Name: "app-live_data", Project: "app-live"}}

	harness.Equal(t, f.sweep(), "", "the log of a sweep with nothing to do")
	harness.Equal(t, len(f.removed), 0, "volumes removed")
}

func TestAVolumeWithNoProjectOfItsOwnIsNeverRemoved(t *testing.T) {
	f := recordedThenRemoved(t)
	f.volumes = []Volume{{Name: "something-somebody-made"}}

	f.at(600).sweep()
	harness.Equal(t, len(f.removed), 0, "volumes removed")
}

func TestAVolumeThatWillNotGoIsOneMessageAndKeepsItsRecord(t *testing.T) {
	f := recordedThenRemoved(t)
	f.volumes = []Volume{{Name: "app-gone_data", Project: "app-gone"}}
	f.volumeErr["app-gone_data"] = "Error response from daemon: volume is in use"

	out := f.at(600).sweep()
	harness.Wants(t, out, "volume app-gone_data would not go: Error response from daemon: volume is in use")

	harness.Wants(t, f.lastSent(), "⚠️ A removed worktree's volume would not go")
	harness.Wants(t, f.lastSent(), "**Volume:** `app-gone_data`")
	harness.Wants(t, f.lastSent(), "**You can run:** `docker volume rm app-gone_data`")

	harness.Equal(t, len(f.state().Projects), 1, "the record the next sweep needs")
}

// A sweep that saw the project in a worktree, and a worktree that has since gone.
func recordedThenRemoved(t *testing.T) *fixture {
	t.Helper()

	f := newFixture(t)
	worktree := f.herdrWorktree("app", "gone", true)
	f.containers = []Container{{Project: "app-gone", WorkingDir: worktree}}
	f.volumes = []Volume{{Name: "app-gone_data", Project: "app-gone", InUse: true}}

	f.at(0).sweep()
	remove(t, worktree)
	f.containers = nil

	return f
}
