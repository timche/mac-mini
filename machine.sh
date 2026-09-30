#!/bin/bash

# The whole of the machine: Homebrew and the packages, the Remote Login this Mac
# is reached through, everything that keeps it running with nobody in front of it,
# the tailnet, docker, and a hardened sshd. What it leaves is a Mac worth having
# with no account anywhere on it.
#
# An entry point, and the usual one: bootstrap.sh exists for a Mac that does not
# have this repo yet and calls this the moment it does. Running it again from the
# clone is how a change is picked up.
#
# Safe to re-run. Everything here checks the machine before touching it, which is
# the point on a Mac rather than a VM: this one is reached over the SSH and the
# tailnet it configures, so a rewritten sshd or a bounced tailscaled would cut the
# run off from the machine it is running on.

set -euo pipefail

if [ "$(uname -s)" != Darwin ]; then
  echo "mac-mini is for a Mac; this is $(uname -s)." >&2
  exit 1
fi

if [ "$(id -u)" -eq 0 ]; then
  echo "machine.sh runs as the account the machine is for, not as root —" >&2
  echo "Homebrew refuses root outright and everything else here lands in" >&2
  echo "\$HOME. It sudos for the parts that need it." >&2
  exit 1
fi

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Asked for once, up front, rather than partway through a long brew run. The
# timestamp lasts five minutes, so a slow download can still cost a second
# password. `sudo -n true` first, because `sudo -v` demands a password even where
# sudoers says NOPASSWD for everything, and a machine with passwordless sudo — a
# CI runner, a test VM — has nothing to ask for.
if ! sudo -n true 2>/dev/null && ! sudo -v; then
  echo "machine.sh needs sudo — the account has to be an administrator." >&2
  exit 1
fi

"$repo/bootstrap-system.sh"
"$repo/remote-login.sh"
"$repo/unattended.sh"
"$repo/tailscale.sh"
"$repo/docker.sh"

# Last of the steps that decide how this Mac is reached, because it is the one
# that turns password logins off. It skips itself when there is no authorized_keys
# yet, rather than locking you out of a machine that is nowhere near you.
"$repo/harden-ssh.sh"

# After everything else, because it is an 11GB download and an Apple ID typed in
# at the time: nothing in the run should wait behind that. A terminal is the whole
# of the condition — with none there is nobody to type it, and the footer below
# says the step is still to do.
xcode_left=false

if [ -t 0 ]; then
  "$repo/xcode.sh" || xcode_left=true
else
  xcode_left=true
  echo
  echo "Skipped xcode.sh — no terminal to type an Apple ID at."
fi

echo
echo "Done. What is left:"
echo

# tailscale's own answer, which the Homebrew CLI gives: it exits non-zero while
# the node is logged out as well as while there is no daemon at all, and either
# means the same thing here. The prefix is spelled out for the same reason as
# OrbStack below.
if ! /opt/homebrew/bin/tailscale status >/dev/null 2>&1; then
  echo "  - $repo/tailscale.sh — this Mac is not on the tailnet."
fi

# harden-ssh.sh skips itself rather than locking a Mac out of its own sshd, and
# the drop-in it would have installed is the only thing that says whether it did.
# Read rather than sudoed: the file is root-owned but world-readable, and the
# directory above it is too.
if [ ! -f /etc/ssh/sshd_config.d/10-hardening.conf ]; then
  echo "  - $repo/harden-ssh.sh — ssh still takes a password. Add the key you"
  echo "    connect with to ~/.ssh/authorized_keys and run it again."
fi

if [ "$xcode_left" = true ]; then
  echo "  - $repo/xcode.sh — Xcode itself, which signs meru's builds. It wants a"
  echo "    terminal, an Apple ID and an hour."
fi

# Reported here as well as by docker.sh, because a fresh OrbStack needs a click at
# the Mac itself and that message scrolls a long way up. The prefix is spelled out
# because this shell can predate Homebrew being on any PATH — bootstrap-system.sh
# put it on its own, not on this one.
if [ "$(/opt/homebrew/bin/orb status 2>/dev/null || true)" != Running ]; then
  echo "  - $repo/docker.sh — OrbStack is not running. If it has never been set up,"
  echo "    that is a click nothing here can make: open it over Screen Sharing,"
  echo "    click through the welcome screen and choose Docker."
fi

# tailscale.sh prints both of these with the URLs, and by the end of a run that
# has scrolled a long way up. Tailscale SSH is the way in over the tailnet and
# tailscaled answers it itself, so nothing in the sshd drop-in applies to those
# sessions — the policy file is what governs them.
cat <<'EOF'
  - In the tailscale admin console: approve this machine's advertised subnet and
    exit node, and allow Tailscale SSH to it in the policy file. Neither works
    until the tailnet says so.
EOF
