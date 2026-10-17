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
            .config/git/strip-claude-attribution \
            .config/git/refuse-claude-attribution \
            .config/herdr/config.toml .config/starship.toml \
            .terminfo/x/xterm-ghostty .terminfo/78/xterm-ghostty \
            .config/hachiko/sync; do
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
check "herdr resolves to its own installer's copy in every zsh mode" \
  'for mode in "" -i -l; do
     [ "$(PATH=$stock_path zsh $mode -c "whence -p herdr")" = \
       "$HOME/.local/bin/herdr" ] || exit 1
   done'
check "herdr runs"          '"$HOME/.local/bin/herdr" --version'
check "interactive zsh has zoxide's z" \
  'PATH=$stock_path zsh -ic "whence -w z" | grep -q "z: function"'
check "oh-my-zsh present"   '[ -d "$HOME/.oh-my-zsh" ]'
check "starship runs"       'starship --version'

# Machine-wide: one proxy holds port 443, and one CA signs for every .localhost
# name.
check "portless runs" 'portless --version'
check "portless is mise's npm install" 'mise which portless | grep -q "/installs/npm-portless/"'

# By OU, because the original authority has the same common name.
check "Apple's G2 Developer ID intermediate is in a keychain" \
  'security find-certificate -a -p -c "Developer ID Certification Authority" |
     /usr/bin/openssl crl2pkcs7 -nocrl -certfile /dev/stdin |
     /usr/bin/openssl pkcs7 -print_certs -noout |
     grep -qE "^subject=.*OU ?= ?G2([,/]|\$)"'

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
for formula in fd ffmpeg glow ripgrep shellcheck starship tuicr zoxide betterleaks; do
  check "$formula is Homebrew's" \
    "brew list --formula --full-name | grep -qxE '(.*/)?$formula'"
done

# Read off the repo rather than the machine: the rule is that mise carries the
# language runtimes and the Brewfile carries the Homebrew packages, and a `brew:`
# entry creeping back into mise.toml is how that stops being true. betterleaks is
# the one that proves the tap, which mise's bootstrap cannot fetch from at all.
check "mise.toml declares no Homebrew package" \
  '! grep -qE "^\"brew(-cask)?:" "$repo/mise.toml"'

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
# mise answers for its own half; install.sh's guards are the four reads above.
check "a second pass over the declared defaults has nothing to write" \
  'mise -C "$repo" bootstrap macos defaults status --missing'

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

# Both hooks in .gitconfig are defined in config rather than in .git/hooks, which
# git learned to run in 2.54: an older git reads those sections and runs neither,
# so every commit goes unscanned and every new worktree uninstalled with nothing
# saying so. Apple's git answers `git version 2.54.0 (Apple Git-157)`, so the
# number is what is left once the prefix and anything after a space are cut off.
check "git is new enough to run the hooks defined in config" \
  'v="$(git --version)"; v="${v#git version }"; v="${v%% *}";
   major="${v%%.*}"; rest="${v#*.}"; minor="${rest%%.*}";
   [ "$major" -gt 2 ] || { [ "$major" -eq 2 ] && [ "$minor" -ge 54 ]; }'

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

# The key is a file git finds through the `~` it expands itself, and nothing asks
# an agent for one. Read out of the tracked file rather than the effective config,
# which a machine-local override is allowed to disagree with.
export tracked="$repo/home/.gitconfig"

check "the tracked config names the signing key file" \
  '[ "$(git config --file "$tracked" --get user.signingkey)" = "~/.ssh/id_signing.pub" ]'
check "the tracked config asks no agent for a key" \
  '! git config --file "$tracked" --get gpg.ssh.defaultKeyCommand'
check "the tracked config includes the machine-local override" \
  '[ "$(git config --file "$tracked" --get include.path)" = "~/.gitconfig.local" ]'

# The effective config too, since the machine-local override is untracked and
# written by hand: a key named there would be signed with in place of this one.
check "nothing outside the tracked config names a signing key" \
  '[ "$(git config --get user.signingkey)" = "~/.ssh/id_signing.pub" ]'

# The key itself, which only a Mac with a 1Password service account has:
# claude/signing-key.sh writes both halves and skips where there is no token.
export signing_key="$HOME/.ssh/id_signing"

if [ -f "$signing_key" ]; then
  # Anything running as the account can read it, which is the whole of what bounds
  # it; nobody else may.
  check "the directory holding the signing key is the account's alone" \
    '[ "$(stat -f %Lp "$HOME/.ssh")" = 700 ]'
  check "the signing key is a plain file only the account can read" \
    '[ ! -L "$signing_key" ] && [ "$(stat -f %Lp "$signing_key")" = 600 ] &&
     [ "$(stat -f %Su "$signing_key")" = "$(id -un)" ]'
  check "the public half beside it is the same key" \
    '[ "$(ssh-keygen -y -f "$signing_key" | awk "{print \$1\" \"\$2}")" = \
       "$(awk "{print \$1\" \"\$2}" "$signing_key.pub")" ]'
  check "a commit signs with it and verifies against the trust list" \
    'd="$(mktemp -d)" && git -C "$d" init -q &&
     git -C "$d" commit --allow-empty -q -m signed &&
     [ "$(git -C "$d" log --format=%G? -1)" = G ]'
else
  echo "  skip  the signing key (no 1Password service account on this machine)"
fi

# git asks no agent for a key, so a job still loading one into an ssh-agent would be
# a private key in memory that nothing reads.
check "no ssh-agent LaunchAgent of this repository's is loaded" \
  '! launchctl print "gui/$(id -u)/io.github.timche.ssh-agent" >/dev/null 2>&1'

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
check "a commit loses Claude's attribution and keeps a person's co-author" \
  'd="$(mktemp -d)" && git init -q "$d" && echo a >"$d/f" && git -C "$d" add f &&
   git -C "$d" -c user.email=t@example.com -c user.name=t -c commit.gpgsign=false \
     commit -q -m x -m "Co-Authored-By: Claude <noreply@anthropic.com>
Co-authored-by: Jane <jane@example.com>" &&
   msg="$(git -C "$d" log -1 --format=%B)" && rm -rf "$d" &&
   ! echo "$msg" | grep -qi anthropic && echo "$msg" | grep -q "Jane"'

check "a push of a commit carrying Claude's attribution is refused" \
  'd="$(mktemp -d)" && git init -q --bare "$d/r.git" && git init -q "$d/w" &&
   echo a >"$d/w/f" && git -C "$d/w" add f &&
   git -C "$d/w" -c user.email=t@example.com -c user.name=t -c commit.gpgsign=false \
     commit -q --no-verify -m x -m "Co-Authored-By: Claude <noreply@anthropic.com>" &&
   ! git -C "$d/w" push -q "$d/r.git" HEAD:main 2>/dev/null; r=$?; rm -rf "$d"; exit $r'

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

# Auto-sync is one `hachiko sync` daemon watching every repository its config lists:
# the project docs, and this repository's `home/.claude` alone. Its own wiring is checked

