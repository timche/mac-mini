#!/bin/bash

# The Claude Code overlay: the half of the provisioning that needs a GitHub, an
# Anthropic or a 1Password account. machine.sh leaves a working Mac; this is what
# turns that Mac into the one Claude Code runs on.
#
# The second entry point, and the only one that is optional. bootstrap.sh runs it
# when asked to, running it by hand against a Mac machine.sh already built is the
# other way in, and it is what you rerun when a token expires.
#
# It installs nothing: every package either half needs is brew's, and brew is the
# machine's. What is here is accounts and keys — and the handover to
# mac-mini-dotfiles, which is the personal half that brings the shell, the runtimes
# and Claude Code itself.
#
# Safe to re-run: logins already in place are left alone, and the dotfiles clone
# is pulled rather than recloned.

set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [ "$(uname -s)" != Darwin ]; then
  echo "mac-mini-setup is for a Mac; this is $(uname -s)." >&2
  exit 1
fi

if [ "$(id -u)" -eq 0 ]; then
  echo "claude.sh runs as the account the machine is for, not as root — the" >&2
  echo "logins and everything they fetch land in \$HOME." >&2
  exit 1
fi

# claude.sh is run from a plain SSH session as often as from bootstrap.sh, and
# nothing puts Homebrew on a PATH until mac-mini-dotfiles' own .zshenv exists.
if ! command -v brew >/dev/null 2>&1 && [ -x /opt/homebrew/bin/brew ]; then
  eval "$(/opt/homebrew/bin/brew shellenv)"
fi

# A string rather than an array: macOS ships bash 3.2, where an empty array read
# under set -u is an unbound variable.
missing=""
for tool in brew gh jq op; do
  command -v "$tool" >/dev/null 2>&1 || missing="$missing $tool"
done

if [ -n "$missing" ]; then
  echo "missing:$missing — they are brew's, and brew is machine.sh's." >&2
  echo "Run $repo/machine.sh first." >&2
  exit 1
fi

"$repo/claude/install.sh"

# After install.sh, which cannot reach the private dotfiles until this has put a
# token on the machine, and reruns itself from here once it has. Needs a terminal
# for the browser flows.
if [ -t 0 ]; then
  "$repo/claude/login.sh"
else
  echo
  echo "Skipped login.sh — no terminal. Run $repo/claude/login.sh to log in to"
  echo "GitHub and Claude Code, and to fetch the dotfiles."
fi

signing_failed=0

# After login.sh, because the principal this writes into allowed_signers is the
# user.email out of the .gitconfig only the dotfiles carry.
"$repo/claude/signing-key.sh" || signing_failed=1

echo
echo "Claude Code side done. What is left:"
echo

# Still not logged in means login.sh was skipped or did not finish, and with it
# the dotfiles and the Claude Code login: the account is still on the shell macOS
# gave it, with none of its own configuration.
if ! gh auth status >/dev/null 2>&1; then
  echo "  - $repo/claude/login.sh — GitHub and Claude Code, and the rerun that"
  echo "    fetches the dotfiles in between."
else
  # claude arrives with the dotfiles, from Anthropic's installer into
  # ~/.local/bin, behind the mise shims this shell also has no reason to have.
  export PATH="$HOME/.local/share/mise/shims:$HOME/.local/bin:$PATH"

  if ! command -v claude >/dev/null 2>&1; then
    echo "  - $repo/claude/install.sh — it did not get as far as installing"
    echo "    claude."
  elif ! claude auth status >/dev/null 2>&1; then
    echo "  - claude auth login — $repo/claude/login.sh tried and did not get"
    echo "    there."
  fi
fi

# The dotfiles installer is what makes zsh the login shell — a Mac is on zsh
# already, so what is left is the rc files it links, which a session that started
# before them has not read.
cat <<'EOF'
  - Log out and back in for the shell the dotfiles installed.
EOF

exit "$signing_failed"
