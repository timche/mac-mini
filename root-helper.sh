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
# Every command here is named by its absolute path, sudo included, and so is every
# command sudo is asked to run. sudoers on this Mac resets the environment but sets
# no secure_path, so `sudo cat` is whichever cat the caller's PATH finds first —
# and the first entry on this account's PATH is ~/.local/bin, which the account can
# write. A session that drops a cat there has it run as root the moment Tim runs
# this. Nothing is read from the environment either, no ${SUDO:-} and no PATH of
# our own: home/.zshenv is a symlink into this checkout, so a session can set any
# variable Tim's shell starts with, and a default that can be overridden is the
# same hole wearing a different hat. The account-side tools are spelled out for the
# same reason one step removed — a shadowed cat or diff would show Tim a review
# that is not what gets installed.
#
# Takes the account to grant as its argument, defaulting to whoever runs it.

set -euo pipefail

repo="$(cd "$(/usr/bin/dirname "${BASH_SOURCE[0]}")" && pwd)"
user="${1:-$(/usr/bin/id -un)}"

helper=/usr/local/libexec/claude-root
helper_dir=/usr/local/libexec
sudoers_file=/etc/sudoers.d/claude-root

# An earlier shape of the helper capped the machine's logs, and installed a
# newsyslog config here to do it with. Both are gone: a root newsyslog aimed at
# paths in $HOME is a root write the account aims wherever it likes, which is the
# same hole the log action itself was. Taken back out rather than left, so that a
# Mac which ran that version converges on this one.
retired_newsyslog=/etc/newsyslog.d/mac-mini.conf

if [ "$(/usr/bin/uname -s)" != Darwin ]; then
  echo "root-helper.sh is macOS only" >&2
  exit 1
fi

# The sudoers rule names the account that may use the helper, and root may
# already run every one of these commands — so a run as root would write a rule
# for root and grant the account nothing. machine.sh refuses root for its own
# reasons and sudos the steps that need it, which is what this does below.
if [ "$(/usr/bin/id -u)" -eq 0 ]; then
  echo "root-helper.sh runs as the account the helper is for, not as root: the" >&2
  echo "sudoers rule it writes names that account, and root needs none of it." >&2
  echo "It sudos the three installs itself." >&2
  exit 1
fi

# Read rather than assumed, because the sudoers rule names this account and a
# rule for an account that does not exist would leave the Mac's own with no sudo.
# No getent on a Mac; the account record is dscl's.
if ! /usr/bin/dscl . -read "/Users/$user" NFSHomeDirectory >/dev/null 2>&1; then
  echo "no such user: $user" >&2
  exit 1
fi

# sudo on this Mac asks for Tim's password, and a run with nobody watching has no
# terminal to type it at. Checked before anything is read, so what happens is a
# message rather than a prompt nothing answers — the same shape as the Xcode step
# machine.sh skips.
if ! /usr/bin/sudo -n /usr/bin/true 2>/dev/null && [ ! -t 0 ]; then
  echo
  echo "Skipped the root helper: sudo wants a password and there is no terminal" >&2
  echo "to type it at. Run $repo/root-helper.sh from a terminal." >&2
  exit 0
fi

# Nothing from either file reaches the screen except through this, and that is the
# part which is not cosmetic. diff, cat and visudo's own error all pass a terminal
# escape straight through: a rule of
#
#   timche ALL=(root) NOPASSWD: ALL # <ESC>[2K<CR><a narrow-looking rule>
#
# parses for visudo, grants everything, and displays as the narrow rule, because
# the escape erases the line and the carriage return reprints over it. So a review
# of raw bytes is a review of whatever the bytes decided to show. `cat -v` writes
# the ESC as ^[ and the CR as ^M instead, which is what makes the trick visible —
# and the check further down then refuses to install it at all.
#
# The locale is left as it is rather than forced to C: under C, `cat -v` is
# bytewise and writes every em dash in this repo's comments as M-bM-^@M-^T, which
# would make the diff of the helper unreadable to defend against bytes the check
# below refuses outright anyway.
render() {
  /bin/cat -v | /usr/bin/sed 's/^/    /'
}

