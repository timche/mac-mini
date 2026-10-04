#!/bin/bash

# Install the one thing a Claude Code session on this Mac may run under sudo
# without Tim: the claude-root helper, and the sudoers rule that lets this account
# run it with no password.
#
# Two root-owned copies, never links into the checkout. The account can write the
# checkout, so a link at either path would hand every session on this Mac root —
# the same reasoning as the sshd drop-in, and the same answer. Which is also why
# adding a daemon to the helper's allowlist is an edit here followed by a run of
# this script: it costs Tim's password, deliberately.
#
# Run after harden-ssh.sh in machine.sh. Nothing in the way this Mac is reached
# depends on it, so it goes after the steps that do, and ahead of the Xcode
# download that wants an Apple ID and an hour.
#
# Safe to re-run: each file is compared with what this repo says it should be,
# contents, owner and mode, and only a file that differs is written. A run with
# nothing to change says so and writes nothing.
#
# Takes the account to grant as its argument, defaulting to whoever runs it.

set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
user="${1:-$(id -un)}"

helper=/usr/local/libexec/claude-root
helper_dir=/usr/local/libexec
sudoers_file=/etc/sudoers.d/claude-root

# An earlier shape of the helper capped the machine's logs, and installed a
# newsyslog config here to do it with. Both are gone: a root newsyslog aimed at
# paths in $HOME is a root write the account aims wherever it likes, which is the
# same hole the log action itself was. Taken back out rather than left, so that a
# Mac which ran that version converges on this one.
retired_newsyslog=/etc/newsyslog.d/mac-mini.conf

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
  echo "It sudos the three installs itself." >&2
  exit 1
fi

# Read rather than assumed, because the sudoers rule names this account and a
# rule for an account that does not exist would leave the Mac's own with no sudo.
# No getent on a Mac; the account record is dscl's.
if ! dscl . -read "/Users/$user" NFSHomeDirectory >/dev/null 2>&1; then
  echo "no such user: $user" >&2
  exit 1
fi

# sudo on this Mac asks for Tim's password, and a run with nobody watching has no
# terminal to type it at. Checked before anything is staged, so what happens is a
# message rather than a prompt nothing answers — the same shape as the Xcode step
# machine.sh skips.
if ! sudo -n true 2>/dev/null && [ ! -t 0 ]; then
  echo
  echo "Skipped the root helper: sudo wants a password and there is no terminal" >&2
  echo "to type it at. Run $repo/root-helper.sh from a terminal." >&2
  exit 0
fi

staged="$(mktemp -d)"
trap 'rm -rf "$staged"' EXIT

# Staged although it needs no rendering, so that the placeholder check below
# covers it too.
cp "$repo/system/libexec/claude-root" "$staged/claude-root"
sed "s/__USER__/$user/g" "$repo/system/sudoers/claude-root" >"$staged/sudoers"

# A placeholder that survived rendering is an account that is not there: a
# sudoers rule for one called __USER__ would leave this Mac's own account with no
# sudo at all.
for file in "$staged"/*; do
  if grep -q '__[A-Z]*__' "$file"; then
    echo "a placeholder survived rendering in ${file##*/}:" >&2
    grep -n '__[A-Z]*__' "$file" >&2
    exit 1
  fi
done

# Before it goes anywhere near /etc. A sudoers file sudo cannot parse takes sudo
# away from Tim as well as from every session, and on this Mac that is only
# fixable over Screen Sharing. visudo -c reads a file as an ordinary user, so this
# is checked without root and before root is used for anything.
if ! visudo_says="$(/usr/sbin/visudo -cf "$staged/sudoers" 2>&1)"; then
  echo "visudo rejected the sudoers rule, so nothing was installed:" >&2
  echo "$visudo_says" >&2
  exit 1
fi

# What a session can shape is exactly this: it cannot run root-helper.sh, but it
# can edit the two files a run of it copies into place, and one of them is a
# command sudo will then run as root without asking again. So what is about to
# change goes up before the first password prompt rather than after it, and the
# password is the moment to read it.
#
# The diff rather than a log of the commits behind it: a commit range from the
# installed copy would have to be guessed at — boswell commits this repository
# every few seconds, and a local edit matches no commit at all — where the diff
# is exactly what will change, whatever produced it.
#
# The helper is world-readable, so this needs no root. The sudoers rule is 0440
# and cannot be read back without it, so what is printed there is what the rule
# will say rather than a diff against what it says now.
echo
echo "About to install as root. Read this before typing a password:"
echo

if [ ! -r "$helper" ]; then
  echo "  $helper"
  echo "  is new. Nothing to diff against, so read the whole of it:"
  echo "    less $repo/system/libexec/claude-root"
elif cmp -s "$staged/claude-root" "$helper"; then
  echo "  $helper is unchanged."
else
  echo "  $helper changes:"
  echo

  # diff exits 1 for files that differ, which is the only reason it is being run,
  # and pipefail would make that end the script.
  diff -u "$helper" "$staged/claude-root" | sed 's/^/    /' || true
fi

echo
echo "  $sudoers_file will say, comments aside:"
echo

# visudo above has already read this file, so a grep that matches nothing would
# mean a rule of nothing but comments. Guarded all the same, rather than letting
# pipefail end the run without saying why.
grep -vE '^[[:space:]]*(#|$)' "$staged/sudoers" | sed 's/^/    /' || true
echo

