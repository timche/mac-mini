#!/bin/bash

# What makes a Mac with nobody in front of it keep going: it comes back on its
# own after power loss, it never sleeps, it never puts a dialog up that something
# waits behind, it never restarts itself for an update, and it has a GUI login
# session to come back into.
#
# The parallel Claude Code sessions are what this is for. Each one builds, runs
# Electron apps that crash, and takes screenshots to show a change working, and
# every one of those is a thing that stops dead at a modal or at a locked screen.
#
# Three of them cannot be arranged from here at all, so this says so rather than
# pretending. Auto-login needs the account password written to /etc/kcpassword,
# obfuscated rather than encrypted. Screen Sharing needs a click that macOS will
# not take from a script. The screen lock is a pane setting this macOS neither
# reads back nor writes — see the README, which says where each one lives and
# what it is for.
#
# Not fatal, any of it. A Mac that sleeps, locks or comes up at the login window
# is still a Mac.
#
# Safe to re-run: every write is guarded on a read of what is already there.

set -euo pipefail

if [ "$(uname -s)" != Darwin ]; then
  echo "unattended.sh is macOS only" >&2
  exit 1
fi

# `defaults read` exits non-zero for a key that is not there, which reads the
# same as one whose value differs — both mean the write has to happen. Guarded
# rather than repeated because every write wakes cfprefsd and notifies whatever
# is watching the domain, and this script runs again on every pull.
#
# -bool writes `true` and reads back `1`, so a guard comparing against what was
# written would never match and would write every time.
as_read() {
  case "$1 $2" in
    "-bool true") echo 1 ;;
    "-bool false") echo 0 ;;
    *) echo "$2" ;;
  esac
}

# Power

# One at a time rather than in a single call: what a Mac's hardware supports
# varies, an unsupported setting is simply absent from what pmset reports back,
# and a call that took four settings would be one warning about which of them
# did not land.
power_setting() {
  pmset -g custom | awk -v s="$1" '$1 == s { print $2; exit }'
}

set_power() {
  local setting="$1" value="$2"

  if [ "$(power_setting "$setting")" = "$value" ]; then
    return 0
  fi

  sudo pmset -a "$setting" "$value" >/dev/null 2>&1 || true

  # Read back rather than trust the exit status, which is 0 for a setting this
  # hardware does not have — pmset takes it and then leaves it out of what it
  # reports. A virtualised Mac has no power supply to come back from, and without
  # this it would be told autorestart landed on every single run.
  if [ "$(power_setting "$setting")" != "$value" ]; then
    echo "could not set $setting to $value — this Mac may not support it" >&2
    return 0
  fi

  echo "pmset $setting is now $value"
}

# Power loss on a machine nobody can walk up to is otherwise a machine that is
# gone until somebody does.
set_power autorestart 1

# Sleep is the other way a Mac stops answering SSH without anything being wrong
# with it. disksleep as well as sleep, because a spun-down disk is what a woken
# machine waits on.
set_power sleep 0
set_power disksleep 0

# displaysleep too, headless though this Mac is: a capture taken while the
# display is asleep comes back a black frame, which reads exactly like a missing
# Screen Recording grant, and showing a change working is what a session takes
# screenshots for.
set_power displaysleep 0

# Crash dialogs

# A crash otherwise leaves Problem Reporter's window on the screen and the
# crashed process waiting behind it, and the Electron apps under development here
# crash as part of the work. `none` is the value that puts up nothing at all —
# the report still lands in ~/Library/Logs/DiagnosticReports, which is where it
# is any use to anybody.
#
# Per user, not /Library/Preferences: the two ReportCrash processes read the
# domain of whoever owns the session the crash happened in, and a root crash has
# no session to put a window in. Not ByHost either — this is an ordinary
# preference domain, unlike the screensaver's below.
set_user_default() {
  local domain="$1" key="$2" type="$3" value="$4"

  if [ "$(defaults read "$domain" "$key" 2>/dev/null)" = "$(as_read "$type" "$value")" ]; then
    return 0
  fi

  defaults write "$domain" "$key" "$type" "$value"
  echo "$domain $key is now $value"
}

set_user_default com.apple.CrashReporter DialogType -string none

# The screen lock