# Read once, here, and never again. Everything below — the placeholder scan, the
# control characters, visudo, the review, and the bytes root is handed — works on
# what these two variables hold and on nothing on disk.
#
# Which is the whole of why they exist. The sources are in a checkout the account
# can write, and every session on this Mac runs as that account, so a file read
# twice is a file that can differ between the reads: one shape to be reviewed and
# passed, another for root to install a moment later. Hashing between the two does
# not close that, since content that alternates wins about half the time, and
# neither does a staging copy under $TMPDIR, which is the same account's. The only
# thing that does is to stop reading.
#
# The sentinel is because command substitution strips trailing newlines, and both
# files end in one. A NUL would be dropped as well, no shell variable being able to
# hold one — and would then be missing from what is installed rather than smuggled
# into it, with assert-machine.sh's byte comparison against the source the thing
# that notices.
if ! helper_bytes="$(/bin/cat "$repo/system/libexec/claude-root" && printf x)"; then
  echo "could not read $repo/system/libexec/claude-root" >&2
  exit 1
fi

helper_bytes="${helper_bytes%x}"

# The one rendering, in the same single read: nothing here is written for one
# account name.
if ! sudoers_bytes="$(/usr/bin/sed "s/__USER__/$user/g" \
                        "$repo/system/sudoers/claude-root" && printf x)"; then
  echo "could not read $repo/system/sudoers/claude-root" >&2
  exit 1
fi

sudoers_bytes="${sudoers_bytes%x}"

# A placeholder that survived rendering is an account that is not there: a sudoers
# rule for one called __USER__ would leave this Mac's own account with no sudo at
# all.
placeholders="$(
  printf '%s' "$sudoers_bytes" | /usr/bin/grep -n '__[A-Z]*__' |
    /usr/bin/sed 's|^|the sudoers rule, line |' || true

  printf '%s' "$helper_bytes" | /usr/bin/grep -n '__[A-Z]*__' |
    /usr/bin/sed 's|^|the helper, line |' || true
)"

if [ -n "$placeholders" ]; then
  echo "a placeholder survived rendering, so nothing was installed:" >&2
  printf '%s\n' "$placeholders" | render >&2
  exit 1
fi

# Before anything goes near /etc. A sudoers file sudo cannot parse takes sudo away
# from Tim as well as from every session, and on this Mac that is only fixable over
# Screen Sharing. visudo -c reads a file as an ordinary user, so this is checked
# without root and before root is used for anything.
#
# From stdin, so that what visudo passes is the same bytes that will be installed
# rather than a file that could have changed since. Its complaint quotes the line
# it tripped on, which is a line somebody else may have written, so it is rendered
# like everything else here.
if ! visudo_says="$(printf '%s' "$sudoers_bytes" |
                      /usr/sbin/visudo -cf - 2>&1)"; then
  echo "visudo rejected the sudoers rule, so nothing was installed:" >&2
  printf '%s\n' "$visudo_says" | render >&2
  exit 1
fi

# What a session can shape is exactly this: it cannot run root-helper.sh, but it
# can edit the two files a run of it copies into place, and one of them is a
# command sudo will then run as root without asking again. So what is about to
# change goes up before the first password prompt rather than after it, and the
# password is the moment to read it.
#
# The diff rather than a log of the commits behind it: a commit range from the
# installed copy would have to be guessed at — a local edit matches no commit at
# all, and the helper is installed from the working tree — where the diff
# is exactly what will change, whatever produced it.
#
# The helper is world-readable, so the diff needs no root. The sudoers rule is 0440
# and cannot be read back without it, so what is printed there is what the rule
# will say rather than a diff against what it says now. Both are read out of the
# bytes above, which is what makes the review a review of what gets installed.
echo
echo "About to install as root. Read this before typing a password:"
echo

# The grant first, being the shorter of the two and the one that decides what the
# other is allowed to do.
echo "  $sudoers_file will grant:"
echo

