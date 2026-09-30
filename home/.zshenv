# Shims rather than `mise activate`, which hooks the prompt and so leaves
# non-interactive shells without any of the tools. Read on every zsh
# invocation, unlike .zshrc, so a script and an agent get the same PATH a
# login does.
#
# Homebrew is on no default PATH at all, and it belongs behind the shims so a
# version the lockfile pins beats a formula of the same name. (N) drops the two
# entries rather than leaving a glob for a prefix that is not there, and
# typeset -U keeps .zprofile's second pass from doubling anything.
#
# ~/.local/bin holds mise and Claude Code, both from their authors' installers.
# Ahead of Homebrew's prefix, so a `brew install mise` cannot shadow the one
# mise.run put there. It was .zshrc's, which left it off
# every non-interactive shell and put it in front of the shims in the ones that
# had it; behind them here, so `claude` resolves the same way whatever kind
# of zsh asks and only a shim left over from when mise installed it could answer
# first.
typeset -U path PATH
path=("$HOME/.local/share/mise/shims" "$HOME/.local/bin" /opt/homebrew/{bin,sbin}(N) $path)
export PATH

# Here rather than in settings.json's env, whose values reach Claude Code
# verbatim — a $HOME in one would be taken as a directory called `$HOME`.
export CLAUDE_CODE_TMPDIR="$HOME/.cache/claude-tmp"

# Where the project docs are, so a repository's own CLAUDE.md can name
# $PROJECT_DOCS_DIR/<repo> without knowing this machine's layout.
export PROJECT_DOCS_DIR="$HOME/projects/docs"

# One proxy serves every app under both. .localhost first, because PORTLESS_URL
# takes the first TLD and a dev server talking to itself should stay on this
# Mac; the second is how Tim's MacBook reaches it over the tailnet, through a
# wildcard DNS record at Cloudflare that points at this Mac's tailnet address.
# The running proxy decides the TLDs and portless only warns when this differs,
# so the daemon portless-root.sh installs is installed from this same list.
export PORTLESS_TLD=localhost,timche.dev

# The cross-tool opt-out from usage analytics (donottrack.sh). varlock is why it
# is here: without it, every project would need a .varlock/config.json of its own.
export DO_NOT_TRACK=1

# Compose names a project after the directory it was started from, which for a
# worktree at ~/.herdr/worktrees/<repo>/<branch> is the branch alone — so the
# same branch name in two repositories would share one project, and with it one
# set of containers, one network and one database volume. <repo>-<branch>
# instead, from the remote rather than the directory for the same reason
# project-docs.sh takes it from there.
#
# Here rather than in .zshrc because a compose command as often comes from a
# script or an agent as from a prompt, and again on every chdir because a shell
# outlives the directory it started in. Unset outside a work tree, which leaves
# compose on its own default.
_compose_project_name() {
  local -a dirs
  local repo branch head name

  # One git call for all three paths: the worktree's own git dir, which is where
  # a linked worktree's HEAD lives, the common one, whose parent is the main
  # worktree, and the top level, which names the folder.
  dirs=(${(f)"$(git rev-parse --path-format=absolute \
                  --git-dir --git-common-dir --show-toplevel 2>/dev/null)"})

  if (( ${#dirs} < 3 )); then
    unset COMPOSE_PROJECT_NAME
    return
  fi

  repo="${${$(git config --get remote.origin.url 2>/dev/null):t}%.git}"
  [[ -n $repo ]] || repo="${dirs[2]:h:t}"

  # The ref out of HEAD rather than a third git process. A detached HEAD holds a
  # hash instead, and there the folder is the only name the worktree has.
  [[ -r ${dirs[1]}/HEAD ]] && read -r head < ${dirs[1]}/HEAD
  branch="${head#ref: refs/heads/}"
  [[ -n $branch && $branch != $head ]] || branch="${dirs[3]:t}"

  # Compose's own rule for a project name, applied here rather than left to it:
  # it rejects a name that breaks the rule instead of sanitising one, and that
  # rule covers the first character as well as the set.
  name="${(L)repo}-${(L)branch}"
  name="${name//[^a-z0-9_-]/-}"
  name="${name#${name%%[a-z0-9]*}}"

  if [[ -n $name ]]; then
    export COMPOSE_PROJECT_NAME="$name"
  else
    unset COMPOSE_PROJECT_NAME
  fi
}

_compose_project_name

# -U because .zprofile re-reads this file after macOS's path_helper, which would
# otherwise leave the function in the array twice.
typeset -gaU chpwd_functions
chpwd_functions+=(_compose_project_name)

# The signing key lives in an agent claude/ssh-agent.sh keeps at this fixed path,
# because the socket launchd's own agent hands out is invisible to an SSH
# session and to a LaunchAgent — and this Mac is only ever reached over SSH.
# Unguarded: pointing at a socket that is momentarily missing costs an error
# naming the agent, where falling through to launchd's would cost one about a
# key that is not there.
if [[ $OSTYPE == darwin* ]]; then
  export SSH_AUTH_SOCK="$HOME/.ssh/agent.sock"
fi
