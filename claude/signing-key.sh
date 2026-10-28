#!/bin/bash

# Install the commit-signing key. Claude-side rather than part of the machine,
# because it is not how the Mac is reached but how it signs as the person whose
# accounts it works from: home/.gitconfig names ~/.ssh/id_signing.pub, this is
# what writes both halves of that key, and register-signing-key.sh puts the
# public one on the GitHub account that has to accept the signature.
#
# The key comes out of 1Password rather than being pasted: a paste is a private key
# on somebody's clipboard. ssh-key.sh writes both halves; 1Password stays the one
# place it is kept, these files being a copy this rewrites.
#
# What it needs is a 1Password service account with read access to the vault, whose
# token op-token.sh stores once and both read. Nothing prints it.
#
# The allowed_signers git verifies against is written here rather than tracked in
# the repo. The same key on every machine would make a tracked copy correct, but
# writing it locally costs nothing and keeps a credential-shaped file out of
# public history.
#
# Safe to re-run: a working token is reused, the trust list is compared before it
# is rewritten, and the private half is read from 1Password only when the file is
# missing or holds a different key — every `op read` spends part of the service
# account's day.

set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

allowed_signers="$HOME/.ssh/allowed_signers"
key="$HOME/.ssh/id_signing"

# The item, not the fields: both halves of one key are two fields of one 1Password
# item by definition.
item="${SIGNING_KEY_OP_ITEM:-op://dev/ssh-commit-signing}"

if [ "$(uname -s)" != Darwin ]; then
  echo "signing-key.sh is macOS only" >&2
  exit 1
fi

# Run by hand, this is the script that follows machine.sh in the same session,
# where nothing has put Homebrew on PATH yet.
if ! command -v brew >/dev/null 2>&1 && [ -x /opt/homebrew/bin/brew ]; then
  eval "$(/opt/homebrew/bin/brew shellenv)"
fi

if ! command -v op >/dev/null 2>&1; then
  echo "op is not installed, so the signing key was not installed — it is" >&2
  echo "brew's, and brew is machine.sh's." >&2
  exit 0
fi

token_status=0
"$repo/op-token.sh" || token_status=$?
case "$token_status" in
  0) ;;
  2)
    echo "skipping the signing key until there is a token." >&2
    exit 0
    ;;
  *) exit 1 ;;
esac

"$repo/ssh-key.sh" "$item" "$key"

key_body() {
  awk 'NF >= 2 { print $1" "$2 }'
}

signer_identity() {
  # The principal has to be the address the commits are authored under, or the
  # signature verifies against nothing. --global because this is often run from
  # inside a project, whose own config may name a different address than the
  # .gitconfig install.sh links.
  git config --global --get user.email || echo "$(id -un)@$(hostname -s)"
}

# Rewritten whole rather than appended to, so that a rotated key takes the line for
# the old one with it: every line for this principal goes and the key 1Password
# serves now is the only one that comes back — otherwise the list collects keys
# this machine no longer holds, and each of them stays trusted.
trust_signer() {
  local principal line kept
  principal="$(signer_identity)"
  line="$principal $(key_body <"$key.pub")"

  kept="$(mktemp)"
  if [ -f "$allowed_signers" ]; then
    awk -v p="$principal" '$1 != p' "$allowed_signers" >"$kept"
  fi
  printf '%s\n' "$line" >>"$kept"

  if [ -f "$allowed_signers" ] && cmp -s "$kept" "$allowed_signers"; then
    rm -f "$kept"
    echo "$allowed_signers already trusts the key in $item"
    return 0
  fi

  install -m 644 "$kept" "$allowed_signers"
  rm -f "$kept"

  echo "trusted the signing key in $allowed_signers"
}

trust_signer

"$repo/register-signing-key.sh"

cat <<EOF

Check the whole of it with a signed commit, which signs with $key and verifies
against $allowed_signers:

    git commit --allow-empty -m test && git log --format='%G?' -1
EOF
