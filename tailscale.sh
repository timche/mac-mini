#!/bin/bash

# tailscale on the Mac, which is the network this machine is reached over. What it
# leaves is a node that serves Tailscale SSH, advertises the LAN it is plugged
# into as a subnet, and offers itself as an exit node.
#
# The open-source tailscaled from Homebrew rather than the standalone app. Both
# can serve Tailscale SSH, so that is not the reason: tailscaled is a system
# daemon and runs before anybody logs in, so a Mac whose auto-login fails or whose
# GUI session dies is still on the tailnet and still reachable — where the app is
# a login item inside a session, the same dependency that makes docker and the
# signing agent wait for one. Tailscale call tailscaled on macOS the less-tested
# variant and point unattended installs at it, which is what this is.
#
# Homebrew is where the binaries come from and `brew upgrade tailscale` is still
# the update, but what root executes is a copy of them in /usr/local/bin under
# this repo's own LaunchDaemon. Every directory on the way to Homebrew's
# tailscaled — the prefix, its Cellar, the opt symlinks — is writable by the
# account, so a root daemon pointed there is root for anything running as the
# account, and so is the log it appends to under /opt/homebrew/var/log.
#
# A step of its own because it is the one part of the machine that is also the way
# the machine is reached: once this Mac is on the tailnet, every later run of it
# comes in over what it configures, so the best outcome is to change nothing.
#
# Not fatal. By the time this runs the rest of the machine is built, and a tailnet
# can be sorted out afterwards.
#
# Safe to re-run: the copies and the daemon are replaced only where they differ
# from what this repo and Homebrew say they should be, the prefs are written only
# where they differ from what is asked for here, and the login is only offered
# when there is nobody logged in yet. A run that does have to restart the daemon
# says so first, since that drops every tailnet connection to this Mac.

set -euo pipefail

if [ "$(uname -s)" != Darwin ]; then
  echo "tailscale.sh is macOS only" >&2
  exit 1
fi

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
console=https://login.tailscale.com/admin

daemon_label=io.github.timche.tailscaled
daemon_plist="/Library/LaunchDaemons/$daemon_label.plist"
repo_plist="$repo/system/launchd/$daemon_label.plist"

# The label `brew services` gave the daemon this replaced, and the one thing that
# has to be gone before ours can hold the tunnel.
brew_daemon_label=sh.brew.tailscale

root_bin=/usr/local/bin
tailscale_cli="$root_bin/tailscale"

if ! command -v brew >/dev/null 2>&1 && [ -x /opt/homebrew/bin/brew ]; then
  eval "$(/opt/homebrew/bin/brew shellenv)"
fi

# What LAN to advertise

# The network behind the interface the default route leaves by, as a CIDR.
# ifconfig rather than `ipconfig getoption`, which only answers for an address
# DHCP handed out; the mask comes back as 0xffffff00, and counting its bits is the
# prefix.
#
# The mask is taken from after the `netmask` keyword rather than from a fixed
# column: a point-to-point interface prints `inet <addr> --> <peer> netmask <mask>`
# and puts the peer where a broadcast interface puts the mask. Anything that is not
# a mask advertises nothing, since the arithmetic below would otherwise abort the
# script — and with it every step machine.sh runs after this one.
lan_cidr() {
  local iface inet address mask mask_re prefix value a b c d network

  iface="$(route -n get default 2>/dev/null | awk '/interface:/ { print $2; exit }')"
  if [ -z "$iface" ]; then
    return 0
  fi

  inet="$(ifconfig "$iface" 2>/dev/null | awk '
    /inet [0-9]/ {
      for (i = 3; i < NF; i++) {
        if ($i == "netmask") {
          print $2, $(i + 1)
          exit
        }
      }
      exit
    }')"
  if [ -z "$inet" ]; then
    return 0
  fi

  address="${inet%% *}"
  mask="${inet##* }"

  mask_re='^0x[0-9a-fA-F]{8}$'
  if ! [[ "$mask" =~ $mask_re ]]; then
    return 0
  fi

  prefix=0
  value=$((mask))
  while [ "$value" -ne 0 ]; do
    prefix=$((prefix + (value & 1)))
    value=$((value >> 1))
  done

  # A /32 is one address rather than a network, which is what a point-to-point
  # interface reports — a VPN's utun holding the default route, say. Advertising it
  # would offer the tailnet a route to this node's own address.
  if [ "$prefix" -eq 32 ]; then
    return 0
  fi

  IFS=. read -r a b c d <<EOF
$address
EOF

  network=$((((a << 24) | (b << 16) | (c << 8) | d) & mask))

  echo "$(((network >> 24) & 255)).$(((network >> 16) & 255)).$(((network >> 8) & 255)).$((network & 255))/$prefix"
}

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

