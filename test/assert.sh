#!/bin/bash

# Assertions against a machine install.sh has just finished with, plus the two
# that read the repo itself, since a path written for the wrong account is
# wrong before it is ever installed. Runs as the account the Mac is for.
#
# To add a case, add a check line: a description and a shell snippet that
# exits non-zero when the expectation is not met.

set -uo pipefail

if [ "$(uname -s)" != Darwin ]; then
  echo "mac-mini is for a Mac; this is $(uname -s)." >&2
  exit 1
fi

export PATH="$HOME/.local/bin:$HOME/.local/share/mise/shims:$PATH"

# What a login shell starts from, so an interactive check cannot pass on a PATH
# this script exported rather than on what the rc file does. It is what /etc/paths
# holds, which is what path_helper rebuilds PATH from.
stock_path=/usr/bin:/bin:/usr/sbin:/sbin

# Exported because the snippets run in a child bash that inherits nothing else.
export stock_path
export repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

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

# Symlinks. -L and -e together mean the link exists and its target does too, so
# a link left pointing at a moved file counts as a failure.
for path in .zshrc .zshenv .zprofile .bashrc .gitconfig \
            .config/mise/config.toml .config/mise/mise.lock \
            .config/mise/locks \
            .config/ccstatusline/settings.json \
            .config/git/worktree-install \
            .config/herdr/config.toml .config/starship.toml \
            .terminfo/x/xterm-ghostty .terminfo/78/xterm-ghostty \
            .config/boswell/config.toml; do
  check "$path is a live symlink" "[ -L \"\$HOME/$path\" ] && [ -e \"\$HOME/$path\" ]"
done

for path in CLAUDE.md settings.json skills agents hooks; do
  check ".claude/$path is a live symlink" \
    "[ -L \"\$HOME/.claude/$path\" ] && [ -e \"\$HOME/.claude/$path\" ]"
done

# The switch is this repo's now, because the .zshrc that keeps a first login out
# of zsh-newuser-install is here too. A Mac is already on zsh, so the check is
# only that nothing moved it off.
check "login shell is zsh" \
  'dscl . -read "/Users/$USER" UserShell | grep -q zsh'

# Tooling. The highlighting first: a Mac is not handed over with it, so this fails
# unless install.sh installed it — brew's, zsh itself coming with the OS.
check "zsh installed"       'command -v zsh'
check "zsh highlighting installed" \
  '[ -f "$(brew --prefix)/share/zsh-syntax-highlighting/zsh-syntax-highlighting.zsh" ]'
# Tagged, because the prompt prints to stdout in an interactive shell too and
# a bare version number is easy to match by accident.
check "interactive zsh loads the highlighting" \
  'PATH=$stock_path zsh -ic "print -r -- highlighting:\$ZSH_HIGHLIGHT_VERSION" |
     grep -qE "^highlighting:[0-9]"'
check "glow runs"           'glow --version'
check "ffmpeg runs"         'ffmpeg -version'
check "rg runs"             'rg --version'
# Homebrew's rather than mise's now, and running them is what catches a shim left
# behind for a tool mise no longer installs: it answers ahead of Homebrew's binary
# and fails.
check "fd runs"             'fd --version'
check "shellcheck runs"     'shellcheck --version'
check "bun runs"            'bun --version'
check "mise runs"           'mise --version'
# Pinned to the current LTS major because the tool list asks for node@lts.
# Bump it when the LTS line moves; a failure here is usually that, not a bug.
check "node is the lts"     'node --version | grep -q "^v24\."'
check "npm runs"            'npm --version'
check "claude runs"         'claude --version'
# Anthropic's installer owns that path and the background updater keeps it
# current. A machine that used to take Claude Code from the tool list has a shim
# of the same name ahead of it on PATH until `mise reshim` clears one out, and a
# shim for a tool mise no longer installs answers with an error.
check "claude resolves to the native install in every zsh mode" \
  'for mode in "" -i -l; do
     [ "$(PATH=$stock_path zsh $mode -c "whence -p claude")" = \
       "$HOME/.local/bin/claude" ] || exit 1
   done'
# mise comes from its own installer, and Homebrew's prefix is on
# the same PATH — so ~/.local/bin ahead of it is the whole of what keeps a formula
# of the same name from answering, which is worth asserting rather than reading off
# the file.
#
# whence -p rather than command -v: `mise activate` defines a shell function of
# the same name, so in an interactive zsh command -v reports the function and
# says nothing about which binary it would call.
check "mise resolves to its own installer's copy in every zsh mode" \
  'for mode in "" -i -l; do
     [ "$(PATH=$stock_path zsh $mode -c "whence -p mise")" = \
       "$HOME/.local/bin/mise" ] || exit 1
   done'
check "herdr runs"          'herdr --version'
check "interactive zsh has zoxide's z" \
  'PATH=$stock_path zsh -ic "whence -w z" | grep -q "z: function"'
check "oh-my-zsh present"   '[ -d "$HOME/.oh-my-zsh" ]'
check "starship runs"       'starship --version'

# Machine-wide: one proxy holds port 443, and one CA signs for every .localhost
# name.
check "portless runs" 'portless --version'
check "portless is mise's npm install" 'mise which portless | grep -q "/installs/npm-portless/"'

# Read off the repo rather than the machine: a runner has already been installed
# once by the time a check runs, and no SSH session anywhere in CI is what the skip
# below turns on.
#
# `portless trust` over SSH is a sudo prompt on this terminal plus a confirmation
# dialog on the Mac's own screen, and only the first of those is reachable — so the
# attempt is skipped where there is no GUI session in reach and the line to run is
# printed instead. The already-trusted read stays in front of both.
check "install.sh trusts portless's CA once, and not over SSH" \
  'grep -q "security verify-cert -c \"\$portless_ca\"" "$repo/install.sh" &&
   grep -q "elif \[ -n \"\${SSH_CONNECTION:-}\" \]; then" "$repo/install.sh" &&
   grep -q "elif portless trust </dev/null; then" "$repo/install.sh"'

# The app rather than the cask, because a runner is handed Chrome by its own image
# and brew bundle's own --adopt takes that copy over instead of installing a second
# one.
check "Google Chrome installed" '[ -d "/Applications/Google Chrome.app" ]'

# The whole Homebrew list at once, which is what one Brewfile buys. --no-upgrade to
# match the install: an outdated formula is not a missing one, and install.sh moves
# no version.
check "the Brewfile's dependencies are satisfied" \
  'brew bundle check --no-upgrade --file="$repo/Brewfile"'

# Provenance rather than a version, because a mise-installed copy of the same
# name runs just as well and the point is which one the Mac has.
for formula in fd ffmpeg glow herdr ripgrep shellcheck starship zoxide boswell; do
  check "$formula is Homebrew's" \
    "brew list --formula --full-name | grep -qxE '(.*/)?$formula'"
done

# Read off the repo rather than the machine: the rule is that mise carries the
# language runtimes and the Brewfile carries the Homebrew packages, and a `brew:`
# entry creeping back into mise.toml is how that stops being true. boswell is the
# one that used to be neither, installed by a line of install.sh's own.
check "mise.toml declares no Homebrew package" \
  '! grep -qE "^\"brew(-cask)?:" "$repo/mise.toml"'

