#!/bin/bash

# Install the commit-signing key. Claude-side rather than part of the machine,
# because it is not how the Mac is reached but how it signs as the person whose
# accounts it works from: home/.gitconfig names no key at all and
# asks the agent for one, and register-signing-key.sh puts the same key on the
# GitHub account that has to accept the signature.
#
# The key is the same one the VM uses and it comes out of 1Password rather than
# being pasted: a paste is a private key on somebody's clipboard, and this is the
# one machine where neither half touches the disk. The private half is read by the
# agent ssh-agent.sh installs, straight into memory, every time it starts; the
# public half is read here only to trust it and to register it, and 1Password stays
# the one place it is kept.
#
# What it needs is a 1Password service account with read access to the vault, whose
# token is stored once and read by both. Nothing prints it.
#
# The allowed_signers git verifies against is the one derived file left, written
# here rather than tracked in the repo. The same key on every machine would make a
# tracked copy correct, but writing it locally costs nothing and keeps a
# credential-shaped file out of public history.
#
# Safe to re-run: a stored token is reused, and the trust list is compared before
# it is rewritten.

set -euo pipefail
# The service-account token is expanded into commands below, where a trace would
# print it.
set +x

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

allowed_signers="$HOME/.ssh/allowed_signers"

# The item, not the fields: both halves of one key are two fields of one 1Password
# item by definition, and the agent is handed the same override.
item="${SIGNING_KEY_OP_ITEM:-op://dev/ssh-commit-signing}"
token_file="${OP_SERVICE_ACCOUNT_TOKEN_FILE:-$HOME/.config/op/service-account-token}"

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

mkdir -p "$HOME/.ssh"
chmod 700 "$HOME/.ssh"

confirm() {
  local answer
  read -r -p "$1 [y/N] " answer
  [ "$answer" = y ] || [ "$answer" = Y ]
}

read_field() {
  OP_SERVICE_ACCOUNT_TOKEN="$(cat "$token_file")" op read "$item/$1"
}

# Taken from the environment rather than an argument, which is where op wants it
# and keeps it out of any process list. Verified before it is stored, because a
# token that cannot read the vault is indistinguishable afterwards from a key that
# moved.
store_token() {
  if [ ! -t 0 ]; then
    echo "no service-account token at $token_file and no terminal to ask at —" >&2
    echo "skipping the signing key. Run $repo/signing-key.sh directly." >&2
    return 1
  fi

  cat <<EOF

The signing key is read from 1Password with a service account. Paste its token —
it is not echoed, and it needs read access to the vault in $item.

EOF

  local token
  read -rs -p "token> " token
  echo

  if [ -z "$token" ]; then
    echo "nothing pasted — skipping the signing key" >&2
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

if [ ! -f "$token_file" ] && ! store_token; then
  exit 0
fi

if ! public="$(read_field 'public key')"; then
  echo
  echo "the token in $token_file could not read $item — it may have been" >&2
  echo "revoked, or the item may have moved." >&2

  if [ ! -t 0 ] || ! confirm "Replace the stored token?"; then
    exit 1
  fi

  store_token || exit 1
  public="$(read_field 'public key')"
fi

staged="$(mktemp)"
trap 'rm -f "$staged"' EXIT
printf '%s\n' "$public" >"$staged"

# 1Password serves whatever is in the field, and a field that holds something else
# would otherwise be found out by git, months later, as a signature nobody can
# verify.
if ! ssh-keygen -l -f "$staged" >/dev/null 2>&1; then
  echo "$item/public key is not a public key that ssh-keygen recognises" >&2
  exit 1
fi

signer_identity() {
  # The principal has to be the address the commits are authored under, or the
  # signature verifies against nothing.
  git config --get user.email || echo "$(id -un)@$(hostname -s)"
}

# Rewritten whole rather than appended to. With no copy of the key left on disk
# there is nothing to compare a rotated one against, so every line for this
# principal goes and the key 1Password serves now is the only one that comes back —
# otherwise the list collects keys this machine no longer holds, and each of them
# stays trusted.
trust_signer() {
  local principal line kept
  principal="$(signer_identity)"
  line="$principal $(awk '{print $1" "$2}' "$staged")"

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

# The private half, into an agent and nowhere else.
"$repo/ssh-agent.sh"

"$repo/register-signing-key.sh"

cat <<EOF

Check the whole of it with a signed commit, which reads the key out of the agent
and verifies it against $allowed_signers:

    git commit --allow-empty -m test && git log --format='%G?' -1
EOF
