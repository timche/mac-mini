#!/bin/bash

# Install the one command a Claude Code session on this Mac may run under sudo
# without Tim: the claude-root helper, and the sudoers rule that lets this account
# run it with no password.
#
# The narrow allowlist is a guard against a session's mistakes, not a boundary
# against the account. Sessions here run with permissions bypassed, and anything
# running as this account can reach root by other routes anyway — so what the rule
# buys is that an agent can bounce dasd or flush DNS on its own and nothing else,
# which is a list somebody can read.
#
# Run after harden-ssh.sh in machine.sh. Nothing in the way this Mac is reached
# depends on it, so it goes after the steps that do, and ahead of the Xcode
# download that wants an Apple ID and an hour.
#
# Safe to re-run: each file is compared with what this repo says it should be,
# contents, owner and mode, and a run with nothing to change writes nothing and
# says so.
#
# Takes the account to grant as its argument, defaulting to whoever runs it.

set -euo pipefail

dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
user="${1:-$(id -un)}"

helper_source="$dir/system/libexec/claude-root"
helper=/usr/local/libexec/claude-root
helper_dir=/usr/local/libexec
sudoers_file=/etc/sudoers.d/claude-root

if [ "$(uname -s)" != Darwin ]; then
  echo "root-helper.sh is macOS only" >&2
  exit 1
fi

# The sudoers rule names the account that may use the helper, and root may
# already run every one of these commands — so a run as root would write a rule
# for root and grant the account nothing. machine.sh refuses root for its own
# reasons and sudos the steps that need it, which is what this does below.
if [ "$(id -u)" -eq 0 ]; then
  echo "root-helper.sh runs as the account the helper is for, not as root: the" >&2
  echo "sudoers rule it writes names that account, and root needs none of it." >&2
  echo "It sudos the installs itself." >&2
  exit 1
fi

# Read rather than assumed, because a rule for an account that does not exist —
# a typo in the argument, or a placeholder that survived the rendering below —
# would leave the Mac's own account with no sudo. No getent on a Mac; the account
# record is dscl's.
if ! dscl . -read "/Users/$user" NFSHomeDirectory >/dev/null 2>&1; then
  echo "no such user: $user" >&2
  exit 1
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# Nothing here is written for one account name, so the rule carries the account
# as a placeholder and this is where it becomes a file.
sed "s/__USER__/$user/g" "$dir/system/sudoers/claude-root" >"$work/sudoers"

# Before anything goes near /etc. A sudoers file sudo cannot parse takes sudo away
# from Tim as well as from every session, and on this Mac that is only fixable
# over Screen Sharing. visudo -c reads a file as an ordinary user, so this is
# checked without root and before root is used for anything.
if ! visudo -cf "$work/sudoers" >/dev/null; then
  echo "visudo rejected the sudoers rule, so nothing was installed." >&2
  exit 1
fi

# sudo on this Mac asks for Tim's password, and a run with nobody watching has no
# terminal to type it at. Checked before anything else needs it, so what happens
# is a message rather than a prompt nothing answers — the same shape as the Xcode
# step machine.sh skips.
if ! sudo -n true 2>/dev/null && [ ! -t 0 ]; then
  echo
  echo "Skipped the root helper: sudo wants a password and there is no terminal" >&2
  echo "to type it at. Run $dir/root-helper.sh from a terminal." >&2
  exit 0
fi

# A drop-in under /etc/sudoers.d is a file nothing reads unless /etc/sudoers
# includes the directory. macOS has shipped that line for years, but an
# /etc/sudoers replaced by hand would silently ignore everything here — and a run
# that reported success while the account still had no sudo would be worse than
# one that refused.
#
# Read into a variable rather than grepped under sudo directly, because a grep
# that found nothing and a sudo that was refused both exit non-zero, and telling
# Tim his sudoers is missing a line when the truth is that nobody typed a password
# would send him after the wrong thing.
if ! sudoers_text="$(sudo cat /etc/sudoers 2>/dev/null)"; then
  echo
  echo "Skipped the root helper: /etc/sudoers could not be read, so whether a" >&2
  echo "drop-in there is included is unknown. Run $dir/root-helper.sh from a" >&2
  echo "terminal that can answer sudo." >&2
  exit 0
fi

if ! printf '%s\n' "$sudoers_text" |
       grep -qE '^[[:space:]]*[#@]includedir[[:space:]]+(/private)?/etc/sudoers\.d'; then
  echo
  echo "Skipped the root helper: /etc/sudoers has no includedir for" >&2
  echo "/etc/sudoers.d, so a drop-in there would do nothing. Add the line with" >&2
  echo "'sudo visudo' and run this again." >&2
  exit 0