check "install.sh installs the Brewfile, boswell included, and moves no version" \
  'grep -q "brew bundle --no-upgrade --file=\"\$repo/Brewfile\"" "$repo/install.sh" &&
   ! grep -qE "(^|[^A-Za-z])brew install " "$repo/install.sh" &&
   grep -q "^tap \"timche/tap\", trusted: true$" "$repo/Brewfile" &&
   grep -q "^brew \"boswell\"$" "$repo/Brewfile"'

# Tim's preferences on the Mac, read back rather than taken on trust: mise warns
# about a config key it does not recognise and carries on, so an older mise would
# leave every declared default unset and say so only in passing. The values are
# what `defaults read` prints, which is 1 and 0 for a boolean.
# mise.toml's half. Both Clicking domains, though only the current-host key
# below is believed to be what macOS 26 actually reads — System Settings shows
# these two, and this is what proves the friendly `tap_to_click` wrote both.
check "tap to click is on for the built-in trackpad" \
  '[ "$(defaults read com.apple.AppleMultitouchTrackpad Clicking)" = 1 ]'
check "tap to click is on for the Magic Trackpad" \
  '[ "$(defaults read com.apple.driver.AppleBluetoothMultitouch.trackpad Clicking)" = 1 ]'
check "tap to click is on per machine, which is the key macOS 26 reads" \
  '[ "$(defaults -currentHost read NSGlobalDomain com.apple.mouse.tapBehavior)" = 1 ]'

check "Finder shows hidden files" \
  '[ "$(defaults read com.apple.finder AppleShowAllFiles)" = 1 ]'
check "Finder shows every filename extension" \
  '[ "$(defaults read NSGlobalDomain AppleShowAllExtensions)" = 1 ]'
check "Finder shows the status bar" \
  '[ "$(defaults read com.apple.finder ShowStatusBar)" = 1 ]'
check "Finder shows the path bar" \
  '[ "$(defaults read com.apple.finder ShowPathbar)" = 1 ]'
check "Finder does not warn about a changed extension" \
  '[ "$(defaults read com.apple.finder FXEnableExtensionChangeWarning)" = 0 ]'

# install.sh's half, each for a reason mise.toml's comment gives.
check "screenshots save to ~/Pictures" \
  '[ "$(defaults read com.apple.screencapture location)" = "$HOME/Pictures" ]'
check "~/Library is not hidden from Finder" \
  '[ -z "$(find "$HOME/Library" -maxdepth 0 -flags +hidden)" ] &&
   ! xattr "$HOME/Library" | grep -qx com.apple.FinderInfo'
# Emptied once and recorded, so the next install leaves alone whatever Tim
# pinned in the meantime. Both halves, because the marker without the empty
# array would mean it never ran and the empty array without the marker would
# mean it runs again.
check "the Dock holds none of the icons macOS ships with, recorded as done once" \
  '[ "$(defaults read com.apple.dock persistent-apps | tr -d "[:space:]")" = "()" ] &&
   [ -e "${XDG_STATE_HOME:-$HOME/.local/state}/mac-mini/dock-emptied" ]'

# Safe to re-run, which for a preference means the second pass finds nothing to
# write and so has no reason to restart Finder, the Dock or SystemUIServer.
# mise answers for its own half; install.sh's guards are the four reads above,
# and this is the one that is only about the repeat.
check "a second pass over the declared defaults has nothing to write" \
  'mise -C "$repo" bootstrap macos defaults status --missing'

# Reading before writing, and restarting only what changed, is the part a runner
# cannot show: it has already been installed once by the time this runs, so every
# guard above is satisfied and no branch is left to exercise. Read instead, as the
# boswell and gc checks read theirs.
check "install.sh writes a preference only when it differs and restarts only then" \
  'grep -q "if \[ \"\$(defaults read com.apple.screencapture location 2>/dev/null)\" != \"\$screenshots\" \]" \
     "$repo/install.sh" &&
   grep -q "find \"\$HOME/Library\" -maxdepth 0 -flags +hidden" "$repo/install.sh" &&
   grep -q "if \[ ! -e \"\$dock_marker\" \]" "$repo/install.sh" &&
   grep -q "if \[ \"\$finder_changed\" = true \]" "$repo/install.sh" &&
   grep -q "killall SystemUIServer screencaptureui" "$repo/install.sh"'

# The rc files have to work in a real interactive shell, which is the only
# place the mise block is reached.
check "interactive zsh resolves node"  'PATH=$stock_path zsh -ic "node --version" | grep -q "^v"'
check "interactive bash resolves node" 'PATH=$stock_path bash -ic "node --version" | grep -q "^v"'
check "non-interactive zsh resolves node" 'PATH=$stock_path zsh -c "node --version" | grep -q "^v"'
check "interactive zsh draws the prompt with starship" \
  'PATH=$stock_path zsh -ic "print -r -- starship:\$STARSHIP_SHELL" | grep -qx "starship:zsh"'

# The order every zsh has to end up with on the Mac: the shims, then Homebrew,
# then the system directories. A login shell is the one at risk, because
# /etc/zprofile runs path_helper after .zshenv and puts the system directories
# first until .zprofile undoes it. Exported so the snippets, which run in a
# child bash, can call it.
zsh_path_order() {
  PATH=$stock_path zsh ${1:+"$1"} -c 'print -rl -- $path' |
    awk -v shims="$HOME/.local/share/mise/shims" '
      $0 == shims && !s { s = NR }
      $0 == "/opt/homebrew/bin" && !b { b = NR }
      $0 == "/usr/bin" && !u { u = NR }
      END { exit !(s && b && u && s < b && b < u) }'
}
export -f zsh_path_order

check "non-interactive zsh finds the shims before Homebrew before /usr/bin" \
  'zsh_path_order ""'
check "login zsh keeps that order through path_helper" 'zsh_path_order -l'
check "interactive zsh keeps that order" 'zsh_path_order -i'

# The signing key lives in the agent claude/ssh-agent.sh keeps at that socket, and
# nothing finds it without this: launchd hands every session an SSH_AUTH_SOCK
# of its own whose agent holds nothing. Unguarded in .zshenv, so it is set
# whether or not the agent happens to be up — which is what makes it
# assertable on a runner that has no agent at all.
check "zsh points at the agent socket claude/ssh-agent.sh keeps" \
  '[ "$(PATH=$stock_path zsh -c "print -r -- \$SSH_AUTH_SOCK")" = \
     "$HOME/.ssh/agent.sock" ]'

# git config survives the $HOME rewrite and stays readable through the symlink.
check "git reads the linked config" 'git config --get user.email | grep -q "@"'
# The scan runs from config beside a repository's own hooks, so it has to block a
# token-shaped value even where .git/hooks has a pre-commit of its own.
check "every commit is scanned for secrets, beside the repository's own hooks" \
  'd="$(mktemp -d)" && git -C "$d" init -q &&
   printf "#!/bin/sh\ntouch repo-hook-ran\n" >"$d/.git/hooks/pre-commit" &&
   chmod +x "$d/.git/hooks/pre-commit" &&
   printf "GITHUB_TOKEN=ghp_%s\n" 0123456789abcdefABCDEF0123456789abcd >"$d/leak.env" &&
   git -C "$d" add leak.env &&
   ! git -C "$d" -c commit.gpgsign=false commit -q -m leak >/dev/null 2>&1 &&
   [ -z "$(git -C "$d" rev-list --all 2>/dev/null)" ] &&
   rm "$d/leak.env" && git -C "$d" rm -q --cached leak.env && : >"$d/ok" && git -C "$d" add ok &&
   git -C "$d" -c commit.gpgsign=false commit -q -m ok >/dev/null 2>&1 &&
   [ -f "$d/repo-hook-ran" ]'