# TS_ADVERTISE_ROUTES overrides it, and set-but-empty means advertise nothing —
# the same shape as the VM repo's TS_AUTHKEY, so a machine that should not be a
# subnet router can say so without this script being edited.
if [ -n "${TS_ADVERTISE_ROUTES+set}" ]; then
  routes="$TS_ADVERTISE_ROUTES"
else
  routes="$(lan_cidr)"
fi

# The daemon

# The formula is the Brewfile's, installed by bootstrap-system.sh — the cask of
# nearly the same name is the standalone app, and an app is a login item inside a
# GUI session.
brew_prefix="$(brew --prefix tailscale 2>/dev/null || true)"

if [ -z "$brew_prefix" ] || [ ! -x "$brew_prefix/bin/tailscaled" ]; then
  echo "tailscaled is not installed — it is declared in $repo/Brewfile, which" >&2
  echo "$repo/bootstrap-system.sh installs; rerun that, then $repo/tailscale.sh." >&2
  exit 0
fi

# cmp rather than a version string: a formula rebuilt at the same version is still
# a different binary, and the whole point of the copy is that it is Homebrew's
# exact bytes. The CLI is copied beside the daemon because this script runs it
# under sudo, and `sudo /opt/homebrew/bin/tailscale` is the same account-writable
# path to root that the daemon was.
stale=""
for name in tailscaled tailscale; do
  if ! cmp -s "$brew_prefix/bin/$name" "$root_bin/$name"; then
    stale="$stale $name"
  fi
done

plist_stale=false
if ! cmp -s "$repo_plist" "$daemon_plist"; then
  plist_stale=true
fi

brew_loaded=false
if sudo launchctl print "system/$brew_daemon_label" >/dev/null 2>&1; then
  brew_loaded=true
fi

ours_loaded=false
if sudo launchctl print "system/$daemon_label" >/dev/null 2>&1; then
  ours_loaded=true
fi

# Whether anything below actually bounces the tunnel. A first install on a Mac
# with no daemon loaded does not: there is no connection to drop.
restarts=false
if [ "$brew_loaded" = true ] ||
   { [ "$ours_loaded" = true ] && { [ "$plist_stale" = true ] || [ -n "$stale" ]; }; }; then
  restarts=true
fi

if [ "$restarts" = true ]; then
  echo
  echo "tailscaled has to be restarted, which drops every tailnet connection to"
  echo "this Mac — including an SSH session that came in over it. The LAN's sshd"
  echo "is unaffected:"
  echo "  ssh $(id -un)@$(lan_address)"
  echo

  # Only where there is a terminal to answer at. Unattended it goes ahead: a run
  # that stopped here would leave the daemon and the copies disagreeing about
  # which binary is loaded, which is worse than a bounce nobody is watching.
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

for name in $stale; do
  if ! sudo install -m 0755 -o root -g wheel \
       "$brew_prefix/bin/$name" "$root_bin/$name"; then
    echo "warning: could not put a root-owned $name in $root_bin, so the daemon" >&2
    echo "was left as it is. $repo/tailscale.sh will try again." >&2
    exit 0
  fi
done

if [ "$plist_stale" = true ]; then
  if ! sudo install -m 0644 -o root -g wheel "$repo_plist" "$daemon_plist"; then
    echo "warning: could not install $daemon_plist, so the daemon was left as it" >&2
    echo "is. $repo/tailscale.sh will try again." >&2
    exit 0
  fi
fi

# `brew services stop` rather than a bootout, because it also takes its generated
# plist back out of /Library/LaunchDaemons — leaving it would give the next `brew
# services` run a second daemon to load over this one.
if [ "$brew_loaded" = true ]; then
  if sudo brew services stop tailscale; then
    echo "stopped Homebrew's $brew_daemon_label"
  else
    echo "warning: could not stop $brew_daemon_label. Two tailscaled cannot share" >&2
    echo "one tunnel, so ours is not started: 'sudo brew services stop tailscale'" >&2
    echo "then $repo/tailscale.sh." >&2
    exit 0
  fi
fi