# A locked session keeps going — sshd answers, launchd keeps its jobs, builds
# finish. What it stops is everything that has to look at the screen:
# `screencapture` over SSH hands back a black frame from a locked session, and so
# does every recording, which is how a session shows that a change works.
#
# Two separate settings, and only one of them can be written. idleTime is the
# screensaver's, still in the ByHost domain even though Sonoma moved the engine
# that reads it into a sandbox — 0 is never.
#
# The lock itself is only the Lock Screen pane's, on macOS 27. It lives in no
# preference domain — com.apple.screensaver has no askForPassword any more, and
# ByHost holds idleTime and nothing else — `sysadminctl -screenLock status` still
# answers "delay is immediate" with the pane set to Never, and
# `sudo sysadminctl -screenLock off -password -` over SSH fails with
# MKBDeviceSetGracePeriod error -17 and changes nothing. So it is named here and
# neither written nor read back.
set_host_default() {
  local domain="$1" key="$2" type="$3" value="$4"

  if [ "$(defaults -currentHost read "$domain" "$key" 2>/dev/null)" = "$(as_read "$type" "$value")" ]; then
    return 0
  fi

  defaults -currentHost write "$domain" "$key" "$type" "$value"
  echo "$domain $key is now $value"

  # The engine holds what it read when the session started, so the write alone
  # does not reach a session that is already up.
  killall -HUP cfprefsd 2>/dev/null || true
}

set_host_default com.apple.screensaver idleTime -int 0

echo
echo "Set System Settings > Lock Screen > \"Require password after screen saver"
echo "begins or display is turned off\" to Never, over Screen Sharing. Nothing"
echo "here can set it or tell you whether it is already set."

# macOS updates

# Downloaded, never installed. An update that installs itself restarts the Mac,
# and a restart takes every session's worktree state, every running build and
# every browser with it — and then waits at the login window if auto-login is
# off. Downloaded means the install is a decision somebody makes, on a machine
# they have looked at, and it takes minutes rather than an hour of downloading.
#
# ConfigDataInstall and CriticalUpdateInstall stay on. Those are XProtect, its
# remediator and the security data files: they install in place, without a
# restart and without a dialog, which is the one kind of automatic update that
# costs nothing here.
#
# Rapid Security Response — Background Security Improvement, as of 2026 — is not
# among them. It moved to declarative device management
# (com.apple.configuration.softwareupdate.settings), which needs a supervised
# MDM enrollment, so there is no key here to set it with either way. It is on by
# default and it can restart the Mac; nothing in this repo can change that.
#
# In /Library/Preferences, because these govern the machine rather than the
# account: a bare com.apple.SoftwareUpdate from a user shell is that user's own
# domain and nothing reads it. The MDM profile payload carrying the same keys is
# deprecated in macOS 26 and gone in 27, but that is how a fleet locks the
# setting down — the local preferences these toggles read and write are a
# different path, and this Mac is enrolled in nothing.
#
# Read back through sudo as well as written through it: `defaults` run by root
# creates a plist the account cannot read, so a guard that read it as the user
# would miss on a Mac where the file did not exist before — and then write on
# every run.
set_machine_default() {
  local domain="$1" key="$2" type="$3" value="$4" plist="/Library/Preferences/$1" current

  current="$(sudo defaults read "$plist" "$key" 2>/dev/null || true)"

  if [ "$current" = "$(as_read "$type" "$value")" ]; then
    return 0
  fi

  if ! sudo defaults write "$plist" "$key" "$type" "$value"; then
    echo "could not set $key in $plist" >&2
    return 0
  fi

  echo "$domain $key is now $value"
}

set_machine_default com.apple.SoftwareUpdate AutomaticDownload -bool true
set_machine_default com.apple.SoftwareUpdate AutomaticallyInstallMacOSUpdates -bool false
set_machine_default com.apple.SoftwareUpdate ConfigDataInstall -bool true
set_machine_default com.apple.SoftwareUpdate CriticalUpdateInstall -bool true

# Checking for updates at all is the one of the five that is no longer a key
# here. macOS 26 takes an AutomaticCheckEnabled written to that plist and drops
# it — the key is simply gone from the file afterwards, by every way of reading
# it — so `softwareupdate --schedule` is what is left, and it is the documented
# switch anyway. Without it nothing is ever downloaded and AutomaticDownload
# above has nothing to do.
if softwareupdate --schedule 2>&1 | grep -qi ' on$'; then
  echo "macOS checks for updates on its own"