check "commit signing stays on"     'git config --get commit.gpgsign | grep -q true'

# The key is the agent's and no file names it: gpg.ssh.defaultKeyCommand is how git
# asks, and with no user.signingkey it signs with the first key the agent answers
# with. Read out of the tracked file rather than the effective config, which a
# machine-local override is allowed to disagree with.
export tracked="$repo/home/.gitconfig"

check "the tracked config names no signing key" \
  '! git config --file "$tracked" --get user.signingkey'
check "the tracked config asks the agent for one" \
  '[ "$(git config --file "$tracked" --get gpg.ssh.defaultKeyCommand)" = "ssh-add -L" ]'
check "the tracked config includes the machine-local override" \
  '[ "$(git config --file "$tracked" --get include.path)" = "~/.gitconfig.local" ]'

# The effective config too, since the machine-local override is untracked and
# written by hand: a key named there would be signed with in place of the agent's.
check "nothing outside the agent names a signing key" \
  '! git config --get user.signingkey'

# A new worktree of a JS project, installed from config rather than remembered.
# The fixture is a repository with a bun lockfile and a bun of its own on PATH
# that records where it ran and with what, so the checks below read what the hook
# did rather than whether a real install happened to succeed.
worktree_install_fixture() {
  local dir="$1" status="${2-0}"

  mkdir -p "$dir/bin"
  cat > "$dir/bin/bun" <<EOF
#!/bin/sh
echo "\$(pwd) \$*" >> "$dir/bun.log"
[ $status -eq 0 ] || exit $status
mkdir -p node_modules
EOF
  chmod +x "$dir/bin/bun"

  git init -q -b main "$dir/app"
  printf '{ "name": "app" }\n' > "$dir/app/package.json"
  : > "$dir/app/bun.lock"
  git -C "$dir/app" add -A
  git -C "$dir/app" -c user.email=t@example.com -c user.name=t \
    -c commit.gpgsign=false commit -qm init
}
export -f worktree_install_fixture

# mktemp's path goes through /var, and the hook reports the worktree as git
# resolves it, so the fixture root is resolved once and compared against that.
check "the tracked config installs a new worktree's dependencies" \
  '[ "$(git config --file "$tracked" --get hook.worktree-install.event)" = post-checkout ] &&
   git config --file "$tracked" --get hook.worktree-install.command |
     grep -q "config/git/worktree-install" &&
   [ -x "$HOME/.config/git/worktree-install" ]'

check "git worktree add installs when the main worktree is already installed" \
  'd="$(cd "$(mktemp -d)" && pwd -P)" && worktree_install_fixture "$d" &&
   mkdir "$d/app/node_modules" &&
   PATH="$d/bin:$PATH" git -C "$d/app" worktree add -q -b feat "$d/wt" &&
   [ -d "$d/wt/node_modules" ] &&
   grep -qx "$d/wt install --frozen-lockfile" "$d/bun.log"'

# An uninstalled main worktree is a project nobody has run the install for by
# hand, so nothing here decides to run its lifecycle scripts for the first time.
check "git worktree add installs nothing when the main worktree has not been" \
  'd="$(cd "$(mktemp -d)" && pwd -P)" && worktree_install_fixture "$d" &&
   PATH="$d/bin:$PATH" git -C "$d/app" worktree add -q -b feat "$d/wt" &&
   [ ! -e "$d/wt/node_modules" ] && [ ! -f "$d/bun.log" ]'

check "git clone installs nothing" \
  'd="$(cd "$(mktemp -d)" && pwd -P)" && worktree_install_fixture "$d" &&
   mkdir "$d/app/node_modules" &&
   PATH="$d/bin:$PATH" git clone -q "$d/app" "$d/cloned" &&
   [ ! -e "$d/cloned/node_modules" ] && [ ! -f "$d/bun.log" ]'

# Everything else the hook asks for holds here — a linked worktree of an
# installed project with no node_modules — so the previous HEAD is the only
# thing left saying this is not a worktree being added.
check "a branch checkout in a worktree installs nothing" \
  'd="$(cd "$(mktemp -d)" && pwd -P)" && worktree_install_fixture "$d" &&
   export PATH="$d/bin:$PATH" &&
   mkdir "$d/app/node_modules" &&
   git -C "$d/app" worktree add -q -b feat "$d/wt" &&
   rm -rf "$d/wt/node_modules" "$d/bun.log" &&
   git -C "$d/wt" checkout -q -b feat2 &&
   [ ! -e "$d/wt/node_modules" ] && [ ! -f "$d/bun.log" ]'

check "an install that fails leaves the worktree added and says what to run" \
  'd="$(cd "$(mktemp -d)" && pwd -P)" && worktree_install_fixture "$d" 1 &&
   mkdir "$d/app/node_modules" &&
   out="$(PATH="$d/bin:$PATH" git -C "$d/app" worktree add -b feat "$d/wt" 2>&1)" &&
   [ -d "$d/wt" ] &&
   printf "%s" "$out" | grep -q "cd \"$d/wt\" && bun install --frozen-lockfile"'

# Auto-sync is one boswell daemon watching every repository its config lists. A
# runner has no docs clone and so cannot start it, which is why the wiring is what
# gets checked rather than a running daemon. boswell-agent.sh is the one that makes
# launchd load the plist.
#
# An agent gets no PATH worth anything, so it names boswell where Homebrew keeps it.
export plist="$HOME/Library/LaunchAgents/io.github.timche.boswell.plist"

# A link rather than a rendered copy: launchd resolves it at bootstrap and reads
# the file behind it, so a pull is the whole of an update.
check "boswell's LaunchAgent is a link into the checkout" \
  '[ -L "$plist" ] &&
   [ "$plist" -ef "$repo/home/Library/LaunchAgents/io.github.timche.boswell.plist" ]'
# Through the link, which is what launchd reads: a plist it cannot parse is a job
# rejected at load with nothing in it to say why.
check "the agent lints through the link" 'plutil -lint "$plist"'
check "nothing in the agent is left to render" '! grep -q "{{" "$plist"'

# launchd expands neither ~ nor $HOME in a plist, so every path this job needs
# belongs to a shell — the one thing it hands a HOME to. That is what lets the
# file be a link and name no account.
check "the agent runs its command through a shell" \
  '[ "$(plutil -extract ProgramArguments.0 raw -o - "$plist")" = "/bin/sh" ] &&
   [ "$(plutil -extract ProgramArguments.1 raw -o - "$plist")" = "-c" ]'

export agent_command="$(plutil -extract ProgramArguments.2 raw -o - "$plist")"

# exec, so the status launchd reads is boswell's rather than the shell's and
# KeepAlive below still means what it says.
check "the agent execs Homebrew's boswell" \
  'printf "%s\n" "$agent_command" | grep -qF "exec /opt/homebrew/bin/boswell"'
check "the agent starts at load" \
  'plutil -extract RunAtLoad xml1 -o - "$plist" | grep -q "<true/>"'

# boswell exits when a watcher dies rather than staying up watching nothing,
# which only helps if something brings it back — and only on a failure, so a
# boswell that was told to stop stays stopped.
check "a boswell that exits is restarted" \
  'plutil -extract KeepAlive.SuccessfulExit xml1 -o - "$plist" | grep -q "<false/>"'