# launchd reads a plist at bootstrap and not again, so a changed one needs the job
# taken out and put back; a changed binary under an unchanged plist only needs the
# process killed, which KeepAlive brings straight back.
if [ "$ours_loaded" = true ] && [ "$plist_stale" = true ]; then
  sudo launchctl bootout "system/$daemon_label" || true
  ours_loaded=false
fi

if [ "$ours_loaded" = false ]; then
  if sudo launchctl bootstrap system "$daemon_plist"; then
    echo "tailscaled runs as $daemon_label from $root_bin/tailscaled"
  else
    echo "could not start tailscaled — 'sudo launchctl print system/$daemon_label'" >&2
    echo "says where it is, and $repo/tailscale.sh will try again." >&2
    exit 0
  fi
elif [ -n "$stale" ]; then
  sudo launchctl kickstart -k "system/$daemon_label"
  echo "tailscaled restarted on the copy Homebrew's $brew_prefix just moved"
else
  echo "tailscaled is already $daemon_label on a root-owned binary, left alone"
fi

# What tailscaled says about itself. Two reads of the same JSON, both of which
# have to survive a daemon that is not answering at all.
tailscale_status() {
  sudo "$tailscale_cli" status --json 2>/dev/null | jq -r "$1 // empty" 2>/dev/null || true
}

# The prefs

# Read before written. `tailscale set` is cheap, but a machine that is already
# configured the way this asks for should come out of a run untouched — and
# reading prefs is also the only way to tell that somebody chose something else
# on purpose.
prefs="$(sudo "$tailscale_cli" debug prefs 2>/dev/null || true)"

if [ -z "$prefs" ]; then
  echo "warning: tailscaled is not answering, so nothing was configured. 'sudo" >&2
  echo "$tailscale_cli status' and 'sudo launchctl print system/$daemon_label'" >&2
  echo "say why." >&2
  exit 0
fi

# An exit node shows up in the prefs as the two default routes alongside whatever
# subnet is advertised, which is why there is one comparison rather than two.
want_routes="$(printf '%s\n0.0.0.0/0\n::/0\n' "$routes" | tr ',' '\n' | sed '/^[[:space:]]*$/d' | sort -u)"
have_routes="$(printf '%s' "$prefs" | jq -r '(.AdvertiseRoutes // [])[]' | sort -u)"
have_ssh="$(printf '%s' "$prefs" | jq -r '.RunSSH // false')"

if [ "$have_ssh" = true ] && [ "$have_routes" = "$want_routes" ]; then
  echo "tailscale already serves ssh and advertises ${routes:-no subnet} and an exit node"
else
  # `set` rather than `up`, which would start a login this node has already done.
  #
  # An exit node on macOS routes in userspace and only while the machine is awake:
  # unattended.sh's `pmset sleep 0` is what keeps it one. IP forwarding needs
  # nothing here — on macOS Tailscale turns it on itself when routes are
  # advertised.
  if sudo "$tailscale_cli" set --ssh --advertise-exit-node --advertise-routes="$routes"; then
    echo "tailscale now serves ssh and advertises ${routes:-no subnet} and an exit node"
  else
    echo "warning: 'tailscale set' did not take — this Mac advertises whatever it" >&2
    echo "did before. 'sudo $tailscale_cli debug prefs' says what that is." >&2
  fi
fi

# The tailnet

state="$(tailscale_status .BackendState)"

if [ "$state" != Running ]; then
  login="sudo $tailscale_cli up --ssh --advertise-exit-node --advertise-routes=$routes"

  # `up` is the one command here that waits: it prints a URL to open on a machine
  # that has a browser and sits there until somebody does. So only where there is
  # a terminal to sit at — a provision with nobody watching is told what to run
  # instead of hanging on it.
  if [ -t 0 ]; then
    echo
    echo "Not on the tailnet yet (${state:-no state}). This prints a URL to open"
    echo "on another machine, and waits for it:"
    echo

    if ! sudo "$tailscale_cli" up --ssh --advertise-exit-node --advertise-routes="$routes"; then
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

# Both of these are the tailnet's rather than the machine's, and neither can be
# done from here: an advertised route is advertised until somebody approves it,
# and tailscaled's SSH server answers nobody until the policy says who.
cat <<EOF

Two things only the tailnet's admin console can do:

  - Approve this machine's subnet route${routes:+ ($routes)} and its exit node,
    unless the policy file's autoApprovers already covers them:
    $console/machines
  - Allow Tailscale SSH to it — an ssh rule naming who may connect and as whom.
    Until there is one, nothing reaches tailscaled's SSH server, and sshd on the
    LAN is the only way in:
    $console/acls
EOF
