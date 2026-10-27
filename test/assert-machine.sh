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
# claude-root is named rather than matched, being the one script here that is not
# a *.sh: it is installed under a fixed system path and so carries no extension.
check "no hardcoded home directory in the scripts" \
  '! grep -rhoE --include="*.sh" --include="*.plist" --include="mise.toml" \
       --include="claude-root" \
       "/Users/[A-Za-z0-9_.-]+" "$root" |
     grep -q .'

# Homebrew and its packages, which is all the machine half installs.
check "brew is the Apple Silicon prefix" '[ -x /opt/homebrew/bin/brew ]'

# The whole package list at once, which is what one Brewfile buys. --no-upgrade to
# match the install: an outdated formula is not a missing one, and nothing here moves
# a version.
check "the Brewfile's dependencies are satisfied" \
  'brew bundle check --no-upgrade --file="$root/Brewfile"'

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

# The label is asked for rather than spelled out, because Homebrew has renamed it
# once already and names the plist it installs after it.
tailscale_daemon="$(brew services info tailscale --json 2>/dev/null |
  jq -r '.[0].service_name // empty')"
export tailscale_daemon

# `brew services start` installs the plist and `brew services stop` takes it back
# out, so its presence in the boot path is what says the daemon is registered to
# start at boot — and it reads without root, where the system domain does not.
check "brew services registered tailscaled as a system daemon" \
  '[ -n "$tailscale_daemon" ] &&
   [ -f "/Library/LaunchDaemons/$tailscale_daemon.plist" ]'

# The daemon is in the system domain, which needs root to read — and a Mac being
# provisioned by hand should not meet a password prompt inside a test.
if /usr/bin/sudo -n /usr/bin/true 2>/dev/null; then
  check "tailscaled is a loaded system daemon" \
    '/usr/bin/sudo -n /bin/launchctl print "system/$tailscale_daemon"'
else
  echo "  --    sudo wants a password, so tailscaled's daemon was not checked"
fi

# The prefs, which tailscale.sh writes whether or not the node has ever logged in.
# tailscaled gives a read-write connection to members of the admin group on macOS,
# so no root for either of these; the sudo is the fallback for an account outside
# that group, whose connection is read-only and which cannot read prefs at all.
prefs() {
  tailscale debug prefs 2>/dev/null ||
    /usr/bin/sudo -n "$(brew --prefix tailscale)/bin/tailscale" debug prefs 2>/dev/null
}
export -f prefs

check "tailscale serves ssh"          '[ "$(prefs | jq -r .RunSSH)" = true ]'
# An exit node is the two default routes, so an empty list rules out both it and a
# subnet.
check "tailscale advertises no routes" \
  '[ "$(prefs | jq -r "(.AdvertiseRoutes // []) | length")" = 0 ]'

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
      'case ",$(printf "%s" "$login_items" | tr -d " ")," in
       *",/Applications/OrbStack.app,"*) ;; *) exit 1 ;; esac'
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

check "Rosetta runs x86_64 binaries" '/usr/bin/arch -x86_64 /usr/bin/true'

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

  if /usr/bin/sudo -n /usr/bin/true 2>/dev/null; then
    check "Xcode's licence is accepted" \
      '/usr/bin/sudo -n /usr/bin/xcodebuild -license check'
    check "Xcode's first launch is done" \
      '/usr/bin/sudo -n /usr/bin/xcodebuild -checkFirstLaunchStatus'
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

restart=autorestart
[ -n "$(power_setting autorestartatconnect)" ] && restart=autorestartatconnect

for pair in $restart:1 sleep:0 disksleep:0 displaysleep:0; do
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

  defaults read "$plist" "$1" 2>/dev/null ||
    /usr/bin/sudo -n /usr/bin/defaults read "$plist" "$1"
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
if /usr/bin/sudo -n /usr/bin/true 2>/dev/null; then
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

check "media analysis is off" \
  'launchctl print-disabled "gui/$(id -u)" | grep -q "\"com.apple.mediaanalysisd\" => disabled"'

# Reading the system domain needs root, and a Mac being provisioned by hand should
# not have this script sitting on a password prompt.
if /usr/bin/sudo -n /usr/bin/true 2>/dev/null; then
  check "Remote Login is on" \
    '/usr/bin/sudo -n /bin/launchctl print system/com.openssh.sshd'
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
  if /usr/bin/sudo -n /usr/bin/true 2>/dev/null; then
    check "sshd accepts the drop-in" '/usr/bin/sudo -n /usr/sbin/sshd -t'
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

# The root helper, which is the whole of the sudo a session on this Mac has. Two
# root-owned copies and nothing else, and both halves of it matter: that the
# helper runs with no password, and that sudo still refuses everything outside it.
helper=/usr/local/libexec/claude-root
helper_source="$root/system/libexec/claude-root"
sudoers_file=/etc/sudoers.d/claude-root
export helper helper_source sudoers_file

