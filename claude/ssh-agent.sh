#!/bin/bash

# Install and load the LaunchAgent that keeps the signing key in an ssh-agent of
# its own. Two symlinks into the checkout rather than two copies, now that the
# checkout stays on disk for good — a pull is then the whole of an update, and
# nothing has to be reinstalled to pick one up:
#
#   ~/Library/LaunchAgents/io.github.timche.ssh-agent.plist -> launchd/<same name>
#   ~/.ssh/agent.sh                                         -> launchd/agent.sh
#
# launchd accepts a symlinked agent plist — it resolves the link at bootstrap and
# remembers the file behind it — and it hands a gui-domain agent a HOME, so the
# plist needs no rendering: a shell in ProgramArguments expands $HOME, and the
# wrapper derives the socket, the log and the token file from the same one. The
# second link is what makes that work wherever MAC_MINI_DIR put the checkout:
# ~/.ssh/agent.sh is a fixed path that reaches whatever the checkout's is.
#
# launchd reads a plist only at bootstrap, so a plist that changed is a bootout and
# a fresh bootstrap, while a wrapper that changed is a kickstart. Neither is visible
# in a link that still points where it did, so the wrapper's hash is kept beside it
# and compared. Beside the wrapper rather than beside the plist, because
# ~/Library/LaunchAgents is a directory launchd reads at login and it is for plists.
#
# The agent lives in the gui/<uid> domain, which exists only while the account is
# logged in at the console — so this is the other half of the auto-login
# unattended.sh checks for, and an SSH session against a Mac sitting at its login
# window cannot load it at all.
#
# Safe to re-run: the links and the hash are compared before anything is replaced,
# and launchd is only disturbed when one of them changed.

set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$repo/.." && pwd)"

label=io.github.timche.ssh-agent

source_plist="$root/launchd/$label.plist"
source_agent="$root/launchd/agent.sh"

plist="$HOME/Library/LaunchAgents/$label.plist"
agent="$HOME/.ssh/agent.sh"
stamp="$HOME/.ssh/agent.sh.sha256"
log="$HOME/Library/Logs/ssh-agent.log"
socket="$HOME/.ssh/agent.sock"
uid="$(id -u)"

# The wrapper's defaults, and the agent gets no others: the plist carries no
# environment, which is what lets it be a link. A run by hand takes the overrides.
item="${SIGNING_KEY_OP_ITEM:-op://dev/ssh-commit-signing}"
token_file="${OP_SERVICE_ACCOUNT_TOKEN_FILE:-$HOME/.config/op/service-account-token}"

if ! command -v op >/dev/null 2>&1; then
  echo "op is not installed, so the agent was not installed — it is brew's," >&2
  echo "and brew is machine.sh's." >&2
  exit 0
fi

if [ "$item" != "op://dev/ssh-commit-signing" ] ||
   [ "$token_file" != "$HOME/.config/op/service-account-token" ]; then
  echo "note: the agent launchd starts reads the default 1Password item and token" >&2
  echo "file, since its plist carries no environment — change the defaults in" >&2
  echo "$source_agent if they have to move." >&2
fi

mkdir -p "$HOME/.ssh"
chmod 700 "$HOME/.ssh"
mkdir -p "$HOME/Library/LaunchAgents"

plist_relinked=false
if [ "$(readlink "$plist" || true)" != "$source_plist" ]; then
  rm -f "$plist"
  ln -s "$source_plist" "$plist"
  plist_relinked=true
fi

if [ "$(readlink "$agent" || true)" != "$source_agent" ]; then
  rm -f "$agent"
  ln -s "$source_agent" "$agent"
fi

agent_hash="$(shasum -a 256 "$source_agent" | awk '{print $1}')"

stamped=none
if [ -f "$stamp" ]; then
  stamped="$(cat "$stamp")"
fi

# launchd would hand the job the account's home directory and the plist names
# $HOME, so a run against any other one can install the links but never load
# something that reads them. test/signing-agent.sh is the caller that hits this.
account_home="$(dscl . -read "/Users/$(id -un)" NFSHomeDirectory 2>/dev/null |
  sed -n 's/^NFSHomeDirectory: //p')"

if [ -n "$account_home" ] && [ "$HOME" != "$account_home" ]; then
  echo "installed the links under $HOME and left launchd alone: the plist names"
  echo "\$HOME, and the one launchd would hand the job is $account_home."
  exit 0
fi

loaded=false
if launchctl print "gui/$uid/$label" >/dev/null 2>&1; then
  loaded=true
fi

# launchd holds the plist it read at bootstrap, and kickstart restarts the process
# from that copy — so a changed plist is a bootout and a fresh bootstrap or it is
# nothing at all. A link that now points somewhere else counts as changed, since
# what launchd remembers is the file it resolved to.
if [ "$loaded" = true ] && [ "$plist_relinked" = true ]; then
  launchctl bootout "gui/$uid/$label" || true
  loaded=false
fi

if [ "$loaded" = false ]; then
  if launchctl bootstrap "gui/$uid" "$plist"; then
    echo "loaded $label"
  else
    echo "could not load $label — the gui/$uid domain belongs to a console" >&2
    echo "login, which a Mac sitting at its login window does not have. Log in" >&2
    echo "there, or turn auto-login on, and run this again:" >&2
    echo "  $repo/ssh-agent.sh" >&2
    exit 0
  fi
elif [ "$agent_hash" != "$stamped" ]; then
  launchctl kickstart -k "gui/$uid/$label"
  echo "restarted $label, which is what re-reads the key"
else
  echo "$label is already loaded"
fi

# Written only once the job is running the wrapper this hash is of, so a bootstrap
# that failed leaves the next run to try again rather than to skip the restart.
printf '%s\n' "$agent_hash" >"$stamp"
chmod 600 "$stamp"

# Nothing for the agent to load yet, and it keeps trying — so there is no point
# waiting on a key that cannot arrive until signing-key.sh has stored a token.
if [ ! -f "$token_file" ]; then
  echo "no service-account token in $token_file yet, so the agent is up and"
  echo "empty. It picks the key up on its own once there is one."
  exit 0
fi

# The key comes out of 1Password over the network, so there is nothing to see for
# a moment after the job starts.
waited=0
while [ "$waited" -lt 30 ]; do
  if SSH_AUTH_SOCK="$socket" ssh-add -l >/dev/null 2>&1; then
    echo "the agent holds $(SSH_AUTH_SOCK="$socket" ssh-add -l | wc -l | tr -d ' ') key(s)"
    exit 0
  fi
  sleep 1
  waited=$((waited + 1))
done

echo "the agent is loaded but holding no key yet — it keeps trying, and the" >&2
echo "reason is in $log" >&2
