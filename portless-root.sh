#!/bin/bash

# portless's proxy as the root LaunchDaemon it is designed to be, running code
# only root can write.
#
# Port 443 on 127.0.0.1 is root's on macOS, and a session has no terminal to
# answer sudo from, so a proxy that is not already running is one the first dev
# server of a session cannot start. Root holding 443 is also what keeps every URL
# on this Mac port-free, which is the shape portless's own URLs and `portless
# list` assume.
#
# What `portless service install` generates is the problem it solves: the plist
# it writes names the node that ran it and its own cli.js, neither resolved
# through the symlinks it arrived by, and under mise both sit in
# ~/.local/share/mise/installs — this account's. A daemon pointed there is root
# for anything running as the account at the next start. So both are copied to
# /usr/local/lib/portless as root:wheel under root-owned directories, and
# `service install` is run from that copy, which is what puts root-owned paths in
# the plist. tailscale.sh does the same with Homebrew's tailscaled, for the same
# reason, and README says why neither can be a symlink.
#
# Tim's to run and nobody else's: it needs sudo, which a Claude Code session has
# no terminal to type a password at. install.sh calls `--check` instead, which
# reads the plist and compares the copy and needs root for neither, and prints
# this script's path when the daemon is behind.
#
# Not part of machine.sh or claude.sh. It copies what mise installed, so it can
# only run after install.sh has installed it, and it must not run unattended: it
# restarts the proxy, and with it every dev URL on the Mac.
#
# Safe to re-run: nothing is copied that is already byte for byte what mise
# resolves, and the daemon is reinstalled only when the copy moved or its plist
# points somewhere else — so a re-run with nothing new restarts no proxy.

set -euo pipefail

if [ "$(uname -s)" != Darwin ]; then
  echo "portless-root.sh is macOS only" >&2
  exit 1
fi

# As the account, not as root. portless takes the state directory from SUDO_USER
# and chowns what it writes back to that account, so a run as root would move the
# daemon's routes and certificates to /var/root/.portless, where no client looks.
if [ "$(id -u)" -eq 0 ]; then
  echo "portless-root.sh runs as the account and sudoes what needs root." >&2
  exit 1
fi

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

label=sh.portless.proxy
daemon_plist="/Library/LaunchDaemons/$label.plist"

root_lib=/usr/local/lib/portless
copy_node="$root_lib/bin/node"
copy_modules="$root_lib/lib/node_modules"
copy_cli="$copy_modules/portless/dist/cli.js"

check_only=false
case "${1:-}" in
  --check) check_only=true ;;
  "") ;;
  *)
    echo "usage: portless-root.sh [--check]" >&2
    exit 2
    ;;
esac

# claude.sh runs install.sh from a bash that read no rc file, and install.sh is
# what calls --check.
export PATH="$HOME/.local/bin:$HOME/.local/share/mise/shims:$PATH"

if ! command -v mise >/dev/null 2>&1; then
  echo "mise is not installed, so there is no portless to copy: run install.sh" >&2
  exit 1
fi

node_which="$(mise which node 2>/dev/null || true)"
portless_which="$(mise which portless 2>/dev/null || true)"

if [ -z "$node_which" ] || [ -z "$portless_which" ]; then
  echo "mise resolves no node or no portless — both are in" >&2
  echo "home/.config/mise/config.toml, which install.sh applies" >&2
  exit 1
fi

