#!/bin/bash

# Install the commit-signing key. Claude-side rather than part of the machine,
# because it is not how the Mac is reached but how it signs as the person whose
# accounts it works from: home/.gitconfig names ~/.ssh/id_signing.pub, this is
# what writes both halves of that key, and register-signing-key.sh puts the
# public one on the GitHub account that has to accept the signature.
#
# The key is the same one the VM uses and it comes out of 1Password rather than
# being pasted: a paste is a private key on somebody's clipboard. The private half
# goes from `op read` down a pipe into one file in ~/.ssh and nowhere else — never
# into a variable, a trace or an argument — and 1Password stays the one place it
# is kept, these files being a copy this rewrites.
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
# The service-account token is expanded into commands below, where a trace would
# print it.
set +x

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

allowed_signers="$HOME/.ssh/allowed_signers"
key="$HOME/.ssh/id_signing"

# The item, not the fields: both halves of one key are two fields of one 1Password
# item by definition.
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

read_field() {
  OP_SERVICE_ACCOUNT_TOKEN="$(cat "$token_file")" op read "$item/$1"
}

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

if ! public="$(read_field 'public key')"; then
  echo "the token in $token_file could not read $item" >&2
  exit 1
fi

staged_public="$(mktemp)"
trap 'rm -f "$staged_public"' EXIT
printf '%s\n' "$public" >"$staged_public"

# 1Password serves whatever is in the field, and a field that holds something else
# would otherwise be found out by git, months later, as a signature nobody can
# verify.
if ! ssh-keygen -l -f "$staged_public" >/dev/null 2>&1; then
  echo "$item/public key is not a public key that ssh-keygen recognises" >&2
  exit 1
fi

# GitHub, the trust list and ssh-keygen all keep the type and the body and drop the
# comment — and the comment ssh-keygen -y prints is the one the private key itself
# carries rather than the one in the 1Password field — so every comparison below is
# on the two fields that identify the key.
key_body() {
  awk 'NF >= 2 { print $1" "$2 }'
}

# SSH_ASKPASS_REQUIRE because a key with a passphrase would otherwise sit on a
# prompt that an unattended provision has nowhere to display.
public_half() {
  SSH_ASKPASS_REQUIRE=never ssh-keygen -y -f "$1" </dev/null 2>/dev/null | key_body
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
  line="$principal $(key_body <"$staged_public")"

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

# Both halves into files git signs with. The private one lands through a temporary
# file in ~/.ssh, which is 700, so it is never under a world-readable TMPDIR and
# never briefly readable at its final path; umask 077 because the redirect is what
# fills it.
install_key() {
  local want staged
  want="$(key_body <"$staged_public")"

  if [ -f "$key" ] && [ "$(public_half "$key")" = "$want" ]; then
    echo "$key already holds the key in $item"
  else
    staged="$(mktemp "$HOME/.ssh/.id_signing.XXXXXX")"

    if ! (umask 077; read_field 'private key?ssh-format=openssh' >"$staged"); then
      rm -f "$staged"
      echo "the token in $token_file could not read $item/private key" >&2
      return 1
    fi

    # The same reason the public half is checked, and the one check that catches a
    # 1Password item whose two fields are halves of different keys.
    if [ "$(public_half "$staged")" != "$want" ]; then
      rm -f "$staged"
      echo "$item/private key is not the private half of $item/public key" >&2
      return 1
    fi

    mv "$staged" "$key"
    chmod 600 "$key"
    echo "wrote the private half of $item to $key"
  fi

  if [ ! -f "$key.pub" ] || [ "$(key_body <"$key.pub")" != "$want" ]; then
    install -m 644 "$staged_public" "$key.pub"
    echo "wrote the public half of $item to $key.pub"
  fi
}

trust_signer
install_key

"$repo/register-signing-key.sh"

cat <<EOF

Check the whole of it with a signed commit, which signs with $key and verifies
against $allowed_signers:

    git commit --allow-empty -m test && git log --format='%G?' -1
EOF
