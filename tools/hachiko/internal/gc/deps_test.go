package gc

import (
	"strings"
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// What docker prints for `{{.Labels}}`, and the two keys in it that decide anything.
func TestAVolumesProjectAndWhetherItWasEverNamed(t *testing.T) {
	v := parseVolume("repeek-main_postgres-data",
		"com.docker.compose.project=repeek-main,com.docker.compose.volume=postgres-data", false)

	harness.Equal(t, v.Project, "repeek-main", "the project")
	harness.Equal(t, v.Anonymous, false, "whether it is anonymous")
	harness.Equal(t, v.InUse, false, "whether it is in use")
}

func TestAnAnonymousVolumeIsOneByLabelOrByName(t *testing.T) {
	labelled := parseVolume("app-x_cache",
		"com.docker.compose.project=app-x,com.docker.volume.anonymous=", true)
	harness.Equal(t, labelled.Anonymous, true, "whether a labelled one is anonymous")

	// What docker names a volume nobody named: 64 hex characters, and no label at all.
	named := parseVolume(strings.Repeat("ab", 32), "", false)
	harness.Equal(t, named.Anonymous, true, "whether a 64-character name is anonymous")

	// A volume somebody named that happens to be long is not one of those.
	ours := parseVolume(strings.Repeat("ab", 31)+"zz", "", false)
	harness.Equal(t, ours.Anonymous, false, "whether a name with no hex in it is anonymous")
}

// lsof prints a `p` line per process and an `n` line per file, and the sweep reads one pass
// of the whole machine rather than one call per pid.
func TestCWDsReadOnePassOfLsof(t *testing.T) {
	out := "p501\nfcwd\nn/Users/x/projects/app\np502\nfcwd\nn/Users/x/.herdr/worktrees/app/gone\n"

	got := parseCWDs(out)
	harness.Equal(t, len(got), 2, "the directories found")
	harness.Equal(t, got[0].PID, 501, "the first pid")
	harness.Equal(t, got[1].Dir, "/Users/x/.herdr/worktrees/app/gone", "the second directory")
}
