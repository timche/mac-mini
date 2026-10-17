#!/bin/bash

# tailscale on the Mac, which is the network this machine is reached over. What it
# leaves is a node that serves Tailscale SSH and routes for nobody: no subnet, no
# exit node. Agents run unattended here, so a route through this Mac would put the
# LAN behind it, and every byte of a client using it as an exit, within reach of
# whatever one of them ran.
#
# The open-source tailscaled from Homebrew rather than the standalone app. Both
# can serve Tailscale SSH, so that is not the reason: tailscaled is a system
# daemon and runs before anybody logs in, so a Mac whose auto-login fails or whose
# GUI session dies is still on the tailnet and still reachable — where the app is
# a login item inside a session, the same dependency that makes docker and the
# signing agent wait for one. Tailscale call tailscaled on macOS the less-tested
# variant and point unattended installs at it, which is what this is.
#
# Root runs Homebrew's own binary under the LaunchDaemon `brew services` writes,
# which is the install Tailscale document. An upgrade is `brew upgrade tailscale`
# and then `sudo brew services restart tailscale`, from the LAN: launchd reads a
# plist at bootstrap and not again, and the plist names the opt symlink rather
# than a version, so the formula moving under it changes nothing about the process
# that is already running — `brew upgrade` restarts no service, and `brew services
# restart` is Homebrew's own answer to a package that has been upgraded.
#
# A step of its own because it is the one part of the machine that is also the way
# the machine is reached: once this Mac is on the tailnet, every later run of it
# comes in over what it configures, so the best outcome is to change nothing.
#
# Not fatal. By the time this runs the rest of the machine is built, and a tailnet
# can be sorted out afterwards.
#
# Safe to re-run: the daemon is started only where `brew services` has not already
# registered it in the system domain, the prefs are written only where they differ
# from what is asked for here, and the login is only offered when there is nobody
# logged in yet. A run that does have to restart the daemon says so first, since
# that drops every tailnet connection to this Mac.

set -euo pipefail

if [ "$(uname -s)" != Darwin ]; then
  echo "tailscale.sh is macOS only" >&2
  exit 1
fi

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
console=https://login.tailscale.com/admin

# The daemon this replaced: a copy of Homebrew's binaries in /usr/local/bin under
# a LaunchDaemon of this repo's own. A Mac still on it is migrated below.
retired_label=io.github.timche.tailscaled
retired_plist="/Library/LaunchDaemons/$retired_label.plist"
retired_bin=/usr/local/bin

if ! command -v brew >/dev/null 2>&1 && [ -x /opt/homebrew/bin/brew ]; then
  eval "$(/opt/homebrew/bin/brew shellenv)"
fi

# The LAN

# The address on that same interface, which is the one that still answers sshd
# when the tailnet is down — including while this script is restarting the daemon
# the tailnet runs on.
lan_address() {
  local iface

  iface="$(route -n get default 2>/dev/null | awk '/interface:/ { print $2; exit }')"
  if [ -z "$iface" ]; then
    return 0
  fi

  ipconfig getifaddr "$iface" 2>/dev/null || true
}

# The daemon

# The formula is the Brewfile's, installed by bootstrap-system.sh — the cask of
# nearly the same name is the standalone app, and an app is a login item inside a
# GUI session.
brew_bin="$(command -v brew || true)"
brew_prefix="$(brew --prefix tailscale 2>/dev/null || true)"
brew_cellar="$(brew --cellar tailscale 2>/dev/null || true)"

if [ -z "$brew_bin" ] || [ -z "$brew_prefix" ] ||
   [ ! -x "$brew_prefix/bin/tailscaled" ]; then
  echo "tailscaled is not installed — it is declared in $repo/Brewfile, which" >&2
  echo "$repo/bootstrap-system.sh installs; rerun that, then $repo/tailscale.sh." >&2
  exit 0
fi

tailscale_cli="$brew_prefix/bin/tailscale"

# Asked rather than spelled out: Homebrew has renamed this label once already,
# from homebrew.mxcl.tailscale to sh.brew.tailscale, and the plist it installs is
# named after it.
label="$("$brew_bin" services info tailscale --json 2>/dev/null |
  jq -r '.[0].service_name // empty')"