# The one file the installer renders is compared against the same rendering rather
# than against the source, which carries the account name as a placeholder because
# nothing here is written for one account.
rendered="$(mktemp -d)"
trap 'rm -rf "$rendered"' EXIT
export rendered

sed "s/__USER__/$(id -un)/g" "$root/system/sudoers/claude-root" \
  >"$rendered/sudoers"

# A rule sudo cannot parse takes sudo away from Tim as well as from every session,
# so what the installer checks before it writes anything is checked here against
# the same rendering. visudo -c reads a file as an ordinary user.
check "sudo parses the rendering of this repo's sudoers rule" \
  'visudo -cf "$rendered/sudoers"'

# A copy at a path only root can write, never a link into the checkout: sudo
# ignores a drop-in that is not a root-owned 0440 regular file, and the helper is
# what the rule names.
check "the root helper is a copy rather than a link into the checkout" \
  '[ -f "$helper" ] && [ ! -L "$helper" ]'
check "the root helper is this repo's, byte for byte" \
  'cmp -s "$helper_source" "$helper"'
check "the root helper belongs to root" \
  '[ "$(stat -f "%Su %Lp" "$helper")" = "root 755" ]'

# stat reads a 0440 file the account cannot open, so the owner and the mode are
# asserted here and the contents below, where there is sudo to read them with.
check "the sudoers rule belongs to root and only root may read it" \
  '[ "$(stat -f "%Su %Lp" "$sudoers_file")" = "root 440" ]'
check "the sudoers rule is a copy rather than a link" \
  '[ -f "$sudoers_file" ] && [ ! -L "$sudoers_file" ]'

# No action may touch a path the account can write, which is the rule a log-capping
# action here broke: the account replaces the file with a symlink and a root copy,
# chown or truncate follows it wherever it points. Read off the helper rather than
# only written in its header, so an action that reaches into one cannot be added
# without this failing.
#
# A home directory is one of them and the temporary directories are the rest: any
# account can write /tmp, and $TMPDIR is per account already. One pattern covers
# the three spellings, /private/tmp and /var/tmp both having /tmp inside them. A
# bare ~ counts as well, which is why nothing in the helper's own output spells a
# path that way.
#
# reinstall_portless is cut out before the read, being the one named exception:
# portless's daemon runs the account's own node and JavaScript as root by
# portless's own design, so reinstalling it reaches paths the account writes and
# opens nothing that is not open already. Everything outside that one function is
# held to the rule.
check "no action but the portless reinstall reaches into a path the account can write" \
  '! sed "/^reinstall_portless() {\$/,/^}\$/d" "$helper_source" |
       grep -qE "NFSHomeDirectory|/Library/Logs|\\\$HOME|\\\$TMPDIR|/tmp|~/"'

# The TLDs the helper hands portless are the ones the account's shell tells every
# client about, and the proxy serves what it was installed with — so a reinstall
# with a list of its own would take a dev URL off the Mac.
check "the helper's proxy TLDs are the ones home/.zshenv exports" \
  'tlds="$(sed -n "s/^export PORTLESS_TLD=//p" "$root/home/.zshenv" | tr , " ")" &&
   [ -n "$tlds" ] &&
   "$helper_source" --list | grep -q "Proxy TLDs: $tlds"'

# What the account can actually run, which is the whole point of the thing. -k so
# the answer comes from the sudoers rule rather than from a password typed a
# minute ago: it makes sudo ignore the cached credentials for the one call without
# clearing them.
check "sudo runs the helper with no password" \
  '/usr/bin/sudo -n -k "$helper" --list'
# The helper's own refusal rather than only a non-zero exit, which sudo would
# give for having refused to run it at all.
check "sudo refuses a daemon the helper does not allow" \
  '/usr/bin/sudo -n -k "$helper" restart-daemon not-on-the-list 2>&1 |
     grep -q "not an allowed daemon"'
# The rule as sudo itself reads it, which is the one reading that cannot be out of
# step with what the account can do. -l needs no password where the account has a
# passwordless rule, which is exactly the case here. Matched in two greps rather
# than one pattern, because the secrets scan every commit on this Mac goes through
# reads a path after the keyword as a password being set.
check "sudo lists the helper as the account's passwordless command" \
  '/usr/bin/sudo -n -l 2>/dev/null | grep -F "$helper" | grep -q NOPASSWD'

# And the half that says it is narrow rather than blanket. A machine whose sudoers
# already grants the account everything cannot answer this — a CI runner arrives
# that way, which is also what lets the installer run there at all — so what is
# asked first is whether some other command runs too, and the answer decides
# whether this is a check or a line saying it could not be made.
if /usr/bin/sudo -n -k /usr/bin/true 2>/dev/null; then
  echo "  --    this account's sudoers grants more than the helper, as a CI"
  echo "        runner's does, so the refusal of everything outside it was not"
  echo "        checked"
else
  check "sudo runs nothing outside the helper" \
  '! /usr/bin/sudo -n -k /bin/ls /'
fi