check "the agent waits before restarting boswell" \
  '[ "$(plutil -extract ThrottleInterval raw -o - "$plist")" = 10 ]'

# git's credential helper is gh, and boswell asks gh for a token when it files
# an issue. launchd hands a job almost no PATH, so the command exports its own.
check "the agent's PATH reaches the shims and Homebrew" \
  'printf "%s\n" "$agent_command" |
     grep -qF "export PATH=\"\$HOME/.local/share/mise/shims:/opt/homebrew/bin:"'

# boswell's own commits are signed, and it reads no rc file: the socket has to
# come from the agent or every push it makes fails on a key it cannot find.
check "the agent reaches the ssh-agent holding the signing key" \
  'printf "%s\n" "$agent_command" |
     grep -qF "export SSH_AUTH_SOCK=\"\$HOME/.ssh/agent.sock\""'
# Appended by the shell, since a log that came back to the same file every
# restart is the only way to read why the last one died.
check "the agent appends to the log in ~/Library/Logs" \
  'printf "%s\n" "$agent_command" |
     grep -qF ">>\"\$HOME/Library/Logs/boswell.log\" 2>&1"'

# StandardOutPath holding a $HOME is an EX_CONFIG failure at load, with nothing
# anywhere naming the key that was wrong, and EnvironmentVariables are not
# expanded either — which is why both moved into the command above.
check "the agent names no key launchd leaves unexpanded" \
  '! plutil -extract StandardOutPath raw -o - "$plist" &&
   ! plutil -extract StandardErrorPath raw -o - "$plist" &&
   ! plutil -extract EnvironmentVariables xml1 -o - "$plist"'

# One process watches both, and the docs repo is the one that is easy to
# forget: it is cloned by install.sh rather than being the clone install.sh
# runs from.
check "the config lists both repositories" \
  'grep -q "^path = \"~/projects/docs\"$" "$HOME/.config/boswell/config.toml" &&
   grep -q "^path = \"~/.mac-mini\"$" "$HOME/.config/boswell/config.toml"'

# Homebrew's, from a tap of its own rather than from homebrew/core — so that the
# formula landed and runs is worth proving rather than assuming.
check "boswell runs"        'boswell --help'

# A token that reaches this repo does not necessarily reach the docs one, and
# CI's does not. The clone has to degrade to a warning, or the shell never gets
# installed on a machine whose only problem is a missing docs repo.
check "a failed docs clone does not abort install.sh" \
  'grep -q "elif gh repo clone timche/docs" "$repo/install.sh"'

check "install.sh loads the daemon and clones the docs repo" \
  'grep -q "launchctl bootstrap \"gui/\$uid\" \"\$plist\"" "$repo/install.sh" &&
   grep -q "gh repo clone timche/docs" "$repo/install.sh"'

# One config names both repositories and boswell rejects all of it if either
# path is not a work tree, so starting the daemon on a machine whose docs clone
# failed would cost the dotfiles their sync too. One gate for both, which is why
# the greps are for the variable rather than for two conditions.
check "install.sh starts the daemon only once both repositories exist" \
  'grep -q "^if \[ -d \"\$repo/.git\" \] && \[ -d \"\$docs/.git\" \]; then" \
     "$repo/install.sh" &&
   grep -q "elif \[ \"\$boswell_ready\" != true \]; then" "$repo/install.sh"'

# launchd reads a plist when it loads the job and never again, so one that changed —
# the SSH_AUTH_SOCK added to it, say — would otherwise wait for a reboot. The reload
# is not reachable from here, a runner having no docs clone, so this reads the wiring
# as the checks around it do.
#
# The plist is a link into the checkout, and a link that still points where it did
# says nothing about the file behind it: what the comparison reads is a copy of the
# definition the daemon was last loaded from, kept outside the checkout and written
# only once the load succeeded.
check "install.sh reloads boswell when the definition it loaded changed" \
  'grep -q "launchctl bootout \"gui/\$uid/\$boswell_label\"" "$repo/install.sh" &&
   grep -q "cmp -s \"\$plist\" \"\$boswell_loaded\"" "$repo/install.sh" &&
   grep -c "^ *record_boswell_loaded$" "$repo/install.sh" | grep -q 1'

# Claude Code updates itself, which is why it is not in the tool list at all:
# something that pinned it would have to turn the updater off, and then the pin
# would be the only thing keeping it current.
check "nothing disables Claude Code's updater" \
  '! grep -q DISABLE_AUTOUPDATER "$HOME/.claude/settings.json"'

# ccstatusline is an npm package in mise's tool list, run through its shim, so a
# draw pays for neither a registry lookup nor npx starting node twice. Run rather
# than read, because Claude Code says nothing at all about a statusLine command
# that fails.
check "the statusLine draws a line with mise's ccstatusline" \
  'grep -q "mise/shims/ccstatusline" "$HOME/.claude/settings.json" &&
   mise which ccstatusline | grep -q "/installs/npm-ccstatusline/" &&
   printf "{}" | "$HOME/.local/share/mise/shims/ccstatusline" | grep -q .'

# Asked of ~/.claude.json rather than `claude mcp list`, which starts every server
# to report its health.
check "chrome-devtools-mcp is mise's npm install" \
  'mise which chrome-devtools-mcp | grep -q "/installs/npm-chrome-devtools-mcp/"'
check "every session has the chrome-devtools MCP server, headless and isolated" \
  'jq -e ".mcpServers[\"chrome-devtools\"] | (.command | endswith(\"mise/shims/chrome-devtools-mcp\")) and (.args | index(\"--headless\") and index(\"--isolated\"))" "$HOME/.claude.json"'
check "every session has the context7 MCP server" \
  'jq -e ".mcpServers.context7.url == \"https://mcp.context7.com/mcp\"" "$HOME/.claude.json"'

# The PostToolUse hook both of these replaced fired on every write, which put
# half-finished edits upstream and made syncing Claude's job. Nothing should
# wire it back up.
check "no auto-sync hook is left in settings.json" \
  '! grep -qE "git-sync|dotfiles-sync|boswell" "$HOME/.claude/settings.json"'

# Nothing under home/ spells a home directory out. The account differs between this
# Mac and a runner, and a path written for one is a hook that never fires for the
# other, silently.
check "no absolute home paths under home/" \
  '! grep -rhoE "/(home|Users)/[A-Za-z0-9_.-]+" "$repo/home" | grep -q .'

check "the scripts the hooks call are executable" \
  '[ -x "$HOME/.claude/hooks/herdr-agent-state.sh" ] &&
   [ -x "$HOME/.claude/hooks/project-docs.sh" ] &&
   [ -x "$HOME/.claude/hooks/worktree-info.sh" ]'

# Worktree isolation. A fixture repository with a linked worktree, built per
# check rather than once, because the checks below are independent of each other.
worktree_fixture() {
  local dir="$1" scripts="${2-}" deps="${3-}"

  git init -q -b main "$dir/app"
  git -C "$dir/app" remote add origin https://example.com/acme/app.git
  printf '{ "name": "appsite", "scripts": { %s }, "devDependencies": { %s } }\n' \
    "$scripts" "$deps" > "$dir/app/package.json"
  git -C "$dir/app" add -A
  # Signing off, because this account signs every commit with a key that only
  # exists in an agent a runner does not have.
  git -C "$dir/app" -c user.email=t@example.com -c user.name=t \
    -c commit.gpgsign=false commit -qm init
  git -C "$dir/app" worktree add -q -b fix-ui "$dir/wt/fix-ui"
}
export -f worktree_fixture

