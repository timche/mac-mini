#!/bin/bash

# The account half of the provisioning: everything that needs a GitHub, an
# Anthropic or a 1Password account. machine.sh leaves a working Mac; this is what
# turns that Mac into the one Claude Code runs on and controls.
#
# The second entry point. bootstrap.sh runs it after machine.sh, running it by
# hand against a Mac machine.sh already built is the other way in, and it is what
# you rerun when a token expires.
#
# Three phases: install.sh links $HOME out of this checkout and installs the
# tooling those files configure, login.sh logs in to GitHub and Claude Code, and
# signing-key.sh puts the commit-signing key into an agent. install.sh first,
# because login.sh logs in to a Claude Code it installs and signing-key.sh needs
# the user.email install.sh's .gitconfig carries; login.sh runs it again for the
# half that wanted a token. shibuya is deployed and its ping token made after all
# three, since both reach 1Password with the token signing-key.sh stores.
#
# Safe to re-run: logins already in place are left alone.

set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [ "$(uname -s)" != Darwin ]; then
  echo "mac-mini is for a Mac; this is $(uname -s)." >&2
  exit 1
fi

if [ "$(id -u)" -eq 0 ]; then
  echo "claude.sh runs as the account the machine is for, not as root — the" >&2
  echo "logins and everything they fetch land in \$HOME." >&2
  exit 1
fi

# claude.sh is run from a plain SSH session as often as from bootstrap.sh, and
# nothing puts Homebrew on a PATH until install.sh has linked .zshenv.
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

"$repo/install.sh"

# After install.sh, whose docs clone wants a token this is what puts on the
# machine, and which is rerun from here once there is one. Needs a terminal for
# the browser flows.
if [ -t 0 ]; then
  "$repo/claude/login.sh"
else
  echo
  echo "Skipped login.sh — no terminal. Run $repo/claude/login.sh to log in to"
  echo "GitHub and Claude Code."
fi

signing_failed=0

# After install.sh, because the principal this writes into allowed_signers is
# the user.email out of the .gitconfig it links.
"$repo/claude/signing-key.sh" || signing_failed=1

# shibuya, the dead man's switch, after signing-key.sh rather than with install.sh's own
# deploy at the top of this script: both steps here reach 1Password through `op run`, and
# the service-account token they need is what signing-key.sh has just stored. A Mac that
# never got this far is one hachiko cannot check in with, so shibuya reports it offline a
# quarter of an hour after the first sweep — the switch has to be made on the way in.
#
# Reported rather than fatal, exactly as install.sh treats the same deploy: a Cloudflare
# that is down or a service account with nothing left for the day is not a reason for the
# account side to fail. Neither script prompts, so a provision with nobody watching
# finishes either way, and deploy.sh does nothing at all when the sources have not moved.
op_token_file="${OP_SERVICE_ACCOUNT_TOKEN_FILE:-$HOME/.config/op/service-account-token}"
shibuya_token="${SHIBUYA_TOKEN_FILE:-$HOME/.config/hachiko/shibuya-token}"

if [ ! -f "$op_token_file" ]; then
  echo "no service-account token, so shibuya was left alone — the deploy and the ping" >&2
  echo "token both reach 1Password through \`op run\`. Run $repo/claude/signing-key.sh," >&2
  echo "then $repo/shibuya/deploy.sh and $repo/shibuya/secrets.sh." >&2
elif ! "$repo/shibuya/deploy.sh"; then
  echo "shibuya was not deployed, so the dead man's switch is whatever was deployed" >&2
  echo "last — run $repo/shibuya/deploy.sh when Cloudflare and 1Password are both" >&2
  echo "reachable." >&2
elif [ ! -f "$shibuya_token" ]; then
  # Only when there is no token file, because this makes one and replaces the Worker's
  # PING_TOKEN with it: there is one Mac checking in, so the token it just generated is
  # the only one that has to work, and a Mac that already has a file keeps the token
  # hachiko is using rather than spending an `op run` to set the same value again.
  if ! "$repo/shibuya/secrets.sh"; then
    echo "shibuya has no ping token, so nothing is watching hachiko — run" >&2
    echo "$repo/shibuya/secrets.sh when 1Password is reachable" >&2
  fi
fi

echo
echo "The account side is done. What is left:"
echo

# Still not logged in means login.sh was skipped or did not finish, and with it
# the Claude Code login and the project docs.
if ! gh auth status >/dev/null 2>&1; then
  echo "  - $repo/claude/login.sh — GitHub and Claude Code, and the rerun of"
  echo "    install.sh that clones the project docs in between."
else
  # claude is install.sh's, from Anthropic's installer into ~/.local/bin, behind
  # the mise shims this shell also has no reason to have.
  export PATH="$HOME/.local/share/mise/shims:$HOME/.local/bin:$PATH"

  if ! command -v claude >/dev/null 2>&1; then
    echo "  - $repo/install.sh — it did not get as far as installing claude."
  elif ! claude auth status >/dev/null 2>&1; then
    echo "  - claude auth login — $repo/claude/login.sh tried and did not get"
    echo "    there."
  fi
fi

# install.sh is what makes zsh the login shell — a Mac is on zsh already, so what
# is left is the rc files it links, which a session that started before them has
# not read.
cat <<'EOF'
  - Log out and back in for the shell install.sh set up.
EOF

exit "$signing_failed"
