# Every Homebrew package this repo installs, applied by install.sh with
# `brew bundle`. mise is a version manager for language runtimes and nothing else,
# so a CLI tool is Homebrew's and belongs here rather than in mise's tool list;
# node, bun, python and the two npm packages are what is left there. CLAUDE.md has
# the rule.
#
# install.sh passes --no-upgrade: a re-run installs what is missing and moves no
# version, which is the same promise `mise install --locked` makes for the tool
# list. Without it every install would be an unasked-for `brew upgrade` of the whole
# list, and a shell would change under a session that only wanted its links put
# back. `brew bundle upgrade` is the deliberate version.

# boswell is published from a tap of its own, and these two lines are what removed
# install.sh's special case for it: mise's bootstrap reads a tap's own published
# api/formula/<name>.json rather than proxying to the brew CLI, a plain tap
# publishes no such file, and it says outright that it will not fall back. brew
# bundle taps in passing and installs from the tap like any other formula.
#
# `trusted: true` because Homebrew refuses to load a formula from a third-party tap
# it has no trust entry for. Naming one fully qualified on the command line is
# consent, which is why the `brew install timche/tap/boswell` this replaced never
# had to say so; a bare `brew "boswell"` in a Brewfile is not, and brew bundle
# writes the trust entry from this option before it loads anything. An older
# Homebrew with no trust store ignores the option.
tap "timche/tap", trusted: true
brew "boswell"

# macOS ships zsh itself, so the highlighting is the only half of the shell left to
# install — and install.sh is fatal without brew for exactly this: a shell that came
# out of a provision with no highlighting and nothing saying why.
brew "zsh-syntax-highlighting"

brew "fd"
brew "ffmpeg"
brew "glow"
brew "herdr"
brew "ripgrep"
brew "shellcheck"
brew "starship"
brew "zoxide"

# The secrets scan .gitconfig runs before every commit in every repository here,
# boswell's auto-commits included. Betterleaks rather than gitleaks, whose author
# declared it feature complete and moved to this successor.
brew "betterleaks"

# The machine with a display. Nothing declares `adopt`, which the mise entry this
# replaced had to: brew bundle passes --adopt to every cask install of its own
# accord, and that is what takes over the Chrome a macOS runner's image already has
# instead of refusing to lay a cask over an app that arrived by another route.
cask "google-chrome"

# Windows on ARM for the Windows half of an Electron app, which CI otherwise tests
# first. UTM rather than Parallels, whose command line is its paid edition's, or
# VMware Fusion, whose download sits behind a Broadcom account Homebrew cannot
# reach; utmctl starts and stops the VM from an SSH session. Windows only: Tart,
# below, is the one for macOS and Linux guests.
cask "utm"

# macOS and Linux guests: a CLI throughout (clone, run --no-graphics, ip, then
# SSH), images pulled from a registry rather than installed by hand, and clones
# cheap enough to throw one away per test or CI job. No Windows, which is why UTM
# stays. Fair Source, royalty-free on a personal machine and under 100 cores. From
# openai/tools, where the project moved from Cirrus Labs — cirruslabs/cli still
# publishes a formula current Homebrew refuses to read — and trusted for the
# reason given at boswell's.
tap "openai/tools", trusted: true
brew "openai/tools/tart"
