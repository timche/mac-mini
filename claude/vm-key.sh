#!/bin/bash

# Install ~/.ssh/id_vm, the key sessions use to reach this Mac's local VMs (UTM's
# Windows guest and the macOS base). It lives in 1Password because a key made on
# one Mac goes when that Mac does, and every guest then needs the new one put in
# by hand.
#
# Safe to re-run: the private half is read from 1Password only when the file is
# missing or holds a different key.

set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

item="${VM_KEY_OP_ITEM:-op://dev/vm-ssh}"

if [ "$(uname -s)" != Darwin ]; then
  echo "vm-key.sh is macOS only" >&2
  exit 1
fi

if ! command -v brew >/dev/null 2>&1 && [ -x /opt/homebrew/bin/brew ]; then
  eval "$(/opt/homebrew/bin/brew shellenv)"
fi

if ! command -v op >/dev/null 2>&1; then
  echo "op is not installed, so the VM key was not installed — it is brew's," >&2
  echo "and brew is machine.sh's." >&2
  exit 0
fi

token_status=0
"$repo/op-token.sh" || token_status=$?
case "$token_status" in
  0) ;;
  2)
    echo "skipping the VM key until there is a token." >&2
    exit 0
    ;;
  *) exit 1 ;;
esac

"$repo/ssh-key.sh" "$item" "$HOME/.ssh/id_vm"
