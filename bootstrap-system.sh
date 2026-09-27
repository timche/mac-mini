#!/bin/bash

# Homebrew, the Xcode command line tools it brings with it, and every package the
# machine has. Needs sudo the first time only — Homebrew's installer is what wants
# it, and everything after that lands in a prefix the account owns.
#
# The packages are the Brewfile's, including the ones docker.sh and tailscale.sh
# used to install for themselves: Homebrew is the machine, so one declarative list
# says what it has and each of those scripts is left to decide about the daemon and
# the VM. The Brewfile says which two packages stay out of it and why.
#
# Safe to re-run: an existing Homebrew is left alone and --no-upgrade keeps an
# already-installed package at the version it is on, so a re-run is not a way to
# move versions.

set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

homebrew_install=https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh

# NONINTERACTIVE because there is nobody to press RETURN, and because it is what
# keeps the installer on the softwareupdate path for the command line tools: the
# fallback is `xcode-select --install`, which puts a dialog on a display this Mac
# does not have. The installer checks for the tools' own git rather than
# xcode-select, so a Mac with Xcode proper is left alone too.
if ! command -v brew >/dev/null 2>&1 && [ ! -x /opt/homebrew/bin/brew ]; then
  NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL "$homebrew_install")"
fi

# The installer only prints the line that would do this, and the shell that reads
# that line is install.sh's. Skipped when brew is already reachable: a PATH that
# reaches it has an order somebody chose, and prepending the prefix again steps over
# it.
if ! command -v brew >/dev/null 2>&1 && [ -x /opt/homebrew/bin/brew ]; then
  eval "$(/opt/homebrew/bin/brew shellenv)"
fi

# --no-upgrade because this Mac is re-run in place and is reached over the sshd and
# the tailnet a run here touches: `brew upgrade tailscale` restarts the daemon the
# SSH session is riding on, and nothing about installing a missing package asks for
# that. `brew bundle upgrade` is the deliberate version of it.
#
# Not fatal, which is the same trade docker.sh and tailscale.sh have always made for
# their own packages: an OrbStack that will not install is no reason to leave the Mac
# without its hardened sshd. brew bundle names what it could not do.
if ! brew bundle --no-upgrade --file="$repo/Brewfile"; then
  echo "some of the Brewfile did not install — rerun $repo/bootstrap-system.sh" >&2
  echo "once the reason above is fixed." >&2
fi

# The exception to that, and the reason this script runs first: nothing after it
# does anything at all without these three. The rest of the Brewfile is checked by
# whichever script wants it.
for required in gh jq op; do
  if command -v "$required" >/dev/null 2>&1; then
    continue
  fi

  echo "$required is not installed, and nothing after this works without it." >&2
  echo "Fix the install above and rerun $repo/bootstrap-system.sh." >&2
  exit 1
done
