# macOS runs path_helper from /etc/zprofile in every login shell, which rebuilds
# PATH with the system directories in front of everything .zshenv put there —
# the reason Homebrew's own advice is to set its prefix up from a .zprofile.
# Re-reading .zshenv puts the order back, and its typeset -U means nothing is
# listed twice.
source "$HOME/.zshenv"

# Added by OrbStack: command-line tools and integration
# This won't be added again if you remove it.
source ~/.orbstack/shell/init.zsh 2>/dev/null || :
