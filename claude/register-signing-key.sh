#!/bin/bash

# Put the public half of the signing key on the GitHub account. signing-key.sh
# trusts the key on this machine; this is the other half, and the only part of it
# that needs an account rather than a file.
#
# The key comes from the agent holding it, or from 1Password when the agent is not
# up yet. Never from a file: nothing here writes one, and the agent is the same
# place git asks under gpg.ssh.defaultKeyCommand, so what gets registered is what
# signatures will carry.
#
# Everything that stops it is a skip, not a failure: no key to be had yet, no gh,
# gh not logged in, or a token without the scope. On a fresh Mac all of those are
# true before signing-key.sh has run.
#
# Safe to re-run: a key already on the account is left alone.

set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# The agent ssh-agent.sh keeps, named rather than read from SSH_AUTH_SOCK: launchd
# hands every session a socket of its own whose agent holds nothing of this.
socket="$HOME/.ssh/agent.sock"

item="${SIGNING_KEY_OP_ITEM:-op://dev/ssh-commit-signing}"
token_file="${OP_SERVICE_ACCOUNT_TOKEN_FILE:-$HOME/.config/op/service-account-token}"

public=""

# The first key, because that is the one git signs with when it asks the agent.
if [ -S "$socket" ]; then
  public="$(SSH_AUTH_SOCK="$socket" ssh-add -L 2>/dev/null | head -n 1 || true)"
fi

if [ -z "$public" ] && command -v op >/dev/null 2>&1 && [ -f "$token_file" ]; then
  public="$(OP_SERVICE_ACCOUNT_TOKEN="$(cat "$token_file")" \
    op read "$item/public key" || true)"
fi

if [ -z "$public" ]; then
  echo "the agent at $socket holds no key and $item could not be read, so" >&2
  echo "nothing was registered — run $repo/signing-key.sh first" >&2
  exit 0
fi

staged="$(mktemp)"
trap 'rm -f "$staged"' EXIT
printf '%s\n' "$public" >"$staged"

# Whatever served it could have served something else — a field holding a note, an
# agent answering with a line that is not a key — and a key GitHub accepted but
# cannot match would be found out months later as a signature nobody can verify.
if ! ssh-keygen -l -f "$staged" >/dev/null 2>&1; then
  echo "what came back is not a public key ssh-keygen recognises, so nothing" >&2
  echo "was registered" >&2
  exit 1
fi

if ! command -v gh >/dev/null 2>&1; then
  echo "gh is not installed, so the signing key was not registered" >&2
  exit 0
fi

if ! gh auth status >/dev/null 2>&1; then
  echo "gh is not authenticated, so the signing key was not registered. Run" >&2
  echo "'gh auth login', then this script." >&2
  exit 0
fi

# Signing keys are a different collection from authentication keys, under a scope
# of their own that 'gh auth login' does not ask for. Reading the list is what
# tells us whether the token has it, and doubles as the check that keeps a re-run
# from adding a key twice.
#
# write:ssh_signing_key is the least that works — the scopes nest, so it covers
# the read below as well. gh's own error suggests admin:, which is more than this
# needs.
registered=""
if ! registered="$(gh api user/ssh_signing_keys --jq '.[].key' 2>/dev/null)"; then
  echo "gh has no scope for signing keys, so the key was not registered. Grant" >&2
  echo "it and run this script again:" >&2
  echo "  gh auth refresh -h github.com -s write:ssh_signing_key" >&2
  exit 0
fi

# GitHub keeps the type and the body, and drops the comment, so compare on the two
# fields it stores.
key="$(awk '{print $1" "$2}' "$staged")"

if printf '%s\n' "$registered" | grep -qxF "$key"; then
  echo "the signing key is already on the GitHub account"
  exit 0
fi

# Titled after the machine, since the account carries one key per machine that
# signs — and the file this reads from is a temporary one whose name gh would
# otherwise take.
gh ssh-key add "$staged" --type signing --title "$(hostname -s)"

echo "registered the signing key with GitHub as '$(hostname -s)'"
