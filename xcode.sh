#!/bin/bash

# Xcode proper, which this Mac needs for one thing: signing builds of meru with a
# Developer ID certificate. The command line tools Homebrew brought are enough to
# compile, but not the whole toolchain electron-builder reaches for when it signs
# and notarises, and there is no separate download for that half.
#
# Interactive, and that is why machine.sh only calls it when there is a terminal:
# Apple hands nobody an Xcode without an Apple ID, a 2FA code typed in while it is
# still valid, and then the account password for the privileged end of the install.
# A run with nobody watching is told to come back with a terminal.
#
# Not part of a plain provision either way. It is an 11GB download that takes the
# better part of an hour to unpack, and a Mac without Xcode is still the machine
# the rest of this repo built.
#
# Safe to re-run: an Xcode that is already installed is selected, licensed and
# first-launched rather than fetched again. It is not a way to move versions —
# `xcodes install <version>` is.

set -euo pipefail

if [ "$(uname -s)" != Darwin ]; then
  echo "xcode.sh is macOS only" >&2
  exit 1
fi

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if ! command -v brew >/dev/null 2>&1 && [ -x /opt/homebrew/bin/brew ]; then
  eval "$(/opt/homebrew/bin/brew shellenv)"
fi

# What is already installed

# xcode-select's answer is where this starts, because an Xcode that is installed
# but not selected is a Mac where xcodebuild is still the command line tools' and
# nothing says so.
app=""
developer_dir="$(xcode-select -p 2>/dev/null || true)"

case "$developer_dir" in
/Applications/Xcode*.app/Contents/Developer)
  if [ -d "$developer_dir" ]; then
    app="${developer_dir%/Contents/Developer}"
    echo "Xcode is already installed at $app"
  fi
  ;;
esac

# The download

if [ -z "$app" ]; then
  # The xip is around 11GB and unpacks to more than twice that before the copy
  # into /Applications, and a download that fills the boot disk is worse than one
  # that never started. -P keeps df to one line per filesystem, however long the
  # device is named.
  free_gib="$(df -Pg / | awk 'END { print $4 }')"

  if [ "$free_gib" -lt 40 ]; then
    echo "Xcode needs about 40GB free while it unpacks and / has ${free_gib}GB." >&2
    echo "Clear some space and run $repo/xcode.sh again." >&2
    exit 1
  fi

  cat <<EOF

About to download Xcode: an 11GB or so xip, then the better part of an hour to
unpack it. It asks for an Apple ID, then a 2FA code, then this account's password
for the last step of the install.

EOF

  # The two packages that are not in the Brewfile. Both exist only for the download
  # below, which happens only where there is a terminal to type an Apple ID at, so
  # declaring them would install them on every unattended provision for a step it is
  # never going to run — and would make aria2's absence a failed dependency rather
  # than a slower download. Installed here, where that has just been decided.
  installed="$(brew list --formula -1)"

  # Not required: xcodes finds aria2 on PATH by itself and downloads over 16
  # connections instead of one. Without it the download is slower, which is not a
  # reason to stop.
  if ! printf '%s\n' "$installed" | grep -qxF aria2; then
    brew install aria2 ||
      echo "no aria2, so xcodes downloads over one connection" >&2
  fi

  # xcodes is how a Mac with no Xcode gets one from the command line: `mas` wants
  # an App Store signed in at the screen, and Apple's own download page wants a
  # browser. homebrew/core rather than upstream's xcodesorg/made tap: the tap has
  # no bottle for macOS 27 and builds from source there, and that build fails to
  # link its x86_64 slice against the macOS 27 SDK (XcodesOrg/homebrew-made#4),
  # while core pours a bottle for it.
  if ! printf '%s\n' "$installed" | grep -qxF xcodes; then
    if ! brew install xcodes; then
      echo "could not install xcodes — rerun $repo/xcode.sh to try again" >&2
      exit 1
    fi
  fi

  # --latest is the latest release, never a beta; --empty-trash deletes the xip
  # rather than leaving 11GB in a Trash that nobody is at the screen to empty.
  if ! xcodes install --latest --empty-trash; then
    echo "xcodes did not install Xcode. If the Apple ID was the problem, 'xcodes" >&2
    echo "signout' clears the stored one and $repo/xcode.sh asks again." >&2
    exit 1
  fi

  # xcodes names the app after the version it installed — Xcode-27.0.0.app — so
  # the path is whatever it reports rather than something to write down. The
  # highest release rather than the last line: the list is in version order but a
  # beta or release candidate installed alongside sorts after the release it
  # precedes, and --latest never fetches one. --no-color because this script does
  # have a terminal, and colour codes here would be part of the path.
  app="$(xcodes installed --no-color |
    awk -F'\t' '
      NF > 1 && $1 !~ /Beta|Release Candidate|RC/ {
        split($1, field, " ")
        print field[1] "\t" $NF
      }' |
    sort -V |
    awk -F'\t' 'END { print $2 }')"

  if [ -z "$app" ] || [ ! -d "$app" ]; then
    echo "Xcode installed but xcodes does not say where — 'xcodes installed'" >&2
    echo "lists them, and 'sudo xcode-select -s <path>' selects one." >&2
    exit 1
  fi
fi

# Selecting, licensing and finishing it off

# Everything below needs root and all of it is guarded, so a re-run against a Mac
# that is already set up asks for nothing and changes nothing.
if [ "$(xcode-select -p 2>/dev/null || true)" != "$app/Contents/Developer" ]; then
  sudo xcode-select -s "$app"
  echo "xcode-select now points at $app"
fi

# `-license check` exits non-zero until the agreements are accepted, which is what
# makes accepting them conditional. Without it xcodebuild refuses everything, and
# on a headless Mac the refusal only ever appears in a build log.
if ! sudo xcodebuild -license check >/dev/null 2>&1; then
  sudo xcodebuild -license accept
  echo "Xcode's licence is accepted"
fi

# The packages Xcode installs on its first launch, which it would otherwise ask
# for in a window. -checkFirstLaunchStatus is the guard, and it is also what says
# a version upgrade has left something to do.
if ! sudo xcodebuild -checkFirstLaunchStatus; then
  sudo xcodebuild -runFirstLaunch
  echo "Xcode's first-launch packages are installed"
fi

echo
xcodebuild -version

cat <<EOF

Nothing else to do for signing: the Developer ID certificate comes out of
1Password at build time, into a keychain electron-builder makes and throws away,
so it is never imported into the login keychain here.
EOF
