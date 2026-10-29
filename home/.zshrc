# Interactive zsh, on zsh's and macOS's own defaults (/etc/zshrc gives it a history
# file) with the few settings Tim uses on top. PATH and everything a non-interactive
# shell also needs is in .zshenv instead, which every zsh reads.

# Every open pane sees the commands typed in the others, as they are typed.
setopt share_history

autoload -Uz compinit && compinit
# Case-insensitive first, then partial-word, then substring, each pass only
# reached when the one before it matched nothing.
zstyle ':completion:*' matcher-list 'm:{[:lower:][:upper:]}={[:upper:][:lower:]}' 'r:|=*' 'l:|=* r:|=*'
setopt complete_in_word always_to_end

# Up and down search the history for the line already typed. Both forms of each
# arrow, since a terminal sends the O one while the line editor holds it in
# application mode and the bracket one outside it.
autoload -Uz up-line-or-beginning-search down-line-or-beginning-search
zle -N up-line-or-beginning-search
zle -N down-line-or-beginning-search
bindkey '^[[A' up-line-or-beginning-search
bindkey '^[OA' up-line-or-beginning-search
bindkey '^[[B' down-line-or-beginning-search
bindkey '^[OB' down-line-or-beginning-search

eval "$(starship init zsh)"
eval "$(zoxide init zsh)"

# Claude
alias c="claude"

# The git aliases Tim uses, by the names and meanings oh-my-zsh gave them. Every
# repository's main branch is main, so gcm names it rather than looking it up.
alias ga="git add"
alias gc="git commit --verbose"
alias gcm="git checkout main"
alias gcmsg="git commit --message"
alias gco="git checkout"
alias gd="git diff"
alias gl="git pull"
alias glog="git log --oneline --decorate --graph"
alias grb="git rebase"
alias gst="git status"

# mise from its own installer, at ~/.local/bin. By name rather than by path, and
# .zshenv puts ~/.local/bin ahead of Homebrew's prefix, so a formula of the same
# name cannot answer first.
eval "$(mise activate zsh)"
