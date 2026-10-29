# Interactive zsh, set directly rather than through a framework or a plugin
# manager: what an interactive shell here needs is a dozen settings, and each is
# one line of zsh that nothing has to keep up to date. PATH and everything a
# non-interactive shell also needs is in .zshenv instead, which every zsh reads.

HISTFILE="$HOME/.zsh_history"
HISTSIZE=50000
SAVEHIST=10000
# extended_history is the format the file is already written in, timestamp and
# duration per line, so `history` can show when a command ran.
setopt extended_history hist_expire_dups_first hist_ignore_dups hist_ignore_space \
  hist_verify share_history

autoload -Uz compinit && compinit
# Case-insensitive first, then partial-word, then substring, each pass only
# reached when the one before it matched nothing.
zstyle ':completion:*' matcher-list 'm:{[:lower:][:upper:]}={[:upper:][:lower:]}' 'r:|=*' 'l:|=* r:|=*'
zstyle ':completion:*' menu select
setopt complete_in_word always_to_end
# No flow control, so a Ctrl-S reached for out of habit cannot leave the
# terminal looking hung with no way back that anyone remembers.
unsetopt flow_control
# Every punctuation character ends a word, so ^W over a path takes one segment
# rather than the whole of it.
WORDCHARS=''

setopt auto_cd interactive_comments

bindkey -e
# Up and down search the history for the line already typed rather than walking
# it blindly. Bound by the sequences themselves rather than through terminfo,
# which an unknown TERM leaves empty: the O form is what a terminal sends while
# the line editor holds it in application mode, the bracket form what it sends
# outside one, and the ~ form what a multiplexer sends for Home and End.
autoload -Uz up-line-or-beginning-search down-line-or-beginning-search
zle -N up-line-or-beginning-search
zle -N down-line-or-beginning-search
bindkey '^[[A' up-line-or-beginning-search
bindkey '^[OA' up-line-or-beginning-search
bindkey '^[[B' down-line-or-beginning-search
bindkey '^[OB' down-line-or-beginning-search
bindkey '^[[H' beginning-of-line
bindkey '^[OH' beginning-of-line
bindkey '^[[1~' beginning-of-line
bindkey '^[[F' end-of-line
bindkey '^[OF' end-of-line
bindkey '^[[4~' end-of-line
bindkey '^[[Z' reverse-menu-complete

# BSD ls takes its colours from a flag and LSCOLORS rather than from LS_COLORS.
export LSCOLORS=Gxfxcxdxbxegedabagacad
alias ls='ls -G'

eval "$(starship init zsh)"
eval "$(zoxide init zsh)"

# Claude
alias c="claude"

# mise from its own installer, at ~/.local/bin. By name rather than by path, and
# .zshenv puts ~/.local/bin ahead of Homebrew's prefix, so a formula of the same
# name cannot answer first.
eval "$(mise activate zsh)"
