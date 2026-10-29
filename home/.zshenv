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
# so the daemon install.sh names is installed from this same list.
export PORTLESS_TLD=localhost,timche.dev

# The cross-tool opt-out from usage analytics (donottrack.sh), so no CLI a
# session runs reports home or writes an install id without a per-tool setting.
export DO_NOT_TRACK=1