# Claude Code updates itself, which is why it is not in the tool list at all:
# something that pinned it would have to turn the updater off, and then the pin
# would be the only thing keeping it current.
check "nothing disables Claude Code's updater" \
  '[ -f "$HOME/.claude/settings.json" ] &&
   ! grep -q DISABLE_AUTOUPDATER "$HOME/.claude/settings.json"'

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
  '[ -f "$HOME/.claude/settings.json" ] &&
   ! grep -qE "git-sync|dotfiles-sync" "$HOME/.claude/settings.json"'

# Nothing under home/ spells a home directory out, and mise.toml is in it because
# a dotfile entry there could render one in. The account differs between this Mac
# and a runner, and a path written for one is a hook that never fires for the
# other, silently. The synced skills are the
# account's claude.ai copies rather than this repo's, and gitignored.
check "no absolute home paths under home/ or in mise.toml" \
  '[ -d "$repo/home" ] && [ -f "$repo/mise.toml" ] &&
   ! grep -rhoE --exclude-dir=synced "/(home|Users)/[A-Za-z0-9_.-]+" \
       "$repo/home" "$repo/mise.toml" | grep -q .'

check "the scripts the hooks call are executable" \
  '[ -x "$HOME/.claude/hooks/herdr-agent-state.sh" ] &&
   [ -x "$HOME/.claude/hooks/herdr-tab-reset.sh" ] &&
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


check "the worktree-info hook prints the Electron profile rule" \
  'd="$(mktemp -d)" &&
   worktree_fixture "$d" "" "\"electron\": \"^44.0.0\"" &&
   worktree_info "$d/wt/fix-ui" | grep -q "DevToolsActivePort"'

# The one gc check a Go test cannot write: a real compose project on a real
# daemon, which is what proves the labels `hachiko gc` reads are the labels
# compose actually sets. Everything else the sweep decides is `go test ./...` in
# hachiko/, against injected seams.
#
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

check "window-shot is a live symlink and prints its usage" \
  '[ -L "$HOME/.local/bin/window-shot" ] && [ -x "$HOME/.local/bin/window-shot" ] &&
   window-shot --help 2>&1 | grep -q "usage: window-shot <owner> <out.png>"'
check "window-shot refuses a missing output path" \
  'window-shot Finder; [ $? -eq 2 ]'
check "window-shot exits 1 and says so when the owner has no window, writing nothing" \
  'out="$(mktemp -d)/shot.png" &&
   ! err="$(window-shot "no-such-owner-$$" "$out" 2>&1)" &&
   printf "%s" "$err" | grep -q "no on-screen window owned by no-such-owner-$$" &&
   [ ! -e "$out" ]'

# A machine with neither docker nor a daemon cannot answer this one either way,
# and saying so is better than a check that passes because nothing happened.
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  check "hachiko gc --dry-run sweeps a removed worktree's compose project and nothing else" \
    'h="$(mktemp -d)" && gone="$h/.herdr/worktrees/app/gone" &&
     gc_compose_fixture "$gone" gc-gone &&
     gc_compose_fixture "$h/.herdr/worktrees/app/kept" gc-kept &&
     gc_compose_fixture "$h/elsewhere" gc-elsewhere &&
     rm -rf "$gone" "$h/elsewhere" &&
     sock="$(docker context inspect -f "{{.Endpoints.docker.Host}}")" &&
     out="$(HACHIKO_CACHE_DIR="$HOME/Library/Caches/hachiko" HOME="$h" DOCKER_HOST="$sock" hachiko gc --dry-run)" &&
     docker compose -p gc-gone down -v >/dev/null 2>&1
     docker compose -p gc-kept down -v >/dev/null 2>&1
     docker compose -p gc-elsewhere down -v >/dev/null 2>&1
     printf "%s" "$out" | grep -q "would remove compose project gc-gone" &&
     ! printf "%s" "$out" | grep -q gc-kept &&
     ! printf "%s" "$out" | grep -q gc-elsewhere'

  # The daily prune against a real daemon, which is where the age could be a filter docker
  # refuses: the dry run names the two commands as they would be run, and a state directory of
  # its own under the throwaway HOME is what makes the prune due.
  check "hachiko gc --dry-run says what the daily prune would run, and prunes nothing" \
    'h="$(mktemp -d)" &&
     sock="$(docker context inspect -f "{{.Endpoints.docker.Host}}")" &&
     out="$(HACHIKO_CACHE_DIR="$HOME/Library/Caches/hachiko" HOME="$h" DOCKER_HOST="$sock" hachiko gc --dry-run)" &&
     printf "%s" "$out" | grep -qF "would run docker builder prune --force --filter until=168h" &&
     printf "%s" "$out" | grep -qF "would run docker image prune --force --filter until=168h" &&
     ! find "$h" -name state.json | grep -q .'
else
  echo "  skip  hachiko gc's compose sweep (no docker daemon on this machine)"
fi

# The herdr server has to come from launchd in the GUI session, or every pane is an
# SSH session and Claude Code withholds computer use from it.
export herdr_plist="$HOME/Library/LaunchAgents/io.github.timche.herdr.plist"
check "the herdr agent is a live symlink and a valid plist" \
  '[ -L "$herdr_plist" ] && [ -e "$herdr_plist" ] && plutil -lint "$herdr_plist" &&
   [ "$(plutil -extract Label raw "$herdr_plist")" = io.github.timche.herdr ]'
check "the herdr agent runs the launcher and is always kept alive" \
  'plutil -extract ProgramArguments.2 raw "$herdr_plist" |
     grep -qF "exec \"\$HOME/.local/libexec/herdr-launcher\" server" &&
   [ "$(plutil -extract KeepAlive raw "$herdr_plist")" = true ]'
# The launcher is what macOS holds responsible for the grants herdr needs — Screen
# Recording, Full Disk Access and Device Control and Data Access — and a grant is
# pinned to the exact contents of an ad-hoc signed binary. So its bytes may move
# only when its source does: a real file at a fixed path, built from a hash of that
# source, and never from the build-in-place wrapper the Go tools use, whose rebuild
# depends on whatever go mise resolved that day.
export launcher="$HOME/.local/libexec/herdr-launcher"
export launcher_src="$repo/launchd/herdr-launcher.c"

check "the launcher is a real executable at the path the plist names" \
  '[ -x "$launcher" ] && [ ! -L "$launcher" ] && [ -f "$launcher" ]'
check "the launcher was built from the source in this checkout" \
  'stamp="${XDG_STATE_HOME:-$HOME/.local/state}/mac-mini/herdr-launcher.sha256" &&
   [ "$(cat "$stamp")" = "$(shasum -a 256 "$launcher_src" | cut -d" " -f1)" ]'
# Nothing may rebuild it on a schedule or behind a wrapper: that is the one way the
# grants go away again without anybody deciding to. mise declares no link to it and
# there is no wrapper in ~/.local/bin, so install.sh is the only thing that compiles it.
check "no tool list or wrapper builds the launcher" \
  '! grep -q "herdr-launcher" "$repo/mise.toml" &&
   [ ! -e "$repo/home/.local/bin/herdr-launcher" ]'

