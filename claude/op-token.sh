#!/bin/bash

# Store the 1Password service-account token this Mac signs in with, since it has
# no 1Password app to do it. One file, ~/.config/op/service-account-token, read by
# the signing agent at every start, by the op wrapper in ~/.local/bin for every
# project's `op run`, and by a project's varlock schema through .env.1password.
#
# Run it with --replace after rotating the token in 1Password. Without it, a stored
# token that still works is left alone, and one that no longer does is offered for
# replacement — so signing-key.sh can call it on every run.
#
# The token is checked against the signing key's item before it is stored, because
# a token that cannot read the vault is indistinguishable afterwards from a key
# that moved.
#
# Exits 0 with a working token stored, 2 when there is none and nobody at a
# terminal to ask, and 1 when the stored one fails and is not replaced.

set -euo pipefail
# The service-account token is expanded into commands below, where a trace would
# print it.
set +x

item="${SIGNING_KEY_OP_ITEM:-op://dev/ssh-commit-signing}"
token_file="${OP_SERVICE_ACCOUNT_TOKEN_FILE:-$HOME/.config/op/service-account-token}"

replace=false
case "${1:-}" in
  "") ;;
  --replace) replace=true ;;
  *)
    echo "usage: op-token.sh [--replace]" >&2
    exit 1
    ;;
esac

# Run by hand, this is the script that follows machine.sh in the same session,
# where nothing has put Homebrew on PATH yet.
if ! command -v brew >/dev/null 2>&1 && [ -x /opt/homebrew/bin/brew ]; then
  eval "$(/opt/homebrew/bin/brew shellenv)"
fi

if ! command -v op >/dev/null 2>&1; then
  echo "op is not installed, so no service-account token was stored — it is" >&2
  echo "brew's, and brew is machine.sh's." >&2
  exit 2
fi

confirm() {
  local answer
  read -r -p "$1 [y/N] " answer
  [ "$answer" = y ] || [ "$answer" = Y ]
}

stored_token_works() {
  OP_SERVICE_ACCOUNT_TOKEN="$(cat "$token_file")" op read "$item/public key" >/dev/null
}

# Taken from the environment rather than an argument, which is where op wants it
# and keeps it out of any process list.
store_token() {
  cat <<EOF

Paste the 1Password service-account token — it is not echoed, and it needs read
access to the vault in $item.

EOF

  local token
  read -rs -p "token> " token
  echo

  if [ -z "$token" ]; then
    echo "nothing pasted — no token stored" >&2
    return 1
  fi

  if ! OP_SERVICE_ACCOUNT_TOKEN="$token" op read "$item/public key" >/dev/null; then
    echo "that token cannot read $item — not stored" >&2
    return 1
  fi

  install -d -m 700 "$(dirname "$token_file")"

  # Through a temporary file mktemp made private, so the token is never in a
  # command line and never briefly readable at its final path.
  local staged
  staged="$(mktemp)"
  chmod 600 "$staged"
  printf '%s' "$token" >"$staged"
  mv "$staged" "$token_file"
  chmod 600 "$token_file"

  echo "stored the service-account token in $token_file"
}

if $replace; then
  if [ ! -t 0 ]; then
    echo "--replace needs a terminal to paste the new token at" >&2
    exit 1
  fi
  store_token || exit 1
  exit 0
fi

if [ ! -f "$token_file" ]; then
  if [ ! -t 0 ]; then
    echo "no service-account token at $token_file and no terminal to ask at." >&2
    echo "Run $0 directly." >&2
    exit 2
  fi
  store_token || exit 2
  exit 0
fi

if stored_token_works; then
  exit 0
fi

echo
echo "the token in $token_file could not read $item — it may have been" >&2
echo "revoked, or the item may have moved." >&2

if [ ! -t 0 ] || ! confirm "Replace the stored token?"; then
  exit 1
fi

store_token || exit 1
