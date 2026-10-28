#!/bin/bash

# Write one SSH key from a 1Password item into two files: the private half at the
# path given, mode 600, and the public half beside it as <path>.pub. The item's
# `private key` and `public key` fields are the two halves, which is how 1Password
# stores an SSH Key item. Shared by signing-key.sh and vm-key.sh, which check the
# service-account token before calling it.
#
#   ssh-key.sh op://<vault>/<item> <path>
#
# The private half goes from `op read` down a pipe into a temporary file beside its
# final path under umask 077, then a rename — never into a variable, a trace or an
# argument — and is read only when the file is missing or holds a different key,
# since every `op read` spends part of the service account's day.

set -euo pipefail
# The service-account token is expanded into commands below, where a trace would
# print it.
set +x

if [ $# -ne 2 ]; then
  echo "usage: ssh-key.sh op://<vault>/<item> <path>" >&2
  exit 2
fi

item="$1"
key="$2"
token_file="${OP_SERVICE_ACCOUNT_TOKEN_FILE:-$HOME/.config/op/service-account-token}"

read_field() {
  OP_SERVICE_ACCOUNT_TOKEN="$(cat "$token_file")" op read "$item/$1"
}

mkdir -p "$(dirname "$key")"
chmod 700 "$(dirname "$key")"

if ! public="$(read_field 'public key')"; then
  echo "the token in $token_file could not read $item" >&2
  exit 1
fi

staged_public="$(mktemp)"
trap 'rm -f "$staged_public"' EXIT
printf '%s\n' "$public" >"$staged_public"

# 1Password serves whatever is in the field, and a field that holds something else
# would otherwise be found out much later, by whatever the key was for.
if ! ssh-keygen -l -f "$staged_public" >/dev/null 2>&1; then
  echo "$item/public key is not a public key that ssh-keygen recognises" >&2
  exit 1
fi

# GitHub, a trust list and ssh-keygen all keep the type and the body and drop the
# comment — and the comment ssh-keygen -y prints is the one the private key itself
# carries rather than the one in the 1Password field — so every comparison is on
# the two fields that identify the key.
key_body() {
  awk 'NF >= 2 { print $1" "$2 }'
}

# SSH_ASKPASS_REQUIRE because a key with a passphrase would otherwise sit on a
# prompt that an unattended provision has nowhere to display.
public_half() {
  SSH_ASKPASS_REQUIRE=never ssh-keygen -y -f "$1" </dev/null 2>/dev/null | key_body
}

want="$(key_body <"$staged_public")"

if [ -f "$key" ] && [ "$(public_half "$key")" = "$want" ]; then
  echo "$key already holds the key in $item"
else
  # Beside the final path, in a directory at 700, so it is never under a
  # world-readable TMPDIR and never briefly readable where it ends up.
  staged="$(mktemp "$(dirname "$key")/.$(basename "$key").XXXXXX")"

  if ! (umask 077; read_field 'private key?ssh-format=openssh' >"$staged"); then
    rm -f "$staged"
    echo "the token in $token_file could not read $item/private key" >&2
    exit 1
  fi

  # The one check that catches an item whose two fields are halves of different
  # keys.
  if [ "$(public_half "$staged")" != "$want" ]; then
    rm -f "$staged"
    echo "$item/private key is not the private half of $item/public key" >&2
    exit 1
  fi

  mv "$staged" "$key"
  chmod 600 "$key"
  echo "wrote the private half of $item to $key"
fi

if [ ! -f "$key.pub" ] || [ "$(key_body <"$key.pub")" != "$want" ]; then
  install -m 644 "$staged_public" "$key.pub"
  echo "wrote the public half of $item to $key.pub"
fi
