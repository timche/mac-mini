#!/bin/bash

# Install the tooling and link this repo into $HOME and ~/.claude.
#
# mac-mini-setup builds the Mac and gets as far as an authenticated gh — the
# earliest anything here can be reached, the repo being private. So
# this half owns everything that is personal rather than structural — the
# shell, the prompt, the runtimes, and the Claude Code configuration.
#
# Safe to re-run. Tools already present are skipped, existing symlinks are
# replaced, and anything real found at a target is moved aside to
# <name>.backup first.

set -euo pipefail

if [ "$(uname -s)" != Darwin ]; then
  echo "mac-mini-dotfiles is for a Mac; this is $(uname -s)." >&2
  exit 1
fi

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
user="$(id -un)"

# Tooling

# A Mac that has just run mac-mini-setup has Homebrew installed and nothing on
# PATH pointing at it, since the .zprofile that would is one of the links below.
if [ -x /opt/homebrew/bin/brew ]; then
  export PATH="/opt/homebrew/bin:/opt/homebrew/sbin:$PATH"
fi

# Fatal rather than a warning, because the packages below are brew's: a shell
# would come out of here without its syntax highlighting and nothing would say
# why.
if ! command -v brew >/dev/null 2>&1; then
  echo "Homebrew is missing — run mac-mini-setup first, which installs it along" \
       "with gh, git and tailscale, then re-run this" >&2
  exit 1
fi

# Every Homebrew package this repo installs, boswell included: one declarative
# file, and `brew bundle check` in test/assert.sh reads the same one back.
#
# --no-upgrade so that a re-run installs what is missing and moves no version,
# which is the promise `mise install --locked` makes for the tool list beside it.
# Without it every install would be an unasked-for `brew upgrade` of the whole
# list. `brew bundle upgrade` is the deliberate version.
brew bundle --no-upgrade --file="$repo/Brewfile"

export PATH="$HOME/.local/share/mise/shims:$HOME/.local/bin:$PATH"

# Both rc files activate whichever mise is on PATH, so a login shell is broken
# until one is there. mise's own installer into ~/.local/bin, which is the install
# its authors document and updates itself with `mise self-update`; Homebrew's
# formula is the same tool a fortnight behind, and mise joins Claude Code as the
# exception to a CLI tool being Homebrew's for the same reason — the upstream
# installer is the supported route.
#
# Asked of that path rather than of PATH, because a mise anywhere else is not the
# one the rc files activate: both put ~/.local/bin ahead of Homebrew's prefix, so
# a formula of the same name cannot answer first.
if [ ! -x "$HOME/.local/bin/mise" ]; then
  curl -fsSL https://mise.run | sh
fi

# Anthropic's installer rather than the tool list: Claude Code replaces its own
# binary in the background, so a pinned version is stale within the day and mise
# would spend every install putting the pin back. Guarded because the installer
# downloads unconditionally, and the background updater is what keeps it current
# once it is there.
if [ ! -x "$HOME/.local/bin/claude" ]; then
  curl -fsSL https://claude.ai/install.sh | bash
fi

# The machine

boswell_label=io.github.timche.boswell
plist="$HOME/Library/LaunchAgents/$boswell_label.plist"

# boswell's plist is a symlink into this checkout, so comparing the target across
# the bootstrap below says nothing: what the link points at is a file a pull
# changed before this script ever ran. A copy of the definition the daemon was
# last loaded from is kept instead, and compared against. Beside the Dock marker
# rather than in ~/Library/LaunchAgents, which launchd reads at login and is for
# plists.
state="${XDG_STATE_HOME:-$HOME/.local/state}/mac-mini-dotfiles"
boswell_loaded="$state/$(basename "$plist").loaded"

# Written only once the daemon is running the definition it is a copy of, so a load
# that failed leaves the next run to try again rather than to skip the reload.
record_boswell_loaded() {
  mkdir -p "$state" && cp "$plist" "$boswell_loaded"
}

gc_label=io.github.timche.worktree-gc
gc_plist="$HOME/Library/LaunchAgents/$gc_label.plist"

# worktree-gc's plist is the one still rendered rather than linked, so the bootstrap
# rewrites the file itself and there is nothing left to compare against afterwards.
gc_plist_before="$(mktemp)"
trap 'rm -f "$gc_plist_before"' EXIT

