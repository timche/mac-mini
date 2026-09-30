#!/bin/bash

# Assertions against a Mac that machine.sh has just finished with — the machine
# alone, with assert.sh covering the account on top of it and assert-claude.sh
# the logins and the signing key. Runs on the machine itself, which is a CI
# runner: there is no macOS container to put any of this in.
#
# To add a case, add a check line: a description and a shell snippet that exits
# non-zero when the expectation is not met.

set -uo pipefail

export PATH="/opt/homebrew/bin:/opt/homebrew/sbin:$PATH"
export root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

failures=0

check() {
  local description="$1" snippet="$2"

  if bash -c "$snippet" >/dev/null 2>&1; then
    echo "  ok    $description"
  else
    echo "  FAIL  $description"
    failures=$((failures + 1))
  fi
}

# Nothing may be written for one account: the Mac's is timche and a runner's is
# runner, and a path hardcoded for either is a script that silently does nothing
# on the other. The dscl and launchctl reads that build a path from a variable are
# what this has to leave alone, which is why the pattern needs a literal name.
check "no hardcoded home directory in the scripts" \
  '! grep -rhoE --include="*.sh" --include="*.plist" --include="mise.toml" \
       "/Users/[A-Za-z0-9_.-]+" "$root" |
     grep -q .'

# The clone is ~/.mac-mini, hidden because it is machinery rather than work, and
# the LaunchAgent the Claude half installs is a link into it — so where it lands is
# also where launchd reads the agent from. bootstrap.sh is the only thing that
# decides that, and a runner's clone is the workspace, so the default is what there
# is to assert here.
check "the clone defaults to the hidden path" \
  'grep -q "MAC_MINI_DIR:-\$HOME/\.mac-mini" "$root/bootstrap.sh"'

# Homebrew and its packages, which is all the machine half installs.
check "brew is the Apple Silicon prefix" '[ -x /opt/homebrew/bin/brew ]'

# The whole package list at once, which is what one Brewfile buys. --no-upgrade to
# match the install: an outdated formula is not a missing one, and nothing here moves
# a version.
check "the Brewfile's dependencies are satisfied" \
  'brew bundle check --no-upgrade --file="$root/Brewfile"'

check "bootstrap-system.sh installs the Brewfile and moves no version" \
  'grep -q "brew bundle --no-upgrade --file=\"\$repo/Brewfile\"" \
     "$root/bootstrap-system.sh"'

# xcodes and aria2 are xcode.sh's, because that step only runs where there is a
# terminal — so nothing else may install a package and the Brewfile may not declare
# those two. Both halves, or the decision holds in one direction only.
# A word boundary in front, or a sentence of prose about what Homebrew installs
# reads as a package install.
check "every package is the Brewfile's, bar the Xcode download's two" \
  '! grep -qE "^(brew|cask) \"(xcodes|aria2)\"" "$root/Brewfile" &&
   for script in "$root"/*.sh; do
     case "${script##*/}" in xcode.sh) continue ;; esac
     ! grep -qE "(^|[^A-Za-z])brew install " "$script" || exit 1
   done'
check "gh installed"   'command -v gh'
check "jq installed"   'command -v jq'
check "btop installed" 'command -v btop'
check "op installed"   'command -v op'
check "op is where launchd will look for it" '[ -x /opt/homebrew/bin/op ]'
check "git came with the command line tools" \
  '[ -x /Library/Developer/CommandLineTools/usr/bin/git ] || xcode-select -p'

# tailscale, which here is the open-source tailscaled from Homebrew rather than the
# app: a system daemon, so that a Mac with no login session is still on the tailnet.
check "tailscaled installed"      'command -v tailscaled'
check "the tailscale CLI answers" 'tailscale version'

# The daemon is in the system domain, which needs root to read — and a Mac being
# provisioned by hand should not meet a password prompt inside a test.
if sudo -n true 2>/dev/null; then
  check "tailscaled is a loaded system daemon" \
    'sudo -n launchctl print system/sh.brew.tailscale'
else
  echo "  --    sudo wants a password, so tailscaled's daemon was not checked"
fi

# The prefs, which tailscale.sh writes whether or not the node has ever logged in.
# Readable without root on macOS; the sudo is the fallback for a daemon that
# disagrees.
prefs() {
  tailscale debug prefs 2>/dev/null || sudo -n tailscale debug prefs 2>/dev/null
}
export -f prefs

check "tailscale serves ssh"          '[ "$(prefs | jq -r .RunSSH)" = true ]'
check "tailscale advertises an exit node" \
  'prefs | jq -e "(.AdvertiseRoutes // []) | index(\"0.0.0.0/0\") and index(\"::/0\")"'

