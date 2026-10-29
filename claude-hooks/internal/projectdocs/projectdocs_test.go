package projectdocs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A case builds a docs root and a directory to run in, and says what the listing of the
// two is. ROOT in what it wants stands for the docs root, which is a temporary directory
// and so different every run.
type testCase struct {
	about string

	// docs is the docs root, a path per entry: a trailing slash is a directory, an
	// arrow a symlink to another entry, anything else a file.
	docs []string

	// origin is the remote the project directory is given, and dir the name of the
	// directory the repository is made in, which is what the listing falls back to
	// without a remote. No dir at all is a directory that is not a repository.
	origin   string
	dir      string
	worktree bool

	// byHome reaches the docs root through $HOME rather than $PROJECT_DOCS_DIR, and
	// byCwd the project through the working directory rather than $CLAUDE_PROJECT_DIR.
	byHome bool
	byCwd  bool

	// slash asks for the docs root with a trailing slash, as a session may be
	// configured with one.
	slash bool

	want string
}

var cases = []testCase{
	{
		about: "outside a repository there is still the shared folder",
		docs:  []string{"shared/cloudflare.md", "shared/README.md", "widget/decisions.md"},
		want: `Shared docs for every project: ROOT/shared

  cloudflare.md
  README.md

`,
	},
	{
		about:  "a repository with docs of its own gets both",
		docs:   []string{"shared/cloudflare.md", "widget/decisions.md", "widget/prs.md"},
		origin: "https://example.com/nobody/widget.git",
		want: `Shared docs for every project: ROOT/shared

  cloudflare.md

Working docs for widget: ROOT/widget

  decisions.md
  prs.md
`,
	},
	{
		about:  "a docs root with no shared folder starts at the project",
		docs:   []string{"widget/decisions.md"},
		origin: "https://example.com/nobody/widget.git",
		want: `Working docs for widget: ROOT/widget

  decisions.md
`,
	},
	{
		// Every session on every other project would otherwise start with an
		// apology for a folder that was never going to be there.
		about:  "a project with no docs folder is not mentioned",
		docs:   []string{"shared/cloudflare.md"},
		origin: "https://example.com/nobody/no-docs-here.git",
		want: `Shared docs for every project: ROOT/shared

  cloudflare.md

`,
	},
	{
		about:  "an empty docs root says nothing at all",
		docs:   nil,
		origin: "https://example.com/nobody/widget.git",
		want:   "",
	},
	{
		// A worktree is named for its branch and every branch of a repository
		// shares one docs folder, so the remote is what the folder is looked up by.
		about:    "a linked worktree is named for the remote and not for its directory",
		docs:     []string{"widget/decisions.md"},
		origin:   "https://example.com/nobody/widget.git",
		dir:      "widget-some-branch",
		worktree: true,
		want: `Working docs for widget: ROOT/widget

  decisions.md
`,
	},
	{
		about: "a repository with no remote is named for its directory",
		docs:  []string{"widget/decisions.md"},
		dir:   "widget",
		want: `Working docs for widget: ROOT/widget

  decisions.md
`,
	},
	{
		about:    "a worktree of a repository with no remote is named for the main one",
		docs:     []string{"widget/decisions.md"},
		dir:      "widget",
		worktree: true,
		want: `Working docs for widget: ROOT/widget

  decisions.md
`,
	},
	{
		// The order `sort` put these in, which is the order the listing had
		// before it was this program. internal/collate has the rules.
		about:  "the listing is in the shell's order, not a byte order",
		docs:   []string{"widget/prs.md", "widget/README.md", "widget/secrets.md", "widget/mac-mini.md", "widget/mac.md", "widget/10-plan.md", "widget/2-plan.md"},
		origin: "https://example.com/nobody/widget.git",
		want: `Working docs for widget: ROOT/widget

  10-plan.md
  2-plan.md
  mac-mini.md
  mac.md
  prs.md
  README.md
  secrets.md
`,
	},
	{
		// The shape is what is worth injecting: a folder of numbered plans put
		// fifty lines into the context of every session on the largest project.
		about: "a subdirectory is a name and a count of every .md below it",
		docs: []string{
			"widget/decisions.md",
			"widget/notes.markdown",
			"widget/plans/a.md",
			"widget/plans/2026/b.md",
			"widget/plans/2026/deep/c.md",
			"widget/plans/2026/deep/not.txt",
			"widget/empty/",
			"widget/Archive/old.md",
		},
		origin: "https://example.com/nobody/widget.git",
		want: `Working docs for widget: ROOT/widget

  decisions.md
  Archive/ (1)
  empty/ (0)
  plans/ (3)
`,
	},
	{
		// The docs folder is a checkout, so its .git is a subdirectory like any
		// other until it is left out; one nested inside a subdirectory is not,
		// the count standing for everything under that subdirectory.
		about: "the docs folder's own .git is not in the listing",
		docs: []string{
			"widget/.git/config.md",
			"widget/decisions.md",
			"widget/sub/.git/inside.md",
			"widget/sub/n.md",
		},
		origin: "https://example.com/nobody/widget.git",
		want: `Working docs for widget: ROOT/widget

  decisions.md
  sub/ (2)
`,
	},
	{
		// `find -type f` and `-type d` are what this listed with, and neither
		// follows a link.
		about: "a symlink is neither a file nor a subdirectory",
		docs: []string{
			"widget/decisions.md",
			"widget/plans/a.md",
			"widget/link.md -> decisions.md",
			"widget/linkdir -> plans",
		},
		origin: "https://example.com/nobody/widget.git",
		want: `Working docs for widget: ROOT/widget

  decisions.md
  plans/ (1)
`,
	},
	{
		about:  "the docs root is $HOME/projects/docs when nothing says otherwise",
		docs:   []string{"shared/cloudflare.md"},
		byHome: true,
		origin: "https://example.com/nobody/widget.git",
		want: `Shared docs for every project: ROOT/shared

  cloudflare.md

`,
	},
	{
		about:  "the project is the working directory when nothing says otherwise",
		docs:   []string{"widget/decisions.md"},
		origin: "https://example.com/nobody/widget.git",
		byCwd:  true,
		want: `Working docs for widget: ROOT/widget

  decisions.md
`,
	},
	{
		// The paths are printed, so they are the paths the session was
		// configured with rather than tidied versions of them.
		about:  "a docs root with a trailing slash is printed as it was given",
		docs:   []string{"shared/cloudflare.md"},
		slash:  true,
		origin: "https://example.com/nobody/widget.git",
		want: `Shared docs for every project: ROOT//shared

  cloudflare.md

`,
	},
}

