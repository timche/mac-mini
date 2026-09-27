#!/bin/bash

# Docker on a Mac, which means a Linux VM and a CLI that talks into it: OrbStack
# runs the VM under Virtualization.framework with a docker daemon inside it, and
# links its own docker, compose and buildx into /usr/local/bin and ~/.docker so the
# CLI on this side reaches that daemon. All of it is the one cask.
#
# OrbStack rather than colima, which this replaced. A colima VM rarely handed
# memory back: whatever a build or a test suite made it touch stayed taken until
# somebody restarted it, on a Mac whose parallel sessions run browsers and Electron
# outside the VM and want that memory. OrbStack's memory is dynamic and returns to
# macOS, so its memory setting is a ceiling rather than a reservation and the
# default is left alone. Not Docker Desktop, which is heavier again and whose
# licence costs the same as this one's.
#
# OrbStack is free for personal use and needs a paid licence for commercial work.
# Nothing here can buy or apply one, and a Mac without one still runs containers.
#
# The first run is the app's own, and it needs somebody at the screen: a welcome
# screen whose Next accepts OrbStack's terms, then a choice between Docker and
# Linux machines. Nothing in a script can click either, so this one says so and
# stops rather than failing.
#
# Start at login means a login item, so docker here is only running once the Mac
# has logged itself in. That is the auto-login unattended.sh checks for and will
# not turn on, and it is the same thing the agent holding the signing key depends
# on.
#
# Not fatal. Every step says what it could not do, and a Mac with no VM running is
# still the machine the rest of this repo built.
#
# Safe to re-run: the settings are compared before they are set, the VM is
# restarted only when something changed, and what colima left behind is removed
# only where it is still there.

set -euo pipefail

if [ "$(uname -s)" != Darwin ]; then
  echo "docker.sh is macOS only" >&2
  exit 1
fi

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if ! command -v brew >/dev/null 2>&1 && [ -x /opt/homebrew/bin/brew ]; then
  eval "$(/opt/homebrew/bin/brew shellenv)"
fi

# What colima left

# Only where it is still there, so this is a no-op on every Mac but the one that
# ran the old version of this script. The VM is deleted before the state directory
# goes, because colima's delete is what tells its own lima layer to let go of the
# VM rather than leaving a stopped one registered.
if command -v colima >/dev/null 2>&1; then
  case "$(brew services list | awk '$1 == "colima" { print $2 }')" in
  started | scheduled | error)
    echo "stopping the colima LaunchAgent OrbStack replaces"
    brew services stop colima || true
    ;;
  esac

  if colima list 2>/dev/null | grep -q .; then
    echo "deleting the colima VM OrbStack replaces"
    colima delete --force || true
  fi
fi

if [ -d "$HOME/.colima" ]; then
  echo "removing what colima kept in $HOME/.colima"
  rm -rf "$HOME/.colima"
fi

# The plugin directory the old script wrote into docker's config. OrbStack puts its
# compose and buildx plugins in ~/.docker/cli-plugins, which the CLI searches by
# itself, and Homebrew's prefix no longer holds any — so the entry left there points
# at nothing.
docker_config="$HOME/.docker/config.json"
stale_plugin_dir=/opt/homebrew/lib/docker/cli-plugins

if [ -f "$docker_config" ] && jq -e . "$docker_config" >/dev/null 2>&1; then
  # Merged rather than written: this is also the file docker keeps the current
  # context in — OrbStack sets one — and whatever credential helper a registry
  # login left behind.
  merged="$(
    jq --arg dir "$stale_plugin_dir" '
      if (.cliPluginsExtraDirs // []) | index($dir) then
        .cliPluginsExtraDirs -= [$dir]
        | if (.cliPluginsExtraDirs | length) == 0 then del(.cliPluginsExtraDirs) else . end
      else . end
    ' "$docker_config"
  )"

  if [ "$merged" != "$(cat "$docker_config")" ]; then
    tmp="$(mktemp "$docker_config.XXXXXX")"
    printf '%s\n' "$merged" >"$tmp"
    mv "$tmp" "$docker_config"
    echo "docker's config no longer names Homebrew's empty plugin directory"
  fi
