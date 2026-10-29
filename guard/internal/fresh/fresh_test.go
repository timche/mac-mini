package fresh

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Every tree starts with the binary newer than every source in it, which is what a build
// leaves behind, and each case then moves one time. The three are hours apart so that no
// filesystem's timestamp resolution can blur them together.
var (
	laidOut = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	built   = laidOut.Add(time.Hour)
	edited  = built.Add(time.Hour)
)

func TestStale(t *testing.T) {
	tests := []struct {
		name string
		// change moves what the case is about, after the tree has been laid out and the
		// binary built.
		change func(t *testing.T, src string)
		params func(t *testing.T, p *Params)
		want   bool
	}{
		{
			name: "a tree nothing has touched since the build is fresh",
		},
		{
			name:   "a source file written after the build is stale",
			change: func(t *testing.T, src string) { touch(t, filepath.Join(src, "internal", "shell", "shell.go"), edited) },
			want:   true,
		},
		{
			name:   "go.mod written after the build is stale",
			change: func(t *testing.T, src string) { touch(t, filepath.Join(src, "go.mod"), edited) },
			want:   true,
		},
		{
			name:   "go.sum written after the build is stale",
			change: func(t *testing.T, src string) { touch(t, filepath.Join(src, "go.sum"), edited) },
			want:   true,
		},
		{
			// The file whose time would say so is the file that is gone, so a deletion
			// and a rename show up on the folder alone.
			name: "a source file deleted after the build is stale",
			change: func(t *testing.T, src string) {
				dir := filepath.Join(src, "internal", "shell")
				if err := os.Remove(filepath.Join(dir, "shell.go")); err != nil {
					t.Fatal(err)
				}
				touch(t, dir, edited)
			},
			want: true,
		},
		{
			// What a failed build leaves: sources newer than the binary, and a marker
			// saying they have already been tried.
			name: "a source file older than the marker is fresh",
			change: func(t *testing.T, src string) {
				touch(t, filepath.Join(src, "main.go"), edited)
			},
			params: func(t *testing.T, p *Params) { touch(t, p.Marker, edited.Add(time.Minute)) },
			want:   false,
		},
		{
			name:   "a file that is not a source is fresh however new it is",
			change: func(t *testing.T, src string) { touch(t, filepath.Join(src, "README.md"), edited) },
			want:   false,
		},
		{
			name:   "a source folder that is not there is fresh",
			change: func(t *testing.T, src string) { os.RemoveAll(src) },
			want:   false,
		},
		{
			name:   "a binary that is not there is fresh",
			params: func(t *testing.T, p *Params) { os.Remove(p.Binary) },
			want:   false,
		},
		{
			// A `go run`, a test binary, a build by hand: no wrapper wrote a folder in,
			// so there is nothing to compare and nothing to hand the event to.
			name:   "a binary with no source folder written into it is fresh",
			change: func(t *testing.T, src string) { touch(t, filepath.Join(src, "main.go"), edited) },
			params: func(t *testing.T, p *Params) { p.Source = "" },
			want:   false,
		},
		{
			name:   "a process the wrapper started is fresh",
			change: func(t *testing.T, src string) { touch(t, filepath.Join(src, "main.go"), edited) },
			params: func(t *testing.T, p *Params) { p.Wrapped = true },
			want:   false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params := tree(t)
			if test.change != nil {
				test.change(t, params.Source)
			}
			if test.params != nil {
				test.params(t, &params)
			}
			if got := Stale(params); got != test.want {
				t.Errorf("Stale() = %v, want %v", got, test.want)
			}
		})
	}
}

// A source folder shaped like guard's own, a cache holding the binary and the marker, and
// every time in it set: the build after the sources, the marker with the sources.
func tree(t *testing.T) Params {
	t.Helper()

	root := t.TempDir()
	src := filepath.Join(root, "guard")
	cache := filepath.Join(root, "cache")

	for _, dir := range []string{
		filepath.Join(src, "internal", "shell"),
		filepath.Join(src, "internal", "focus"),
		cache,
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	files := []string{
		filepath.Join(src, "main.go"),
		filepath.Join(src, "go.mod"),
		filepath.Join(src, "go.sum"),
		filepath.Join(src, "README.md"),
		filepath.Join(src, "internal", "shell", "shell.go"),
		filepath.Join(src, "internal", "shell", "shell_test.go"),
		filepath.Join(src, "internal", "focus", "focus.go"),
	}
	params := Params{
		Source: src,
		Binary: filepath.Join(cache, "guard"),
		Marker: filepath.Join(cache, "attempted"),
	}
	for _, path := range append(files, params.Marker) {
		if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		touch(t, path, laidOut)
	}
	if err := os.WriteFile(params.Binary, []byte("x\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Deepest first: writing a file sets the folder holding it, so the folders are
	// stamped after everything in them.
	for _, dir := range []string{
		filepath.Join(src, "internal", "shell"),
		filepath.Join(src, "internal", "focus"),
		filepath.Join(src, "internal"),
		src,
	} {
		touch(t, dir, laidOut)
	}
	touch(t, params.Binary, built)

	return params
}

func touch(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}