# A drop-in under /etc/sudoers.d is a file nothing reads unless /etc/sudoers
# includes the directory. macOS has shipped that line for years, but an
# sudoers replaced by hand would silently ignore everything here — and a run that
# reported success while the account still had no sudo would be worse than one
# that refused. The first thing here that needs root, and so the prompt the review
# above is meant to be read at.
#
# Read into a variable rather than grepped under sudo directly, because a grep
# that found nothing and a sudo that was refused both exit non-zero, and telling
# Tim his sudoers is missing a line when the truth is that nobody typed a password
# would send him after the wrong thing.
if ! sudoers_text="$(sudo cat /etc/sudoers 2>/dev/null)"; then
  echo
  echo "Skipped the root helper: /etc/sudoers could not be read, so whether a" >&2
  echo "drop-in there is included is unknown. Run $repo/root-helper.sh from a" >&2
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

# Every directory on the way to something root executes has to be root's and
# writable by nobody else, or the file at the end of it is only as safe as the
# weakest directory above it. /usr/local is Homebrew's neighbour and worth reading
# back rather than assuming.
root_owned_path() {
  local path="$1" owner mode

  while :; do
    owner="$(stat -f '%Su' "$path" 2>/dev/null)" || return 1
    mode="$(stat -f '%OLp' "$path" 2>/dev/null)" || return 1

    [ ! -L "$path" ] || return 1
    [ "$owner" = root ] || return 1
    [ $((8#$mode & 8#022)) -eq 0 ] || return 1

    case "$path" in /) return 0 ;; esac
    path="$(dirname "$path")"
  done
}

if ! sudo install -d -m 0755 -o root -g wheel "$helper_dir"; then
  echo "warning: could not make $helper_dir root-owned, so nothing was" >&2
  echo "installed. $repo/root-helper.sh will try again." >&2
  exit 0
fi

# Not fatal, and not this repo's doing either: something has made a directory
# above the helper writable by somebody other than root, and until that is dealt
# with the machine is where it was before this existed. machine.sh names it among
# what is left.
if ! root_owned_path "$helper_dir"; then
  echo "warning: $helper_dir is not root-owned all the way up, so a helper" >&2
  echo "there would be only as safe as whichever directory above it is" >&2
  echo "writable. Nothing was installed." >&2
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

  if [ -L "$destination" ]; then
    echo "is a symlink, which it may never be"
    return 0
  fi

  # sudo cmp, because the sudoers rule is installed 0440 and the account cannot
  # read what it is being compared against — a plain cmp would fail for want of
  # permission and reinstall the file on every run.
  if ! sudo cmp -s "$source" "$destination"; then
    echo "contents differ from this repo's"
    return 0
  fi

  # %OLp prints the three octal digits with no leading zero, which is what the
  # modes below are stripped to rather than written twice.
  found="$(stat -f '%Su:%Sg %OLp' "$destination" 2>/dev/null || true)"
  if [ "$found" != "root:wheel ${mode#0}" ]; then
    echo "is $found, want root:wheel ${mode#0}"
    return 0
  fi

  return 1
}

changed=false

install_copy() {
  local source="$1" destination="$2" mode="$3" why

  if ! why="$(difference "$source" "$destination" "$mode")"; then
    echo "  unchanged  $destination"
    return 0
  fi

  # A failed install stops the run here rather than going on to the next file:
  # the three only mean anything together, and a sudoers rule pointing at a
  # helper that did not land would be worse than none. Not fatal to machine.sh,
  # which names the step among what is left.
  if ! sudo install -m "$mode" -o root -g wheel "$source" "$destination"; then
    echo "warning: could not install $destination, so the root helper is" >&2
    echo "incomplete. $repo/root-helper.sh will try again." >&2
    exit 0
  fi

  echo "  installed  $destination ($why)"
  changed=true
}

echo
echo "The root helper:"

# The helper first, so that the moment the sudoers rule exists there is something
# at the path it names.
install_copy "$staged/claude-root" "$helper" 0755
install_copy "$staged/sudoers" "$sudoers_file" 0440

if [ -e "$retired_newsyslog" ]; then
  if sudo rm -f "$retired_newsyslog"; then
    echo "  removed    $retired_newsyslog (the log action it served is gone)"
    changed=true
  else
    echo "warning: could not remove $retired_newsyslog, which a root newsyslog" >&2
    echo "still reads every half hour against paths the account can write." >&2
  fi
fi

echo

# -k so the answer is the sudoers rule's rather than a password typed a minute
# ago: it makes sudo ignore the cached credentials for this one call without
# clearing them, so the steps after this still have the timestamp machine.sh
# warmed up. -n so a rule that is not working prints a refusal instead of a
# prompt.
if sudo -n -k "$helper" --list >/dev/null 2>&1; then
  if [ "$changed" = true ]; then
    echo "Done. '$helper --list' says what it allows."
  else
    echo "Nothing to change. '$helper --list' says what it allows."
  fi
else
  # Not fatal: by the time this runs the machine is built, and an account that
  # has to ask Tim for every root command is where it was before this existed.
  # machine.sh names it among what is left.
  echo "warning: sudo still wants a password for $helper, so a session cannot" >&2
  echo "run it. Check $sudoers_file and the includedir in /etc/sudoers." >&2
fi