elif sudo softwareupdate --schedule on >/dev/null 2>&1; then
  echo "macOS now checks for updates on its own"
else
  echo "could not turn automatic update checks on" >&2
fi

# The login session

auto_login_user="$(
  defaults read /Library/Preferences/com.apple.loginwindow autoLoginUser 2>/dev/null || true
)"
user="$(id -un)"

if [ "$auto_login_user" = "$user" ]; then
  echo "auto-login is on for $user"
else
  echo
  if [ -z "$auto_login_user" ]; then
    echo "warning: auto-login is off. After a restart this Mac sits at the login" >&2
    echo "window, where there is no gui/$(id -u) domain — so hachiko and the" >&2
    echo "ssh-agent that holds the signing key are not running, and nothing says" >&2
    echo "so beyond commits failing to sign." >&2
  else
    echo "warning: auto-login logs in $auto_login_user rather than $user, whose" >&2
    echo "session is the one the agents are loaded into." >&2
  fi
  echo >&2
  echo "Turn it on in System Settings > Users & Groups > Automatic login, which" >&2
  echo "needs FileVault off. This script will not: it means writing the account" >&2
  echo "password to /etc/kcpassword, obfuscated rather than encrypted." >&2
fi

# Screen Sharing

# Checked, not turned on. `launchctl enable system/com.apple.screensharing`
# clears the Disabled flag the way remote-login.sh does for sshd, but for this
# job that is only half of it: the TCC rights the screen sharing agent needs are
# registered by the Sharing pane itself, so a Mac enabled from a script ends up
# with the job loaded, the toggle still reading off, and nothing answering. So
# the port is what is looked at rather than launchd's opinion of the job.
#
# It matters more than the other two: Screen Sharing is how the privacy
# permissions in the README get granted, and those are what let a session take a
# screenshot at all.
if nc -z -G 1 -w 1 127.0.0.1 5900 >/dev/null 2>&1; then
  echo "Screen Sharing is on"
else
  echo
  echo "warning: nothing is answering VNC on this Mac, so Screen Sharing is off." >&2
  echo "Turn it on in System Settings > General > Sharing. A script cannot: the" >&2
  echo "Sharing pane is what registers the screen recording rights the agent" >&2
  echo "needs, and launchctl alone leaves the job loaded and nothing listening." >&2
  echo >&2
  echo "It is the way in for everything else that needs a click, including the" >&2
  echo "privacy permissions the README lists." >&2
fi

# Spotlight

# Off altogether, rather than a privacy list holding the folders the sessions work
# in. A checkout is a few thousand files; the same checkout with node_modules in
# it is a few hundred thousand, and every worktree is another copy — so mds spends
# a core walking files nobody on a headless Mac will ever search for by name,
# again after every install.
#
# The privacy list was the old approach and needed a click: only the Spotlight
# pane hands the list to the running mds, and a write to the Exclusions array
# underneath it is ignored. `mdutil -i off` is scriptable, and `mdutil -s` reads
# the result back without sudo, so this is a step rather than a report.
#
# What goes with it: ⌘Space and Finder search at the Mac itself, `mdfind`, and the
# dSYM lookup that symbolicates a native crash report — none of which anybody
# reaches a headless Mac for.
#
# Matched loosely because mdutil says "Indexing disabled." for a volume it has a
# store for and "Indexing and searching disabled." for one it does not.
indexing_off() {
  mdutil -s /System/Volumes/Data 2>/dev/null | grep -qi 'indexing.*disabled'
}

if indexing_off; then
  echo "Spotlight indexing is off"
elif sudo -n true 2>/dev/null || [ -t 0 ]; then
  sudo mdutil -a -i off >/dev/null 2>&1 || true

  if indexing_off; then
    echo "Spotlight indexing is now off"
  else
    echo "could not turn Spotlight indexing off" >&2
  fi
else
  echo
  echo "warning: Spotlight is indexing, which costs a core walking every" >&2
  echo "worktree's node_modules. sudo wants a password and there is no terminal" >&2
  echo "to type one at, so run:" >&2
  echo >&2
  echo "  sudo mdutil -a -i off" >&2
fi
