#!/bin/bash

# Puts shibuya's two secrets into the Worker, and makes the ping token first if there is
# none. Run by hand, once per Mac, and again to rotate: delete the token file and re-run.
#
# The ping token is the one secret here that is deliberately not in 1Password. Writing a
# resolved `op://` value into a file is the thing this machine never does, and hachiko has
# to read the token twice an hour for ever — 288 `op run` calls a day against a service
# account with a daily limit, for a secret whose whole power is to fake a check-in. So it
# is generated here, on the Mac, kept at 0600, and handed to Cloudflare once; nothing ever
# resolves it from anywhere.
#
# The webhook is the other way round: it is Discord's, hachiko already references it, and
# it reaches this script only through `op run`'s environment. Neither value is ever an
# argument — `ps` shows every process on this Mac the command line of every other — and
# neither is echoed. What this prints is the names it set.

set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
dir="$repo/shibuya"
token_file="${SHIBUYA_TOKEN_FILE:-$HOME/.config/hachiko/shibuya-token}"

# The second half of this script, reached only under `op run`, which is the one step that
# sees either secret.
if [ "${1:-}" = "--under-op" ]; then
  cd "$dir"

  if [ -z "${CLOUDFLARE_API_TOKEN:-}" ]; then
    echo "CLOUDFLARE_API_TOKEN did not resolve, so no secret was set — ask Tim about" \
         "op://dev/shibuya/api token" >&2
    exit 1
  fi
  if [ -z "${DISCORD_WEBHOOK_URL:-}" ]; then
    echo "DISCORD_WEBHOOK_URL did not resolve, so no secret was set — ask Tim about" \
         "op://dev/hachiko-discord/webhook url" >&2
    exit 1
  fi

  # Down a pipe in both cases. `tr` is what makes a token file somebody rotated by hand
  # match the secret: hachiko trims what it reads, so a trailing newline that reached
  # Cloudflare would be a 401 on every check-in with nothing to show why.
  tr -d '\r\n' <"$token_file" | npx wrangler secret put PING_TOKEN >/dev/null
  echo "set PING_TOKEN"

  printf '%s' "$DISCORD_WEBHOOK_URL" | npx wrangler secret put DISCORD_WEBHOOK_URL >/dev/null
  echo "set DISCORD_WEBHOOK_URL"
  exit 0
fi

if [ "$#" -gt 0 ]; then
  echo "usage: secrets.sh" >&2
  exit 2
fi

if [ ! -f "$token_file" ]; then
  mkdir -p "$(dirname "$token_file")"

  # umask before the redirection, so the file is never world-readable even for the instant
  # between being created and being chmod'ed.
  (
    umask 077
    if [ -x /usr/bin/openssl ]; then
      /usr/bin/openssl rand -hex 32 | tr -d '\n' >"$token_file"
    else
      dd if=/dev/urandom bs=32 count=1 2>/dev/null | xxd -p | tr -d '\n ' >"$token_file"
    fi
  )
  echo "made $token_file"
fi

if [ ! -s "$token_file" ]; then
  echo "$token_file is empty, so there is no token to set — delete it and re-run" >&2
  exit 1
fi

# hachiko refuses a token anyone but the account can read and says so once, which would be
# a Mac that never checks in. Fixed here rather than reported, since this script is what
# owns the file.
chmod 600 "$token_file"

mise="$(command -v mise 2>/dev/null)" || mise="$HOME/.local/bin/mise"
node="$("$mise" which -C "$repo" node 2>/dev/null)" || node=""

if [ -z "$node" ]; then
  echo "the node mise installs globally is not there, so wrangler cannot run —" \
       "run install.sh" >&2
  exit 1
fi

PATH="$(dirname "$node"):$PATH"
export PATH

exec op run --env-file "$dir/.env.op" -- "$dir/secrets.sh" --under-op
