#!/bin/bash

# Turn Remote Login on — sshd, which on this Mac is the only way in that a
# script can reach. It is checked before it is touched, because a Mac that is
# being provisioned over SSH has it on already, and the point of looking is to
# leave that alone.
#
# Not `systemsetup -setremotelogin on`, which needs Full Disk Access for the
# terminal it is typed into: without it the command fails outright on current
# macOS and, worse, used to report success while changing nothing. The grant is
# per responsible process, so it cannot be arranged from inside the script that
# needs it. launchd is the same switch without the gate — `enable` clears the
# persistent Disabled flag Apple ships the job with, and `bootstrap` loads the
# socket listener now.
#
# Not fatal. If this fails the machine is still built, and Remote Login is a
# toggle in System Settings > General > Sharing for whoever can reach the screen.
#
# Safe to re-run: a Mac that already answers SSH is left exactly as it is.

set -euo pipefail

label=com.openssh.sshd
plist=/System/Library/LaunchDaemons/ssh.plist

if [ "$(uname -s)" != Darwin ]; then
  echo "remote-login.sh is macOS only" >&2
  exit 1
fi

loaded() {
  sudo launchctl print "system/$label" >/dev/null 2>&1
}

if loaded; then
  echo "Remote Login is already on"
  exit 0
fi

sudo launchctl enable "system/$label" || true

# launchd answers a bootstrap of a job it already has with "Bootstrap failed: 5:
# Input/output error" rather than saying so, and the same error covers real
# failures — which is why the state is read back rather than the status trusted.
if failure="$(sudo launchctl bootstrap system "$plist" 2>&1)" || loaded; then
  echo "Remote Login is on"
else
  echo "could not turn Remote Login on: $failure" >&2
  echo "Turn it on in System Settings > General > Sharing, or grant the" >&2
  echo "terminal Full Disk Access and run 'sudo systemsetup -setremotelogin on'." >&2
fi
