#!/bin/bash

# The first command a Mac has, and the only one that works before this repo is
# on it:
#
#   curl -fsSL https://raw.githubusercontent.com/timche/mac-mini/main/bootstrap.sh | bash
#
# A fresh Mac has no git — it comes with the Xcode command line tools — and no
# package manager at all, so there is nothing here to clone with. Homebrew's own
# installer is what fixes both: it installs the tools through softwareupdate
# rather than the dialog nobody is in front of, and this repo needs Homebrew
# anyway. Then the git that arrived clones, machine.sh builds the Mac and
# claude.sh makes it the one Claude Code runs on.
#
# The second entry point is the clone itself: once it is there, machine.sh and
# claude.sh are run directly and this script has nothing left to do.
#
# Unlike a VM there is no account to create, so this runs as the user the
# machine is for and sudo asks for their password along the way.
#
# Safe to re-run: an existing clone is pulled rather than recloned.

set -euo pipefail

repo_url="${MAC_MINI_REPO:-https://github.com/timche/mac-mini.git}"
homebrew_install=https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh

# There is one Mac to provision and one shape for it, so there is nothing to
# choose: an argument is a misunderstanding rather than a request, and refusing
# it says so.
if [ "$#" -gt 0 ]; then
  echo "usage: bootstrap.sh — it takes no arguments." >&2
  exit 1
fi

if [ "$(uname -s)" != Darwin ]; then
  echo "mac-mini is for a Mac; this is $(uname -s)." >&2
  exit 1
fi

# /opt/homebrew is the Apple Silicon prefix, and it is written into the PATH
# this repo installs and into the agents launchd loads. An Intel Mac puts
# Homebrew in /usr/local and would come out of this half working with nothing
# saying why.
if [ "$(uname -m)" != arm64 ]; then
  echo "mac-mini is for an Apple Silicon Mac; this is $(uname -m), where" >&2
  echo "Homebrew lives in /usr/local rather than the /opt/homebrew everything" >&2
  echo "here expects." >&2
  exit 1
fi

if [ "$(id -u)" -eq 0 ]; then
  echo "bootstrap.sh runs as the account the machine is for, not as root —" >&2
  echo "Homebrew refuses to install as root and everything else here lands in" >&2
  echo "\$HOME. Log in as that account and start again; it needs to be an" >&2
  echo "administrator, since sudo is what the machine half runs on." >&2
  exit 1
fi

# Homebrew, and the command line tools with it

# NONINTERACTIVE because there is nobody to press RETURN, and because it is what
# keeps the installer on the softwareupdate path for the command line tools: its
# fallback is `xcode-select --install`, which puts up a dialog on a machine with
# no display. The same mode never prompts for sudo, though: it asks with `sudo -n`
# and gives up with "Insufficient permissions" when no credential is cached, so
# the password is asked for first, from the terminal rather than from stdin,
# which under `curl | bash` is the pipe.
if ! command -v brew >/dev/null 2>&1 && [ ! -x /opt/homebrew/bin/brew ]; then
  echo "Installing Homebrew, and the Xcode command line tools with it."
  sudo -v </dev/tty
  NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL "$homebrew_install")"
fi

# The installer puts nothing on PATH — that is what it prints instructions for —
# and the shell that would read them is install.sh's business rather than this
# script's. Skipped when brew is already reachable: a PATH that reaches it has an
# order somebody chose, and prepending the prefix again steps over it.
if ! command -v brew >/dev/null 2>&1 && [ -x /opt/homebrew/bin/brew ]; then
  eval "$(/opt/homebrew/bin/brew shellenv)"
fi

# The repo

# Hidden, because it is machinery rather than work: $HOME holds what is worked
# on, and this is what makes the machine and the account on it. It stays for
# good — a Mac is pulled and re-run rather than reprovisioned from a URL, and the
# LaunchAgent the Claude half installs is a link into it. MAC_MINI_DIR moves it.
target="${MAC_MINI_DIR:-$HOME/.mac-mini}"

if [ -d "$target/.git" ]; then
  git -C "$target" pull --ff-only
else
  git clone "$repo_url" "$target"
fi

# Hand over

# Under the documented curl install stdin is the pipe feeding this script, so
# every prompt in either half would be one nobody can answer. The terminal
# itself is the one to hand them, when there is one.
run() {
  if (exec </dev/tty) 2>/dev/null; then
    "$1" </dev/tty
  else
    "$1"
  fi
}

# A fumbled paste or a refused sudo is enough to make a half exit non-zero,
# and the closing message is worth more than the exit status is.
run_failed=0

run "$target/machine.sh" || run_failed=1
run "$target/claude.sh" || run_failed=1

cat <<EOF

Bootstrapped from $target. Pull it and re-run either half to pick up a change:

    git -C $target pull
    $target/machine.sh
    $target/claude.sh
EOF

if [ "$run_failed" -ne 0 ]; then
  echo
  echo "One of the halves did not finish — read back for which, and rerun it" >&2
  echo "from $target." >&2
fi

exit "$run_failed"