fi

# The app

# The Brewfile's, installed by bootstrap-system.sh: Homebrew is the machine, so one
# declarative list holds every package and what is left here is the decision this
# script is the only one that can make — about the VM's shape and what the first run
# needs a person for. The app bundle as well as the CLI, because the cask links orb
# into Homebrew's prefix and a link outliving the app it points at is exactly the
# state worth naming.
if [ ! -d /Applications/OrbStack.app ] || ! command -v orb >/dev/null 2>&1; then
  echo "OrbStack is not installed — it is declared in $repo/Brewfile, which" >&2
  echo "$repo/bootstrap-system.sh installs; rerun that, then $repo/docker.sh." >&2
  exit 0
fi

# The first run, which needs a person

# OrbStack writes ~/.orbstack the first time it is set up and never before, so its
# absence is the one reading that tells a fresh install from a stopped VM. Said and
# skipped rather than failed: this is a click on the Mac's own screen, which on a
# headless Mac means Screen Sharing, and the rest of the provision has nothing to
# do with it.
if [ ! -d "$HOME/.orbstack" ]; then
  cat <<EOF

OrbStack is installed but has never been set up, which is a step no script can do:
its first run puts a welcome screen on the Mac's own screen, and Next there is what
accepts OrbStack's terms.

Over Screen Sharing: open OrbStack, click through the welcome screen, and choose
Docker when it asks what to use. Then rerun $repo/docker.sh, which settles the
settings that first run leaves at OrbStack's defaults.
EOF
  exit 0
fi

# The settings

# All the cores but two, so that macOS and whatever is watching the machine keep
# somewhere to run. Read from the hardware rather than written down: this repo is
# aimed at one Mac, but nothing in it should have to be edited to suit the next one.
#
# Memory is deliberately not here. OrbStack's is dynamic — the VM gives back what it
# stops using — so its memory_mib is a ceiling that costs nothing until it is
# reached, and the default is half the machine's. That is the difference from colima
# that this whole step exists for.
cores="$(sysctl -n hw.ncpu)"
cpu=$((cores - 2))
if [ "$cpu" -lt 2 ]; then
  cpu=2
fi

orb_config() {
  orb config show 2>/dev/null | sed -n "s/^$1: *//p" | head -1
}

# Set one at a time and only where they differ, so a re-run prints nothing and
# leaves a running VM alone.
restart=false

for pair in "app.start_at_login true" "cpu $cpu"; do
  key="${pair%% *}"
  want="${pair##* }"
  have="$(orb_config "$key")"

  if [ "$have" = "$want" ]; then
    continue
  fi

  if ! orb config set "$key" "$want"; then
    echo "warning: OrbStack would not take $key=$want, so it is ${have:-unset}." >&2
    continue
  fi

  echo "OrbStack's $key is now $want"

  # The VM reads its CPU count when it boots, where the login item is the app's own
  # and takes immediately. So only this one is worth bouncing a VM for.
  if [ "$key" = cpu ]; then
    restart=true
  fi
done

if [ "$restart" = true ]; then
  echo "restarting OrbStack, which is when the VM reads its new CPU count"
  orb stop || true
  orb start || true
fi

# Where it stands

if orb status >/dev/null 2>&1; then
  echo "OrbStack is running with $(orb_config cpu) CPUs and $(orb_config memory_mib)MiB of memory"

  # OrbStack points docker at its VM by setting a context, and the context is the
  # only way the CLI knows where the daemon is.
  context="$(docker context show 2>/dev/null || true)"

  if [ "$context" != orbstack ]; then
    echo "warning: docker's context is ${context:-unset} rather than orbstack, so" >&2
    echo "the CLI is not pointed at this VM. 'docker context use orbstack' settles" >&2
    echo "it." >&2
  fi
else
  echo
  echo "warning: OrbStack is not running." >&2
  echo "'orb start' starts it, and 'orb status' says where it got to." >&2
fi