if [ -f "$gc_plist" ]; then
  cp "$gc_plist" "$gc_plist_before"
fi

# Finder reads its preferences once at launch, and mise deliberately restarts no
# application — so the bootstrap below can leave Finder showing the old thing
# with the new value already stored. Taken aside first for the same reason the
# plists are: afterwards there is nothing to compare against.
# The whole domain rather than the four keys, so this cannot drift out of step
# with mise.toml's list, plus the one Finder preference that lives in the global
# domain.
# Each line ends in a `|| true` because an unset key is a `defaults read` that
# fails, and this runs under set -e inside a command substitution, where that
# would take the whole install down before the bootstrap it is here to watch.
finder_prefs() {
  defaults export com.apple.finder - 2>/dev/null || true
  defaults read NSGlobalDomain AppleShowAllExtensions 2>/dev/null || true
}

finder_prefs_before="$(finder_prefs)"

# mise.toml declares the oh-my-zsh clone, every symlink and the macOS
# preferences; this applies them in that order. --force-dotfiles because this
# repo replaces a real file found at a link target wholesale rather than merging
# with it.
mise trust "$repo"
(cd "$repo" && mise bootstrap --yes --force-dotfiles)

# After the link above, because the tool list mise installs from is that file.
# --locked so a rebuilt Mac gets the versions mise.lock resolved rather than
# whatever latest means on the day, which is the same promise --no-upgrade makes
# for the Brewfile.
mise install --locked

# portless signs every https://<name>.localhost with a certificate authority of
# its own, and a browser only believes it once that CA is in the system trust
# store. portless offers to do this on its first run, which is a prompt for a
# sudo password in whichever session happens to start a dev server first — here
# instead, while a provision still has sudo.
#
# Asked of the trust store rather than of portless: `portless doctor` reports CA
# trust in prose with no machine-readable form, while verify-cert answers for the
# certificate portless generated and needs no sudo to do it. A missing CA means
# portless has never run at all, and `portless trust` generates one as it trusts
# it.
if command -v portless >/dev/null 2>&1; then
  portless_ca="$HOME/.portless/ca.pem"

  if [ -f "$portless_ca" ] && security verify-cert -c "$portless_ca" >/dev/null 2>&1; then
    echo "portless's CA is already trusted"
  # Not attempted over SSH, because it cannot succeed there and trying is worse than
  # saying so: the sudo prompt lands on this terminal, but the change to the system
  # trust store also puts a confirmation dialog on the Mac's own screen, and nobody
  # at the far end of an SSH session can click it. So the one line to run is printed
  # for whoever next reaches the Mac over Screen Sharing.
  elif [ -n "${SSH_CONNECTION:-}" ]; then
    echo "portless's CA is not trusted, and trusting it needs a confirmation on" \
         "the Mac's own screen. Over Screen Sharing, in Terminal, run:" \
         "portless trust" >&2
  # Never fatal: an untrusted CA costs a browser warning on a dev URL, and the
  # shell this script is really here to install has nothing to do with it. From
  # /dev/null so that a sudo which wants a password fails on the spot instead of
  # holding an unattended provision open on a prompt nobody is there to answer.
  elif portless trust </dev/null; then
    echo "trusted portless's CA"
  else
    echo "could not trust portless's CA — https://<name>.localhost will warn" \
         "in a browser until it is; run: portless trust" >&2
  fi

  # Port 443 on 127.0.0.1 is root's on macOS, and a session has no terminal to
  # sudo from, so without this daemon the first dev server a session starts fails
  # outright. It records the absolute paths of the node and the portless that
  # installed it, both under mise's versioned install directories, so a `mise up`
  # of either leaves launchd pointing at a file that is gone.
  # From zsh, because .zshenv is where the list is and mac-mini-setup runs this
  # from a bash that never read it.
  portless_tlds="$(zsh -c 'print -r -- $PORTLESS_TLD' 2>/dev/null)"
  portless_tlds="${portless_tlds:-localhost}"
  portless_install="sudo portless service install --tld ${portless_tlds//,/ --tld }"
  portless_daemon=/Library/LaunchDaemons/sh.portless.proxy.plist
  if [ ! -f "$portless_daemon" ]; then
    echo "portless's proxy is not installed as a daemon, so dev servers cannot" \
         "take port 443; run: $portless_install" >&2
  else
    for i in 0 1; do
      target="$(plutil -extract "ProgramArguments.$i" raw "$portless_daemon" 2>/dev/null)"
      if [ ! -e "$target" ]; then
        echo "portless's daemon runs $target, which is gone since a mise" \
             "upgrade; run: $portless_install" >&2
        break
      fi
    done
  fi
