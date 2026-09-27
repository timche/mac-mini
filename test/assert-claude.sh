#!/bin/bash

# Assertions against the account steps of claude.sh — the logins and the signing
# key. What install.sh left is assert.sh's, and the machine under both is
# assert-machine.sh's.
#
# A runner has no authenticated gh, no 1Password service account and no terminal,
# so nothing here is ever logged in. What is asserted is that every step of it
# skips rather than failing, or worse, sitting on a prompt. The signing key and
# its agent are driven with a stub op in signing-agent.sh.
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
  '"$root/claude/login.sh" < /dev/null'
check "signing-key.sh exits without a terminal or a stored token" \
  'OP_SERVICE_ACCOUNT_TOKEN_FILE="$(mktemp -u)" "$root/claude/signing-key.sh" < /dev/null'
check "register-signing-key.sh skips with no agent, no vault and no gh" \
  '"$root/claude/register-signing-key.sh"'

# Symlinked into ~/Library/LaunchAgents rather than rendered, so what is in the repo
# is what launchd reads: a stray character is a file it rejects at load with nothing
# in it to say why.
export agent_plist="$root/launchd/io.github.timche.ssh-agent.plist"

check "the agent plist is valid" 'plutil -lint "$agent_plist"'

# The whole of why it can be a link. launchd expands nothing itself, so the one
# absolute path it needs is a shell's, and $HOME is what that shell expands — while
# EnvironmentVariables and StandardOutPath, which would each want a path spelled
# out, are absent: the wrapper works both out for itself.
check "the agent plist reaches the wrapper through \$HOME" \
  '[ "$(plutil -extract ProgramArguments.2 raw -o - "$agent_plist")" = \
     "exec \"\$HOME/.ssh/agent.sh\"" ]'
check "the agent plist spells out no path of its own" \
  '! plutil -extract EnvironmentVariables raw -o - "$agent_plist" &&
   ! plutil -extract StandardOutPath raw -o - "$agent_plist"'

# The wrapper is linked into ~/.ssh and run by launchd, neither of which would say
# why a syntax error stopped it.
check "every script parses" \
  'find "$root" -name "*.sh" -not -path "*/.git/*" -exec bash -n {} +'

if [ "$failures" -gt 0 ]; then
  echo "  $failures check(s) failed"
  exit 1
fi
