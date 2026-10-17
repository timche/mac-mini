#!/bin/bash

# Rosetta 2, which runs x86_64 binaries on Apple Silicon: meru's Windows build
# runs electron-builder's x86_64 tools under it. macOS installs it only when an
# app asks for it in a dialog, which on a Mac with nobody at the screen is never.
#
# The install agrees to Apple's Rosetta licence on the account's behalf, the way
# xcode.sh accepts Xcode's: running machine.sh is that agreement. It needs sudo,
# which machine.sh has already asked for.
#
# Safe to re-run. Asked of an x86_64 process rather than of pkgutil, whose
# package names for Rosetta include RosettaLinux, the one for Linux VMs, which
# translates nothing on the Mac itself.

set -euo pipefail

if [ "$(uname -s)" != Darwin ]; then
  echo "rosetta.sh is macOS only" >&2
  exit 1
fi

if /usr/bin/arch -x86_64 /usr/bin/true 2>/dev/null; then
  echo "Rosetta is already installed"
  exit 0
fi

if sudo softwareupdate --install-rosetta --agree-to-license; then
  echo "Rosetta is installed"
else
  echo "could not install Rosetta — x86_64 tools, meru's Windows build among" \
       "them, will not run until it is; rerun $0" >&2
  exit 1
fi