fi

# A worktree's dev server reaches Tim's MacBook as https://<branch>.<app>.<tld>
# for the second TLD in PORTLESS_TLD, whose wildcard DNS record points at this
# Mac's tailnet address. Tailscale Serve hands the tailnet's port 443 to portless
# as raw TCP, so portless keeps TLS and routes by hostname, and each app keeps a
# cookie jar of its own — which portless's own --tailscale gives up by putting
# every app on this node's one name and a port each. Only when the Mac is on a
# tailnet, and only when the forward differs, since serve rewrites its config on
# every call.
if tailscale status >/dev/null 2>&1; then
  if tailscale serve status --json 2>/dev/null |
    jq -e '.TCP["443"].TCPForward == "127.0.0.1:443"' >/dev/null; then
    echo "the tailnet's port 443 already reaches portless"
  elif tailscale serve --bg --tcp 443 tcp://127.0.0.1:443 >/dev/null; then
    echo "forwarded the tailnet's port 443 to portless"
  else
    echo "could not forward the tailnet's port 443 to portless" >&2
  fi
fi

# Tim's preferences
#
# mise.toml holds the plain user defaults and the bootstrap above has already
# written them. Everything here is what that section cannot express, for a reason
# mise gives in each case: a system domain needs sudo and is out of scope there, a
# value holding the home directory cannot be written because those values are not
# templated, an empty Dock has to happen once rather than on every bootstrap, a
# folder flag is not a preference at all, and mise restarts no application while a
# post-defaults hook would restart one on every run whether anything changed or
# not.
#
# None of it is fatal. A preference that will not write costs a tap or a Finder
# sidebar; the shell this script is really here to install has
# nothing to do with any of them.
finder_changed=false

if [ "$(finder_prefs)" != "$finder_prefs_before" ]; then
  finder_changed=true
fi

# Not mise's, because the value has to be an absolute path: neither mise nor
# the screenshot service expands a `~`, and mise does not template a defaults
# value either — so the only way to say this in mise.toml would be to spell the
# account name out, which nothing in this repo does.
screenshots="$HOME/Pictures"
if [ "$(defaults read com.apple.screencapture location 2>/dev/null)" != "$screenshots" ]; then
  if defaults write com.apple.screencapture location -string "$screenshots"; then
    # Both, because which one holds the stale copy has moved: every guide still
    # says SystemUIServer, but the process found caching it since Monterey is
    # screencaptureui, the resident Screenshot toolbar. Neither being up is
    # fine — killall says so and is ignored.
    killall SystemUIServer screencaptureui 2>/dev/null || true
    echo "screenshots now save to $screenshots"
  else
    echo "could not set the screenshot location — screenshots keep landing on" \
         "the Desktop" >&2
  fi
fi

# ~/Library is hidden by a flag on the folder rather than by a preference, so
# there is nothing for mise to write. Asked of the flag rather than run blind,
# because chflags always succeeds and a restart below is owed only to a change.
if [ -n "$(find "$HOME/Library" -maxdepth 0 -flags +hidden 2>/dev/null)" ]; then
  if chflags nohidden "$HOME/Library"; then
    finder_changed=true
    echo "$HOME/Library is visible in Finder"
  else
    echo "could not unhide ~/Library — run: chflags nohidden ~/Library" >&2
  fi
fi

# A Finder info xattr on the folder hides it again whatever the flag says, and
# a Mac restored from a backup can carry one. Only when it is there, so a
# machine without one is not told about it.
if xattr "$HOME/Library" 2>/dev/null | grep -qx com.apple.FinderInfo; then
  if xattr -d com.apple.FinderInfo "$HOME/Library"; then
    finder_changed=true
  fi
fi