if [ -z "$label" ]; then
  echo "warning: 'brew services info tailscale' said nothing about a service, so" >&2
  echo "the daemon was left as it is. 'brew services info tailscale' says why," >&2
  echo "and $repo/tailscale.sh will try again." >&2
  exit 0
fi

label_plist="/Library/LaunchDaemons/$label.plist"

# `brew services start` installs that plist and `brew services stop` takes it back
# out, so its presence is what says the service is registered to start at boot —
# and it reads without root, where the system domain does not.
registered=false
if [ -f "$label_plist" ]; then
  registered=true
fi

retired=false
if [ -e "$retired_plist" ]; then
  retired=true
fi

# A first install on a Mac with no daemon of either kind does not bounce the
# tunnel: there is nothing running to drop.
if [ "$retired" = true ]; then
  echo
  echo "tailscaled has to be restarted, which drops every tailnet connection to"
  echo "this Mac — including an SSH session that came in over it. The LAN's sshd"
  echo "is unaffected:"
  echo "  ssh $(id -un)@$(lan_address)"
  echo

  # Only where there is a terminal to answer at. Unattended it goes ahead: a run
  # that stopped here would leave this Mac on a daemon this repo no longer
  # installs, which is worse than a bounce nobody is watching.
  if [ -t 0 ]; then
    reply=""
    printf 'Restart tailscaled now? [y/N] '
    read -r reply || reply=""

    case "$reply" in
      y | Y | yes | Yes) ;;
      *)
        echo
        echo "Nothing changed. Run $repo/tailscale.sh again from the LAN."
        exit 0
        ;;
    esac
  else
    echo "No terminal to confirm at, so it goes ahead."
  fi
fi

