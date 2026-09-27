#!/usr/bin/env bash
# Tells Claude, at session start, the names this worktree owns: its compose
# project, its dev URL, its Electron profile.
#
# Several sessions run at once, a worktree each, and every one of those names is
# shared state the moment two of them agree on it — one compose project is one
# database, one host port is bound by whichever session got there first, one
# Electron profile is one lock file. All three are derived from the worktree
# rather than chosen, so the alternative to printing them is every session
# guessing at the convention.
#
# Only what applies, and nothing at all otherwise: a project with no compose
# file, no portless and no Electron costs one exit.
#
# The worktree comes from the hook's own stdin. CLAUDE_PROJECT_DIR stays at the
# directory the session started in — Claude Code documents that it does not
# follow a session into a worktree — while the cwd field is the worktree root and
# moves with it.

# jq, for the hook's own input and for package.json, comes from the machine repo
# rather than from here: it is in mac-mini-setup's Brewfile, which that repo needs
# for itself. Without it this says nothing at all, which is the same as a project
# with none of the three.

set -uo pipefail

input="$(cat)"

cwd=""
if command -v jq >/dev/null 2>&1; then
  cwd="$(printf '%s' "$input" | jq -r '.cwd // empty' 2>/dev/null)"
fi
[ -n "$cwd" ] && [ -d "$cwd" ] || cwd="$PWD"

# One git call for all three paths. The top level first, because that is the one
# that fails outside a work tree and git stops at the failure: asked last, it
# would leave two paths on stdout and nothing to say the third was missing.
info="$(git -C "$cwd" rev-parse --path-format=absolute \
          --show-toplevel --git-dir --git-common-dir 2>/dev/null)"

root="${info%%$'\n'*}"
rest="${info#*$'\n'}"
git_dir="${rest%%$'\n'*}"
common_dir="${rest##*$'\n'}"

[ -n "$root" ] && [ -d "$root" ] || exit 0

# The remote name, not the directory: a worktree under ~/.herdr/worktrees/ is
# named for its branch. The same derivation project-docs.sh and .zshenv use,
# which is what makes the project name below the one a shell in this worktree has
# already exported.
repo="$(basename -s .git "$(git -C "$root" remote get-url origin 2>/dev/null)" 2>/dev/null)"
[ -n "$repo" ] || repo="$(basename "$(dirname "$common_dir")")"

branch="$(git -C "$root" branch --show-current 2>/dev/null)"
[ -n "$branch" ] || branch="$(basename "$root")"

linked=false
[ "$git_dir" != "$common_dir" ] && linked=true

compose_project="$(printf '%s-%s' "$repo" "$branch" |
                     tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9_-' '-')"
compose_project="${compose_project#"${compose_project%%[a-z0-9]*}"}"

compose_file=""
for candidate in compose.yaml compose.yml docker-compose.yaml docker-compose.yml; do
  if [ -f "$root/$candidate" ]; then
    compose_file="$candidate"
    break
  fi
done

package="$root/package.json"
portless=false
electron=false

# A script that runs portless, rather than a dependency on it: portless is
# installed once for the machine, so what says a project uses it is the command
# it starts its dev server with.
if command -v jq >/dev/null 2>&1 && [ -f "$package" ]; then
  jq -e '(.scripts // {}) | to_entries
         | any(.value | test("(^|[^a-z])portless"))' "$package" >/dev/null 2>&1 &&
    portless=true

  jq -e '(.dependencies // {}) + (.devDependencies // {}) | has("electron")' \
    "$package" >/dev/null 2>&1 && electron=true
fi

if [ -z "$compose_file" ] && [ "$portless" = false ] && [ "$electron" = false ]; then
  exit 0
fi

if [ "$linked" = true ]; then
  echo "Worktree $branch of $repo:"
else
  echo "Main worktree of $repo, on $branch:"
fi

if [ -n "$compose_file" ]; then
  echo "  compose project $compose_project, from $compose_file. Its volumes are this worktree's database, and \`docker compose down -v\` is what deletes it."
  echo "  Publish no fixed host port — \"5432\", not \"5432:5432\" — and read the one it got with \`docker compose port <service> 5432\`."
fi

if [ "$portless" = true ]; then
  # portless takes the name from portless.json, then package.json's portless key,
  # then the package name, and prepends the branch as a subdomain in a linked
  # worktree. It resolves a workspace root against the packages under it and
  # rewrites a branch name that is not a hostname label, and neither is worth
  # guessing at when the CLI answers for itself.
  name=""
  if command -v jq >/dev/null 2>&1; then
    [ -f "$root/portless.json" ] &&
      name="$(jq -r '.name // empty' "$root/portless.json" 2>/dev/null)"
    [ -n "$name" ] ||
      name="$(jq -r 'if (.portless | type) == "string" then .portless
                     else (.portless.name // empty) end' "$package" 2>/dev/null)"
    [ -n "$name" ] ||
      name="$(jq -r 'if has("workspaces") then empty else (.name // empty) end' \
                "$package" 2>/dev/null)"
  fi

  case "$name" in
    "" | *[!a-zA-Z0-9-]*) name="" ;;
  esac
  if [ "$linked" = true ]; then
    case "$branch" in
      *[!a-zA-Z0-9-]*) name="" ;;
    esac
  fi

  [ "$linked" = true ] && [ -n "$name" ] && name="$branch.$name"

  # The first TLD is the one PORTLESS_URL carries, and any after it is a name
  # Tim's MacBook reaches over the tailnet.
  if [ -z "$name" ]; then
    echo "  Served by portless, which names the URL itself — \`portless list\` prints it."
  else
    IFS=, read -r local_tld remote_tlds <<< "${PORTLESS_TLD:-localhost}"
    echo "  URL https://$name.$local_tld, portless's own for this worktree; \`portless list\` confirms it."
    IFS=, read -ra remote_tlds <<< "$remote_tlds"
    for tld in "${remote_tlds[@]}"; do
      echo "  From Tim's MacBook: https://$name.$tld, over the tailnet."
    done
  fi
fi

if [ "$electron" = true ]; then
  echo "  Electron: dev runs take a user-data profile named for the branch, which the project's dev script does, and debug ports of 0 — the one it got is in DevToolsActivePort in that profile folder."
fi
