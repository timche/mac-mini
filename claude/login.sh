#!/bin/bash

# The account logins that need a browser: GitHub and Claude Code. Split out of
# claude.sh because both want a person at the keyboard, and kept a script of its
# own because it is what you rerun when one was skipped or has since expired.
# The tailnet is the machine's business rather than an account's, so tailscale.sh
# stays in the generic half.
#
# Neither of them is fatal. By the time this runs the machine is built; what is
# missing is an account on it, and that can be sorted out later.
#
# Safe to re-run: a login already in place is left alone.

set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$repo/.." && pwd)"

if [ ! -t 0 ]; then
  echo "login.sh needs a terminal for the browser flows — run it directly." >&2
  exit 0
fi

# Whether the gh login happened here is what decides if install.sh has anything
# new to pick up, so it has to be sampled before the attempt.
gh_was_authenticated=false
if gh auth status >/dev/null 2>&1; then
  gh_was_authenticated=true
fi

# GitHub

# gh puts the token in the login keychain when it can, and this machine is only
# ever reached over SSH, where a locked keychain is answered with a prompt nobody
# sees. --insecure-storage writes it to hosts.yml instead: a file the account owns
# on a Mac whose disk is not encrypted anyway, and the only form the LaunchAgents
# that sign and push can read.
if [ "$gh_was_authenticated" = true ]; then
  echo "gh is already authenticated"
else
  cat <<'EOF'

Logging in to GitHub. There is no browser on this Mac, so gh prints a code and a
URL to open on whichever machine has one.

EOF

  # write:ssh_signing_key is not in the default set and the interactive flow never
  # offers it, so asking here is what saves a later 'gh auth refresh' before
  # register-signing-key.sh can do anything. https because that is how this repo
  # and its personal half are cloned; asking also skips the prompt.
  gh auth login --hostname github.com --git-protocol https --web \
    --insecure-storage --scopes write:ssh_signing_key ||
    echo "gh login did not finish — rerun $repo/login.sh to try again" >&2
fi

# install.sh skipped its gh-authenticated half on the way here — the project
# docs. It is idempotent, and this is the pass that picks them up. Only worth it
# if this run is what logged in; otherwise the earlier pass already had
# everything it needed.
if [ "$gh_was_authenticated" = false ] && gh auth status >/dev/null 2>&1; then
  echo
  echo "gh is logged in now — rerunning install.sh for the parts that needed it."
  echo

  "$root/install.sh"
fi

# Claude Code

# Last, because claude is install.sh's: from Anthropic's own installer into
# ~/.local/bin, ahead of which .zshenv puts the mise shims that carry the
# runtimes. This shell has a reason for neither directory until now.
export PATH="$HOME/.local/share/mise/shims:$HOME/.local/bin:$PATH"

if ! command -v claude >/dev/null 2>&1; then
  echo "claude is not installed, so it was not logged in — install.sh is what" >&2
  echo "installs it; run $root/install.sh and rerun $repo/login.sh" >&2
elif claude auth status >/dev/null 2>&1; then
  echo "claude is already authenticated"
else
  cat <<'EOF'

Logging in to Claude Code. Same as the others: a URL to open on whichever machine
has a browser, and a code to paste back here, since the callback it would rather
use cannot reach this Mac.

Claude Code keeps the credential in the login keychain on a Mac and falls back to
~/.claude/.credentials.json when the keychain refuses the write, which is what an
SSH session usually gets. If this does not take, `claude setup-token` prints a
token that lasts a year and CLAUDE_CODE_OAUTH_TOKEN in the environment is the
other way to carry it.

EOF

  claude auth login ||
    echo "claude login did not finish — rerun $repo/login.sh to try again" >&2
fi

# The credential is only half of it: the TUI gates its first run on setup state
# kept separately in ~/.claude.json, which no amount of `claude auth login`
# writes. Without this the first interactive claude walks its first-launch setup,
# and the opening screen of that is the login one all over again.
#
# hasCompletedOnboarding is not a documented setting, so this is written to fall
# through quietly rather than fail the run: the cost of it going stale is one
# login prompt, which is where we were anyway.
config="$HOME/.claude.json"

if claude auth status >/dev/null 2>&1 && [ -f "$config" ]; then
  # Alongside the file rather than in /tmp, so the replacement is a rename on the
  # same filesystem and never a half-written config.
  patched="$(mktemp "$config.XXXXXX")"

  if jq '.hasCompletedOnboarding = true' "$config" >"$patched"; then
    mv "$patched" "$config"
  else
    rm -f "$patched"
    echo "could not mark first-launch setup done in $config — the first" >&2
    echo "interactive claude will ask to log in again" >&2
  fi
fi
