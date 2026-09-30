#!/bin/bash

# The ssh-agent that holds the commit-signing key, and the only place the private
# half of that key ever exists on this Mac: it comes out of 1Password down a pipe
# into ssh-add and is never written anywhere. home/.gitconfig names no
# key of its own and asks this agent for one through gpg.ssh.defaultKeyCommand — so
# this process is the whole of what makes a commit signable, and losing it costs
# nothing but a re-read.
#
# Run by launchd out of the checkout — ~/.ssh/agent.sh is a symlink to this file,
# installed by claude/ssh-agent.sh — and restarted whenever it exits; that is the
# whole recovery story, since a restart re-reads the key. It waits on the agent
# rather than backgrounding it for the same reason: a job that exits would leave
# launchd thinking the pair had finished.
#
# The socket path is fixed rather than the one ssh-agent would print, because
# launchd hands every login session an SSH_AUTH_SOCK of its own pointing at the
# agent macOS starts, which holds nothing of this. .zshenv and the LaunchAgents in
# the rc files name this path instead — and it is read from AGENT_SOCKET rather
# than SSH_AUTH_SOCK so that a hand-run never rm's the socket of the agent macOS
# had already put in the environment.
#
# Run it by hand to see what it would do; it logs a line per step, to the terminal
# when there is one and to ~/Library/Logs/ssh-agent.log when launchd is the caller.

set -uo pipefail

# Every path here comes from $HOME, which is what keeps the plist a static file with
# nothing to render: launchd expands nothing itself, but it does hand a gui-domain
# agent the account's home directory. The overrides are for a run by hand — the
# plist carries no environment, so they never reach the job launchd starts.
sock="${AGENT_SOCKET:-$HOME/.ssh/agent.sock}"
token_file="${OP_SERVICE_ACCOUNT_TOKEN_FILE:-$HOME/.config/op/service-account-token}"
item="${SIGNING_KEY_OP_ITEM:-op://dev/ssh-commit-signing}"
log="$HOME/Library/Logs/ssh-agent.log"

# launchd hands a job /usr/bin:/bin:/usr/sbin:/sbin and nothing else, and op is
# Homebrew's. /opt/homebrew because this is Apple Silicon only, refused up front.
# Appended rather than prepended: launchd's PATH holds nothing this shadows, and a
# PATH that already reaches op has an order somebody chose.
PATH="$PATH:/opt/homebrew/bin:/opt/homebrew/sbin"
export PATH

# StandardOutPath would be the obvious place for this, but it takes an absolute path
# and launchd will not expand a $HOME in one — a plist that tried would be an
# EX_CONFIG failure at load. Opened here instead, and only when nobody is watching.
if [ ! -t 1 ]; then
  mkdir -p "$(dirname "$log")"
  exec >>"$log" 2>&1
fi

# launchd stamps nothing, and every line here is read long after the fact.
say() {
  printf '%s %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*"
}

# A wrapper that was killed outright rather than signalled never reached the trap
# below, and left an ssh-agent behind holding the key with no socket anybody can
# reach it through. Matched on the whole command line, so it is this agent and
# never somebody else's.
pkill -f "ssh-agent -D -a $sock" 2>/dev/null

# The socket the dead process left behind is still a file, and ssh-agent will not
# bind over one.
rm -f "$sock"

ssh-agent -D -a "$sock" &
agent=$!

# launchd signals the job, not the ssh-agent it started, so without this a bootout
# leaves an agent holding the key and owning the socket the next one needs.
trap 'kill "$agent" 2>/dev/null; exit 0' TERM INT

export SSH_AUTH_SOCK="$sock"

# A key with a passphrase would otherwise sit on a prompt that no launchd job has
# anywhere to display, for as long as the Mac is up.
export SSH_ASKPASS_REQUIRE=never

waited=0
while [ ! -S "$sock" ] && [ "$waited" -lt 100 ]; do
  sleep 0.1
  waited=$((waited + 1))
done

if [ ! -S "$sock" ]; then
  say "ssh-agent did not create $sock"
  kill "$agent" 2>/dev/null
  exit 1
fi

say "ssh-agent is listening on $sock"

# At boot this job starts before the network does, and the token is no use without
# one — so a failure to read is retried rather than fatal, and the agent stays up
# and empty in the meantime. Everything that can go wrong here goes wrong the same
# way: no token file yet, no network, a token that has been revoked.
delay=5

while :; do
  if OP_SERVICE_ACCOUNT_TOKEN="$(cat "$token_file")" \
       op read "$item/private key?ssh-format=openssh" | ssh-add -; then
    say "loaded the signing key from $item"
    break
  fi

  if ! kill -0 "$agent" 2>/dev/null; then
    say "the agent exited before the key could be loaded"
    exit 1
  fi

  say "could not load the signing key; trying again in ${delay}s"
  sleep "$delay"

  if [ "$delay" -lt 320 ]; then
    delay=$((delay * 2))
  fi
done

wait "$agent"
status=$?

say "ssh-agent exited with $status; launchd starts it again"

exit "$status"
