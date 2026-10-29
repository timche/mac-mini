#!/bin/bash

# Install the tooling and link this repo into $HOME and ~/.claude: the shell, the
# prompt, the runtimes and the Claude Code configuration.
#
# The first phase of claude.sh, and run by hand after a pull to put a changed
# link or a new tool in place. It wants the Mac machine.sh built — Homebrew above
# all — and the half of it that clones the project docs wants an authenticated
# gh, which login.sh is what arranges.
#
# Safe to re-run. Tools already present are skipped, existing symlinks are
# replaced, and anything real found at a target is moved aside to
# <name>.backup first.

set -euo pipefail

if [ "$(uname -s)" != Darwin ]; then
  echo "mac-mini is for a Mac; this is $(uname -s)." >&2
  exit 1
fi

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
user="$(id -un)"

# Tooling

# A Mac that has just run machine.sh has Homebrew installed and nothing on PATH
# pointing at it, since the .zprofile that would is one of the links below.
if [ -x /opt/homebrew/bin/brew ]; then
  export PATH="/opt/homebrew/bin:/opt/homebrew/sbin:$PATH"
fi

# Fatal rather than a warning, because the packages below are brew's: a shell
# would come out of here without its syntax highlighting and nothing would say
# why.
if ! command -v brew >/dev/null 2>&1; then
  echo "Homebrew is missing — run machine.sh first, which installs it along" \
       "with gh and tailscale, then re-run this" >&2
  exit 1
fi

# Every Homebrew package this repo installs: one declarative file, and
# `brew bundle check` in test/assert.sh reads the same one back.
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

# herdr's own installer, for the reason Claude Code's is: `herdr update` replaces
# the binary it runs from, which under Homebrew is a Cellar path brew then
# disowns. Guarded like it, and `herdr update` is the upgrade from then on.
if [ ! -x "$HOME/.local/bin/herdr" ]; then
  curl -fsSL https://herdr.dev/install.sh | sh
fi

# The machine

# Where each agent's copy of the definition launchd was last loaded from goes.
# Beside the Dock marker rather than in ~/Library/LaunchAgents, which launchd reads
# at login and is for plists.
state="${XDG_STATE_HOME:-$HOME/.local/state}/mac-mini"

# Finder reads its preferences once at launch, and mise deliberately restarts no
# application — so the bootstrap below can leave Finder showing the old thing
# with the new value already stored. Read before the bootstrap, which is the thing
# that changes them, so afterwards there is nothing to compare against.
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