# Once, not every run. mise could declare `apps = []` and it would own
# persistent-apps from then on, wiping an icon Tim pinned the moment the next
# install ran — and what he asked for is the factory set gone, not a Dock that
# refuses to hold anything. So the marker is what says it has been done, and
# nothing looks at persistent-apps again afterwards.
dock_marker="$state/dock-emptied"
if [ ! -e "$dock_marker" ]; then
  if mkdir -p "$state" &&
     defaults write com.apple.dock persistent-apps -array &&
     : > "$dock_marker"; then
    killall Dock 2>/dev/null || true
    echo "emptied the Dock of the icons macOS ships with — anything pinned from" \
         "now on stays put"
  else
    echo "could not empty the Dock — run: defaults write com.apple.dock" \
         "persistent-apps -array && killall Dock" >&2
  fi
fi

if [ "$finder_changed" = true ]; then
  killall Finder 2>/dev/null || true
  echo "restarted Finder, which is what makes the changed preferences show"
fi

# Not [bootstrap.user], which runs chsh as the user: that asks PAM for the
# account password, so it cannot run unattended. sudo needs no password during a
# provision, and the switch belongs after the bootstrap above either way — zsh
# with no startup file drops the next login into zsh-newuser-install, and the
# .zshrc that saves it from the wizard is one of the links it just made.
zsh_path="$(command -v zsh || true)"

# Read rather than assumed, because a Mac already logs in to /bin/zsh and running
# chsh anyway would ask for a password nobody is there to type.
login_shell="$(dscl . -read "/Users/$user" UserShell 2>/dev/null |
                 sed 's/^UserShell: //')"

if [ -n "$zsh_path" ] && [ "$login_shell" != "$zsh_path" ]; then
  sudo chsh -s "$zsh_path" "$user"
  echo "login shell is now $zsh_path"
fi

# Somewhere for project checkouts to go, so they are not loose in $HOME beside
# the two repositories that have to be. Named in the instructions Claude
# loads, and made here so the convention exists before the first clone.
mkdir -p "$HOME/projects"

# The project docs. They live outside every checkout, one folder per project,
# and Claude reads and writes them during a session — so the clone has to be
# here before any work starts rather than on first use. Private, like this
# repo, because the folder names alone say which projects exist.
docs="$HOME/projects/docs"
if [ ! -d "$docs/.git" ]; then
  if ! gh auth status >/dev/null 2>&1; then
    echo "gh is not authenticated — $docs not cloned" >&2
  # Never fatal. The docs are what Claude reads during a session, not what the
  # machine needs to work, and the token that reaches this repo does not
  # necessarily reach that one — CI's is scoped to this repo alone. A shell
  # that fails to install because a docs clone failed is the wrong trade.
  elif gh repo clone timche/docs "$docs" -- -q; then
    echo "cloned timche/docs -> $docs"
  else
    echo "could not clone timche/docs — $docs is missing, and Claude will" \
         "have no project docs until it is there" >&2
  fi
fi

# The daemon that carries edits upstream, watching both repositories from one
# process. It replaced a per-repository timer, which replaced a Claude Code
# hook, so it has to survive a machine with no session attached — which is what
# the GUI login mac-mini-setup arranges is for. A runner may have no GUI session,
# so none of this is fatal: the links are already made and a real machine starts
# the daemon on its next install.
#
# boswell rejects the whole config when any path in it is not a work tree, so a
# machine whose docs clone failed above would restart-loop and sync neither
# repository. Left unstarted instead, which syncs nothing but says so.
boswell_ready=false
if [ -d "$repo/.git" ] && [ -d "$docs/.git" ]; then
  boswell_ready=true
fi

uid="$(id -u)"

# The agent's own shell opens the log, and a redirect into a directory that is
# not there fails before boswell starts.
mkdir -p "$HOME/Library/Logs"

if [ ! -f "$plist" ]; then
  echo "$plist is missing — mise links it from mise.toml" >&2
elif [ "$boswell_ready" != true ]; then
  echo "boswell is not running — it watches $docs too and refuses a config" \
       "naming a path that is not a git repository; clone it and re-run" \
       "install.sh" >&2