# What the launcher has to be, built from the source rather than taken from the
# installed copy, so the check is of this checkout: the parent of herdr and not a
# process that execs it, a forwarder of the signals launchd and a restart by hand
# send, and transparent in what it exits with. HOME points at a fixture tree,
# because the launcher finds herdr under HOME and nothing else.
herdr_launcher_fixture() {
  local d="$1"

  mkdir -p "$d/home/.local/bin" || return 1
  clang -O2 -o "$d/launcher" "$launcher_src" || return 1
  clang -O2 -o "$d/responsible" "$repo/test/responsible.c" || return 1
}
export -f herdr_launcher_fixture

# A signal the launcher does not pass on is a server that outlives `launchctl
# bootout` and a KeepAlive that restarts it beside the old one. The fixture exits
# 42 of its own accord, so an exit status that is not forwarded shows up too.
herdr_launcher_signal() {
  local d="$1" sig="$2" pid status waited=0

  cat >"$d/home/.local/bin/herdr" <<EOF
#!/bin/sh
trap 'exit 42' $sig
echo running >"$d/running"
while :; do sleep 0.2; done
EOF
  chmod +x "$d/home/.local/bin/herdr"
  rm -f "$d/running"

  HOME="$d/home" "$d/launcher" server >/dev/null 2>&1 &
  pid=$!

  while [ ! -f "$d/running" ]; do
    [ "$waited" -lt 100 ] || { kill "$pid" 2>/dev/null; return 1; }
    sleep 0.1
    waited=$((waited + 1))
  done

  kill "-$sig" "$pid"
  wait "$pid"
  status=$?

  [ "$status" = 42 ]
}
export -f herdr_launcher_signal

check "the launcher forwards the signals launchd sends and exits as herdr exited" \
  'd="$(mktemp -d)" && herdr_launcher_fixture "$d" &&
   herdr_launcher_signal "$d" TERM &&
   herdr_launcher_signal "$d" INT &&
   herdr_launcher_signal "$d" HUP'

# The one check that is the arrangement rather than a description of it. Under
# launchd a job's own process is its own TCC-responsible process, and a child
# inherits that unless it disclaims — so the fixture herdr, which is the launcher's
# child, has to report the launcher. `launchctl submit` is the smallest way to get
# that shape, under a label of its own and removed afterwards whatever happens.
#
# $PPID in the fixture and the responsible pid the binary reads are the two halves:
# equal to each other and to the launcher means the launcher both stayed the parent
# and is what macOS will ask about Screen Recording. The fixture tree is resolved
# with pwd -P, because the kernel answers with the real path and a temporary
# directory on a Mac is under a symlink.
herdr_launcher_responsible() {
  local d="$1" label="$2" waited=0

  cat >"$d/home/.local/bin/herdr" <<EOF
#!/bin/sh
"$d/responsible"
echo "args=\$* ppid=\$PPID"
EOF
  chmod +x "$d/home/.local/bin/herdr"

  launchctl submit -l "$label" -o "$d/out" -e "$d/err" -- \
    /usr/bin/env "HOME=$d/home" "$d/launcher" server || return 1

  while [ "$waited" -lt 40 ]; do
    grep -q "ppid=" "$d/out" 2>/dev/null && break
    sleep 0.5
    waited=$((waited + 1))
  done

  launchctl remove "$label" 2>/dev/null
  grep -q "ppid=" "$d/out" 2>/dev/null
}
export -f herdr_launcher_responsible

check "macOS holds the launcher responsible for the herdr it starts under launchd" \
  'd="$(cd "$(mktemp -d)" && pwd -P)" && herdr_launcher_fixture "$d" &&
   label="io.github.timche.herdr-launcher.assert.$$" &&
   herdr_launcher_responsible "$d" "$label" &&
   grep -q "args=server " "$d/out" &&
   responsible="$(sed -n "s/.*responsible_pid=\([0-9]*\) .*/\1/p" "$d/out")" &&
   [ -n "$responsible" ] &&
   [ "$responsible" = "$(sed -n "s/.*ppid=\([0-9]*\).*/\1/p" "$d/out")" ] &&
   grep -q "responsible_path=$d/launcher$" "$d/out"'

# The sweep is on a timer rather than on a session, so the agent is the whole of
# its wiring: an invalid plist is a job launchd rejects at load with nothing in
# it to say why. Not reachable on a runner with no GUI session, so this reads the
# file rather than loading the job.
export gc_plist="$HOME/Library/LaunchAgents/io.github.timche.hachiko-gc.plist"

check "the gc agent is a live symlink and a valid plist" \
  '[ -L "$gc_plist" ] && [ -e "$gc_plist" ] && plutil -lint "$gc_plist" &&
   [ "$(plutil -extract Label raw "$gc_plist")" = io.github.timche.hachiko-gc ]'
# launchd expands neither ~ nor $HOME in a plist, so every path this job needs
# belongs to a shell — the one thing it hands a HOME to. No WatchPaths either,
# which is the one key that would have to arrive absolute: it is not recursive,
# so a worktree two levels under the root would be invisible to it, and the
# interval is what fires on a removal.
check "the gc agent runs hachiko gc through a shell and logs to ~/Library/Logs" \
  '[ "$(plutil -extract ProgramArguments.0 raw -o - "$gc_plist")" = "/bin/sh" ] &&
   [ "$(plutil -extract ProgramArguments.1 raw -o - "$gc_plist")" = "-c" ] &&
   plutil -extract ProgramArguments.2 raw -o - "$gc_plist" |
     grep -qF "exec \"\$HOME/.local/bin/hachiko\" gc >>\"\$HOME/Library/Logs/hachiko-gc.log\"" &&
   ! plutil -extract StandardOutPath raw -o - "$gc_plist" &&
   ! plutil -extract StandardErrorPath raw -o - "$gc_plist" &&
   ! plutil -extract EnvironmentVariables xml1 -o - "$gc_plist" &&
   ! plutil -extract WatchPaths xml1 -o - "$gc_plist"'
check "the gc agent sweeps at load" \
  'plutil -extract RunAtLoad xml1 -o - "$gc_plist" | grep -q "<true/>"'
check "the gc agent sweeps every ten minutes" \
  '[ "$(plutil -extract StartInterval raw -o - "$gc_plist")" = 600 ]'
# launchd hands a job almost no PATH, and this one shells out to docker.
check "the gc agent's PATH reaches docker" \
  'plutil -extract ProgramArguments.2 raw -o - "$gc_plist" |
     grep -qF "export PATH=\"\$HOME/.local/bin:/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:"'


# `hachiko sync`, which commits and pushes the repositories its config lists. It is a
# long-running agent rather than a timer, so the plist is the whole of what assert.sh
# can read; sync-agent.sh is the one that makes launchd load it.
export sync_plist="$HOME/Library/LaunchAgents/io.github.timche.hachiko-sync.plist"

check "the sync agent is a link into the checkout and a valid plist" \
  '[ -L "$sync_plist" ] && [ -e "$sync_plist" ] && plutil -lint "$sync_plist" &&
   [ "$sync_plist" -ef "$repo/home/Library/LaunchAgents/io.github.timche.hachiko-sync.plist" ] &&
   ! grep -q "{{" "$sync_plist"'

