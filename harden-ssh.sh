#!/bin/bash

# Install the sshd hardening drop-in, naming one user in AllowUsers. Run last,
# because it is the step that turns password logins off, and on a Mac that is a
# harder thing to undo than on a VM: there is no provider console to fall back
# to, only Screen Sharing over the same tailnet.
#
# So it refuses when the user has no key sshd could let them in with, which is
# the state a Mac reached with a password is in. FORCE_HARDEN=true overrides
# that, for the case where you are certain of another way in.
#
# The drop-in lands rather than an edit to sshd_config because macOS builds its
# own copy with an `Include /etc/ssh/sshd_config.d/*` line in it — one this
# checks for, since without it nothing here has any effect at all. There is
# nothing to restart afterwards: launchd holds port 22 and spawns an sshd per
# connection, so the next login reads the new config and the one running this
# is unaffected.
#
# Safe to re-run, and takes the user to allow as its argument, defaulting to
# whoever runs it.

set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
user="${1:-$(id -un)}"
config=/etc/ssh/sshd_config
drop_in=/etc/ssh/sshd_config.d/10-hardening.conf

if [ "$(uname -s)" != Darwin ]; then
  echo "harden-ssh.sh is macOS only" >&2
  exit 1
fi

# No getent on a Mac; the account record is dscl's.
home="$(dscl . -read "/Users/$user" NFSHomeDirectory 2>/dev/null |
          sed 's/^NFSHomeDirectory: //')"
if [ -z "$home" ]; then
  echo "no such user: $user" >&2
  exit 1
fi

# sshd's StrictModes, which is on by default: it refuses to read authorized_keys
# when the home directory, .ssh or the file itself belongs to somebody else or is
# writable by anyone but its owner, and it says so in its own log rather than to
# the client. The first path that fails, or nothing.
strict_modes_offender() {
  local uid path owner mode

  uid="$(id -u "$user" 2>/dev/null || true)"
  if [ -z "$uid" ]; then
    return 0
  fi

  for path in "$home" "$home/.ssh" "$home/.ssh/authorized_keys"; do
    owner="$(stat -f '%u' "$path" 2>/dev/null || true)"
    mode="$(stat -f '%OLp' "$path" 2>/dev/null || true)"

    # A path that is not there is the no-key case below rather than this one.
    if [ -z "$owner" ] || [ -z "$mode" ]; then
      continue
    fi

    if [ "$owner" != "$uid" ] || [ $((8#$mode & 8#022)) -ne 0 ]; then
      echo "$path"
      return 0
    fi
  done
}

offender="$(strict_modes_offender)"

# A file with something in it is not a file sshd can let you in with: a wrapped
# or truncated key leaves lines that parse as nothing, and a key under a
# world-writable directory is one sshd never reads. ssh-keygen -l reads the whole
# file and succeeds if any one line is a key, which together with the modes above
# is the question worth asking before turning password logins off.
has_usable_key() {
  [ -z "$offender" ] &&
    ssh-keygen -l -f "$home/.ssh/authorized_keys" >/dev/null 2>&1
}

if ! has_usable_key && [ "${FORCE_HARDEN:-false}" != true ]; then
  echo
  if [ -n "$offender" ]; then
    echo "Skipped ssh hardening: sshd's StrictModes will not read $user's" >&2
    echo "authorized_keys while $offender is group- or world-writable or owned by" >&2
    echo "another account, so no key there is a way in. Take the write bits off it" >&2
    echo "with 'chmod go-w $offender', give it to $user if it is somebody else's," >&2
    echo "then run" >&2
  else
    echo "Skipped ssh hardening: $user has no key sshd could use, so disabling" >&2
    echo "password logins now would leave Screen Sharing as the only way in." >&2
    echo "Add the key you connect with to $home/.ssh/authorized_keys, then run" >&2
  fi
  echo "  $repo/harden-ssh.sh $user" >&2
  exit 0
fi

if ! has_usable_key; then
  echo "warning: hardening with no usable key installed (FORCE_HARDEN=true)." >&2
fi

# macOS has shipped the line since Monterey, injected into its own build of the
# stock config rather than coming from upstream — so a Mac old enough, or an
# sshd_config replaced by hand, silently ignores everything in the directory.
if ! grep -qE '^[[:space:]]*Include[[:space:]]+/etc/ssh/sshd_config\.d/' "$config"; then
  echo
  echo "Skipped ssh hardening: $config has no Include for" >&2
  echo "/etc/ssh/sshd_config.d, so a drop-in there would do nothing. Add the" >&2
  echo "line and run this again." >&2
  exit 0
fi

staged="$(mktemp)"
trap 'rm -f "$staged"' EXIT
sed "s/__USER__/$user/" "$repo/system/ssh/10-hardening.conf" >"$staged"

sudo install -d -m 0755 /etc/ssh/sshd_config.d
sudo install -m 0644 -o root -g wheel "$staged" "$drop_in"

# A drop-in sshd will not parse is worse than none at all, so take it back out
# rather than leave it for the next connection to trip over — and on a Mac the
# next connection is every connection, since each one reads the config again.
#
# Only when there are host keys to check it with: sshd -t exits non-zero without
# them, and a Mac whose Remote Login has just been turned on has none until the
# first connection generates them. That failure would read as a rejected drop-in.
if ! ls /etc/ssh/ssh_host_*_key >/dev/null 2>&1; then
  echo "warning: sshd has no host keys yet, so the drop-in was installed without" >&2
  echo "being checked. It is checked on the next run, once a first connection has" >&2
  echo "made them." >&2
elif ! sudo /usr/sbin/sshd -t; then
  sudo rm -f "$drop_in"
  echo "sshd rejected the drop-in; removed it and left the running config alone" >&2
  exit 1
fi

echo "ssh hardened: keys only, no root, AllowUsers $user"