# The chain of links mise leaves is exactly what must not reach the daemon: a
# shim, a .bin entry, a `latest` pointing at a versioned directory. What is
# copied is what all of them resolve to. Done here rather than with `readlink
# -f`, which is GNU's spelling and only recently macOS's.
resolve() {
  local path="$1" dir base target

  dir="$(cd "$(dirname "$path")" && pwd -P)" || return 1
  base="$(basename "$path")"

  while [ -L "$dir/$base" ]; do
    target="$(readlink "$dir/$base")"
    case "$target" in
      /*) dir="$(cd "$(dirname "$target")" && pwd -P)" || return 1 ;;
      *) dir="$(cd "$dir" && cd "$(dirname "$target")" && pwd -P)" || return 1 ;;
    esac
    base="$(basename "$target")"
  done

  printf '%s\n' "$dir/$base"
}

node_src="$(resolve "$node_which")"
cli_src="$(resolve "$portless_which")"
package_src="$(cd "$(dirname "$cli_src")/.." && pwd -P)"

# The package's node_modules rather than the package alone: portless bundles its
# dependencies into dist today, and a version that stops doing so would resolve
# its siblings from here. Node walks up to this directory from the cli.js below
# it, so the copy keeps the same two levels.
modules_src="$(dirname "$package_src")"

if [ ! -x "$node_src" ] || [ ! -f "$package_src/package.json" ] ||
   [ "$(basename "$package_src")" != portless ] ||
   [ "$(basename "$modules_src")" != node_modules ]; then
  echo "mise's portless is not the layout this copies — $cli_src should be" >&2
  echo "<node_modules>/portless/dist/cli.js" >&2
  exit 1
fi

node_stale=false
cmp -s "$node_src" "$copy_node" || node_stale=true

package_stale=false
diff -rq "$modules_src" "$copy_modules" >/dev/null 2>&1 || package_stale=true

daemon_program() {
  plutil -extract "ProgramArguments.$1" raw -o - "$daemon_plist" 2>/dev/null
}

daemon_stale=true
if [ -f "$daemon_plist" ] &&
   [ "$(daemon_program 0)" = "$copy_node" ] &&
   [ "$(daemon_program 1)" = "$copy_cli" ]; then
  daemon_stale=false
fi

if [ "$check_only" = true ]; then
  if [ "$daemon_stale" = true ]; then
    echo "portless's proxy daemon does not run from $root_lib" >&2
    exit 1
  fi

  if [ "$node_stale" = true ] || [ "$package_stale" = true ]; then
    echo "the copy in $root_lib is behind the node or the portless mise" >&2
    echo "resolves now" >&2
    exit 1
  fi

  exit 0
fi

if [ "$daemon_stale" = false ] &&
   [ "$node_stale" = false ] && [ "$package_stale" = false ]; then
  echo "portless's proxy is already $label on 443, from $root_lib"
  exit 0
fi

# A copy is only as safe as what can rename it, so every directory above it has
# to be root's as well. Reported rather than fixed: /usr/local belonging to
# somebody else is something to understand before writing into it.
for dir in /usr/local /usr/local/lib; do
  if [ -d "$dir" ] && [ "$(stat -f '%Su' "$dir")" != root ]; then
    echo "$dir belongs to $(stat -f '%Su' "$dir") rather than root, so nothing" >&2
    echo "under it is safe for a root daemon to run. Nothing was changed." >&2
    exit 1
  fi
done

if ! sudo -n true 2>/dev/null && [ ! -t 0 ]; then
  echo "portless-root.sh needs sudo and there is no terminal to type a password" >&2
  echo "at — a Claude Code session is a pane without one. From a shell:" >&2
  echo "  $repo/portless-root.sh" >&2
  exit 1
fi

echo
echo "This installs portless's proxy as the root daemon on 127.0.0.1:443 and"
echo "restarts it. Every dev server's URL stops answering while it comes back,"
echo "and every page open on one has to be reloaded."
echo

if [ -t 0 ]; then
  reply=""
  printf 'Install it now? [y/N] '
  read -r reply || reply=""

  case "$reply" in
    y | Y | yes | Yes) ;;
    *)
      echo
      echo "Nothing changed."
      exit 0
      ;;
  esac
else
  echo "No terminal to confirm at, so it goes ahead."
fi

for dir in "$root_lib" "$root_lib/bin" "$root_lib/lib"; do
  [ -d "$dir" ] || sudo install -d -m 0755 -o root -g wheel "$dir"
done

if [ "$node_stale" = true ]; then
  sudo install -m 0755 -o root -g wheel "$node_src" "$copy_node"
  echo "copied mise's node to $copy_node"
fi

if [ "$package_stale" = true ]; then
  # Staged beside the live copy and moved over it, so a copy interrupted halfway
  # leaves the daemon running the one that was already there rather than half of
  # a package. chmod after the copy because cp keeps the source's modes, which
  # are this account's umask rather than root's.
  staging="$root_lib/lib/node_modules.incoming"
  sudo rm -rf "$staging"
  sudo cp -R "$modules_src" "$staging"
  sudo chown -R root:wheel "$staging"
  sudo chmod -R go-w "$staging"
  sudo rm -rf "$copy_modules"
  sudo mv "$staging" "$copy_modules"
  echo "copied portless to $copy_modules"
fi

# The TLD list is .zshenv's, read from zsh because that is the one file naming it
# and this may be running from a bash that never read it. A running proxy only
# warns when the daemon's list differs from its own, so the daemon is installed
# from the same list everything else uses.
tlds="$(zsh -c 'print -r -- $PORTLESS_TLD' 2>/dev/null || true)"
tlds="${tlds:-localhost}"

tld_args=()
IFS=, read -ra tld_list <<<"$tlds"
for tld in ${tld_list[@]+"${tld_list[@]}"}; do
  [ -n "$tld" ] && tld_args+=(--tld "$tld")
done

# --https and --port spelled out although both are what `service install`
# defaults to: it reads PORTLESS_HTTPS and PORTLESS_PORT out of the environment
# before it reads its own flags and bakes the result into the plist, so a stray
# one in the shell this is run from would otherwise move the daemon off 443.
#
# There is no --skip-trust to pass here — the flag is rejected on this command
# and set on the daemon's own arguments — so an untrusted CA is trusted at this
# point, which puts a confirmation on the Mac's own screen. install.sh trusts it
# first, so there is normally nothing left to confirm.
if sudo "$copy_node" "$copy_cli" service install --https --port 443 \
     ${tld_args[@]+"${tld_args[@]}"}; then
  echo
  echo "portless's proxy is $label on 443, from $root_lib."
  echo "It serves .${tlds//,/ and .}, and \`portless list\` prints the URLs."
else
  echo
  echo "warning: the daemon was not installed, so nothing holds 443 and the" >&2
  echo "first dev server of a session will fail. 'sudo launchctl print" >&2
  echo "system/$label' says where it stands, and this script will try again." >&2
  exit 1
fi