# The subnet route is derived from the hardware, so what is asserted is that the
# route this Mac advertises is the one its own address sits in — not a number
# repeated from the script. python3 because the arithmetic is the thing under test
# and awk on a Mac has no bitwise operators to redo it with.
lan_route() {
  prefs | jq -r '(.AdvertiseRoutes // [])[] | select(. != "0.0.0.0/0" and . != "::/0")'
}
export -f lan_route

default_address() {
  local iface
  iface="$(route -n get default 2>/dev/null | awk '/interface:/ { print $2; exit }')"
  [ -n "$iface" ] && ipconfig getifaddr "$iface"
}
export -f default_address

if [ -z "$(default_address)" ]; then
  echo "  --    no default route, so the advertised subnet was not checked"
elif ! command -v python3 >/dev/null 2>&1; then
  echo "  --    no python3, so the advertised subnet was not checked"
else
  check "the advertised subnet is the LAN this Mac is on" \
    'python3 -c "
import ipaddress, sys
address, route = sys.argv[1], sys.argv[2]
sys.exit(0 if ipaddress.ip_address(address) in ipaddress.ip_network(route) else 1)
" "$(default_address)" "$(lan_route)"'
fi

# docker, which on a Mac is a Linux VM and a CLI pointed into it — here the one
# OrbStack app, which brings the VM, the docker CLI, compose and buildx together.
# The app bundle as well as the CLI, since the cask links orb into Homebrew's prefix
# and a link outliving its app would pass a check on the name alone.
check "OrbStack installed"    '[ -d /Applications/OrbStack.app ]'
check "the orb CLI answers"   'command -v orb'

# colima and Homebrew's docker are gone, and the assertion is on what this repo
# installs rather than on what is on the machine: a runner image may ship either,
# and nothing here uninstalls a package — the Brewfile is the whole of what this Mac
# is told to have.
check "no colima or Homebrew docker is declared any more" \
  '! grep -qE "^brew \"(colima|docker|docker-buildx|docker-compose)\"" "$root/Brewfile"'
check "no colima VM state is left behind" '[ ! -d "$HOME/.colima" ]'
check "docker.sh clears a colima left by an older run" \
  'grep -q "colima delete --force" "$root/docker.sh"'

