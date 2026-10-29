#!/bin/bash

# Deploys shibuya, the dead man's switch hachiko checks in with.
#
# This is the only thing that deploys the Worker: there is no release pipeline here and
# nothing to publish, exactly as for the Go tools. claude.sh runs it on the way in, so a
# fresh Mac has a switch rather than being reported offline a quarter of an hour after its
# first sweep; every deploy after that is a decision somebody makes, which is why no
# install runs it. What it does not do is set the secrets — tools/shibuya/secrets.sh is that,
# once, by hand, because a token generated on the Mac is not in 1Password for a re-run to
# find.

set -euo pipefail

if [ "$#" -gt 0 ]; then
  echo "usage: deploy.sh" >&2
  exit 2
fi

dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$dir/../.." && pwd)"
ping_token="${SHIBUYA_TOKEN_FILE:-$HOME/.hachiko/shibuya-token}"

# op with no token and a terminal offers to add a personal account rather than failing, so
# a Mac that has none is told instead of prompted at. claude.sh deploys once
# signing-key.sh has stored one, and skips this script until then — which is also what
# keeps CI from deploying.
op_token="${OP_SERVICE_ACCOUNT_TOKEN_FILE:-$HOME/.config/op/service-account-token}"
if [ -z "${OP_SERVICE_ACCOUNT_TOKEN:-}" ] && [ ! -f "$op_token" ]; then
  echo "no 1Password service-account token, so shibuya cannot be deployed — run" \
       "account/signing-key.sh, which stores one" >&2
  exit 1
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

# ~/.local/bin for the op wrapper, which signs op in with the stored service-account
# token: a provision's own PATH does not have it yet, and Homebrew's op on its own would
# offer to add a personal account instead.
PATH="$(dirname "$node"):$HOME/.local/bin:$PATH"
export PATH

cd "$dir"

npm ci
npm test

# One --env-file, and the reference rather than the value: the API token reaches wrangler's
# environment and nothing else, and the bot token is not needed here at all —
# secrets.sh is what puts that in the Worker.
op run --env-file "$dir/.env.op" -- npx wrangler deploy

echo "deployed shibuya"

# Said after the deploy rather than instead of it: a Worker with no PING_TOKEN set is one
# that answers 401 to every check-in, and hachiko with no token file does not check in at
# all, so a Mac that has never run secrets.sh has a switch that is deployed and deaf.
if [ ! -f "$ping_token" ]; then
  echo "$ping_token is missing, so nothing on this Mac can check in with shibuya —" \
       "run tools/shibuya/secrets.sh once to make the token and set the Worker's secrets" >&2
fi