# Compose names a project after the directory, which for a worktree is the
# branch alone — so two repositories with a branch of the same name would share
# one database. Every zsh, because a compose command as often comes from a
# script as from a prompt, and again after a cd, because a shell outlives the
# directory it started in.
check "zsh names the compose project after the repository and the branch" \
  'd="$(mktemp -d)" && worktree_fixture "$d" &&
   [ "$(cd "$d/wt/fix-ui" && PATH=$stock_path zsh -c "print -r -- \$COMPOSE_PROJECT_NAME")" = \
     app-fix-ui ] &&
   [ "$(cd "$d/app" && PATH=$stock_path zsh -c "print -r -- \$COMPOSE_PROJECT_NAME")" = \
     app-main ]'

check "zsh renames the compose project on a cd and unsets it outside a repository" \
  'd="$(mktemp -d)" && worktree_fixture "$d" &&
   [ "$(cd / && PATH=$stock_path zsh -c "cd \"$d/wt/fix-ui\"
          print -r -- \$COMPOSE_PROJECT_NAME:\${+COMPOSE_PROJECT_NAME}
          cd /
          print -r -- \$COMPOSE_PROJECT_NAME:\${+COMPOSE_PROJECT_NAME}" | tr "\n" " ")" = \
     "app-fix-ui:1 :0 " ]'

# The hook reads the worktree out of its stdin, because CLAUDE_PROJECT_DIR stays
# where the session started.
worktree_info() {
  printf '{"session_id":"t","cwd":"%s","hook_event_name":"SessionStart","source":"startup"}' "$1" |
    bash "$HOME/.claude/hooks/worktree-info.sh"
}
export -f worktree_info

check "the worktree-info hook is wired to SessionStart" \
  'grep -q "hooks/worktree-info.sh" "$HOME/.claude/settings.json"'

# A project with none of the three has to stay silent, or every session on every
# other project starts with a paragraph about nothing.
check "the worktree-info hook says nothing without compose, portless or Electron" \
  'd="$(mktemp -d)" && worktree_fixture "$d" &&
   [ -z "$(worktree_info "$d/wt/fix-ui")" ] && [ -z "$(worktree_info "$d/app")" ]'

# <repo>-<branch> from the remote and the branch, which is what .zshenv exports
# into the same worktree: the hook naming a different project than the shell
# would be worse than naming none.
check "the worktree-info hook names the compose project of a linked worktree" \
  'd="$(mktemp -d)" && worktree_fixture "$d" &&
   printf "services:\n  db:\n    image: postgres:17\n" > "$d/wt/fix-ui/compose.yaml" &&
   out="$(worktree_info "$d/wt/fix-ui")" &&
   [ "$(printf "%s" "$out" | head -1)" = "Worktree fix-ui of app:" ] &&
   printf "%s" "$out" | grep -q "compose project app-fix-ui" &&
   printf "%s" "$out" | grep -q "down -v" &&
   printf "%s" "$out" | grep -q "docker compose port"'

# The branch is a subdomain of the app name in a linked worktree and absent in
# the main one, which is portless's own rule.
check "the worktree-info hook prints portless's URL for each kind of worktree" \
  'd="$(mktemp -d)" &&
   worktree_fixture "$d" "\"dev\": \"portless run next dev\"" &&
   worktree_info "$d/wt/fix-ui" | grep -q "https://fix-ui.appsite.localhost" &&
   worktree_info "$d/app" | grep -q "https://appsite.localhost"'

check "the worktree-info hook prints the tailnet URL for every TLD after the first" \
  'd="$(mktemp -d)" &&
   worktree_fixture "$d" "\"dev\": \"portless run next dev\"" &&
   out="$(PORTLESS_TLD=localhost,mac-mini.example.dev worktree_info "$d/wt/fix-ui")" &&
   printf "%s" "$out" | grep -q "URL https://fix-ui.appsite.localhost" &&
   printf "%s" "$out" | grep -q "MacBook: https://fix-ui.appsite.mac-mini.example.dev" &&
   ! PORTLESS_TLD=localhost worktree_info "$d/wt/fix-ui" | grep -q MacBook'

check "PORTLESS_TLD serves .localhost first and the tailnet name after it" \
  '[ "$(zsh -c "echo \$PORTLESS_TLD")" = "localhost,timche.dev" ]'

check "install.sh forwards the tailnet's port 443 to portless and names its daemon" \
  'grep -q "tailscale serve --bg --tcp 443 tcp://127.0.0.1:443" "$repo/install.sh" &&
   grep -q "sudo portless service install --tld" "$repo/install.sh"'

check "the worktree-info hook prints the Electron profile rule" \
  'd="$(mktemp -d)" &&
   worktree_fixture "$d" "" "\"electron\": \"^44.0.0\"" &&
   worktree_info "$d/wt/fix-ui" | grep -q "DevToolsActivePort"'

# The sweep runs against a HOME of its own, so the fixture's worktree roots are
# throwaway ones and the check leaves nothing in the real home directory. That
# HOME hides the docker context in the real ~/.docker, so the socket is
# pinned from the context before the switch.
#
# Three projects, because the rule is narrower than "the folder is gone": only a
# working directory inside a worktree root counts. A daemon shared with another
# machine reports projects whose folder was never on this disk, and `down -v` on
# one of those would delete a database over a path that only looks missing.
gc_compose_fixture() {
  local dir="$1" project="$2"

  mkdir -p "$dir"
  printf 'services:\n  idle:\n    image: alpine:3\n    command: sleep 300\n' \
    > "$dir/compose.yaml"
  (cd "$dir" && COMPOSE_PROJECT_NAME="$project" docker compose create) >/dev/null 2>&1
}
export -f gc_compose_fixture

check "worktree-gc is a live symlink and runs" \
  '[ -L "$HOME/.local/bin/worktree-gc" ] && [ -x "$HOME/.local/bin/worktree-gc" ] &&
   worktree-gc --help | grep -q "dry-run"'

# A machine with neither docker nor a daemon cannot answer this one either way,
# and saying so is better than a check that passes because nothing happened.
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  check "worktree-gc --dry-run sweeps a removed worktree's compose project and nothing else" \
    'h="$(mktemp -d)" && gone="$h/.herdr/worktrees/app/gone" &&
     gc_compose_fixture "$gone" gc-gone &&
     gc_compose_fixture "$h/.herdr/worktrees/app/kept" gc-kept &&
     gc_compose_fixture "$h/elsewhere" gc-elsewhere &&
     rm -rf "$gone" "$h/elsewhere" &&
     sock="$(docker context inspect -f "{{.Endpoints.docker.Host}}")" &&
     out="$(HOME="$h" DOCKER_HOST="$sock" worktree-gc --dry-run)" &&
     docker compose -p gc-gone down -v >/dev/null 2>&1
     docker compose -p gc-kept down -v >/dev/null 2>&1
     docker compose -p gc-elsewhere down -v >/dev/null 2>&1
     printf "%s" "$out" | grep -q "would remove compose project gc-gone" &&
     ! printf "%s" "$out" | grep -q gc-kept &&
     ! printf "%s" "$out" | grep -q gc-elsewhere'
else
  echo "  skip  worktree-gc's compose sweep (no docker daemon on this machine)"
fi