fi

if ! sudo install -d -m 0755 -o root -g wheel "$helper_dir"; then
  echo "warning: could not make $helper_dir root-owned, so nothing was" >&2
  echo "installed. $dir/root-helper.sh will try again." >&2
  exit 0
fi

# What differs, or nothing. Printed rather than only acted on: a re-run that
# changed a file is worth seeing, and so is the reason.
difference() {
  local source="$1" destination="$2" mode="$3" found

  if [ ! -e "$destination" ]; then
    echo "not installed yet"
    return 0
  fi

  # The rule is installed 0440 and the account cannot read it, so the comparison
  # goes through the sudo the steps above have already asked for — a plain cmp
  # would fail for want of permission and reinstall on every run.
  if ! sudo cmp -s "$source" "$destination"; then
    echo "contents differ from this repo's"
    return 0
  fi

  # %OLp prints the three octal digits with no leading zero, which is what the
  # modes below are stripped to rather than written twice.
  found="$(stat -f '%Su:%Sg %OLp' "$destination")"
  if [ "$found" != "root:wheel ${mode#0}" ]; then
    echo "is $found, want root:wheel ${mode#0}"
    return 0
  fi

  return 1
}

review() {
  local source="$1" destination="$2" why="$3"

  echo "  $destination — $why"
  echo

  if [ -e "$destination" ]; then
    # diff exits 1 for files that differ, which is the only reason it is run here.
    sudo diff -u "$destination" "$source" | sed 's/^/    /' || true
  else
    sed 's/^/    /' "$source"
  fi

  echo
}

install_copy() {
  local source="$1" destination="$2" mode="$3" why="$4"

  # A failed install stops the run here rather than going on to the next file: the
  # two only mean anything together, and a sudoers rule naming a helper that did
  # not land would be worse than neither. Not fatal to machine.sh, which names the
  # step among what is left.
  if ! sudo install -m "$mode" -o root -g wheel "$source" "$destination"; then
    echo "warning: could not install $destination, so the root helper is" >&2
    echo "incomplete. $dir/root-helper.sh will try again." >&2
    exit 0
  fi

  echo "  installed  $destination ($why)"
}

if helper_why="$(difference "$helper_source" "$helper" 0755)"; then
  helper_changes=true
else
  helper_changes=false
fi

if sudoers_why="$(difference "$work/sudoers" "$sudoers_file" 0440)"; then
  sudoers_changes=true
else
  sudoers_changes=false
fi

if [ "$helper_changes" = false ] && [ "$sudoers_changes" = false ]; then
  echo
  echo "Nothing to change: both files are this repo's, root-owned."
  echo "'$helper --list' says what the helper allows."
  exit 0
fi

# A session cannot run this script, but it can write the two files a run of it
# copies into place, and one of them is a command sudo will then run as root
# without asking again. So what is about to change goes up before it is
# installed, and this is the moment to read it.
echo
echo "About to install as root. Read this first:"
echo

[ "$sudoers_changes" = false ] ||
  review "$work/sudoers" "$sudoers_file" "$sudoers_why"
[ "$helper_changes" = false ] ||
  review "$helper_source" "$helper" "$helper_why"

if [ -t 0 ]; then
  printf 'Install? [y/N] '

  # A read that ends in EOF rather than a line is an answer of nothing, which is
  # a no; without the guard set -e would end the run here instead of saying so.
  read -r answer || answer=

  case "$answer" in
    y | Y | yes | Yes) ;;
    *)
      echo "Nothing was installed."
      exit 0
      ;;
  esac
fi

echo
echo "The root helper:"

# The helper first, so that the moment the sudoers rule exists there is something
# at the path it names.
[ "$helper_changes" = false ] ||
  install_copy "$helper_source" "$helper" 0755 "$helper_why"
[ "$sudoers_changes" = false ] ||
  install_copy "$work/sudoers" "$sudoers_file" 0440 "$sudoers_why"

echo

# -k so the answer is the sudoers rule's rather than a password typed a minute
# ago: it makes sudo ignore the cached credentials for this one call without
# clearing them, so the steps after this still have the timestamp machine.sh
# warmed up. -n so a rule that is not working prints a refusal instead of a
# prompt.
if sudo -n -k "$helper" --list >/dev/null 2>&1; then
  echo "Done. '$helper --list' says what it allows."
else
  # Not fatal: by the time this runs the machine is built, and an account that
  # has to ask Tim for every root command is where it was before this existed.
  # machine.sh names it among what is left.
  echo "warning: sudo still wants a password for $helper, so a session cannot" >&2
  echo "run it. Check $sudoers_file and the includedir in /etc/sudoers." >&2
fi