else
  loaded=false
  if launchctl print "gui/$uid/$boswell_label" >/dev/null 2>&1; then
    loaded=true
  fi

  # launchd keeps the copy of the plist it read when it loaded the job, and
  # kickstart restarts the process from that copy — so a plist that has changed
  # is a bootout and a fresh bootstrap or it is nothing at all until the Mac
  # reboots. The link still pointing where it did says nothing about the file
  # behind it, which is what the copy taken above is for. mac-mini-setup's
  # claude/ssh-agent.sh keeps a hash beside its wrapper for the same reason.
  reloading=false
  if [ "$loaded" = true ] && ! cmp -s "$plist" "$boswell_loaded"; then
    launchctl bootout "gui/$uid/$boswell_label" || true
    loaded=false
    reloading=true
  fi

  if [ "$loaded" = true ]; then
    echo "$boswell_label is already loaded"
  elif launchctl bootstrap "gui/$uid" "$plist"; then
    record_boswell_loaded

    if [ "$reloading" = true ]; then
      echo "reloaded $boswell_label, which is what reaches the running boswell"
    else
      echo "loaded $boswell_label"
    fi
  else
    # The gui domain belongs to a logged-in GUI session, which an SSH login to
    # a Mac sitting at its login window does not have.
    echo "could not load $boswell_label — the gui/$uid domain needs a GUI" \
         "session logged in on the Mac; log in there and re-run install.sh, or" \
         "start it by hand with: launchctl bootstrap gui/$uid $plist" >&2
  fi
fi

# The sweep that follows a removed worktree, on a timer because no removal hands
# anything a hook: herdr, Claude Code and `git worktree remove` all leave, and
# Claude Code's WorktreeRemove does not fire for a git worktree. Nothing gates it
# the way boswell is gated on the docs clone — it has nothing to wait for, and a
# machine with no worktrees gives it nothing to do.
#
# The shape is boswell's above, for the reason given there: launchd keeps the copy
# of the plist it read at load, so one the bootstrap re-rendered reaches a job
# that is already loaded only through a bootout and a fresh bootstrap.
#
# WatchPaths wants the path to exist when the job loads, and herdr creates its
# worktree root only when it makes the first worktree.
mkdir -p "$HOME/.herdr/worktrees"

if [ ! -f "$gc_plist" ]; then
  echo "$gc_plist is missing — mise renders it from mise.toml" >&2
else
  gc_loaded=false
  if launchctl print "gui/$uid/$gc_label" >/dev/null 2>&1; then
    gc_loaded=true
  fi

  if [ "$gc_loaded" = true ] && ! cmp -s "$gc_plist_before" "$gc_plist"; then
    launchctl bootout "gui/$uid/$gc_label" || true
    gc_loaded=false
  fi

  if [ "$gc_loaded" = true ]; then
    echo "$gc_label is already loaded"
  elif launchctl bootstrap "gui/$uid" "$gc_plist"; then
    echo "loaded $gc_label"
  else
    echo "could not load $gc_label — the gui/$uid domain needs a GUI session" \
         "logged in on the Mac; nothing will sweep a removed worktree's" \
         "containers until it is loaded, and worktree-gc runs by hand" >&2
  fi
fi

# The herdr server, from launchd in the GUI session so its panes are local
# sessions rather than SSH ones. Never booted out or restarted from here: this
# script usually runs in one of that server's panes, and restarting the server
# ends every session on it, this one included. So a server already running
# outside launchd, or a plist changed since launchd loaded it, is reported with
# the commands to run once no session needs the server, and left alone.
herdr_label=io.github.timche.herdr
herdr_plist="$HOME/Library/LaunchAgents/$herdr_label.plist"
herdr_loaded="$state/$herdr_label.plist.loaded"
herdr_switch="herdr server stop; launchctl bootout gui/$uid/$herdr_label; launchctl bootstrap gui/$uid $herdr_plist"

if [ ! -f "$herdr_plist" ]; then
  echo "$herdr_plist is missing — mise links it from mise.toml" >&2
elif launchctl print "gui/$uid/$herdr_label" >/dev/null 2>&1; then
  if [ -f "$herdr_loaded" ] && ! cmp -s "$herdr_plist" "$herdr_loaded"; then
    echo "$herdr_label changed since launchd loaded it, and reloading ends every" \
         "herdr session; when none is needed, from a shell outside herdr" \
         "(a plain ssh timche@mac-mini), run: $herdr_switch" >&2
  else
    echo "$herdr_label is already loaded"
  fi
