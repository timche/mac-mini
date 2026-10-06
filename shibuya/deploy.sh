#!/bin/bash

# Deploys shibuya, the dead man's switch hachiko checks in with, and does nothing at all
# when its sources have not changed since the last deploy that worked.
#
# The hash is the whole point. install.sh calls this on every account install and an
# install is run after every pull, so without it each one would spend an `op run` against
# the service account's daily limit, a Cloudflare deploy, and an npm install — to upload
# the same bundle. So a re-run with nothing to do touches neither 1Password nor the
# network and says where it stands.
#
# This is the only thing that deploys the Worker: there is no release pipeline here and
# nothing to publish, exactly as for the Go tools, and a deploy is a decision rather than
# a timer. What it does not do is set the secrets — shibuya/secrets.sh is that, once, by
# hand, because a token generated on the Mac is not in 1Password for a re-run to find.

set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
dir="$repo/shibuya"
state="${XDG_STATE_HOME:-$HOME/.local/state}/mac-mini"
stamp="$state/shibuya.sha256"
token_file="${SHIBUYA_TOKEN_FILE:-$HOME/.config/hachiko/shibuya-token}"

force=false
for arg in "$@"; do
  case "$arg" in
    --force) force=true ;;
    *)
      echo "usage: deploy.sh [--force]" >&2
      exit 2
      ;;
  esac
done

# The contents and the names both, so a renamed or deleted source counts as a change. The
# lockfile is in it because a wrangler or a vitest that moved is a different deploy, and
# node_modules is not because npm's own record below answers for that.
# Relative to shibuya/, because shasum prints the path it hashed and an absolute one would
# make the same sources hash differently in a worktree than in the checkout.
hash="$(
  cd "$dir" &&
    find src -type f 2>/dev/null | sort |
    xargs shasum -a 256 2>/dev/null |
    cat - <(shasum -a 256 wrangler.jsonc package.json package-lock.json tsconfig.json vitest.config.ts) |
    shasum -a 256 | cut -d' ' -f1
)"

if [ "$force" = false ] && [ "$hash" = "$(cat "$stamp" 2>/dev/null)" ]; then
  echo "shibuya is already deployed from these sources"
  exit 0
fi

# mise's node, which is every project's here, asked about this checkout rather than about
# the working directory: mise decides what is active from the directory it is asked about,
# and from anywhere else nothing this repo declares is.
mise="$(command -v mise 2>/dev/null)" || mise="$HOME/.local/bin/mise"

if [ ! -x "$mise" ]; then
  echo "mise is missing, so shibuya cannot be built — run install.sh" >&2
  exit 1
fi

node="$("$mise" which -C "$repo" node 2>/dev/null)" || node=""
if [ -z "$node" ]; then
  echo "the node mise installs globally is not there, so shibuya cannot be built —" \
       "run install.sh" >&2
  exit 1
fi

PATH="$(dirname "$node"):$PATH"
export PATH

cd "$dir"

# npm's own record of what it installed from, which is what says the tree matches the
# lockfile. `npm ci` deletes node_modules and installs it again, so it is not something to
# run on a tree that is already right.
if ! cmp -s node_modules/.package-lock.json package-lock.json; then
  npm ci
fi

npm test

# One --env-file, and the reference rather than the value: the API token reaches wrangler's
# environment and nothing else, and the webhook is not needed here at all — secrets.sh is
# what puts that in the Worker.
op run --env-file "$dir/.env.op" -- npx wrangler deploy

mkdir -p "$state"
printf '%s\n' "$hash" >"$stamp"
echo "deployed shibuya"

# Said after the deploy rather than instead of it: a Worker with no PING_TOKEN set is one
# that answers 401 to every check-in, and hachiko with no token file does not check in at
# all, so a Mac that has never run secrets.sh has a switch that is deployed and deaf.
if [ ! -f "$token_file" ]; then
  echo "$token_file is missing, so nothing on this Mac can check in with shibuya —" \
       "run shibuya/secrets.sh once to make the token and set both Worker secrets" >&2
fi
