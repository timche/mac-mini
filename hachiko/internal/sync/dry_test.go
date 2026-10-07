package sync

import (
	"strings"
	"testing"
	"time"
)

// The whole of what a dry run may do: fetch and read. It is how sync runs beside the daemon
// it replaces, so anything else would be two things committing the same trees.
func TestADryRunCommitsPushesAndPostsNothing(t *testing.T) {
	f := newFixture(t)
	f.dry = true
	f.tree.unpushed = []string{"deadbee mine"}
	f.tree.oldest = base
	f.tree.behind = []string{"cafe123 theirs"}

	_, l := f.loop()
	write(f, l, " M a.md\x00", "a.md")

	for _, forbidden := range []string{"add ", "commit ", "push ", "pull ", "rebase "} {
		if f.ranAny(forbidden) {
			t.Errorf("a dry run ran `git %s`: %v", forbidden, f.calls)
		}
	}
	if len(f.sent) != 0 {
		t.Errorf("a dry run posted: %v", f.sent)
	}
	if len(f.state().Posted) != 0 || len(f.state().Paused) != 0 {
		t.Errorf("a dry run wrote state: %+v", f.state())
	}
}

func TestADryRunSaysWhatItWouldCommitAndPush(t *testing.T) {
	f := newFixture(t)
	f.dry = true
	f.tree.unpushed = []string{"deadbee one", "cafebab two"}
	f.tree.oldest = base
	f.tree.behind = []string{"cafe123 theirs"}

	_, l := f.loop()
	f.log.Reset()

	write(f, l, " M a.md\x00?? b.md\x00")

	log := f.log.String()
	for _, says := range []string{
		"would commit `Update a.md, b.md`",
		"would push 2 commits to origin",
		"would rebase onto 1 commit from origin",
	} {
		if !strings.Contains(log, says) {
			t.Errorf("the log does not say %q:\n%s", says, log)
		}
	}
}

func TestADryRunSaysAPushItWouldHold(t *testing.T) {
	f := newFixture(t)
	f.dry = true
	f.repo.PushDelay = time.Hour
	f.tree.unpushed = []string{"deadbee mine"}
	f.tree.oldest = base.Add(-30 * time.Minute)

	f.loop()

	if log := f.log.String(); !strings.Contains(log, "would hold 1 commit for another 30m0s") {
		t.Errorf("log: %s", log)
	}
}

// A dry run commits nothing, so the tree stays dirty for ever — and a line a second about
// the same uncommitted file is a log nobody reads.
func TestADryRunSaysTheSameThingOnce(t *testing.T) {
	f := newFixture(t)
	f.dry = true
	f.repo.FetchInterval = time.Hour

	_, l := f.loop()
	write(f, l, " M a.md\x00", "a.md")
	f.log.Reset()

	for i := 0; i < 30; i++ {
		tick(f, l, time.Second)
	}
	if log := f.log.String(); log != "" {
		t.Errorf("log: %s", log)
	}

	// A further write is a new thing to say.
	write(f, l, " M a.md\x00 M b.md\x00", "a.md", "b.md")

	if log := f.log.String(); !strings.Contains(log, "would commit `Update a.md, b.md`") {
		t.Errorf("log: %s", log)
	}
}

// A rename is two records in `--porcelain=v1 -z`, the new path and then the old one, and
// only the new one is a path that changed.
func TestTheStatusPathsADryRunReads(t *testing.T) {
	got := statusPaths(" M a.md\x00?? b.md\x00R  new.md\x00old.md\x00A  c/d.md\x00")
	want := []string{"a.md", "b.md", "new.md", "c/d.md"}

	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
	if statusPaths("") != nil {
		t.Error("a clean tree has no paths")
	}
}
