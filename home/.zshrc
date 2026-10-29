# Interactive zsh, on zsh's and macOS's own defaults (/etc/zshrc gives it a history
# file), with only what is this Mac's own. PATH and everything a non-interactive
# shell also needs is in .zshenv instead, which every zsh reads.

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