if /usr/bin/sudo -n /usr/bin/true 2>/dev/null; then
  check "the sudoers rule is the rendering of this repo's" \
    '/usr/bin/sudo -n /usr/bin/cmp -s "$rendered/sudoers" "$sudoers_file"'
  check "sudo parses the sudoers rule" \
    '/usr/bin/sudo -n /usr/sbin/visudo -cf "$sudoers_file"'
else
  echo "  --    sudo wants a password, so the sudoers rule's contents were not"
  echo "        read back"
fi

# The helper's own argument handling, against the copy in the checkout and with no
# sudo anywhere near it. Every refusal below happens before anything privileged is
# reached, which is what makes it testable at all — and CLAUDE_ROOT_DRY_RUN, which
# stands in for root, is deliberately not a way past any of it.
check "the helper is executable in the checkout" '[ -x "$helper_source" ]'
check "the helper lists the actions and the daemons it allows" \
  '"$helper_source" --list | grep -q "restart-daemon" &&
   "$helper_source" --list | grep -q "reinstall-portless" &&
   "$helper_source" --list | grep -q "install-apple-updates" &&
   "$helper_source" --list | grep -q "com.apple.dasd" &&
   "$helper_source" --list | grep -q "sh.brew.tailscale"'

# Nothing else records a run of the one passwordless rule, sudo's own log of an
# allowed run reaching neither /var/log/system.log nor the unified log here, so a
# privileged action that writes no line leaves none at all.
check "every action records itself in the system log before it acts" \
  'grep -q "log_action \"restart-daemon " "$helper_source" &&
   grep -q "log_action flush-dns" "$helper_source" &&
   grep -q "log_action reinstall-portless" "$helper_source" &&
   grep -q "log_action install-apple-updates" "$helper_source"'
check "the sudoers rule leaves that logging to the helper" \
  '! grep -qE "^[^#]*log_allowed" "$rendered/sudoers"'
check "the helper refuses no action at all" '! "$helper_source"'
check "the helper refuses an action that is not on the list" \
  '! "$helper_source" uname'
check "the helper refuses a daemon that is not on the list" \
  '! "$helper_source" restart-daemon not-on-the-list'
check "the helper matches a daemon by string rather than as a pattern" \
  '! "$helper_source" restart-daemon "das*" &&
   ! "$helper_source" restart-daemon "*"'
check "the helper refuses restart-daemon with no name" \
  '! "$helper_source" restart-daemon'
check "the helper refuses an argument the action does not take" \
  '! "$helper_source" restart-daemon dasd extra &&
   ! "$helper_source" flush-dns extra &&
   ! "$helper_source" reinstall-portless extra &&
   ! "$helper_source" install-apple-updates extra &&
   ! "$helper_source" --list extra'
# The action a session was told about and this file no longer has, which is worth
# a case of its own: it has to be refused rather than quietly do nothing.
check "the helper refuses the log action it used to have" \
  '! "$helper_source" rotate-logs'
check "the helper reads -- as the end of its options" \
  '"$helper_source" -- --list | grep -q "restart-daemon"'
check "a dry run is no way past the allowlist" \
  '! CLAUDE_ROOT_DRY_RUN=1 "$helper_source" restart-daemon not-on-the-list &&
   ! CLAUDE_ROOT_DRY_RUN=1 "$helper_source" uname'
check "an allowed action refuses rather than half-doing it without root" \
  '! "$helper_source" flush-dns && ! "$helper_source" reinstall-portless &&
   ! "$helper_source" install-apple-updates'
check "a dry run of an allowed action says what it would do and does nothing" \
  'CLAUDE_ROOT_DRY_RUN=1 "$helper_source" restart-daemon dasd | grep -q "dry run" &&
   CLAUDE_ROOT_DRY_RUN=1 "$helper_source" reinstall-portless | grep -q "dry run" &&
   CLAUDE_ROOT_DRY_RUN=1 "$helper_source" install-apple-updates | grep -q "dry run"'

# An update that restarts or shuts the Mac down ends every session running on it, so
# the Apple install passes no --restart and leaves that kind to Tim: which they are is
# read off softwareupdate's own listing, the same place the labels come from.
check "the Apple install never restarts the Mac" \
  '! grep -qE "^[^#]*--restart" "$helper_source"'

# softwareupdate is a network client with no deadline of its own, and the nightly
# update waits on this one holding a lock. A caller's own timeout is no answer: it
# kills the sudo and leaves softwareupdate running as root.
check "every softwareupdate call the helper makes has a deadline" \
  '! grep -E "^[^#]*softwareupdate --" "$helper_source" | grep -qv "bounded "'

# The helper runs on a PATH of its own rather than the caller's, since sudo here
# sets no secure_path and the account's PATH leads with version-managed shims. A
# PATH of nothing is the observable half.
check "the helper does not run on the PATH it was handed" \
  'PATH=/nonexistent "$helper_source" --list >/dev/null'

if [ "$failures" -gt 0 ]; then
  echo "  $failures check(s) failed"
  exit 1
fi