func TestPrint(t *testing.T) {
	for _, c := range cases {
		t.Run(c.about, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)

			root := filepath.Join(t.TempDir(), "docs")
			if c.byHome {
				root = home + "/projects/docs"
			}
			build(t, root, c.docs)

			t.Setenv("PROJECT_DOCS_DIR", "")
			if !c.byHome {
				given := root
				if c.slash {
					given += "/"
				}
				t.Setenv("PROJECT_DOCS_DIR", given)
			}

			t.Setenv("CLAUDE_PROJECT_DIR", "")
			if dir := project(t, c); dir != "" {
				if c.byCwd {
					t.Chdir(dir)
				} else {
					t.Setenv("CLAUDE_PROJECT_DIR", dir)
				}
			}

			out := &strings.Builder{}
			Print(out)

			want := strings.ReplaceAll(c.want, "ROOT", root)
			if out.String() != want {
				t.Errorf("the listing is\n%s\nand not\n%s", out, want)
			}
		})
	}
}

// The directory the hook is run against: not a repository when the case gives neither a
// remote nor a name, and a linked worktree of one when it asks for that.
func project(t *testing.T, c testCase) string {
	t.Helper()

	if c.origin == "" && c.dir == "" {
		return t.TempDir()
	}

	name := c.dir
	if name == "" {
		name = "checkout"
	}
	repo := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "init", "-q", "-b", "main")
	if c.origin != "" {
		run(t, repo, "remote", "add", "origin", c.origin)
	}
	if !c.worktree {
		return repo
	}

	// A worktree needs a commit to branch from, and an identity to make one with that
	// the account's own configuration is not there to give: HOME is the fixture.
	run(t, repo, "-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false",
		"commit", "-q", "--allow-empty", "-m", "first")
	linked := filepath.Join(filepath.Dir(repo), name+"-branch")
	run(t, repo, "worktree", "add", "-q", linked, "-b", "branch")
	return linked
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

// An entry is a file, a directory when it ends in a slash, or a symlink when it names
// what it points at.
func build(t *testing.T, root string, entries []string) {
	t.Helper()

	for _, entry := range entries {
		if name, target, ok := strings.Cut(entry, " -> "); ok {
			path := filepath.Join(root, name)
			if err := os.Symlink(filepath.Join(filepath.Dir(path), target), path); err != nil {
				t.Fatal(err)
			}
			continue
		}

		path := filepath.Join(root, entry)
		dir := filepath.Dir(path)
		if strings.HasSuffix(entry, "/") {
			dir = path
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if dir == path {
			continue
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