# The effective rule, which is the lines with their comments taken off rather than
# the lines that are not comments: the attack above hides in a trailing comment on
# a real rule, so a line kept whole would still read as the narrow one. What is left
# is what sudo acts on.
#
# visudo above has already passed these bytes, so a grep that matches nothing would
# mean a rule of nothing but comments. Guarded all the same, rather than letting
# pipefail end the run without saying why.
grant="$(
  printf '%s' "$sudoers_bytes" |
    /usr/bin/grep -vE '^[[:space:]]*(#|$)' |
    /usr/bin/sed -e 's/[[:space:]]*#.*$//' -e '/^[[:space:]]*$/d' || true
)"
printf '%s\n' "$grant" | render
echo

if [ ! -r "$helper" ]; then
  # A first install has nothing to diff against, and the file cannot be offered for
  # reading in its place — it is in a checkout the account can rewrite, and these
  # bytes are already out of its reach. So the digest of what will be installed
  # goes up, and reading the file means checking it still hashes to this.
  echo "  $helper is new, so there is nothing to diff against. What will be"
  echo "  installed hashes to:"
  echo
  printf '%s' "$helper_bytes" | /usr/bin/shasum -a 256 |
    /usr/bin/sed 's/ *-$//' | render
  echo
  echo "  Read it, and check the file it came from still matches:"
  echo "    /usr/bin/shasum -a 256 $repo/system/libexec/claude-root"
elif printf '%s' "$helper_bytes" | /usr/bin/cmp -s - "$helper"; then
  echo "  $helper is unchanged."
else
  echo "  $helper changes:"
  echo

  # diff exits 1 for files that differ, which is the only reason it is being run.
  changes="$(printf '%s' "$helper_bytes" | /usr/bin/diff -u "$helper" - || true)"
  printf '%s\n' "$changes" | render
fi

echo

# And then refused outright, because neither file has any reason to hold a control
# character: a sudoers rule is lines of words, and this repo's shell is indented
# with spaces. A tab is the one exception, and only in the sudoers rule, where the
# whitespace between fields is free — it is deleted before the match rather than
# subtracted from the character class, which an ERE cannot do. Newlines never come
# up, grep matching within a line.
#
# After the review above rather than before it, so that what Tim is shown is the
# evidence and not only a refusal, and before anything needs a password, so that
# the refusal costs him nothing.
#
# Two patterns, because one class cannot say this. [:cntrl:] is the C0 controls and
# DEL, which is what the escape trick needs. The C1 controls are the rest — U+0080
# to U+009F, which some terminals act on as their ASCII counterparts and which
# arrive as the two bytes 0xC2 0x80-0x9F — and no character class tells those from
# the em dashes this repo's comments are full of, so they are matched as the byte
# range they are, under a C locale where grep compares bytes.
c1_controls="$(printf '\302[\200-\237]')"

offenders="$(
  printf '%s' "$sudoers_bytes" | LC_ALL=C /usr/bin/tr -d '\011' |
    LC_ALL=C /usr/bin/grep -naE "[[:cntrl:]]|$c1_controls" |
    /usr/bin/sed 's|^|the sudoers rule, line |' || true

  printf '%s' "$helper_bytes" |
    LC_ALL=C /usr/bin/grep -naE "[[:cntrl:]]|$c1_controls" |
    /usr/bin/sed 's|^|the helper, line |' || true
)"

if [ -n "$offenders" ]; then
  echo "Nothing was installed: the files hold control characters, which neither" >&2
  echo "of them has any reason to. On a terminal they can print as something" >&2
  echo "other than what they say, so read them with 'cat -v' before going any" >&2
  echo "further:" >&2
  echo >&2
  printf '%s\n' "$offenders" | render >&2
  exit 1
fi

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
if ! sudoers_text="$(/usr/bin/sudo /bin/cat /etc/sudoers 2>/dev/null)"; then
  echo
  echo "Skipped the root helper: /etc/sudoers could not be read, so whether a" >&2
  echo "drop-in there is included is unknown. Run $repo/root-helper.sh from a" >&2
  echo "terminal that can answer sudo." >&2
  exit 0
fi

