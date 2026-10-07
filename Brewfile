# Every Homebrew package this Mac has, applied with `brew bundle --no-upgrade` —
# by bootstrap-system.sh in the machine phase, and again by install.sh once the
# account is being built. One declarative list for both.
#
# --no-upgrade, so a re-run installs what is missing and moves no version. That
# matters more here than on a box you throw away: this Mac is reached over the
# very sshd and tailnet a run touches. `brew bundle upgrade` moves them when that
# is what is wanted.
#
# mise carries language runtimes and the npm packages that run on the node it
# provides; everything else a CLI is belongs here.
#
# xcodes and aria2 are deliberately not here. Both exist only for the Xcode
# download, which is xcode.sh's and happens only where there is a terminal to type
# an Apple ID at — so declaring them would install two packages on every
# unattended provision for a step it is never going to run, and would turn aria2's
# absence from a slower download into a failed dependency.

# gh clones with the token it holds, and git's credential helper shells out to it
# for every push made here; op reads the signing key and the signing certificates
# out of 1Password; jq patches a config in docker.sh and in install.sh. btop is the
# one that is only for whoever logs in to look at the machine.
brew "btop"
brew "gh"
brew "jq"

# The formula, not the cask of nearly the same name: the cask is the standalone
# app, and an app is a login item inside a GUI session, where tailscale.sh makes
# this a root system daemon that is up before anybody logs in.
#
# This one is the exception to `brew bundle upgrade` being the whole of an
# upgrade: root runs a copy of these binaries rather than the formula's own, so
# `brew upgrade tailscale` moves nothing until tailscale.sh is re-run — which is
# also the point at which the daemon is restarted, from the LAN.
brew "tailscale"

# docker on a Mac is a Linux VM and a CLI pointed into it, and OrbStack is both:
# the VM, the docker CLI, compose and buildx all come out of the one app, which is
# why no docker formula is declared beside it. Its memory is dynamic and returns
# to macOS as the VM stops using it, which a Mac running browsers and Electron
# outside the VM needs.
cask "orbstack"

# The cask rather than a formula because 1Password ships the CLI itself. Nothing
# declares `adopt`: brew bundle passes --adopt to every cask install of its own
# accord, which is what takes over a copy that arrived by another route instead of
# refusing to lay a cask over it.
cask "1password-cli"

# macOS ships zsh itself, so the highlighting is the only half of the shell left
# to install.
brew "zsh-syntax-highlighting"

brew "fd"
brew "ffmpeg"
brew "glow"
brew "pscale"
brew "ripgrep"
brew "shellcheck"
brew "starship"
brew "tuicr"
brew "zoxide"

# The secrets scan .gitconfig runs before every commit in every repository here,
# `hachiko sync`'s included. Published from a tap of its own, and `trusted: true`
# because Homebrew refuses to load a formula from a third-party tap it has no trust
# entry for: naming one fully qualified on the command line is consent, a bare
# `brew` line in a Brewfile is not, and brew bundle writes the trust entry from this
# option before it loads anything. An older Homebrew with no trust store ignores it.
tap "timche/tap", trusted: true
brew "betterleaks"

# The browser sessions drive.
cask "google-chrome"

# The virtual machines, Windows on ARM for the Windows half of an Electron app
# among them. UTM rather than Parallels, whose command line is its paid edition's,
# or VMware Fusion, whose download sits behind a Broadcom account Homebrew cannot
# reach; utmctl starts and stops a VM from an SSH session.
cask "utm"