# launchd expands neither ~ nor $HOME in a plist, so every path this job needs belongs
# to a shell — the one thing it hands a HOME to. No WatchPaths either, which is the one
# key that would have to arrive absolute: sync polls `git status` itself.
check "the sync agent runs hachiko sync through a shell, with no key launchd leaves unexpanded" \
  '[ "$(plutil -extract ProgramArguments.0 raw -o - "$sync_plist")" = "/bin/sh" ] &&
   [ "$(plutil -extract ProgramArguments.1 raw -o - "$sync_plist")" = "-c" ] &&
   plutil -extract ProgramArguments.2 raw -o - "$sync_plist" |
     grep -qF "exec \"\$HOME/.local/bin/hachiko\" sync" &&
   ! plutil -extract StandardOutPath raw -o - "$sync_plist" &&
   ! plutil -extract StandardErrorPath raw -o - "$sync_plist" &&
   ! plutil -extract EnvironmentVariables xml1 -o - "$sync_plist" &&
   ! plutil -extract WatchPaths xml1 -o - "$sync_plist"'

# It reads no rc file, so the PATH is the shell's to export: launchd hands a job almost
# none, and this one shells out to git on every pass and to the `claude` in
# ~/.local/bin for the subject of each commit. Its commits are signed from the key
# ~/.gitconfig names, which needs nothing in the environment.
check "the sync agent reaches git and claude" \
  'c="$(plutil -extract ProgramArguments.2 raw -o - "$sync_plist")" &&
   printf "%s\n" "$c" | grep -qF "export PATH=\"\$HOME/.local/bin:" &&
   printf "%s\n" "$c" | grep -qF ":/usr/bin:/bin:" &&
   printf "%s\n" "$c" | grep -qF ">>\"\$HOME/Library/Logs/hachiko-sync.log\" 2>&1"'

# It exits when a repository stops being synced and when the wrapper has built a new
# binary behind it, which only helps if something brings it back — and only on a
# failure, so one that was told to stop stays stopped.
check "a sync that exits is restarted, after a wait" \
  'plutil -extract RunAtLoad xml1 -o - "$sync_plist" | grep -q "<true/>" &&
   plutil -extract KeepAlive.SuccessfulExit xml1 -o - "$sync_plist" | grep -q "<false/>" &&
   [ "$(plutil -extract ThrottleInterval raw -o - "$sync_plist")" = 10 ]'

# The project docs whole, and this repository limited to `home/.claude` — the files Tim,
# his tools and Claude Code write through the `~/.claude` symlink, which no session sets
# out to change and so nothing would commit. The rest of this repository is a session's,
# so the `paths =` line is the whole of what makes a daemon on it safe: without it one
# would commit a half-written script behind a session mid-edit. The docs clone is the one
# install.sh makes rather than the one it runs from, which is why the load is gated on it.
check "the sync config names the docs whole and this repository's home/.claude alone, and is live" \
  'grep -qx "mode = live" "$HOME/.config/hachiko/sync" &&
   grep -qx "repo = ~/projects/docs" "$HOME/.config/hachiko/sync" &&
   grep -qx "repo = ~/.mac-mini" "$HOME/.config/hachiko/sync" &&
   grep -qx "paths = home/.claude" "$HOME/.config/hachiko/sync" &&
   grep -qx "debounce = 2m" "$HOME/.config/hachiko/sync" &&
   grep -qx "subject_model = haiku" "$HOME/.config/hachiko/sync" &&
   [ "$(grep -c "^repo = " "$HOME/.config/hachiko/sync")" = 2 ] &&
   [ "$(grep -c "^paths = " "$HOME/.config/hachiko/sync")" = 1 ] &&
   ! grep -q "/Users/" "$HOME/.config/hachiko/sync"'

# hachiko, the one compiled tool here. Its behaviour is `go test ./...` in hachiko/,
# which drives the whole of the decision-making against injected seams; what is left
# for this script is the wiring a Go test cannot see — the links, the agent, and the
# wrapper that builds the binary.
check "hachiko is a live symlink and runs through its wrapper" \
  '[ -L "$HOME/.local/bin/hachiko" ] && [ -x "$HOME/.local/bin/hachiko" ] &&
   hachiko --help | grep -q "dry-run" &&
   hachiko --help | grep -q "notify" &&
   hachiko --help | grep -q "oncall" &&
   hachiko --help | grep -q "gc" &&
   hachiko --help | grep -q "sync" &&
   hachiko --help | grep -q "status"'

# The one command here nothing on a timer runs, and the only one a Go test cannot prove
# anything about: it reads this Mac's own launchd, volume, repositories and logs, and what
# matters is that it reads and nothing else. A state directory of its own, empty before and
# after, is what says so — the real one is rewritten by the watch every five minutes and could
# not tell a write of this command's from a sweep's.
check "hachiko status prints one screen of this Mac and writes nothing" \
  'd="$(mktemp -d)" &&
   out="$(HACHIKO_STATE_DIR="$d" hachiko status)" &&
   for section in Agents Disk Incidents Sync Logs; do
     printf "%s\n" "$out" | grep -qx "$section" || exit 1
   done &&
   printf "%s\n" "$out" | grep -q "the watch" &&
   printf "%s\n" "$out" | grep -q "stale after" &&
   [ -z "$(ls -A "$d")" ]'

# A copy of the checkout rather than the checkout, because the test below has to break a
# test on purpose and the wrapper builds from the working tree, so a broken source in the
# real one reaches the agents running off it. The wrapper finds the repository through its
# own resolved path, so a copy with the wrapper in it is a whole second machine as far as
# it is concerned.
#
# The whole of home/ comes with it, because the tests read the files this repo ships —
# the sync config among them — through their own relative path out of hachiko/.
# MISE_TRUSTED_CONFIG_PATHS rather than `mise trust`, which would leave a temp folder in
# the machine's trust store on every run.
gate_fixture() {
  local dir="$1"

  cp "$repo/mise.toml" "$repo/mise.lock" "$dir/"
  cp -R "$repo/hachiko" "$dir/hachiko"
  cp -R "$repo/home" "$dir/home"
}
export -f gate_fixture

# The gate is the whole of what stands between an edit and the agents running it: what
# lands in this repository is main with no review behind it, and the wrapper is what
# refuses to install a tree whose tests do not pass.
check "a build whose tests fail leaves the binary that was already there" \
  'd="$(mktemp -d)" && gate_fixture "$d" &&
   export MISE_TRUSTED_CONFIG_PATHS="$d" HACHIKO_CACHE_DIR="$d/cache" &&
   "$d/home/.local/bin/hachiko" --help >/dev/null &&
   was="$(shasum -a 256 "$d/cache/hachiko" | cut -d" " -f1)" &&
   printf "package wording\n\nimport \"testing\"\n\nfunc TestTheGateMustCatchThis(t *testing.T) { t.Fatal(\"on purpose\") }\n" \
     > "$d/hachiko/internal/wording/zz_gate_test.go" &&
   out="$("$d/home/.local/bin/hachiko" --help 2>&1 >/dev/null)" &&
   printf "%s" "$out" | grep -q "do not pass" &&
   [ "$was" = "$(shasum -a 256 "$d/cache/hachiko" | cut -d" " -f1)" ] &&
   ! ls "$d/cache"/hachiko.* >/dev/null 2>&1'

