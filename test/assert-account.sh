#!/bin/bash

# Assertions against the account steps of claude.sh — the logins and the signing
# key. What install.sh left is assert.sh's, and the machine under both is
# assert-machine.sh's.
#
# A runner has no authenticated gh, no 1Password service account and no terminal,
# so nothing here is ever logged in. What is asserted is that every step of it
# skips rather than failing, or worse, sitting on a prompt. The signing key itself
# is driven with a stub op in signing-key.sh.
#
# To add a case, add a check line: a description and a shell snippet that exits
# non-zero when the expectation is not met.

set -uo pipefail

export PATH="/opt/homebrew/bin:/opt/homebrew/sbin:$PATH"
export root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

failures=0

check() {
  local description="$1" snippet="$2"

  if bash -c "$snippet" >/dev/null 2>&1; then
    echo "  ok    $description"
  else
    echo "  FAIL  $description"
    failures=$((failures + 1))
  fi
}

# Nothing is logged in here, which is the state every fresh Mac is in. timeout on
# the ones that would otherwise open a browser flow, because that failure mode is a
# hang rather than an exit status.
check "login.sh exits without a terminal" \
  '"$root/account/login.sh" < /dev/null'
check "op-token.sh skips without a terminal or a stored token" \
  'OP_SERVICE_ACCOUNT_TOKEN_FILE="$(mktemp -u)" "$root/account/op-token.sh" < /dev/null; [ $? -eq 2 ]'
check "op-token.sh --replace refuses without a terminal" \
  '! OP_SERVICE_ACCOUNT_TOKEN_FILE="$(mktemp -u)" "$root/account/op-token.sh" --replace < /dev/null'
check "signing-key.sh exits without a terminal or a stored token" \
  'OP_SERVICE_ACCOUNT_TOKEN_FILE="$(mktemp -u)" "$root/account/signing-key.sh" < /dev/null'
# A HOME of its own, so that the key file a provisioned Mac has cannot answer for
# the one a fresh Mac does not.
check "register-signing-key.sh skips with no key file, no vault and no gh" \
  'HOME="$(mktemp -d)" OP_SERVICE_ACCOUNT_TOKEN_FILE="$(mktemp -u)" \
     "$root/account/register-signing-key.sh"'

# Several of these are run by launchd or by a bootstrap with nothing watching,
# neither of which would say why a syntax error stopped one.
check "every script parses" \
  'find "$root" -name "*.sh" -not -path "*/.git/*" -exec bash -n {} +'

if [ "$failures" -gt 0 ]; then
  echo "  $failures check(s) failed"
  exit 1
fi