if ! printf '%s\n' "$sudoers_text" |
       /usr/bin/grep -qE '^[[:space:]]*[#@]includedir[[:space:]]+(/private)?/etc/sudoers\.d'; then
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
    owner="$(/usr/bin/stat -f '%Su' "$path" 2>/dev/null)" || return 1
    mode="$(/usr/bin/stat -f '%OLp' "$path" 2>/dev/null)" || return 1

    [ ! -L "$path" ] || return 1
    [ "$owner" = root ] || return 1
    [ $((8#$mode & 8#022)) -eq 0 ] || return 1

    case "$path" in /) return 0 ;; esac
    path="$(/usr/bin/dirname "$path")"
  done
}

if ! /usr/bin/sudo /usr/bin/install -d -m 0755 -o root -g wheel "$helper_dir"; then
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
  local bytes="$1" destination="$2" mode="$3" found

  if [ ! -e "$destination" ]; then
    echo "not installed yet"
    return 0
  fi

  if [ -L "$destination" ]; then
    echo "is a symlink, which it may never be"
    return 0
  fi

  # Against the bytes that were reviewed, down a pipe, rather than against a file
  # on disk: what decides whether to write has to be what gets written, or the
  # decision is about something else. sudo cmp because the sudoers rule is
  # installed 0440 and the account cannot read what it is being compared against —
  # a plain cmp would fail for want of permission and reinstall on every run.
  if ! printf '%s' "$bytes" | /usr/bin/sudo /usr/bin/cmp -s - "$destination"; then
    echo "contents differ from this repo's"
    return 0
  fi

  # %OLp prints the three octal digits with no leading zero, which is what the
  # modes below are stripped to rather than written twice.
  found="$(/usr/bin/stat -f '%Su:%Sg %OLp' "$destination" 2>/dev/null || true)"
  if [ "$found" != "root:wheel ${mode#0}" ]; then
    echo "is $found, want root:wheel ${mode#0}"
    return 0
  fi

  return 1
}

changed=false

install_copy() {
  local bytes="$1" destination="$2" mode="$3" why

  if ! why="$(difference "$bytes" "$destination" "$mode")"; then
    echo "  unchanged  $destination"
    return 0
  fi

  # The bytes go to root down a pipe and root writes them itself. There is no file
  # for a session to swap between the review and the install, because there is no
  # file: `install` would have had to be handed a path, and BSD install will not
  # take /dev/stdin — "Inappropriate file type or format" — so root does the three
  # steps itself.
  #
  # The temporary is made in the destination's own directory, which is root's and
  # writable by nobody else, and moved over the destination in one step: nothing
  # ever sees a half-written helper, and a run that dies leaves the previous one
  # in place. mktemp's suffix puts a dot in the name, which is also what keeps a
  # stray harmless — sudo ignores every file in sudoers.d whose name has one — and
  # the trap takes it away in any case. umask first, so the file is never readable
  # by anyone else while it is being written.
  #
  # A failed install stops the run here rather than going on to the next file: the
  # two only mean anything together, and a sudoers rule naming a helper that did
  # not land would be worse than neither. Not fatal to machine.sh, which names the
  # step among what is left.
  if ! printf '%s' "$bytes" | /usr/bin/sudo /bin/sh -c '
        PATH=/usr/bin:/bin:/usr/sbin:/sbin
        export PATH
        umask 077

        temporary="$(/usr/bin/mktemp "$1.XXXXXX")" || exit 1
        trap "/bin/rm -f \"\$temporary\"" EXIT

        /bin/cat >"$temporary" &&
          /usr/sbin/chown root:wheel "$temporary" &&
          /bin/chmod "$2" "$temporary" &&
          /bin/mv -f "$temporary" "$1"
      ' sh "$destination" "$mode"; then
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
install_copy "$helper_bytes" "$helper" 0755
install_copy "$sudoers_bytes" "$sudoers_file" 0440

if [ -e "$retired_newsyslog" ]; then
  if /usr/bin/sudo /bin/rm -f "$retired_newsyslog"; then
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
if /usr/bin/sudo -n -k "$helper" --list >/dev/null 2>&1; then
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