# Beside the wrapper because that is where the wrapper looks: `op run --env-file` is
# given the path next to the script's own resolved location, so a missing link is an
# alert that was not sent rather than one sent somewhere else.
check "hachiko's secrets are op:// references beside it, and nothing resolved" \
  '[ -L "$HOME/.local/bin/hachiko.env.op" ] &&
   [ -e "$HOME/.local/bin/hachiko.env.op" ] &&
   grep -qx "HACHIKO_DISCORD_BOT_TOKEN=op://dev/hachiko-discord/bot token" \
     "$HOME/.local/bin/hachiko.env.op" &&
   grep -qx "HACHIKO_APPROVAL_TOTP=op://dev/hachiko-discord/approval" \
     "$HOME/.local/bin/hachiko.env.op" &&
   ! grep -qE "^[A-Z_]+=[^o]" "$HOME/.local/bin/hachiko.env.op"'

# Answering from Discord, which is off until the config file names a channel and a user.
# The channel is where every alert goes, so a config without one is a Mac that cannot alert.
check "the Discord config is a live symlink naming a channel and a user" \
  '[ -L "$HOME/.config/hachiko/discord" ] && [ -e "$HOME/.config/hachiko/discord" ] &&
   grep -qE "^channel *= *[0-9]{17,20}$" "$HOME/.config/hachiko/discord" &&
   grep -qE "^user *= *[0-9]{17,20}$" "$HOME/.config/hachiko/discord"'