# mise never removes a link mise.toml stops listing, so one left pointing into this
# checkout at a file it does not have is taken away here, with the folder it was the last
# thing in.
for link in "$HOME/.config/mochi/config.json" "$HOME/.local/bin/mochi.env.op" \
  "$HOME/.local/bin/hachiko.env.op" "$HOME/.config/hachiko/cpu-allow" \
  "$HOME/.config/hachiko/discord" "$HOME/.config/hachiko/shibuya" \
  "$HOME/.config/hachiko/sync"; do
  if [ -L "$link" ] && [ ! -e "$link" ]; then
    case "$(readlink "$link")" in
      "$repo"/*)
        rm "$link" && echo "removed $link, a link to a file this checkout does not have"
        rmdir "$(dirname "$link")" 2>/dev/null || true
        ;;
    esac
  fi
done

# The one thing in the old folder that is a file rather than a link, so the sweep above
# leaves it and the folder it is in: shibuya's ping token, generated on this Mac by
# shibuya/secrets.sh and in no repository and no vault, which is why it is moved and not
# made again. Never over one already in hachiko's folder, that being the live token.
old_token="$HOME/.config/hachiko/shibuya-token"
if [ -f "$old_token" ] && [ ! -e "$HOME/.hachiko/shibuya-token" ]; then
  mkdir -p "$HOME/.hachiko" &&
    mv "$old_token" "$HOME/.hachiko/shibuya-token" &&
    echo "moved $old_token into ~/.hachiko, where hachiko reads it now"
fi
rmdir "$HOME/.config/hachiko" 2>/dev/null || true

# And the folders the three agents recorded themselves in beside the binary, which hachiko
# empties itself on its next start: whichever of the two runs last takes them, and neither
# minds the other having been first. Only when empty, so a state file still waiting to be
# moved is never one this removes.
for stale in "$HOME/Library/Caches/hachiko-gc" "$HOME/Library/Caches/hachiko-update" \
  "$HOME/Library/Caches/hachiko-sync"; do
  rmdir "$stale" 2>/dev/null || true
done

# After the link above, because the tool list mise installs from is that file.
# --locked so a rebuilt Mac gets the versions mise.lock resolved rather than
# whatever latest means on the day, which is the same promise --no-upgrade makes
# for the Brewfile.
#
# From inside the checkout, so that the [tools] in its own mise.toml are installed
# beside the global list: this repository's compiled tools are built with the go
# declared there, and a Mac without it has a hachiko that cannot be rebuilt.
(cd "$repo" && mise install --locked)

# Reported rather than assumed, because the wrapper that would have said so runs
# from a LaunchAgent into a log: a Mac with no go keeps watching with the binary it
# already has and silently stops picking up changes.
if (cd "$repo" && mise exec -- go version) >/dev/null 2>&1; then
  echo "go is installed for this repo's compiled tools"
else
  echo "mise could not install the go this repo's mise.toml asks for — the tools" \
       "under hachiko/ and any beside it cannot be rebuilt until it can, and each" \
       "keeps running the last binary it built" >&2
fi

# portless signs every https://<name>.localhost with a certificate authority of
# its own, and a browser only believes it once that CA is in the system trust
# store. portless offers to do this on its first run, which is a prompt for a
# sudo password in whichever session happens to start a dev server first — here
# instead, while a provision still has sudo.
#
# Asked of the trust store rather than of portless: `portless doctor` reports CA
# trust in prose with no machine-readable form. A missing CA means portless has
# never run at all, and `portless trust` generates one as it trusts it.
if command -v portless >/dev/null 2>&1; then
  if "$repo/claude/portless-ca-trusted.sh"; then
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
  # From zsh, because .zshenv is where the list is and claude.sh runs this from a
  # bash that never read it.
  portless_tlds="$(zsh -c 'print -r -- $PORTLESS_TLD' 2>/dev/null)"
  portless_tlds="${portless_tlds:-localhost}"
  portless_install="sudo portless service install --tld ${portless_tlds//,/ --tld }"
  portless_daemon=/Library/LaunchDaemons/sh.portless.proxy.plist
  portless_problem=""
  if [ ! -f "$portless_daemon" ]; then
    portless_problem="portless's proxy is not installed as a daemon, so dev servers cannot take port 443"
  else
    for i in 0 1; do
      target="$(plutil -extract "ProgramArguments.$i" raw "$portless_daemon" 2>/dev/null)"
      # An argument that is not there reads back empty, and an empty path is
      # missing as far as `[ -e ]` is concerned — which would send Tim off to
      # sudo a reinstall over nothing.
      if [ -n "$target" ] && [ ! -e "$target" ]; then
        portless_problem="portless's daemon runs $target, which is gone since a mise upgrade"
        break
      fi
    done
  fi

  # Done here rather than printed, because a printed line among a provision's
  # output is easily never run, and every dev URL is down until it is. The helper
  # needs no password and machine.sh installs it before claude.sh runs this, so
  # the printed command is left for a Mac where root-helper.sh was skipped.
  if [ -n "$portless_problem" ]; then
    if sudo -n /usr/local/libexec/claude-root reinstall-portless </dev/null; then
      echo "installed portless's proxy daemon"
    else
      echo "$portless_problem; run: $portless_install" >&2
    fi
  fi
fi

# The MCP servers every session has, whichever project it is in. Claude Code keeps
# user-scope servers in ~/.claude.json, which is its own state as much as config
# and so cannot be a link into this checkout the way settings.json is: they go in
# through its CLI, and only when the definition here differs from what is there.
mcp_server() {
  local name="$1" want current
  want="$(jq -cS . <<<"$2")"
  current="$(jq -cS --arg name "$name" '.mcpServers[$name] // empty' "$HOME/.claude.json" 2>/dev/null || true)"
  if [ "$current" = "$want" ]; then
    echo "MCP server $name is already registered"
    return
  fi
  # From /dev/null because claude is a Bun binary, and Bun fails with "EINVAL,
  # kqueue" on a stdin of /dev/tty, which is what bootstrap.sh hands each phase.
  if [ -n "$current" ]; then
    "$HOME/.local/bin/claude" mcp remove -s user "$name" </dev/null >/dev/null || true
  fi
  if "$HOME/.local/bin/claude" mcp add-json -s user "$name" "$want" </dev/null >/dev/null; then
    echo "registered MCP server $name"
  else
    echo "could not register MCP server $name" >&2
  fi
}

# Headless and isolated, so parallel sessions each get a throwaway browser rather
# than sharing one profile in windows on the Mac's screen. On Chrome for Testing
# through mise's shim, which runs whichever version mise has, since a headless
# copy of Tim's own Chrome is what a Dock or `open -a` launch of it lands in; the
# mise config says why. Google collects usage statistics unless told not to, and
# the update check is noise for a version mise.lock pins.
mcp_server chrome-devtools "{
  \"type\": \"stdio\",
  \"command\": \"$HOME/.local/share/mise/shims/chrome-devtools-mcp\",
  \"args\": [\"--headless\", \"--isolated\", \"--executablePath\", \"$HOME/.local/share/mise/shims/google chrome for testing\", \"--no-usage-statistics\"],
  \"env\": {\"CHROME_DEVTOOLS_MCP_NO_UPDATE_CHECKS\": \"1\"}
}"
mcp_server context7 '{"type": "http", "url": "https://mcp.context7.com/mcp"}'

# The mochi plugin, which settings.json leaves off, on in each repository mochi
# serves; the script says why there rather than everywhere. Reported rather than
# fatal, since the account side is no less set up without it.
"$repo/claude/mochi-plugin.sh" ||
  echo "could not turn the mochi plugin on in mochi's repositories; rerun claude/mochi-plugin.sh" >&2
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

# Apple's G2 Developer ID intermediate, which issues the certificate meru signs
# with. macOS ships only the original authority, Xcode adds neither, and codesign
# can fail to build the chain from the copy in the .p12 that electron-builder
# imports. The login keychain rather than the System one, because adding an
# intermediate changes no trust and so needs neither sudo nor a dialog on the
# Mac's screen. Both authorities share a common name and differ in the OU, so the
# check is by OU. The fingerprint is pinned because codesign builds its chain
# from whatever the keychain holds.
g2_name="Developer ID Certification Authority"
g2_url=https://www.apple.com/certificateauthority/DeveloperIDG2CA.cer
g2_sha256=f16cd3c54c7f83cea4bf1a3e6a0819c8aaa8e4a1528fd144715f350643d2df3a

if security find-certificate -a -p -c "$g2_name" 2>/dev/null |
  /usr/bin/openssl crl2pkcs7 -nocrl -certfile /dev/stdin 2>/dev/null |
  /usr/bin/openssl pkcs7 -print_certs -noout 2>/dev/null |
  grep -qE '^subject=.*OU ?= ?G2([,/]|$)'; then
  echo "Apple's G2 Developer ID intermediate is already in a keychain"
else
  g2_dir="$(mktemp -d)"
  if curl -fsSL -o "$g2_dir/g2.cer" "$g2_url" &&
     [ "$(shasum -a 256 "$g2_dir/g2.cer" | cut -d' ' -f1)" = "$g2_sha256" ] &&
     security add-certificates -k "$HOME/Library/Keychains/login.keychain-db" "$g2_dir/g2.cer"; then
    echo "added Apple's G2 Developer ID intermediate to the login keychain"
  else
    echo "could not add Apple's G2 Developer ID intermediate from $g2_url —" \
         "a Developer ID build may fail to build its chain until it is in" \
         "the login keychain" >&2
  fi
  rm -rf "$g2_dir"
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

# The project docs. They live outside every checkout, a folder per project
# under its owner, and Claude reads and writes them during a session — so the
# clone has to be here before any work starts rather than on first use.
# Private, because the folder names alone say which projects exist.
docs="${PROJECT_DOCS_DIR:-$HOME/projects/docs}"
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

uid="$(id -u)"

# Each agent's own shell opens its log, and a redirect into a directory that is
# not there fails before the job starts.
mkdir -p "$HOME/Library/Logs"

# Every agent below but herdr's is loaded through this, and the comparison is the
# whole of why: launchd reads a plist at bootstrap and not again, so one a pull
# changed reaches a job that is already loaded only through a bootout and a fresh
# bootstrap. The plist is a link into this checkout, so comparing the link's target
# across the bootstrap says nothing — what it points at is the file a pull already
# changed — and the copy here, written only once a load succeeded, is what there is
# to compare against. A load that failed leaves no copy, so the next run tries again
# rather than skipping the reload. The caller says what a failure costs.
#
# bootout returns before launchd has finished tearing the job down, and a bootstrap
# in that window, or now and then one for no reason launchd gives, fails with "5:
# Input/output error" and leaves the agent unloaded. So the bootstrap waits for the
# old job to be gone and is tried a few times, and only the last failure is shown.
load_agent() {  # label plist
  local saved="$state/$1.plist.loaded" tries=0 err

  if launchctl print "gui/$uid/$1" >/dev/null 2>&1; then
    cmp -s "$2" "$saved" && { echo "$1 is already loaded"; return 0; }
    launchctl bootout "gui/$uid/$1" || true
    while [ "$tries" -lt 20 ] && launchctl print "gui/$uid/$1" >/dev/null 2>&1; do
      sleep 0.5
      tries=$((tries + 1))
    done
  fi

  tries=0
  until err="$(launchctl bootstrap "gui/$uid" "$2" 2>&1)"; do
    tries=$((tries + 1))
    if [ "$tries" -ge 5 ]; then
      echo "$err" >&2
      return 1
    fi
    sleep 2
  done

  mkdir -p "$state" && cp "$2" "$saved" && echo "loaded $1"
}

# The sweep that follows a removed worktree, on a timer because no removal hands
# anything a hook: herdr, Claude Code and `git worktree remove` all leave, and
# Claude Code's WorktreeRemove does not fire for a git worktree. Nothing gates it
# the way `hachiko sync` is gated on the docs clone — it has nothing to wait for,
# and a machine with no worktrees gives it nothing to do.
gc_label=io.github.timche.hachiko-gc
gc_plist="$HOME/Library/LaunchAgents/$gc_label.plist"

if [ ! -f "$gc_plist" ]; then
  echo "$gc_plist is missing — mise links it from mise.toml" >&2
elif ! load_agent "$gc_label" "$gc_plist"; then
  echo "could not load $gc_label — the gui/$uid domain needs a GUI session" \
       "logged in on the Mac; nothing will sweep a removed worktree's" \
       "containers until it is loaded, and \`hachiko gc\` runs by hand" >&2
fi

# The watch over free space and the CPU, every five minutes.
#
# Nothing gates it, and nothing here builds it. A bot token it cannot resolve and a
# herdr it cannot reach are lines in its log rather than reasons not to watch, and
# the binary is the wrapper's to build on the first run after a change.
hachiko_label=io.github.timche.hachiko
hachiko_plist="$HOME/Library/LaunchAgents/$hachiko_label.plist"

if [ ! -f "$hachiko_plist" ]; then
  echo "$hachiko_plist is missing — mise links it from mise.toml" >&2
elif ! load_agent "$hachiko_label" "$hachiko_plist"; then
  echo "could not load $hachiko_label — the gui/$uid domain needs a GUI session" \
       "logged in on the Mac; until it is loaded nothing notices the disk" \
       "filling up or a process burning a core, and hachiko runs by hand" >&2
fi

# The half that listens for a reply in Discord. It is loaded whether or not the
# feature is configured: with no channel and no user in ~/.hachiko/discord it
# says so once per start and exits, and the plist's five-minute throttle is what makes
# that cost nothing. Loading it only when configured would mean a reply that works on
# a Mac somebody ran install.sh on after filling the file in and nowhere else.
listen_label=io.github.timche.hachiko-listen
listen_plist="$HOME/Library/LaunchAgents/$listen_label.plist"

if [ ! -f "$listen_plist" ]; then
  echo "$listen_plist is missing — mise links it from mise.toml" >&2
elif ! load_agent "$listen_label" "$listen_plist"; then
  echo "could not load $listen_label — the gui/$uid domain needs a GUI session" \
       "logged in on the Mac; until it is loaded a reply in Discord reaches" \
       "nobody and the on-call session is answered in herdr alone" >&2
fi

# mochi, the Discord listener that starts a Claude Code session when Tim mentions it.
# Loaded unconditionally, like the listener above: a token that does not resolve yet is a
# line in its log and a wait before the next `op run`, and hachiko's watch reports the
# heartbeat it is not writing.
mochi_label=io.github.timche.mochi
mochi_plist="$HOME/Library/LaunchAgents/$mochi_label.plist"

if [ ! -f "$mochi_plist" ]; then
  echo "$mochi_plist is missing — mise links it from mise.toml" >&2
elif ! load_agent "$mochi_label" "$mochi_plist"; then
  echo "could not load $mochi_label — the gui/$uid domain needs a GUI session" \
       "logged in on the Mac; until it is loaded a mention of mochi in Discord" \
       "starts nothing" >&2
fi

# The daemon that carries edits upstream: `hachiko sync`, which commits and pushes
# the repositories ~/.hachiko/sync lists. It has to survive a machine with no
# session attached — which is what the GUI login unattended.sh arranges is for. A
# runner may have no GUI session, so none of this is fatal: the links are already
# made and a real machine starts the daemon on its next install.
#
# It refuses the whole config when a path in it is not a work tree, and the one path
# in it is the docs clone above, so a Mac whose clone failed is left unsynced rather
# than restart-looping.
sync_label=io.github.timche.hachiko-sync
sync_plist="$HOME/Library/LaunchAgents/$sync_label.plist"

if [ ! -f "$sync_plist" ]; then
  echo "$sync_plist is missing — mise links it from mise.toml" >&2
elif [ ! -d "$docs/.git" ]; then
  echo "$sync_label is not running — it watches $docs and refuses a config" \
       "naming a path that is not a git repository; clone it and re-run" \
       "install.sh" >&2
elif ! load_agent "$sync_label" "$sync_plist"; then
  echo "could not load $sync_label — the gui/$uid domain needs a GUI session" \
       "logged in on the Mac; until it is loaded nothing carries what a session" \
       "writes in $docs upstream, and \`hachiko sync --once\` runs by hand" >&2
fi

# The launcher the herdr agent starts, and the process macOS holds responsible for
# the privacy grants every session depends on: launchd makes a job's own process
# responsible for itself, and a child inherits that unless it disclaims, so grants
# given to the launcher cover the herdr it runs as its child.
#
# Built here and only when machine/launchd/herdr-launcher.c has changed, rather
# than by a build-in-place wrapper like hachiko's. A grant is pinned to the exact
# contents of an ad-hoc signed binary, and the whole point of the launcher is that
# those contents do not move: a rebuild because mise or Xcode shipped a new
# compiler would drop the grants it exists to keep, and only Tim can give them
# back, on the Mac's own screen.
launcher_src="$repo/machine/launchd/herdr-launcher.c"
launcher="$HOME/.local/libexec/herdr-launcher"
launcher_stamp="$state/herdr-launcher.sha256"
launcher_hash="$(shasum -a 256 "$launcher_src" | cut -d' ' -f1)"

if [ -x "$launcher" ] && [ "$(cat "$launcher_stamp" 2>/dev/null)" = "$launcher_hash" ]; then
  echo "herdr-launcher is already built from the current source"
elif ! command -v clang >/dev/null 2>&1; then
  echo "clang is missing, so herdr-launcher cannot be built and the herdr agent" \
       "cannot start; the Xcode command line tools are what bring it" >&2
else
  mkdir -p "$HOME/.local/libexec" "$state"
  # Built under its final name in a directory of its own, then moved into place in
  # one step. Two reasons, and they pull in opposite directions: the ad-hoc
  # signature the linker writes takes its identifier from the output's basename, so
  # a build as herdr-launcher.new is what tccd would then name in its log and in
  # these bytes; and the file being replaced is one launchd may be running, where a
  # rename swaps the directory entry and leaves the running process on the inode it
  # started from, while writing over it fails outright.
  launcher_build="$HOME/.local/libexec/.herdr-launcher.build"
  rm -rf "$launcher_build"
  mkdir -p "$launcher_build"

  if clang -O2 -o "$launcher_build/herdr-launcher" "$launcher_src"; then
    mv -f "$launcher_build/herdr-launcher" "$launcher"
    rm -rf "$launcher_build"
    printf '%s\n' "$launcher_hash" >"$launcher_stamp"
    echo "built herdr-launcher — these are new bytes, so Tim has to grant Screen" \
         "Recording, Full Disk Access and Device Control and Data Access to" \
         "$launcher in System Settings > Privacy & Security once the agent below" \
         "is running from it"
  else
    rm -rf "$launcher_build"
    echo "herdr-launcher did not compile from $launcher_src" >&2
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
# bootout ahead of stop: KeepAlive restarts a server stopped under launchd at
# once, so stop alone times out on a new server. stop is for one outside it. The
# record of what launchd read is written by that command rather than here,
# because this script never reloads the job: a restart by hand is the only thing
# that knows which plist the running server came from.
herdr_switch="launchctl bootout gui/$uid/$herdr_label; herdr server stop; launchctl bootstrap gui/$uid $herdr_plist && mkdir -p $state && cp $herdr_plist $herdr_loaded"

if [ ! -f "$herdr_plist" ]; then
  echo "$herdr_plist is missing — mise links it from mise.toml" >&2
elif launchctl print "gui/$uid/$herdr_label" >/dev/null 2>&1; then
  # The agents above reload on a missing record, treating it as changed. Here a
  # reload ends every session, so a missing record is said out loud and nothing else.
  if [ ! -f "$herdr_loaded" ]; then
    echo "no record of which $herdr_label plist launchd loaded — restart herdr" \
         "when convenient, from a shell outside herdr (a plain ssh" \
         "timche@mac-mini), with: $herdr_switch" >&2
  elif ! cmp -s "$herdr_plist" "$herdr_loaded"; then
    echo "$herdr_label changed since launchd loaded it, and reloading ends every" \
         "herdr session; when none is needed, from a shell outside herdr" \
         "(a plain ssh timche@mac-mini), run: $herdr_switch" >&2
  else
    echo "$herdr_label is already loaded"
  fi
elif herdr status server 2>/dev/null | grep -q '^status: running'; then
  echo "a herdr server is running outside launchd, so its panes are SSH sessions;" \
       "when no session is needed, run: $herdr_switch" >&2
elif [ ! -x "$launcher" ]; then
  # Not loaded at all, so nothing is running to disturb — and a job bootstrapped
  # onto a launcher that is not there is one launchd retries every ten seconds for
  # the life of the Mac, while a remote client quietly starts an SSH server of its
  # own instead. Left unloaded and said out loud, so the step above is what gets
  # fixed.
  echo "$launcher is not built, so $herdr_label was not loaded — it is the" \
       "launcher, not herdr, that the agent starts" >&2
elif launchctl bootstrap "gui/$uid" "$herdr_plist"; then
  mkdir -p "$state" && cp "$herdr_plist" "$herdr_loaded"
  echo "loaded $herdr_label"
else
  echo "could not load $herdr_label — the gui/$uid domain needs a GUI session" \
       "logged in on the Mac" >&2
fi