# The herdr server has to come from launchd in the GUI session, or every pane is an
# SSH session and Claude Code withholds computer use from it.
export herdr_plist="$HOME/Library/LaunchAgents/io.github.timche.herdr.plist"
check "the herdr agent is a live symlink and a valid plist" \
  '[ -L "$herdr_plist" ] && [ -e "$herdr_plist" ] && plutil -lint "$herdr_plist" &&
   [ "$(plutil -extract Label raw "$herdr_plist")" = io.github.timche.herdr ]'
check "the herdr agent runs the server and is always kept alive" \
  'plutil -extract ProgramArguments.2 raw "$herdr_plist" | grep -q "exec /opt/homebrew/bin/herdr server" &&
   [ "$(plutil -extract KeepAlive raw "$herdr_plist")" = true ]'
check "install.sh never restarts the herdr server it may be running in" \
  '! grep -qE "launchctl (bootout|kickstart)[^;]*herdr_label\"?\)?$" "$repo/install.sh" &&
   grep -q "a herdr server is running outside launchd" "$repo/install.sh"'

# The sweep is on a timer rather than on a session, so the agent is the whole of
# its wiring: an unrendered or invalid plist is a job launchd rejects at load
# with nothing in it to say why. Not reachable on a runner with no GUI session,
# so this reads the file as boswell's checks do.
export gc_plist="$HOME/Library/LaunchAgents/io.github.timche.worktree-gc.plist"

check "worktree-gc's LaunchAgent is rendered" '[ -f "$gc_plist" ]'
check "the gc agent is a valid plist" 'plutil -lint "$gc_plist"'
check "the gc agent has the home directory filled in" '! grep -q "{{" "$gc_plist"'
check "the gc agent runs worktree-gc" \
  '[ "$(plutil -extract ProgramArguments.0 raw -o - "$gc_plist")" = \
     "$HOME/.local/bin/worktree-gc" ]'
check "the gc agent sweeps at load" \
  'plutil -extract RunAtLoad xml1 -o - "$gc_plist" | grep -q "<true/>"'
# The interval does most of the work: WatchPaths is not recursive, so a
# worktree removed a level below the root it watches is invisible to it.
check "the gc agent sweeps every ten minutes" \
  '[ "$(plutil -extract StartInterval raw -o - "$gc_plist")" = 600 ]'
check "the gc agent watches the herdr worktree root" \
  '[ "$(plutil -extract WatchPaths.0 raw -o - "$gc_plist")" = \
     "$HOME/.herdr/worktrees" ]'
# launchd hands a job almost no PATH, and this one shells out to docker.
check "the gc agent's PATH reaches docker" \
  'plutil -extract EnvironmentVariables.PATH raw -o - "$gc_plist" |
     grep -q "/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:"'
check "the gc agent logs to ~/Library/Logs" \
  '[ "$(plutil -extract StandardErrorPath raw -o - "$gc_plist")" = \
     "$HOME/Library/Logs/worktree-gc.log" ]'

check "install.sh loads the gc agent and reloads a changed one" \
  'grep -q "launchctl bootstrap \"gui/\$uid\" \"\$gc_plist\"" "$repo/install.sh" &&
   grep -q "launchctl bootout \"gui/\$uid/\$gc_label\"" "$repo/install.sh" &&
   grep -q "cmp -s \"\$gc_plist_before\" \"\$gc_plist\"" "$repo/install.sh" &&
   grep -q "mkdir -p \"\$HOME/.herdr/worktrees\"" "$repo/install.sh"'

# The CI dispatcher. These run inside the VM a job is given, which has Tart from
# the Brewfile and no image to clone, so nothing here asks it to start one: the
# wiring is what an assert can answer, and the proof that it dispatches is a run
# appearing in this repository's Actions tab.
check "tart-runner is a live symlink and runs" \
  '[ -L "$HOME/.local/bin/tart-runner" ] && [ -x "$HOME/.local/bin/tart-runner" ] &&
   tart-runner --help | grep -q jitconfig'

# Empty by default, and a live link so that adding a repository is one line in
# the checkout. Every uncommented line has to be an owner/name, since anything
# else is a repository the dispatcher would ask GitHub about forever.
check "the repository list is a live symlink holding owner/name lines and nothing else" \
  '[ -L "$HOME/.config/tart-runner/repos" ] &&
   [ -e "$HOME/.config/tart-runner/repos" ] &&
   ! sed -e "s/#.*//" -e "/^[[:space:]]*$/d" "$HOME/.config/tart-runner/repos" |
     grep -qvE "^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$"'

# The labels are the one thing a workflow has to agree with, and it is a
# workflow in another repository: one asking for a label the dispatcher does not
# register with queues forever, with nothing anywhere saying why. So the set is
# asserted where it is defined, and the README quotes the same three.
check "tart-runner registers with the labels the README tells a workflow to ask for" \
  'grep -q "labels=\"\${TART_RUNNER_LABELS:-self-hosted,macOS,tart}\"" \
     "$repo/home/.local/bin/tart-runner" &&
   grep -q "runs-on: \[self-hosted, macOS, tart\]" "$repo/README.md"'

# A VM per job, and no VM at all between them: an image left booted would hold 8
# GB of a 32 GB Mac for a repository that sees a handful of pushes a week.
check "a job's VM is deleted when the job ends" \
  'grep -q "tart delete \"\$current_vm\"" "$repo/home/.local/bin/tart-runner" &&
   grep -q "sweep_stale" "$repo/home/.local/bin/tart-runner"'

# The local run of the suite on a Mac nothing has touched. Read rather than run,
# for the reason above: this is one of the things running inside such a VM.
check "test/tart.sh runs both phases and every assert, in a VM it deletes" \
  '[ -x "$repo/test/tart.sh" ] && "$repo/test/tart.sh" --help | grep -q machine.sh &&
   grep -qF -- "\"./machine.sh\" \"test/assert-machine.sh\"" "$repo/test/tart.sh" &&
   grep -qF -- "\"./claude.sh\" \"test/assert-claude.sh\" \"test/assert.sh\"" "$repo/test/tart.sh" &&
   grep -qF -- "\"./install.sh\" \"test/assert.sh\"" "$repo/test/tart.sh" &&
   grep -qF -- "\"test/boswell-agent.sh\"" "$repo/test/tart.sh" &&
   grep -q "trap cleanup EXIT" "$repo/test/tart.sh" &&
   grep -q "tart delete \"\$vm\"" "$repo/test/tart.sh"'

check "the dispatcher waits rather than crowding UTM's Windows VM" \
  'grep -q "utmctl" "$repo/home/.local/bin/tart-runner" &&
   grep -q "utm_busy" "$repo/home/.local/bin/tart-runner"'

# Tart's default is NAT, where a job — arbitrary code from somebody's default
# branch — reaches this Mac, the LAN and the tailnet. Softnet takes all three
# away and leaves the internet, which is what a checkout and a `brew install`
# need. The tailnet is named outright because 100.64.0.0/10 is carrier-grade NAT
# rather than a private range, and Softnet's rule is phrased the other way round.
check "a job's VM is boxed in by softnet rather than Tart's default NAT" \
  'grep -qF -- "--net-softnet --net-softnet-block=" \
     "$repo/home/.local/bin/tart-runner" &&
   grep -qF -- "softnet_block=\"\${TART_RUNNER_SOFTNET_BLOCK:-100.64.0.0/10}\"" \
     "$repo/home/.local/bin/tart-runner" &&
   grep -qF -- "--net-softnet" "$repo/README.md"'