# The plugin directory the old script wrote into docker's config, which points at
# nothing now that Homebrew's docker formulae are gone. OrbStack's plugins are in
# ~/.docker/cli-plugins, which the CLI searches by itself.
check "docker's config names no Homebrew plugin directory" \
  '[ ! -f "$HOME/.docker/config.json" ] ||
   ! jq -e ".cliPluginsExtraDirs // [] |
            index(\"/opt/homebrew/lib/docker/cli-plugins\")" \
       "$HOME/.docker/config.json"'

# The VM's shape, which is the machine's rather than a number in the script. The
# arithmetic is spelled out again instead of sourced, because what this asserts is
# that docker.sh read the hardware at all. Memory is not in it: OrbStack's is
# dynamic and its default is a ceiling, so docker.sh sets none.
orb_value() {
  orb config show 2>/dev/null | sed -n "s/^$1: *//p" | head -1
}
export -f orb_value

want_cpu=$(($(sysctl -n hw.ncpu) - 2))
[ "$want_cpu" -lt 2 ] && want_cpu=2

# The VM itself, which a runner cannot have: GitHub's macOS machines are VMs
# already and Virtualization.framework inside one refuses outright with
# "Virtualization is not available on this hardware". Nor can a runner click
# through OrbStack's first run, and an install that has not had one has no settings
# to read. Said out loud, because a suite that quietly asserted nothing here would
# read the same on a Mac where docker is broken.
if [ "$(orb status 2>/dev/null || true)" = Running ]; then
  # macOS's list of login items rather than OrbStack's app.start_at_login, which
  # reports false on a Mac that is in that list and does start with the session.
  # Reading the list is also what says there is a GUI session to read it in, and a
  # process allowed to drive System Events to read it with.
  if login_items="$(osascript -e 'tell application "System Events" to get the path of every login item' 2>/dev/null)"; then
    export login_items
    check "OrbStack starts with the login session" \
      'printf "%s" "$login_items" | tr "," "\n" | sed "s/^ *//; s/ *$//" |
       grep -qxF /Applications/OrbStack.app'
  else
    echo "  --    this Mac's login items cannot be read, so whether OrbStack starts"
    echo "        with the session was not checked"
  fi

  check "OrbStack's VM is every core but two" \
    "[ \"\$(orb_value cpu)\" = $want_cpu ]"
  check "docker's context is orbstack" '[ "$(docker context show)" = orbstack ]'
  check "docker talks to a daemon" 'docker info'
  check "a container runs" 'docker run --rm hello-world'
  check "docker compose resolves as a plugin" 'docker compose version'
  check "docker buildx resolves as a plugin"  'docker buildx version'
  check "a two-service compose file comes up and goes down" \
    'docker compose -f "$root/test/compose.yaml" up -d &&
     running="$(docker compose -f "$root/test/compose.yaml" ps -q | grep -c .)";
     docker compose -f "$root/test/compose.yaml" down &&
     [ "$running" = 2 ]'
else
  echo "  --    OrbStack is not running — a first run needs a click at the Mac"
  echo "        itself — so docker itself was not checked"
fi

# Xcode is xcode.sh's, which machine.sh only runs where there is a terminal to
# type an Apple ID at — so on a runner it is the workflow that calls it, and what
# can be asserted is the half that installs nothing. The download itself is
# nobody's to test: it needs an Apple ID and an hour.
xcode_app=""
for candidate in /Applications/Xcode*.app; do
  if [ -d "$candidate" ]; then
    xcode_app="$candidate"
    break
  fi
done

if [ -n "$xcode_app" ]; then
  check "xcode-select points into an Xcode rather than the command line tools" \
    'case "$(xcode-select -p)" in /Applications/Xcode*.app/Contents/Developer) ;; *) exit 1 ;; esac'
  check "xcodebuild answers" 'xcodebuild -version'

  if sudo -n true 2>/dev/null; then
    check "Xcode's licence is accepted" 'sudo -n xcodebuild -license check'
    check "Xcode's first launch is done" 'sudo -n xcodebuild -checkFirstLaunchStatus'
  else
    echo "  --    sudo wants a password, so Xcode's licence was not checked"
  fi
else
  echo "  --    there is no Xcode on this Mac, so xcode.sh's half was not checked"
fi

# What a Mac's hardware supports varies, and an unsupported setting is absent from
# what pmset reports rather than wrong — a virtualised runner has no power supply
# to come back from. Absent is reported rather than asserted, so this says which
# of them the machine it ran on could actually be told.
power_setting() {
  pmset -g custom | awk -v s="$1" '$1 == s { print $2; exit }'
}
export -f power_setting

for pair in autorestart:1 sleep:0 disksleep:0 displaysleep:0; do
  setting="${pair%%:*}"
  want="${pair##*:}"

  if [ -z "$(power_setting "$setting")" ]; then
    echo "  --    pmset does not report $setting on this Mac"
  else
    check "pmset $setting is $want" "[ \"\$(power_setting $setting)\" = $want ]"
  fi
done

# Nothing on this Mac may stop and wait for a click. Four of those settings a
# script can write, and they are asserted. Three need somebody at the screen, and
# they are reported rather than asserted: a suite that failed on them would fail
# on every runner and on every Mac nobody has been at yet, which is not a
# regression in anything this repo did.
check "a crash puts up no dialog" \
  '[ "$(defaults read com.apple.CrashReporter DialogType)" = none ]'
check "the screensaver never starts" \
  '[ "$(defaults -currentHost read com.apple.screensaver idleTime)" = 0 ]'

# Downloaded, never installed: an update that restarts the Mac takes every
# session's worktree state and every running build with it. The two data-file keys
# stay on because they install in place, with no restart and no dialog. The plist
# path rather than the bare domain, which from a user shell is that user's own —
# and the sudo fallback for the case where root created it unreadable.
software_update() {
  local plist=/Library/Preferences/com.apple.SoftwareUpdate

  defaults read "$plist" "$1" 2>/dev/null || sudo -n defaults read "$plist" "$1"
}
export -f software_update

for pair in AutomaticDownload:1 AutomaticallyInstallMacOSUpdates:0 \
            ConfigDataInstall:1 CriticalUpdateInstall:1; do
  key="${pair%%:*}"
  want="${pair##*:}"
  got="$(software_update "$key" 2>/dev/null || echo absent)"

  # The value is named rather than only judged, because the way this goes wrong on
  # a new macOS is a key that is written and then quietly dropped — which is what
  # AutomaticCheckEnabled does on 26, and why it is `softwareupdate --schedule`
  # below rather than a fifth key here.
  check "SoftwareUpdate $key is $want (read $got)" "[ \"$got\" = $want ]"
done

check "macOS checks for updates on its own" \
  'softwareupdate --schedule 2>&1 | grep -qi " on$"'

# The guard on every one of those writes, which is the whole of what makes a
# re-run safe: a second pass has nothing left to change and says nothing. `is now`
# is what each setter prints when it writes. Given no stdin, because a Mac being
# checked by hand has a terminal and unattended.sh would ask it for a password.
if sudo -n true 2>/dev/null; then
  export rerun="$("$root/unattended.sh" </dev/null 2>/dev/null || true)"

  check "a second unattended.sh changes nothing" \
    '! printf "%s\n" "$rerun" | grep -q "is now"'

  # Named rather than only counted, because which setting failed to guard is the
  # whole of what makes this fixable.
  printf '%s\n' "$rerun" | sed -n 's/.*is now.*/        changed again: &/p'
else
  echo "  --    sudo wants a password, so unattended.sh was not re-run"
fi

# The lock itself is nowhere here. It is the Lock Screen pane's alone on macOS 27:
# in no preference domain, and `sysadminctl -screenLock status` answers "delay is
# immediate" with the pane set to Never — so a check on it would be a check on a
# reading known to be wrong. idleTime above is the half that can be asserted.

# Reported either way rather than asserted: nothing but System Settings > General
# > Sharing can turn Screen Sharing on, since that pane is also what registers the
# screen recording rights the sharing agent needs, so a runner and a Mac waiting
# for a click would both fail a check they can do nothing about. The port rather
# than launchd's opinion of the job, because `launchctl enable` clears the
# Disabled flag and leaves the job loaded with nothing listening.
if nc -z -G 1 -w 1 127.0.0.1 5900 >/dev/null 2>&1; then
  echo "  --    Screen Sharing answers VNC on port 5900"
else
  echo "  --    nothing answers VNC on port 5900, and only System Settings >"
  echo "        General > Sharing can change that"
fi

# Asserted rather than reported, unlike the privacy list this replaced: mdutil
# turns indexing off from a script, and `mdutil -s` reads the result back without
# sudo.
check "Spotlight indexing is off" \
  'mdutil -s /System/Volumes/Data | grep -qi "indexing.*disabled"'

# Reading the system domain needs root, and a Mac being provisioned by hand should
# not have this script sitting on a password prompt.
if sudo -n true 2>/dev/null; then
  check "Remote Login is on" \
    'sudo -n launchctl print system/com.openssh.sshd'
else
  echo "  --    sudo wants a password, so Remote Login was not checked"
fi

# harden-ssh.sh holds the drop-in back until there is a key to log in with, so
# which half of this is asserted depends on whether the caller has seeded one yet.
# Both halves matter: the refusal is what keeps a Mac nobody can walk up to
# reachable. The test is the one harden-ssh.sh gates on, not -s, or a file of
# unusable lines sends this down the wrong half.
if ssh-keygen -l -f "$HOME/.ssh/authorized_keys" >/dev/null 2>&1; then
  check "sshd drop-in names this user" \
    'grep -qx "AllowUsers $(id -un)" /etc/ssh/sshd_config.d/10-hardening.conf'
  check "sshd drop-in kept no placeholder" \
    '! grep -q __USER__ /etc/ssh/sshd_config.d/10-hardening.conf'
  check "sshd drop-in disables passwords" \
    'grep -qx "PasswordAuthentication no" /etc/ssh/sshd_config.d/10-hardening.conf'
  check "sshd drop-in belongs to root" \
    '[ "$(stat -f "%Su %Lp" /etc/ssh/sshd_config.d/10-hardening.conf)" = "root 644" ]'
  # macOS reads the config per connection rather than holding it in a running
  # daemon, so a drop-in it cannot parse breaks every login rather than waiting
  # for a restart. Only sshd can be asked, and only as root, so a Mac whose sudo
  # wants a password is told what was skipped — as the reads above are.
  if sudo -n true 2>/dev/null; then
    check "sshd accepts the drop-in" 'sudo -n /usr/sbin/sshd -t'
  else
    echo "  --    sudo wants a password, so the drop-in was not parsed"
  fi
else
  check "no drop-in until there is a key to log in with" \
    '[ ! -f /etc/ssh/sshd_config.d/10-hardening.conf ]'
  check "harden-ssh.sh refuses rather than failing the run, and writes nothing" \
    '"$root/harden-ssh.sh" && [ ! -f /etc/ssh/sshd_config.d/10-hardening.conf ]'
fi

# macOS has shipped the Include since Monterey, and without it the drop-in above
# is a file nothing reads.
check "sshd_config includes the drop-in directory" \
  'grep -qE "^[[:space:]]*Include[[:space:]]+/etc/ssh/sshd_config\.d/" /etc/ssh/sshd_config'

if [ "$failures" -gt 0 ]; then
  echo "  $failures check(s) failed"
  exit 1
fi