elif herdr status server 2>/dev/null | grep -q '^status: running'; then
  echo "a herdr server is running outside launchd, so its panes are SSH sessions;" \
       "when no session is needed, run: $herdr_switch" >&2
elif launchctl bootstrap "gui/$uid" "$herdr_plist"; then
  mkdir -p "$state" && cp "$herdr_plist" "$herdr_loaded"
  echo "loaded $herdr_label"
else
  echo "could not load $herdr_label — the gui/$uid domain needs a GUI session" \
       "logged in on the Mac" >&2
fi

# This repository's CI dispatcher, which answers a queued job with a macOS VM
# cloned for it and deleted after it. Every other LaunchAgent here is linked by
# mise.toml and loaded below; this one is not linked at all, because launchd
# loads whatever is in ~/Library/LaunchAgents at login and the job has RunAtLoad
# and KeepAlive — the link is the switch. So this makes no decision about whether
# the runner is on, it applies the one Tim already made. The README has both
# steps.
#
# Once it is on, the load is boswell's shape above, with the same
# copy-of-what-launchd-read comparison, because the plist is a symlink into this
# checkout and so says nothing about what the running job was started from.
#
# The one difference is what a reload costs: booting the dispatcher out while it
# is running a job takes the VM down with it and the job fails on GitHub with
# nothing to say why. So a changed plist is reported rather than applied while a
# job VM exists, the way a changed herdr plist is.
tart_label=io.github.timche.tart-runner
tart_plist="$HOME/Library/LaunchAgents/$tart_label.plist"
tart_loaded="$state/$tart_label.plist.loaded"

tart_running=false
if command -v tart >/dev/null 2>&1 &&
   tart list -q --source local 2>/dev/null | grep -q '^gha-runner-job-'; then
  tart_running=true
fi

if [ ! -e "$tart_plist" ]; then
  # A job still loaded from before the link went is still polling GitHub and
  # still able to start a VM, so it is booted out rather than reported: the
  # missing link is the instruction.
  if launchctl print "gui/$uid/$tart_label" >/dev/null 2>&1; then
    if [ "$tart_running" = true ]; then
      echo "$tart_label is loaded with its plist no longer linked, and booting it" \
           "out now would kill the CI job it is running; once the job is done," \
           "run: launchctl bootout gui/$uid/$tart_label" >&2
    elif launchctl bootout "gui/$uid/$tart_label"; then
      echo "unloaded $tart_label, whose plist is no longer linked"
    else
      echo "could not unload $tart_label, whose plist is no longer linked; run:" \
           "launchctl bootout gui/$uid/$tart_label" >&2
    fi
  fi

  echo "$tart_label is off — nothing answers a queued CI job until its plist is" \
       "linked into ~/Library/LaunchAgents; README.md, \"A macOS runner of last" \
       "resort\", has the switch"
else
  if launchctl print "gui/$uid/$tart_label" >/dev/null 2>&1; then
    if [ -f "$tart_loaded" ] && cmp -s "$tart_plist" "$tart_loaded"; then
      echo "$tart_label is already loaded"
    elif [ "$tart_running" = true ]; then
      echo "$tart_label changed since launchd loaded it, and reloading it now" \
           "would kill the CI job it is running; once the job is done, run:" \
           "launchctl bootout gui/$uid/$tart_label; launchctl bootstrap gui/$uid $tart_plist" >&2
    else
      launchctl bootout "gui/$uid/$tart_label" || true
      if launchctl bootstrap "gui/$uid" "$tart_plist"; then
        mkdir -p "$state" && cp "$tart_plist" "$tart_loaded"
        echo "reloaded $tart_label"
      else
        echo "could not reload $tart_label" >&2
      fi
    fi
  elif launchctl bootstrap "gui/$uid" "$tart_plist"; then
    mkdir -p "$state" && cp "$tart_plist" "$tart_loaded"
    echo "loaded $tart_label"
  else
    echo "could not load $tart_label — the gui/$uid domain needs a GUI session" \
         "logged in on the Mac; nothing will answer a queued CI job until it is" \
         "loaded, and tart-runner --once runs by hand" >&2
  fi
fi