# One of the copies the retired daemon ran, rather than anything else that has
# landed at this path: a root-owned regular file whose bytes are a tailscale
# binary Homebrew built. A standalone Tailscale.app puts a symlink here instead,
# and a file that matches nothing in the Cellar is somebody else's.
retired_copy() {
  local name="$1" path="$retired_bin/$1" candidate

  [ -n "$brew_cellar" ] || return 1
  [ -f "$path" ] && [ ! -L "$path" ] || return 1
  [ "$(stat -f '%Su' "$path" 2>/dev/null)" = root ] || return 1

  for candidate in "$brew_cellar"/*/bin/"$name"; do
    [ -f "$candidate" ] || continue
    cmp -s "$candidate" "$path" && return 0
  done

  return 1
}

if [ "$retired" = true ]; then
  sudo launchctl bootout "system/$retired_label" || true

  if ! sudo rm -f "$retired_plist"; then
    echo "warning: could not remove $retired_plist, so nothing was started over" >&2
    echo "it — two tailscaled cannot share one tunnel. Remove it and run" >&2
    echo "$repo/tailscale.sh again." >&2
    exit 0
  fi

  for name in tailscaled tailscale; do
    if retired_copy "$name"; then
      if sudo rm -f "$retired_bin/$name"; then
        echo "removed the retired copy $retired_bin/$name"
      else
        echo "warning: could not remove $retired_bin/$name, which nothing here" >&2
        echo "runs any more." >&2
      fi
    elif [ -e "$retired_bin/$name" ]; then
      echo "note: $retired_bin/$name is not one of the copies this replaced, so"
      echo "it was left alone. Nothing here runs it."
    fi
  done
fi

if [ "$retired" = true ] || [ "$registered" = false ]; then
  # `restart` where the service is already registered, which is Homebrew's way of
  # replacing a plist and the process under it in one step; `start` is the first
  # install.
  action=start
  if [ "$registered" = true ]; then
    action=restart
  fi

  if ! sudo "$brew_bin" services "$action" tailscale; then
    echo "warning: 'sudo brew services $action tailscale' did not take, so this" >&2
    echo "Mac has no tailscaled. Run it again, or $repo/tailscale.sh, from the" >&2
    echo "LAN." >&2
    exit 0
  fi

  if [ ! -f "$label_plist" ]; then
    echo "warning: brew services did not leave $label_plist, so the daemon is not" >&2
    echo "registered to start at boot. 'sudo brew services info tailscale' says" >&2
    echo "what it did instead." >&2
    exit 0
  fi

  echo "tailscaled runs as $label from $brew_prefix/bin/tailscaled"
else
  echo "tailscaled is already $label from Homebrew's formula, left alone"
fi

# The prefs

# No root for any of this. tailscaled's LocalAPI gives a read-write connection to
# root, to its own user and to members of the admin group on macOS, which this
# account is in — and `sudo /opt/homebrew/bin/tailscale` would be root executing a
# path the account can write, for access it already has. The sudo is the fallback
# for an account outside that group, which gets a read-only connection and cannot
# so much as read the prefs over it.
tailscale_cmd=("$tailscale_cli")
if ! id -Gn | tr ' ' '\n' | grep -qx admin; then
  tailscale_cmd=(sudo "$tailscale_cli")
fi

# What tailscaled says about itself, which has to survive a daemon that is not
# answering at all.
tailscale_status() {
  "${tailscale_cmd[@]}" status --json 2>/dev/null |
    jq -r "$1 // empty" 2>/dev/null || true
}

# Read before written. `tailscale set` is cheap, but a machine that is already
# configured the way this asks for should come out of a run untouched — and
# reading prefs is also the only way to tell that somebody chose something else
# on purpose.
prefs="$("${tailscale_cmd[@]}" debug prefs 2>/dev/null || true)"

if [ -z "$prefs" ]; then
  echo "warning: tailscaled is not answering, so nothing was configured. 'sudo" >&2
  echo "launchctl print system/$label' and '${tailscale_cmd[*]} status' say why," >&2
  echo "and 'sudo brew services restart tailscale' is what starts it again." >&2
  exit 0
fi

# An exit node shows up in the prefs as the two default routes, so an empty list
# is both no subnet and no exit node.
have_routes="$(printf '%s' "$prefs" | jq -r '(.AdvertiseRoutes // []) | length')"
have_ssh="$(printf '%s' "$prefs" | jq -r '.RunSSH // false')"

if [ "$have_ssh" = true ] && [ "$have_routes" = 0 ]; then
  echo "tailscale already serves ssh and advertises no routes"
else
  # `set` rather than `up`, which would start a login this node has already done.
  if "${tailscale_cmd[@]}" set --ssh --advertise-exit-node=false --advertise-routes=; then
    echo "tailscale now serves ssh and advertises no routes"
  else
    echo "warning: 'tailscale set' did not take — this Mac advertises whatever it" >&2
    echo "did before. '$tailscale_cli debug prefs' says what that is." >&2
  fi
fi

# The tailnet

state="$(tailscale_status .BackendState)"

if [ "$state" != Running ]; then
  login="${tailscale_cmd[*]} up --ssh"

  # `up` is the one command here that waits: it prints a URL to open on a machine
  # that has a browser and sits there until somebody does. So only where there is
  # a terminal to sit at — a provision with nobody watching is told what to run
  # instead of hanging on it.
  if [ -t 0 ]; then
    echo
    echo "Not on the tailnet yet (${state:-no state}). This prints a URL to open"
    echo "on another machine, and waits for it:"
    echo

    if ! "${tailscale_cmd[@]}" up --ssh; then
      echo "warning: the tailnet login did not finish. Run it again with:" >&2
      echo "  $login" >&2
    fi
  else
    echo
    echo "Not on the tailnet yet (${state:-no state}), and no terminal to log in"
    echo "at. From one:"
    echo "  $login"
  fi
fi

# What the tailnet has to say

# The tailnet's rather than the machine's: tailscaled's SSH server answers nobody
# until the policy says who.
cat <<EOF

One thing only the tailnet's admin console can do: allow Tailscale SSH to this
Mac — an ssh rule naming who may connect and as whom. Until there is one, nothing
reaches tailscaled's SSH server, and sshd on the LAN is the only way in:
  $console/acls
EOF