# Softnet has to be root, and Tart sets the setuid bit for it only when it has a
# terminal to ask for a sudo password at. A LaunchAgent has none, so the
# dispatcher says which command to run rather than clone a VM that `tart run`
# then refuses to start — and rather than quietly going back to NAT. The stub
# softnet is what makes this answer the same on a Mac where the real one has
# already been given the bit.
check "no VM starts while softnet cannot reach root" \
  'h="$(mktemp -d)" && mkdir -p "$h/bin" &&
   printf "#!/bin/bash\nexit 0\n" | tee "$h/bin/tart" >"$h/bin/softnet" &&
   chmod +x "$h/bin/tart" "$h/bin/softnet" &&
   out="$(HOME="$h" PATH="$h/bin:$PATH" \
          "$repo/home/.local/bin/tart-runner" --build-image </dev/null 2>&1)"; rc=$?;
   [ "$rc" -ne 0 ] && printf "%s" "$out" | grep -q "chmod u+s"'

# The references the dispatcher hands to `op`, read off the repo for the reason
# the signing ones are: a reference pointing at the wrong vault is wrong before
# it is ever installed, and a value here would be a GitHub token in public
# history. One line per owner, since a fine-grained token has exactly one
# resource owner.
check "the tart-runner token file is a live symlink holding op references and no secret" \
  '[ -L "$HOME/.config/op/tart-runner.env" ] &&
   [ -e "$HOME/.config/op/tart-runner.env" ] &&
   env_file="$repo/home/.config/op/tart-runner.env" &&
   grep -q "^TART_RUNNER_TOKEN_TIMCHE=\"op://Development/GitHub - tart-runner timche/token\"$" \
     "$env_file" &&
   ! grep -vE "^#|^$|^TART_RUNNER_TOKEN_[A-Z0-9_]+=\"op://" "$env_file"'

# Every question put to GitHub goes through the owner's own fine-grained token.
# A bare `gh api` anywhere in the dispatcher would answer from ~/.config/gh,
# which on this Mac is an OAuth token carrying the `repo` scope — every private
# repository there is, handed to the thing that runs whatever a workflow asks
# for. The public actions-runner release is curl's for the same reason.
check "every GitHub call the dispatcher makes goes through the owner's own token" \
  '! grep -vE "^[[:space:]]*#" "$repo/home/.local/bin/tart-runner" |
     grep -qE "(^|[^_])gh api" &&
   grep -q "GH_TOKEN=\"\${!var}\" gh" "$repo/home/.local/bin/tart-runner"'

# A HOME of its own, with stubs for the three things that reach outside it: a
# runner has no vault, no Tart and no business asking GitHub anything. What is
# being checked is the wiring — that the reference is resolved with the
# service-account token in op's environment, and that what comes back reaches
# `gh` and only `gh`.
tart_runner_home() {
  local h
  h="$(mktemp -d)"

  mkdir -p "$h/.config/op" "$h/bin"
  printf 'stub-service-account\n' >"$h/.config/op/service-account-token"
  printf '%s\n' \
    'TART_RUNNER_TOKEN_TIMCHE="op://Development/GitHub - tart-runner timche/token"' \
    >"$h/.config/op/tart-runner.env"

  cat >"$h/bin/op" <<'STUB'
#!/bin/bash
printf 'op saw %s for %s\n' "${OP_SERVICE_ACCOUNT_TOKEN-unset}" "$2" >>"$HOME/op-calls"
printf 'stub-pat-for-%s\n' "$2"
STUB

  cat >"$h/bin/gh" <<'STUB'
#!/bin/bash
printf '%s\n' "${GH_TOKEN-unset}" >>"$HOME/gh-tokens"
STUB

  printf '#!/bin/bash\nexit 0\n' >"$h/bin/tart"

  chmod +x "$h/bin/op" "$h/bin/gh" "$h/bin/tart"
  printf '%s\n' "$h"
}
export -f tart_runner_home

check "the dispatcher resolves the owner's token from 1Password and spends it on gh alone" \
  'h="$(tart_runner_home)" &&
   HOME="$h" PATH="$h/bin:$PATH" TART_RUNNER_REPOS=timche/mac-mini \
     "$repo/home/.local/bin/tart-runner" --dry-run &&
   grep -q "^op saw stub-service-account for op://Development/GitHub - tart-runner timche/token$" \
     "$h/op-calls" &&
   [ "$(sort -u "$h/gh-tokens")" = "stub-pat-for-op://Development/GitHub - tart-runner timche/token" ]'

# The one thing that must never happen quietly: no reference, and the dispatcher
# carries on with whatever `gh` is logged in as.
check "a repository whose owner has no token reference stops the dispatcher" \
  'h="$(tart_runner_home)" && rm -f "$h/.config/op/tart-runner.env" &&
   out="$(HOME="$h" PATH="$h/bin:$PATH" TART_RUNNER_REPOS=timche/mac-mini \
          "$repo/home/.local/bin/tart-runner" --dry-run 2>&1)"; rc=$?;
   [ "$rc" -ne 0 ] && [ ! -e "$h/gh-tokens" ] &&
   printf "%s" "$out" | grep -q TART_RUNNER_TOKEN_TIMCHE'

# The one LaunchAgent that stays in the checkout. launchd loads everything in
# ~/Library/LaunchAgents at login and this job has RunAtLoad and KeepAlive, so
# the link is the switch: mise.toml must not make it, and install.sh reads it.
# The plist is read where it lives, since on a Mac that is off there is nothing
# in ~/Library/LaunchAgents to read.
export tart_plist="$repo/home/Library/LaunchAgents/io.github.timche.tart-runner.plist"
export tart_link="$HOME/Library/LaunchAgents/io.github.timche.tart-runner.plist"
export tart_label=io.github.timche.tart-runner

check "the tart-runner agent is a valid plist in the checkout" \
  '[ -f "$tart_plist" ] && plutil -lint "$tart_plist" &&
   [ "$(plutil -extract Label raw "$tart_plist")" = io.github.timche.tart-runner ]'
check "nothing links the tart-runner agent into ~/Library/LaunchAgents" \
  '! grep -q "LaunchAgents/io.github.timche.tart-runner.plist" "$repo/mise.toml"'
# Both directions, so this passes on a Mac Tim has switched on as well as on the
# default one and on a runner, where there is no gui domain to load anything in.
check "the tart-runner agent is loaded exactly when its plist is linked" \
  'if [ -e "$tart_link" ]; then
     launchctl print "gui/$(id -u)/$tart_label" >/dev/null 2>&1
   else
     ! launchctl print "gui/$(id -u)/$tart_label" >/dev/null 2>&1
   fi'
check "the tart-runner agent leaves its paths to a shell" \
  '[ "$(plutil -extract ProgramArguments.0 raw -o - "$tart_plist")" = "/bin/sh" ] &&
   [ "$(plutil -extract ProgramArguments.1 raw -o - "$tart_plist")" = "-c" ]'
check "the tart-runner agent runs the dispatcher and logs to ~/Library/Logs" \
  'cmd="$(plutil -extract ProgramArguments.2 raw -o - "$tart_plist")" &&
   printf "%s" "$cmd" | grep -q "exec \"\$HOME/.local/bin/tart-runner\"" &&
   printf "%s" "$cmd" | grep -q "\$HOME/Library/Logs/tart-runner.log" &&
   printf "%s" "$cmd" | grep -q "/opt/homebrew/bin"'
