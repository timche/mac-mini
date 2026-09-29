# ~/.bashrc: executed by bash(1) for non-login shells. bash is nobody's login shell
# here — zsh is — so this exists for the interactive bash a script or a hook drops
# into, and for the PATH it would otherwise have none of.

# If not running interactively, don't do anything
case $- in
    *i*) ;;
      *) return;;
esac

# don't put duplicate lines or lines starting with space in the history.
# See bash(1) for more options
HISTCONTROL=ignoreboth

# append to the history file, don't overwrite it
shopt -s histappend

# for setting history length see HISTSIZE and HISTFILESIZE in bash(1)
HISTSIZE=1000
HISTFILESIZE=2000

# check the window size after each command and, if necessary,
# update the values of LINES and COLUMNS.
shopt -s checkwinsize

# set a fancy prompt (non-color, unless we know we "want" color)
case "$TERM" in
    xterm-color|*-256color) color_prompt=yes;;
esac

if [ "$color_prompt" = yes ]; then
    PS1='\[\033[01;32m\]\u@\h\[\033[00m\]:\[\033[01;34m\]\w\[\033[00m\]\$ '
else
    PS1='\u@\h:\w\$ '
fi
unset color_prompt

# If this is an xterm set the title to user@host:dir
case "$TERM" in
xterm*|rxvt*)
    PS1="\[\e]0;\u@\h: \w\a\]$PS1"
    ;;
*)
    ;;
esac

if [ -f ~/.bash_aliases ]; then
    . ~/.bash_aliases
fi

# bash reads none of the zsh files, so the PATH .zshenv builds is built again
# here and in the same order: the shims first, then ~/.local/bin, which holds
# mise and Claude Code, then Homebrew, which is on no default PATH. ~/.local/bin
# ahead of Homebrew is what keeps a `brew install mise` from answering instead of
# the one mise's installer put there.
export PATH="$HOME/.local/share/mise/shims:$HOME/.local/bin:/opt/homebrew/bin:/opt/homebrew/sbin:$PATH"

# The same opt-out .zshenv makes, for the bash that claude.sh and CI run under.
export DO_NOT_TRACK=1

eval "$(mise activate bash)"