# shibuya, the half of the watch that is not on this Mac. Its own behaviour is `npm test` in
# shibuya/, against the same wrangler config the deploy reads; what is left for this script
# is the wiring — the config file hachiko reads, the token file's mode, and the one script
# that deploys it.
check "the shibuya URL is a live symlink and holds one https URL and no token" \
  '[ -L "$HOME/.config/hachiko/shibuya" ] && [ -e "$HOME/.config/hachiko/shibuya" ] &&
   [ "$(grep -cE "^[^#]" "$HOME/.config/hachiko/shibuya" | tr -d " ")" -ge 1 ] &&
   grep -qx "https://shibuya.timche.dev" "$HOME/.config/hachiko/shibuya" &&
   ! grep -qE "^[^#]*(token|Bearer|op://)" "$HOME/.config/hachiko/shibuya"'

# Not in the repository and not in 1Password: generated on the Mac by shibuya/secrets.sh,
# because hachiko reads it twice an hour and every `op run` counts against the service
# account's day. A Mac that has never run that script has no file here, which is a switch
# nothing checks in with rather than a broken one — so this asserts the mode and only when
# there is a file to assert it on.
check "the ping token, if it is there, is readable by nobody but the account" \
  't="$HOME/.config/hachiko/shibuya-token";
   [ ! -e "$t" ] || [ "$(stat -f %Lp "$t")" = 600 ]'

check "the ping token is in no .env.op and in no op:// reference" \
  '! grep -q "shibuya-token" "$HOME/.local/bin/hachiko.env.op" &&
   ! grep -q "PING_TOKEN" "$repo/shibuya/.env.op"'

check "shibuya's .env.op is op:// references and nothing resolved" \
  'grep -qx "CLOUDFLARE_API_TOKEN=op://dev/shibuya/api token" "$repo/shibuya/.env.op" &&
   grep -qx "DISCORD_BOT_TOKEN=op://dev/shibuya-discord/bot token" "$repo/shibuya/.env.op" &&
   ! grep -qE "^[A-Z_]+=[^o]" "$repo/shibuya/.env.op"'

check "the two shibuya scripts are executable and parse" \
  '[ -x "$repo/shibuya/deploy.sh" ] && [ -x "$repo/shibuya/secrets.sh" ] &&
   bash -n "$repo/shibuya/deploy.sh" && bash -n "$repo/shibuya/secrets.sh"'

# One entrance. A workers.dev subdomain or a version preview URL is a second hostname
# serving the same Worker, with the ping token as the only thing in front of it.
check "shibuya is deployed to its custom domain alone" \
  'grep -q "\"workers_dev\": false" "$repo/shibuya/wrangler.jsonc" &&
   grep -q "\"preview_urls\": false" "$repo/shibuya/wrangler.jsonc" &&
   grep -q "shibuya.timche.dev" "$repo/shibuya/wrangler.jsonc" &&
   grep -q "new_sqlite_classes" "$repo/shibuya/wrangler.jsonc"'

check "the CPU allowlist is a live symlink and names the VMs and the indexer" \
  '[ -L "$HOME/.config/hachiko/cpu-allow" ] &&
   [ -e "$HOME/.config/hachiko/cpu-allow" ] &&
   grep -qx "OrbStack Helper" "$HOME/.config/hachiko/cpu-allow" &&
   grep -qx "mds" "$HOME/.config/hachiko/cpu-allow" &&
   grep -qx "backupd" "$HOME/.config/hachiko/cpu-allow"'

# The standing orders send the on-call session to this agent before it acts on its own,
# so an agent file that is not there is an autonomous action with nobody checking it. The
# tools are the whole of why it is safe to spawn from a session that has been handed
# authority over the machine: it inspects and reports, and nothing it can call writes.
check "the on-call session's review partner is there, reads Opus's second opinion and can change nothing" \
  'p="$HOME/.claude/agents/oncall-partner.md" && [ -e "$p" ] &&
   grep -qx "model: fable" "$p" &&
   grep -qx "tools: Read, Grep, Glob, Bash" "$p" &&
   grep -q "Read-only, absolutely" "$p"'

# The partner checks a proposed action against limits it is given in its own prompt, so one
# that drifted from the limits the session was told is a reviewer agreeing to something nobody
# allowed. These phrases are the load-bearing half of both lists and have to be the same words
# in both files. A function rather than a snippet, because the list holds a quote and an
# apostrophe and the snippets run through two levels of it.
hachiko_limits_in_step() {
  local partner="$1" orders="$2" phrase
  local missing=0

  while IFS= read -r phrase; do
    [ -n "$phrase" ] || continue
    grep -qF "$phrase" "$partner" || { printf 'not in the partner: %s\n' "$phrase"; missing=1; }
    grep -qF "$phrase" "$orders" || { printf 'not in the orders: %s\n' "$phrase"; missing=1; }
  done <<'PHRASES'
SIGTERM first and SIGKILL only if it is still there ten seconds later
*.log, *.out, *.err, *.output or *.log.N
a database or a docker volume
restarting herdr, the Mac or a launchd service other than through that one root-helper command
the root helper's restart-daemon command for the busy daemon, exactly as the incident data gives it after **You can run:**
unless that process is itself the one causing the incident
is not a licence to empty a folder
PHRASES

  return "$missing"
}
export -f hachiko_limits_in_step

check "the partner's limits are the same words as the standing orders'" \
  'hachiko_limits_in_step "$HOME/.claude/agents/oncall-partner.md" "$repo/hachiko/internal/oncall/oncall.go"'

# The go the wrapper builds with is this repository's own, in its root mise.toml: this
# repo is one of the projects that wants a runtime for itself.
check "this repo declares the go its compiled tools are built with" \
  'grep -qE "^go = \"[0-9]" "$repo/mise.toml"'

check "the hachiko module is standard library only and builds without cgo" \
  '[ ! -f "$repo/hachiko/go.sum" ] &&
   ! grep -q "^require" "$repo/hachiko/go.mod" &&
   grep -q "CGO_ENABLED=0" "$repo/home/.local/bin/hachiko"'

# The agent is the whole of the wiring, so an unrendered or invalid plist is a job
# launchd rejects at load with nothing in it to say why.
export hachiko_plist="$HOME/Library/LaunchAgents/io.github.timche.hachiko.plist"

check "the hachiko agent is a live symlink and a valid plist" \
  '[ -L "$hachiko_plist" ] && [ -e "$hachiko_plist" ] && plutil -lint "$hachiko_plist" &&
   [ "$(plutil -extract Label raw "$hachiko_plist")" = io.github.timche.hachiko ]'
check "the hachiko agent runs hachiko and logs to ~/Library/Logs" \
  'plutil -extract ProgramArguments.2 raw -o - "$hachiko_plist" |
     grep -qF "exec \"\$HOME/.local/bin/hachiko\" >>\"\$HOME/Library/Logs/hachiko.log\""'
check "the hachiko agent checks at load" \
  'plutil -extract RunAtLoad xml1 -o - "$hachiko_plist" | grep -q "<true/>"'
check "the hachiko agent checks every five minutes" \
  '[ "$(plutil -extract StartInterval raw -o - "$hachiko_plist")" = 300 ]'
# ~/.local/bin first, for the wrapper itself, for the op there that signs 1Password
# in, for the herdr the server runs from and for the mise that rebuilds the binary;
# Homebrew's op behind it.
check "the hachiko agent's PATH reaches the op wrapper, herdr, mise and Homebrew" \
  'plutil -extract ProgramArguments.2 raw -o - "$hachiko_plist" |
     grep -qF "export PATH=\"\$HOME/.local/bin:/opt/homebrew/bin"'

check "the hachiko agent is loaded" \
  'launchctl print "gui/$(id -u)/io.github.timche.hachiko" >/dev/null'

# The listener, loaded whether or not the feature is configured: with nothing in the config
# file it says so once per start and exits, and the throttle is what makes that cost nothing.
# KeepAlive because the poll is the job — a listener that exited and stayed exited is a reply
# nobody reads.
export listen_plist="$HOME/Library/LaunchAgents/io.github.timche.hachiko-listen.plist"

check "the Discord listener agent is a live symlink and a valid plist" \
  '[ -L "$listen_plist" ] && [ -e "$listen_plist" ] && plutil -lint "$listen_plist" &&
   [ "$(plutil -extract Label raw "$listen_plist")" = io.github.timche.hachiko-listen ]'
check "the listener runs hachiko listen and logs to ~/Library/Logs" \
  'plutil -extract ProgramArguments.2 raw -o - "$listen_plist" |
     grep -qF "exec \"\$HOME/.local/bin/hachiko\" listen >>\"\$HOME/Library/Logs/hachiko-listen.log\""'
check "the listener is kept alive and throttled to five minutes" \
  'plutil -extract KeepAlive xml1 -o - "$listen_plist" | grep -q "<true/>" &&
   [ "$(plutil -extract ThrottleInterval raw -o - "$listen_plist")" = 300 ]'
check "the listener agent is loaded" \
  'launchctl print "gui/$(id -u)/io.github.timche.hachiko-listen" >/dev/null'

# The one check that is the agent rather than a description of it. Everything else here
# runs hachiko as this session, which holds the privacy grants herdr was given — and that
# is exactly the difference that hid a sweep hanging forever on its first real run.
#
# Under launchd an unsigned binary is its own TCC-responsible process. A folder behind
# Full Disk Access answers "operation not permitted" at once, but one behind a consent
# prompt makes open() wait for a dialog on a screen this Mac does not have, so the sweep
# never returns and never releases its lock. `launchctl submit` is the smallest way to get
# that shape: a job in the same gui domain, the built binary, a state and cache directory
# of its own, and the whole of it removed afterwards whatever happens.
#
# Three things have to be true: it exits, it printed its summary, and it asked TCC for
# nothing. The last is read from tccd's own log, which takes no sudo and names the binary
# that asked, and from the window list, which is where a consent dialog would be.
hachiko_launchd() {
  local bin="$1" dir="$2" label="$3" waited=0

  launchctl submit -l "$label" -o "$dir/out" -e "$dir/err" -- \
    /usr/bin/env "HACHIKO_STATE_DIR=$dir/state" "HACHIKO_CACHE_DIR=$dir/cache" \
    "$bin" --dry-run || return 1

  while [ "$waited" -lt 40 ]; do
    grep -q "dry run over" "$dir/out" 2>/dev/null && break
    sleep 0.5
    waited=$((waited + 1))
  done

  launchctl remove "$label" 2>/dev/null
  grep -q "dry run over" "$dir/out" 2>/dev/null
}
export -f hachiko_launchd

# A consent dialog is UserNotificationCenter's window, and nothing here should ever cause
# one. Read with the same CGWindowListCopyWindowInfo window-shot uses, which needs the
# Screen Recording grant this session already has.
hachiko_prompt_on_screen() {
  swift - <<'SWIFT' 2>/dev/null
import CoreGraphics
import Foundation

let windows = CGWindowListCopyWindowInfo([.optionOnScreenOnly], kCGNullWindowID) as? [[String: Any]] ?? []
let owners = windows.compactMap { $0[kCGWindowOwnerName as String] as? String }
if owners.contains(where: { $0.contains("UserNotificationCenter") }) {
  print("prompt")
}
SWIFT
}
export -f hachiko_prompt_on_screen

# Every request tccd logged about this binary has to have reached a result. The denials
# that are fine are the ones tccd records without asking anybody — "does not allow
# prompting; recording denied", microseconds, and the walk carries on. A request with no
# result is the other kind: a prompt on a screen nobody is looking at, and the process
# behind it waiting in open() for as long as the Mac is up. `log show` reads this without
# sudo and names the binary that asked.
hachiko_tcc_unanswered() {
  local log="$1" bin="$2" id

  for id in $(sed -n "s|.*AUTHREQ_ATTRIBUTION: msgID=\([0-9.]*\),.*$bin.*|\1|p" "$log" | sort -u); do
    grep -q "AUTHREQ_RESULT: msgID=$id," "$log" || printf '%s\n' "$id"
  done
}
export -f hachiko_tcc_unanswered

check "hachiko finishes a sweep under launchd, where it has no privacy grants" \
  'cd / && d="$(mktemp -d)" && label="io.github.timche.hachiko.assert.$$" &&
   export HACHIKO_CACHE_DIR="$d/cache" &&
   "$repo/home/.local/bin/hachiko" --help >/dev/null &&
   [ -x "$d/cache/hachiko" ] &&
   since="$(date "+%Y-%m-%d %H:%M:%S")" &&
   hachiko_launchd "$d/cache/hachiko" "$d" "$label" &&
   grep -q "GB free," "$d/out" &&
   ! grep -q "did not answer a read" "$d/out" &&
   [ -z "$(hachiko_prompt_on_screen)" ] &&
   /usr/bin/log show --start "$since" --predicate "process == \"tccd\"" >"$d/tcc" 2>/dev/null &&
   [ -z "$(hachiko_tcc_unanswered "$d/tcc" "$d/cache/hachiko")" ]'

# Every wrapper check below runs from `/`, which is where launchd starts an agent, and
# that is the whole point of them: mise decides which tools are active from the directory
# it is asked about, so a question asked without one is answered for `/` — where nothing
# this repo declares is active. The check that catches it is the next one.
#
# The promise the whole build-in-place arrangement rests on: the wrapper builds from the
# working tree, so a half-written edit is on the Mac as soon as it is written, and the
# agent has to keep watching with the last binary that compiled. Against a copy of the
# module, because the check deliberately breaks it.
check "the wrapper rebuilds from the directory launchd starts the agent in" \
  'cd / && d="$(mktemp -d)" && mkdir -p "$d/home/.local/bin" &&
   cp -R "$repo/hachiko" "$d/hachiko" &&
   cp "$repo/mise.toml" "$d/mise.toml" &&
   cp "$repo/home/.local/bin/hachiko" "$d/home/.local/bin/hachiko" &&
   cp -R "$repo/home/.config" "$d/home/.config" &&
   export MISE_TRUSTED_CONFIG_PATHS="$d" &&
   export HACHIKO_CACHE_DIR="$d/cache" &&
   "$d/home/.local/bin/hachiko" --help >/dev/null 2>"$d/first" &&
   [ ! -s "$d/first" ] &&
   built="$(cat "$d/cache/source.sha256")" &&
   printf "\n// an edit that landed while the agent was running\n" >>"$d/hachiko/main.go" &&
   "$d/home/.local/bin/hachiko" --help 2>"$d/err" | grep -q "dry-run" &&
   [ ! -s "$d/err" ] &&
   [ "$(cat "$d/cache/source.sha256")" != "$built" ]'

check "the wrapper keeps the last good binary when the sources do not compile" \
  'cd / && d="$(mktemp -d)" && mkdir -p "$d/home/.local/bin" &&
   cp -R "$repo/hachiko" "$d/hachiko" &&
   cp "$repo/mise.toml" "$d/mise.toml" &&
   cp "$repo/home/.local/bin/hachiko" "$d/home/.local/bin/hachiko" &&
   cp -R "$repo/home/.config" "$d/home/.config" &&
   export MISE_TRUSTED_CONFIG_PATHS="$d" &&
   export HACHIKO_CACHE_DIR="$d/cache" &&
   "$d/home/.local/bin/hachiko" --help >/dev/null &&
   [ -x "$d/cache/hachiko" ] &&
   built="$(cat "$d/cache/source.sha256")" &&
   printf "func broken( {\n" >>"$d/hachiko/main.go" &&
   "$d/home/.local/bin/hachiko" --help 2>"$d/err" | grep -q "dry-run" &&
   grep -q "do not build, so this is the last binary that did" "$d/err" &&
   [ "$(cat "$d/cache/source.sha256")" = "$built" ]'

# A pull that moved the sources away is not a source tree that fails to compile, and a
# go that is not installed is install.sh's to fix: both say which, and neither stops the
# watch. A LaunchAgent is also the wrong place to fetch a toolchain from, so the build
# never reaches the network on its own.
check "the wrapper says why it could not rebuild and keeps watching" \
  'cd / && d="$(mktemp -d)" && mkdir -p "$d/home/.local/bin" "$d/bin" &&
   cp -R "$repo/hachiko" "$d/hachiko" &&
   cp "$repo/mise.toml" "$d/mise.toml" &&
   cp "$repo/home/.local/bin/hachiko" "$d/home/.local/bin/hachiko" &&
   cp -R "$repo/home/.config" "$d/home/.config" &&
   export MISE_TRUSTED_CONFIG_PATHS="$d" &&
   export HACHIKO_CACHE_DIR="$d/cache" &&
   "$d/home/.local/bin/hachiko" --help >/dev/null &&
   mv "$d/hachiko" "$d/hachiko.moved" &&
   "$d/home/.local/bin/hachiko" --help 2>"$d/gone" | grep -q "dry-run" &&
   grep -q "is not there, so hachiko was not rebuilt" "$d/gone" &&
   mv "$d/hachiko.moved" "$d/hachiko" &&
   printf "edit\n" >>"$d/hachiko/main.go" &&
   printf "#!/bin/sh\nexit 1\n" >"$d/bin/mise" && chmod +x "$d/bin/mise" &&
   PATH="$d/bin:$stock_path" HOME="$d" "$d/home/.local/bin/hachiko" --help \
     2>"$d/nogo" | grep -q "dry-run" &&
   grep -q "is not installed, so hachiko could not be rebuilt — run install.sh" "$d/nogo"'

# A run with nothing to build may not reach for mise at all: this runs every five
# minutes forever, and the binary it execs depends on nothing.
check "the wrapper runs the cached binary without a go of any kind when nothing changed" \
  'cd / && d="$(mktemp -d)" && mkdir -p "$d/home/.local/bin" "$d/bin" &&
   cp -R "$repo/hachiko" "$d/hachiko" &&
   cp "$repo/mise.toml" "$d/mise.toml" &&
   cp "$repo/home/.local/bin/hachiko" "$d/home/.local/bin/hachiko" &&
   cp -R "$repo/home/.config" "$d/home/.config" &&
   export MISE_TRUSTED_CONFIG_PATHS="$d" &&
   export HACHIKO_CACHE_DIR="$d/cache" &&
   "$d/home/.local/bin/hachiko" --help >/dev/null &&
   printf "#!/bin/sh\nexit 97\n" >"$d/bin/mise" && chmod +x "$d/bin/mise" &&
   PATH="$d/bin:$stock_path" HOME="$d" "$d/home/.local/bin/hachiko" --help \
     2>"$d/err" | grep -q "dry-run" &&
   [ ! -s "$d/err" ]'

# The wrapper execs the next op on PATH after itself, so a stub placed behind it
# stands in for Homebrew's: it prints the token it was handed, its arguments,
# and runs what follows `--` for `op run`, the way the real one does.
op_wrapper_home() {
  local h
  h="$(mktemp -d)"

  mkdir -p "$h/.config/op" "$h/wrap" "$h/real"
  printf 'stub-service-account\n' >"$h/.config/op/service-account-token"
  ln -s "$repo/home/.local/bin/op" "$h/wrap/op"

  cat >"$h/real/op" <<'STUB'
#!/bin/bash
printf 'token=%s\n' "${OP_SERVICE_ACCOUNT_TOKEN-unset}"
printf 'args=%s\n' "$*"
if [ "${1:-}" = run ] || [ "${3:-}" = run ]; then
  while [ "$#" -gt 0 ] && [ "$1" != -- ]; do shift; done
  [ "$#" -gt 0 ] && shift && "$@"
fi
exit 0
STUB
  chmod +x "$h/real/op"
  printf '%s\n' "$h"
}
export -f op_wrapper_home

check "the op wrapper is a live symlink ahead of Homebrew's op" \
  '[ -L "$HOME/.local/bin/op" ] && [ -x "$HOME/.local/bin/op" ] &&
   [ "$(PATH="$HOME/.local/bin:/opt/homebrew/bin:$stock_path" command -v op)" = "$HOME/.local/bin/op" ]'
check "the op wrapper hands op the token from the file" \
  'h="$(op_wrapper_home)" &&
   out="$(env -u OP_SERVICE_ACCOUNT_TOKEN HOME="$h" PATH="$h/wrap:$h/real:$PATH" op whoami)" &&
   printf "%s" "$out" | grep -qx "token=stub-service-account" &&
   printf "%s" "$out" | grep -qx "args=whoami"'
check "the op wrapper leaves a token already set alone" \
  'h="$(op_wrapper_home)" &&
   HOME="$h" PATH="$h/wrap:$h/real:$PATH" OP_SERVICE_ACCOUNT_TOKEN=caller op whoami |
     grep -qx "token=caller"'
check "the op wrapper passes op through untouched without a token file" \
  'h="$(op_wrapper_home)" && rm "$h/.config/op/service-account-token" &&
   out="$(env -u OP_SERVICE_ACCOUNT_TOKEN HOME="$h" PATH="$h/wrap:$h/real:$PATH" op run --env-file .env.op -- printenv)" &&
   printf "%s" "$out" | grep -qx "token=unset" &&
   ! printf "%s" "$out" | grep -q "^OP_SERVICE_ACCOUNT_TOKEN="'
check "op run gives op the token and the command none, with flags on either side of --env-file" \
  'h="$(op_wrapper_home)" &&
   for flags in "--env-file .env.op" "--no-masking --env-file .env.op" "--env-file .env.op --no-masking"; do
     out="$(env -u OP_SERVICE_ACCOUNT_TOKEN HOME="$h" PATH="$h/wrap:$h/real:$PATH" op run $flags -- printenv)" &&
     printf "%s" "$out" | grep -qx "token=stub-service-account" &&
     printf "%s" "$out" | grep -qx "args=run $flags -- env -u OP_SERVICE_ACCOUNT_TOKEN printenv" &&
     ! printf "%s" "$out" | grep -q "^OP_SERVICE_ACCOUNT_TOKEN=" || exit 1
   done'
check "op run strips the token after the first -- only, and a pre-set one too" \
  'h="$(op_wrapper_home)" &&
   HOME="$h" PATH="$h/wrap:$h/real:$PATH" OP_SERVICE_ACCOUNT_TOKEN=caller \
     op --account x run -- echo -- a | grep -qx "args=--account x run -- env -u OP_SERVICE_ACCOUNT_TOKEN echo -- a"'
check "op run without -- passes through" \
  'h="$(op_wrapper_home)" &&
   HOME="$h" PATH="$h/wrap:$h/real:$PATH" op run --env-file .env.op | grep -qx "args=run --env-file .env.op"'
check "a command under op run that calls op by name is signed in again" \
  'h="$(op_wrapper_home)" &&
   env -u OP_SERVICE_ACCOUNT_TOKEN HOME="$h" PATH="$h/wrap:$h/real:$PATH" op run -- op whoami |
     grep -c "^token=stub-service-account$" | grep -qx 2'

check "DO_NOT_TRACK is set in every shell" \
  '[ "$(zsh -c "print -r -- \$DO_NOT_TRACK")" = 1 ] &&
   [ "$(bash -ic "printf %s \"\$DO_NOT_TRACK\"" 2>/dev/null)" = 1 ]'

# The markdown rules are assert-markdown.sh's, which CI runs by itself on a Linux
# runner: test.yml's paths filter keeps a markdown-only push off the macOS runner,
# so a check that lived only here would never run for the commits it is about. It
# exits with the number that failed, which this total takes over.
"$repo/test/assert-markdown.sh"
failures=$((failures + $?))

# Finding the docs is a mechanism rather than a rule Claude has to remember, so
# the wiring is what makes the global CLAUDE.md's docs section true.
check "the project-docs hook is wired to SessionStart" \
  'grep -q "hooks/project-docs.sh" "$HOME/.claude/settings.json"'

# A repository with no docs folder has to stay silent, or every session on
# every other project starts with an apology. A bare repository of its own,
# since this one carries docs/ now and ~/projects/docs may hold a folder for
# anything, and an empty docs root, since the real one has shared/.
check "the project-docs hook says nothing without a docs folder" \
  'bare="$(mktemp -d)" && root="$(mktemp -d)" && git -C "$bare" init -q &&
   git -C "$bare" remote add origin https://example.com/nobody/no-docs-here.git &&
   [ -z "$(cd "$bare" && PROJECT_DOCS_DIR="$root" bash "$HOME/.claude/hooks/project-docs.sh")" ]'

# shared/ is what applies across projects, so it is listed even where the
# project has no docs of its own, and outside a repository altogether.
check "the project-docs hook lists the shared docs in any directory" \
  'root="$(mktemp -d)" && out="$(mktemp -d)" &&
   mkdir "$root/shared" && : > "$root/shared/cloudflare.md" &&
   [ "$(cd "$out" && PROJECT_DOCS_DIR="$root" bash "$HOME/.claude/hooks/project-docs.sh" | head -1)" = \
     "Shared docs for every project: $root/shared" ]'

# ~/projects/docs/<repo> and nothing else: a checkout's own docs/ is the project's
# published documentation rather than its working docs. Run against a throwaway
# HOME, so the check does not leave a folder in the real one for `hachiko sync`
# to publish.
check "the project-docs hook reads ~/projects/docs/<repo>, not the checkout's docs folder" \
  'hook="$HOME/.claude/hooks/project-docs.sh" &&
   h="$(mktemp -d)" && d="$(mktemp -d)" && git -C "$d" init -q &&
   git -C "$d" remote add origin https://example.com/nobody/has-docs.git &&
   mkdir "$d/docs" && : > "$d/docs/README.md" &&
   mkdir -p "$h/projects/docs/has-docs" && : > "$h/projects/docs/has-docs/decisions.md" &&
   [ "$(cd "$d" && HOME="$h" PROJECT_DOCS_DIR="$h/projects/docs" bash "$hook" | head -1)" = \
     "Working docs for has-docs: $h/projects/docs/has-docs" ]'

if [ "$failures" -gt 0 ]; then
  echo "  $failures check(s) failed"
  exit 1
fi