check "the tart-runner agent polls from load and is always kept alive" \
  'plutil -extract RunAtLoad xml1 -o - "$tart_plist" | grep -q "<true/>" &&
   [ "$(plutil -extract KeepAlive raw -o - "$tart_plist")" = true ] &&
   [ "$(plutil -extract ThrottleInterval raw -o - "$tart_plist")" = 60 ]'

check "install.sh loads the tart-runner agent only when its plist is linked, and spares a running job" \
  'grep -q "launchctl bootstrap \"gui/\$uid\" \"\$tart_plist\"" "$repo/install.sh" &&
   grep -q "cmp -s \"\$tart_plist\" \"\$tart_loaded\"" "$repo/install.sh" &&
   grep -q "would kill the CI job it is running" "$repo/install.sh" &&
   grep -qF "nothing answers a queued CI job until its plist is" "$repo/install.sh" &&
   grep -qF "whose plist is no longer linked" "$repo/install.sh"'

check ".env.1password is a live symlink that names the token file, not the token" \
  '[ -L "$HOME/.env.1password" ] && [ -e "$HOME/.env.1password" ] &&
   grep -q "^OP_TOKEN=exec(\`cat ~/.config/op/service-account-token\`)$" "$HOME/.env.1password"'

check "DO_NOT_TRACK is set in every shell" \
  '[ "$(zsh -c "print -r -- \$DO_NOT_TRACK")" = 1 ] &&
   [ "$(bash -ic "printf %s \"\$DO_NOT_TRACK\"" 2>/dev/null)" = 1 ]'

# CLAUDE.md is loaded whole into every session and adherence drops past about
# 200 lines of ordinary markdown. Counted in words, since a paragraph here is one
# line however long: 2,400 is 200 lines at the dozen words a wrapped line holds.
# What reaches the context, not what is in the file: YAML frontmatter is
# configuration and block-level HTML comments are stripped before injection, so
# counting either would charge rent on words Claude never sees. Computed here
# rather than inside the snippet, which runs through two levels of quoting.
export loaded_words="$(
  cat "$HOME/.claude/CLAUDE.md" 2>/dev/null |
    awk '/^---$/ { fm = !fm; next } fm { next }
         /<!--/ { c = 1 } c { if (/-->/) c = 0; next }
         { print }' |
    wc -w
)"

check "the always-loaded instructions stay under 2,400 words" \
  '[ "$loaded_words" -lt 2400 ]'

# Markdown prose is never hard-wrapped: a paragraph or list item is one line.
# Reports each line that continues the one before it, which is exactly what an
# unwrap would join; code fences, tables, headings, frontmatter and hard breaks
# are left alone. The synced skills are Anthropic's and written their own way.
export wrapped_prose="$(
  find "$repo" -name '*.md' -not -path '*/.git/*' -not -path '*/synced/*' \
    -exec awk '
      function starts_block(s) {
        return s ~ /^[ \t]*$/ || s ~ /^[ \t]*[#|><]/ || s ~ /^[ \t]*(```|~~~)/ ||
               s ~ /^[ \t]*([-*+]|[0-9]+[.)])[ \t]/ || s ~ /^[ \t]*(---+|\*\*\*+|___+)[ \t]*$/
      }
      FNR == 1 { fm = ($0 == "---"); prev = ""; fence = 0; if (fm) next }
      fm { if ($0 == "---") fm = 0; next }
      /^[ \t]*(```|~~~)/ { fence = !fence; prev = ""; next }
      fence { next }
      {
        if (prev != "" && prev !~ /(  |\\)$/) {
          if (prev ~ /^>/) {
            body = $0; sub(/^> ?/, "", body)
            if ($0 ~ /^>/ && !starts_block(body)) { print FILENAME ":" FNR; found = 1 }
          } else if (!starts_block($0)) { print FILENAME ":" FNR; found = 1 }
        }
        prev = ($0 ~ /^[ \t]*$/ || $0 ~ /^[ \t]*[#|<]/ ||
                $0 ~ /^[ \t]*(---+|\*\*\*+|___+)[ \t]*$/) ? "" : $0
      }
      END { exit found }
    ' {} + 2>/dev/null
)"

check "markdown prose is not hard-wrapped" '[ -z "$wrapped_prose" ]'

# Finding the docs is a mechanism rather than a rule Claude has to remember, so
# the wiring is what makes the global CLAUDE.md's docs section true.
check "the project-docs hook is wired to SessionStart" \
  'grep -q "hooks/project-docs.sh" "$HOME/.claude/settings.json"'

# A repository with no docs folder has to stay silent, or every session on
# every other project starts with an apology. A bare repository of its own,
# since this one carries docs/ now and ~/projects/docs may hold a folder for anything.
check "the project-docs hook says nothing without a docs folder" \
  'bare="$(mktemp -d)" && git -C "$bare" init -q &&
   git -C "$bare" remote add origin https://example.com/nobody/no-docs-here.git &&
   [ -z "$(cd "$bare" && bash "$HOME/.claude/hooks/project-docs.sh")" ]'

# ~/projects/docs/<repo> and nothing else: a checkout's own docs/ is the project's
# published documentation rather than its working docs. Run against a throwaway
# HOME, so the check does not leave a folder in the real one for boswell to
# publish.
check "the project-docs hook reads ~/projects/docs/<repo>, not the checkout's docs folder" \
  'hook="$HOME/.claude/hooks/project-docs.sh" &&
   h="$(mktemp -d)" && d="$(mktemp -d)" && git -C "$d" init -q &&
   git -C "$d" remote add origin https://example.com/nobody/has-docs.git &&
   mkdir "$d/docs" && : > "$d/docs/README.md" &&
   mkdir -p "$h/projects/docs/has-docs" && : > "$h/projects/docs/has-docs/decisions.md" &&
   [ "$(cd "$d" && HOME="$h" PROJECT_DOCS_DIR="$h/projects/docs" bash "$hook" | head -1)" = \
     "Working docs for has-docs: $h/projects/docs/has-docs" ]'

# The name comes from the remote, not the directory: a worktree is named for
# its branch, and every branch of a repository shares one docs folder.
check "the project-docs hook resolves the repo name from the remote" \
  'grep -q "remote get-url origin" "$HOME/.claude/hooks/project-docs.sh"'

# The path and the shape, nothing about what the docs are or how they sync:
# the first differs per project and is its README's to say, the second is the
# same everywhere and is the global CLAUDE.md's.
check "the project-docs hook asserts nothing about the docs themselves" \
  '! grep -qiE "settled decisions|architecture as it stands|synced by a timer|timche/docs" \
     "$HOME/.claude/hooks/project-docs.sh"'

# Every line it prints is paid for on every session start, forever. The path
# and the shape are facts nothing else carries; an instruction to read them is
# already in the global CLAUDE.md and would be rent on a restatement. Counted
# in the script rather than by running it: a runner has neither a checkout with
# docs nor the docs themselves, so a runtime check would pass by printing nothing
# and prove nothing.
check "the project-docs hook prints one line of prose, not a paragraph" \
  'test "$(grep -c "^echo \"[A-Z]" "$HOME/.claude/hooks/project-docs.sh")" -eq 1'

if [ "$failures" -gt 0 ]; then
  echo "  $failures check(s) failed"
  exit 1
fi
