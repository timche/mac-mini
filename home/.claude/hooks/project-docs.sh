#!/usr/bin/env bash
# Tells Claude, at session start, that this project has working docs and what
# is in them.
#
# The global CLAUDE.md already says where a project's docs are, but a rule is
# only as good as it is remembered, and starting work without the docs means
# relitigating a decision they already record. Printing them is the difference
# between an instruction and a mechanism: stdout from a SessionStart hook is
# injected into the session context whether or not anything reads the rule.
#
# Only $HOME/projects/docs/<project>, one folder per project in the separate docs
# repository. A checkout's own docs/ is the project's published documentation,
# not its working docs, so it is never looked at.
#
# Says nothing at all when there are no docs for the project, so the cost on a
# project without them is one exit.
#
# The path and the file list, and nothing beyond them. What a project's docs
# are, how they are structured and what they are good for differ per project
# and are its own README's to say; how the folder syncs is the same everywhere
# and is the global CLAUDE.md's. A hook that repeated either would be asserting
# something it cannot know is still true.
#
# No instruction to read them either. That is in the global CLAUDE.md, and a
# sentence saying it again on every session start costs tokens forever to
# restate something already in context.

set -uo pipefail

DOCS_ROOT="$HOME/projects/docs"

cwd="${CLAUDE_PROJECT_DIR:-$PWD}"

git -C "$cwd" rev-parse --git-dir >/dev/null 2>&1 || exit 0

# The remote name, not the directory: a worktree under ~/.herdr/worktrees/ is
# named for the branch, and every branch of a repository shares one docs
# folder. Falls back to the main worktree's directory for a repo with no
# remote, which is what --git-common-dir points at.
project="$(basename -s .git "$(git -C "$cwd" remote get-url origin 2>/dev/null)" 2>/dev/null)"

if [ -z "$project" ]; then
  common_dir="$(git -C "$cwd" rev-parse --path-format=absolute --git-common-dir 2>/dev/null)"
  [ -n "$common_dir" ] && project="$(basename "$(dirname "$common_dir")")"
fi

[ -n "$project" ] || exit 0

docs="$DOCS_ROOT/$project"
[ -d "$docs" ] || exit 0

echo "Working docs for $project: $docs"
echo

# Top-level files by name, subdirectories as a name and a count. Listing every
# file put fifty lines into the context of every session on the largest
# project, most of them a folder of numbered plans that no session reads all
# of. The shape is what is worth injecting; the contents are one ls away.
#
# Paths rather than find's -printf, which is GNU's alone: on the Mac it is BSD
# find, which fails on the flag — behind the 2>/dev/null, so the listing would
# just be empty. wc -l is padded there too, hence the tr.
find "$docs" -maxdepth 1 -type f -name '*.md' 2>/dev/null |
  sort |
  while IFS= read -r file; do
    printf '  %s\n' "${file##*/}"
  done

find "$docs" -mindepth 1 -maxdepth 1 -type d -not -name '.git' -print 2>/dev/null |
  sort |
  while IFS= read -r dir; do
    files="$(find "$dir" -type f -name '*.md' 2>/dev/null | wc -l | tr -d '[:space:]')"
    printf '  %s/ (%s)\n' "${dir##*/}" "$files"
  done
