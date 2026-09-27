# Mac mini Setup

[![test](https://github.com/timche/mac-mini-setup/actions/workflows/test.yml/badge.svg)](https://github.com/timche/mac-mini-setup/actions/workflows/test.yml)

Provisioning for a headless Apple Silicon Mac, and optionally for the one that runs Claude Code. As the account the machine is for, which unlike a VM's already exists:

```sh
curl -fsSL https://raw.githubusercontent.com/timche/mac-mini-setup/main/bootstrap.sh | bash            # a plain Mac
curl -fsSL https://raw.githubusercontent.com/timche/mac-mini-setup/main/bootstrap.sh | bash -s claude  # the same, plus Claude Code
```

A fresh Mac has no git and no package manager, so there is nothing to clone this with — which is what `bootstrap.sh` is for. It installs Homebrew, which installs the Xcode command line tools on the way through `softwareupdate` rather than the dialog nobody is in front of, clones this repo to `~/.mac-mini-setup` and runs `machine.sh` as the account you are logged in as. The second form runs `claude.sh` on top, and that is the whole of the difference.

The clone stays, unlike `debian-setup`'s, because a Mac is a machine you pull and re-run rather than one you reprovision from a URL — hidden for the same reason `~/.mac-mini-dotfiles` is, since it is machinery rather than work:

```sh
git -C ~/.mac-mini-setup pull
~/.mac-mini-setup/machine.sh
~/.mac-mini-setup/claude.sh
```

`MAC_MINI_SETUP_DIR` puts it somewhere else. It is not a clone to delete, either: the LaunchAgent that holds the signing key runs `launchd/agent.sh` out of it through a symlink, so moving the checkout means re-running `claude.sh` to point the links at the new place.

Both halves are safe to re-run, and both are careful about what is already there. That is the difference from provisioning a VM: a VM is thrown away and built again, while this Mac is re-run in place and is reached over the sshd and the tailnet the run itself configures, so a rewritten sshd or a bounced tailscaled would cut the run off from the machine it is running on.

## A fresh Mac

What the scripts cannot do happens at the Mac itself, with a screen and a keyboard, before the first run:

1. Setup Assistant: the account the machine is for, as an administrator. An Apple ID only matters for Xcode, which `xcodes` downloads with it; the iCloud features can all stay off.
2. FileVault off, then Automatic login for that account in System Settings > Users & Groups. Auto-login needs FileVault off, and everything that lives in the login session waits for it after every restart.
3. Remote Login and Screen Sharing on, in System Settings > General > Sharing: the first is how the bootstrap is run from another machine over the LAN, the second is how everything that needs a click gets done once the screen is gone, including the [privacy permissions](#privacy-permissions) below. Screen Sharing is the one of the two no script can turn on, because that pane is also what registers the rights the sharing agent needs.

Then, over SSH on the LAN, the `bash -s claude` form above. It asks, in order, for the sudo password, a tailnet login URL, the Apple ID and a 2FA code for Xcode, then a GitHub device code, a Claude Code login and the 1Password service-account token.

Afterwards, over Screen Sharing: the [privacy permissions](#privacy-permissions) below, System Settings > Lock Screen > "Require password after screen saver begins or display is turned off" set to Never, and OrbStack opened once — its welcome screen is where OrbStack's terms are accepted and where Docker is chosen, and no script can click either. `machine.sh` names all three every time it runs, so a Mac that is missing one says so rather than being quietly unable to take a screenshot or start a container.

And in the Tailscale admin console: approve the advertised subnet and exit node, add an `ssh` rule for whoever should reach the Mac, and disable key expiry for it, since a node whose key expires drops off the tailnet after 180 days until somebody logs in at it again.

## The machine

`machine.sh` installs Homebrew and the `Brewfile`'s packages, turns Remote Login on if it is off, brings tailscale up as a system daemon serving Tailscale SSH, puts docker on the Mac as OrbStack, hardens sshd down to keys only, no root, one user, and — only where there is a terminal to type an Apple ID at — installs Xcode.

Every package is in the `Brewfile` at the repo root, applied by `bootstrap-system.sh` with `brew bundle --no-upgrade`. One declarative list, including the three the Claude overlay is the only caller of and the OrbStack cask `docker.sh` configures: Homebrew is the machine, so `docker.sh` and `tailscale.sh` are left with the decision only they can make — about the VM's settings and the system daemon — and say what is missing rather than installing it. `--no-upgrade` because this Mac is re-run in place over the very sshd and tailnet a run touches, and `brew upgrade tailscale` restarts the daemon carrying the SSH session; `brew bundle upgrade` is the deliberate version of that. A failed package is not fatal, the same trade `docker.sh` always made — the exception is `gh`, `jq` and `op`, which `bootstrap-system.sh` stops on, since nothing after it works at all without them.

`xcodes` and `aria2` are the two packages the `Brewfile` deliberately leaves out. Both exist only for the Xcode download, which happens only where there is a terminal, so declaring them would install them on every unattended provision for a step it is never going to run — and `aria2` is optional even there, a faster download rather than a dependency. `xcode.sh` installs both itself, once it has decided it is going ahead.

`unattended.sh` is the part that makes several parallel sessions on it possible, and all of it is one idea: nothing on this Mac may stop and wait for a click that nobody is there to make.

- **Restart after power loss, and never sleep.** `pmset autorestart 1`, `sleep 0`, `disksleep 0`. A Mac nobody can walk up to is otherwise gone until somebody does. `displaysleep` is left alone: there is no display, and blanking one that is not there costs nothing.
- **No crash dialogs.** `com.apple.CrashReporter DialogType none`, per user. The Electron apps under development here crash as part of the work, and each crash otherwise leaves Problem Reporter's window on the screen with the crashed process waiting behind it. The report still lands in `~/Library/Logs/DiagnosticReports`, which is where it is any use. Per user rather than per machine because the two `ReportCrash` processes read the domain of whoever owns the session the crash happened in.
- **No screensaver.** `defaults -currentHost write com.apple.screensaver idleTime -int 0`, in the ByHost domain even though Sonoma moved the engine that reads it into a sandbox. This is the half of the screen lock a script can still write; the lock itself is in the pane-only list below.
- **macOS updates download but never install.** `AutomaticDownload` on and `AutomaticallyInstallMacOSUpdates` off, in `/Library/Preferences/com.apple.SoftwareUpdate`, plus `softwareupdate --schedule on` for the check itself — `AutomaticCheckEnabled` used to be the fifth key there and macOS 26 drops it, taking the write and leaving the key absent from the file afterwards. An update that installs itself restarts the Mac, and a restart takes every session's worktree state, every running build and every browser with it, then sits at the login window if auto-login is off. Downloaded means installing it is a decision somebody makes, and takes minutes rather than an hour. `ConfigDataInstall` and `CriticalUpdateInstall` stay on — XProtect and the security data files install in place, with no restart and no dialog. Rapid Security Response, renamed Background Security Improvement in 2026, is not settable here at all: it moved to declarative device management, which needs a supervised MDM enrollment. It is on, and it can restart the Mac.
- **No Spotlight indexing.** `sudo mdutil -a -i off`. A checkout is a few thousand files; the same checkout with `node_modules` in it is a few hundred thousand, and every worktree is another copy, so `mds` spends a core walking files nobody on a headless Mac will ever search for by name — again after every install. This used to be a privacy list naming the project folders, and that needed a click: only the Spotlight pane hands the list to the running `mds`, and a write to the `Exclusions` array underneath it is ignored. `mdutil` is scriptable and `mdutil -s /System/Volumes/Data` reads the result back without sudo, so `unattended.sh` turns it off and `assert.sh` asserts it rather than reporting it. What goes with it: ⌘Space and Finder search at the Mac itself, `mdfind`, and the dSYM lookup that symbolicates a native crash report.

Three things it deliberately does not do, because each needs somebody looking at the screen, which on a headless Mac means Screen Sharing. All three are named on every run rather than assumed:

- **Auto-login.** This matters more than it looks: a LaunchAgent lives in the `gui/<uid>` domain, which exists only while somebody is logged in at the console, so a Mac sitting at its login window is a Mac where neither docker nor anything the Claude half installs is running. Tailscale is deliberately not in that list — its daemon is a LaunchDaemon and comes up without a session, which is why the Mac stays reachable even when this goes wrong. Turning auto-login on means writing the account password to `/etc/kcpassword`, obfuscated rather than encrypted, and that is a decision for whoever owns the Mac rather than for a script. It needs FileVault off. System Settings > Users & Groups > Automatic login.
- **Screen Sharing.** `launchctl enable system/com.apple.screensharing` clears the `Disabled` flag exactly as `remote-login.sh` does for sshd, and for this job that is only half of it: the screen recording rights the sharing agent needs are registered by the Sharing pane itself, so a Mac enabled from a script comes out with the job loaded, the toggle still reading off and nothing answering on port 5900. So `unattended.sh` looks at the port rather than at launchd's opinion of the job, and asks for System Settings > General > Sharing. It is the one to fix first, since it is how the screen lock below and the privacy permissions get done.
- **The screen lock.** System Settings > Lock Screen > "Require password after screen saver begins or display is turned off", set to Never. A locked session keeps running — sshd answers, launchd keeps its jobs, builds finish — but every screenshot and recording taken over SSH comes out black, so a session cannot show that what it changed works. On macOS 27 this is the pane's alone: the setting is in no preference domain, `com.apple.screensaver` has no `askForPassword` any more and its ByHost half holds `idleTime` and nothing else. `sysadminctl` is not a way in either. It still answers "screenLock delay is immediate" with the pane set to Never, so its reading cannot be trusted and nothing asserts it, and `sudo sysadminctl -screenLock off -password -` over Tailscale SSH fails with `MKBDeviceSetGracePeriod error -17` and changes nothing. So `unattended.sh` names the pane setting and neither writes it nor reads it back. What the pane actually did is checked by watching the session: `pmset displaysleepnow`, then `caffeinate -u -t 2` to wake it, and a `screencapture` that comes back a real desktop.

`harden-ssh.sh` refuses to disable password logins unless the user has a key sshd could actually let them in with, because on a Mac there is no provider console to fall back to. A Mac reached with a password comes out of a run unhardened and told what to do about it. `FORCE_HARDEN=true` overrides that if you are certain of another way in.

## Privacy permissions

Three grants that no script can make, granted once each over Screen Sharing and then good until somebody revokes them. TCC only ever asks the screen, and there is no supported way in from an SSH session: the consent dialog cannot appear in a session with no window server of its own, and writing the grant into `TCC.db` by hand needs SIP off.

The one thing worth knowing before the list: over SSH the process TCC asks about is the SSH server's, not the command's, and there are two servers here. Over the tailnet it is Tailscale SSH, so the entry is `tailscaled`: the first capture or Apple event from a tailnet session puts a consent dialog for it on the Mac's screen, and until someone clicks it over Screen Sharing `screencapture` fails with "could not create image from display" and an `osascript` to System Events hangs until its Apple event times out. Homebrew's `tailscaled` is ad-hoc signed, so a `brew upgrade tailscale` can leave TCC holding a grant for a binary that is no longer there; check again after one. On the LAN it is sshd: add `/usr/libexec/sshd-keygen-wrapper`, and on macOS 15 and later `com.apple.sshd-session` as well — the OpenSSH 9.8 split means either can be the one that gets asked, and granting both costs nothing. The panes take no drag from a remote session, so use the **+** button and ⇧⌘G to type a path.

- **Screen Recording** — System Settings > Privacy & Security > Screen & System Audio Recording. For `screencapture` and every recording taken over SSH, which is how a session shows a change working rather than asserting it, and for herdr, which reads the panes it drives. Without it `screencapture` still exits 0 and still writes a PNG; the PNG is black.
- **Accessibility** — System Settings > Privacy & Security > Accessibility. The same entries. This is what lets a session reach into another app's windows — focus, keystrokes, clicks — which is how herdr drives anything with a GUI.
- **Notifications** — System Settings > Notifications, per app rather than per binary. For the Electron app under development, so that a notification it posts is delivered rather than dropped. Its entry only exists once the app is signed and has asked for permission at least once, so the order is run it, let it ask, then allow it; before that there is nothing in the list to switch on.

To check them afterwards, over SSH:

```sh
caffeinate -u -t 2; screencapture -x /tmp/shot.png && ls -l /tmp/shot.png
```

A real desktop is hundreds of kilobytes. A black frame is a few, whatever the resolution — which is the tell, since the exit status and the file are the same either way. Copy it off and look at it if the size is ambiguous.

The `caffeinate` is what makes that tell mean anything. `displaysleep` is left at its ten minutes — there is no display to blank — and a capture taken while the display is asleep comes back black at around 40KB, which reads exactly like a missing grant. `caffeinate -u -t 2` asserts user activity long enough to wake it first.

```sh
osascript -e 'tell application "System Events" to count processes'
```

Accessibility: a count means the grant is there, and `-25211 osascript is not allowed assistive access` means it is not.

macOS 15 and later also put up a "requesting to bypass the system private window picker" dialog every so often, even with the grant in place. It is the one modal this repo cannot switch off, and it needs Screen Sharing and a click — worth knowing about when screenshots start coming back wrong for no reason.

## Tailscale

`tailscale.sh` starts the open-source `tailscaled` the `Brewfile` installed as a root system daemon with `sudo brew services start tailscale`, and sets the three prefs this Mac is on the tailnet for: Tailscale SSH, its LAN advertised as a subnet, and itself offered as an exit node.

The daemon rather than the standalone app, even though both can serve Tailscale SSH. A system daemon runs before anybody logs in, so a Mac whose auto-login fails or whose GUI session dies is still on the tailnet and still reachable — where the app is a login item inside a session, which is the dependency that already makes docker and the signing agent wait for one. Tailscale call this the less-tested variant on macOS and point unattended installs at it, which is what this Mac is. `sudo brew services start` rather than `sudo tailscaled install-system-daemon`, which Tailscale documents next to it, because the brew service runs Homebrew's own binary: `brew upgrade tailscale` moves the daemon with it, where `install-system-daemon` copies the binary to `/usr/local/bin` and pins the daemon to that copy.

The subnet comes from the interface the default route leaves by — its address and netmask, turned into a network and a prefix — so a Mac moved to another LAN needs a re-run rather than an edit. `TS_ADVERTISE_ROUTES` overrides it, and set-but-empty advertises no subnet at all. Nothing here touches IP forwarding: on macOS Tailscale enables it itself when routes are advertised. An exit node on macOS routes in userspace and only while the machine is awake, which is what `unattended.sh`'s `pmset sleep 0` is for.

A node that has never logged in needs `tailscale up`, which prints a URL to open on a machine that has a browser and then waits for it — so `tailscale.sh` runs it only where there is a terminal to wait at, and prints the command when there is not. Everything after that is `tailscale set`, which changes prefs without starting a login, and which only runs where the prefs differ from what the script asks for. A node already advertising what this asks for comes out of a run untouched.

MagicDNS is the one thing the daemon will not do for itself: it leaves the system resolver alone where the app rewrites it. So `tailscale.sh` writes `/etc/resolver/<tailnet>.ts.net` holding `nameserver 100.100.100.100`, which sends tailnet names to Tailscale and leaves every other lookup with the resolvers the Mac already had. Pointing the machine's own DNS servers at 100.100.100.100 would be the other way to do it, and would break all DNS whenever Tailscale is down.

Two things only the tailnet can do, both in [the admin console](https://login.tailscale.com/admin): approve this machine's advertised subnet and its exit node, unless `autoApprovers` in the policy file already covers them, and allow Tailscale SSH to it with an `ssh` rule saying who may connect and as whom. Until that rule exists nothing reaches the SSH server tailscaled is running.

To see where it is:

```sh
tailscale status                       # the node, the tailnet, and who else is on it
sudo tailscale debug prefs             # RunSSH, and AdvertiseRoutes with the subnet and 0.0.0.0/0, ::/0
sudo brew services list                # whether the daemon is loaded
sysctl net.inet.ip.forwarding          # 1 once routes are advertised, and Tailscale's doing
scutil --dns | grep -B2 -A2 100.100.100.100   # the resolver file, as macOS reads it
```

## Docker

Docker here is OrbStack, the one cask in the `Brewfile`: it runs the Linux VM under Virtualization.framework and links its own `docker`, `docker compose` and `docker buildx` into `/usr/local/bin` and `~/.docker/cli-plugins`, so no docker formula is installed beside it. `docker.sh` does what is left — the settings, and saying what it cannot do itself.

OrbStack rather than colima, which this replaced on 2026-09-27. A colima VM rarely handed memory back: whatever a build or a test suite made it touch stayed taken until somebody restarted it, on a Mac whose parallel sessions run browsers and Electron *outside* the VM and want that memory for themselves. OrbStack's memory is dynamic and returns to macOS as the VM stops using it, which is the whole reason for the switch — so its `memory_mib` is a ceiling rather than a reservation and `docker.sh` leaves it at OrbStack's default of half the machine. Not Docker Desktop, which is heavier again for the same licence terms.

The one thing it costs: OrbStack is free for personal use, and commercial use needs a paid licence bought per seat from OrbStack. A Mac that builds anything sold from it needs one, and nothing in this repo can buy or apply it — an unlicensed install runs on a Pro trial and then asks.

The first run is the app's own and needs somebody at the screen, which on this Mac means Screen Sharing: a welcome screen whose Next accepts OrbStack's terms, then a choice between Docker and Linux machines. `docker.sh` stops at `orb status` — the only thing it asks of OrbStack until that answers `Running`, since an install with no first run behind it has no settings to read and `orb config show` against one hangs rather than failing — and says what to click; `machine.sh` lists it under what is left. Everything OrbStack links — the CLIs, the plugins, the `orbstack` docker context, the `Include ~/.orbstack/ssh/config` in `~/.ssh/config` — it does for itself at that first run, and it touches no shell rc file.

After it, `docker.sh` settles two settings and only where `orb config show` disagrees: **start at login**, and **every core but two**, read from the hardware so that a different Mac needs no edit. It restarts the VM with `orb stop && orb start` only when the core count changed, since that is the one of the two the VM reads when it boots. A re-run on a settled Mac prints where it stands and touches nothing.

Start at login means a login item, and a login item lives in the GUI session — so docker is running only once the Mac has logged itself in, exactly like the agent holding the signing key. A Mac at its login window has no docker.

To see where it is:

```sh
orb status                       # whether the VM is up
orb config show                  # every setting, including the two docker.sh sets
docker context show              # orbstack, the context OrbStack sets for itself
docker run --rm hello-world
docker compose version           # the plugins OrbStack links into ~/.docker/cli-plugins
```

The VM, its disk, its images and its volumes are all under `~/.orbstack`. `orb config set cpu <n>` and `orb config set memory_mib <n>` change the shape of it, and take effect at the next `orb stop && orb start`; re-running `docker.sh` puts the core count back to what this Mac works out to.

## Xcode

`xcode.sh` installs the full Xcode, which this Mac needs for signing builds of meru with a Developer ID certificate — the command line tools Homebrew brought are enough to compile but not the whole toolchain electron-builder reaches for. It is the one step of the machine `machine.sh` will not do unattended: Apple hands nobody an Xcode without an Apple ID, a 2FA code typed in while it is still valid, and the account password for the privileged end of the install. `machine.sh` runs it when there is a terminal and lists it under what is left when there is not.

```sh
~/.mac-mini-setup/xcode.sh
```

It stops before downloading anything if `/` has less than 40GB free, since the xip is around 11GB and unpacks to more than twice that before the copy into `/Applications`. The download comes through [`xcodes`](https://github.com/XcodesOrg/xcodes) — `--latest`, so a release and never a beta — and then the script selects what it installed, accepts the licence and runs the first launch, each one guarded so that a re-run asks for nothing.

`xcodes` remembers the Apple ID and keeps its password in the login keychain. This Mac is reached over SSH, where a keychain that needs to ask for anything cannot, so that write may be refused — in which case xcodes says so and asks for the Apple ID again next time, `XCODES_USERNAME` and `XCODES_PASSWORD` in the environment answer it without a keychain at all, and `xcodes signout` clears whatever is stored.

To see where it is:

```sh
xcode-select -p        # /Applications/Xcode-<version>.app/Contents/Developer
xcodebuild -version
xcodes installed       # every Xcode on the Mac, and which one is selected
```

Nothing here installs the Developer ID certificate, and nothing should: electron-builder takes it from `CSC_LINK` and `CSC_KEY_PASSWORD` and imports it into a keychain of its own for the length of a build. It comes out of 1Password at that point, through a wrapper in `mac-mini-dotfiles`, so the login keychain never holds it and a Mac reprovisioned from here has nothing to re-import.

## The Claude Code overlay

`claude.sh` installs nothing — every package is Homebrew's and Homebrew is the machine's. What it does is the part that needs an account: it logs in to GitHub and to Claude Code, installs the commit-signing key, and hands over to `mac-mini-dotfiles` for everything after that — the shell, the prompt, the runtimes and `~/.claude`. Run it against a Mac `machine.sh` has already built, and rerun it when a token expires.

The GitHub token goes in a file rather than the login keychain (`gh auth login --insecure-storage`). This Mac is reached over SSH and does its work from LaunchAgents, and both of those meet a keychain that will not answer with a prompt nobody sees. Claude Code keeps its own credential in the keychain and falls back to `~/.claude/.credentials.json` when the write is refused, which is what an SSH session usually gets; if the login does not take, `claude setup-token` prints a token that lasts a year and `CLAUDE_CODE_OAUTH_TOKEN` carries it instead.

## The signing key

The key is the same one every machine here signs with, and it lives in 1Password. Neither half is written to this Mac. The private half is read out of 1Password straight into an ssh-agent, over a pipe, every time that agent starts; the public half is read only to trust it and to register it, and `mac-mini-dotfiles`' `.gitconfig` names no key at all — it sets `gpg.ssh.defaultKeyCommand` to `ssh-add -L`, which is git's own way of asking the agent, and with no `user.signingkey` git signs with the first key it answers with. There is no file to leak and nothing to paste.

The one file derived from the key is `~/.ssh/allowed_signers`, a principal and a public key so that git can verify what it signs. It is not a second source: `claude/signing-key.sh` rewrites it from 1Password on every run, so a rotated key takes the line for the old one with it.

What it expects:

- A 1Password item with the key in it, at `op://Mac Mini/SSH Key`, whose `private key` and `public key` fields are the two halves. `SIGNING_KEY_OP_ITEM` names a different one.
- A 1Password service account with read access to that vault. `claude/signing-key.sh` asks for its token once, without echoing it, checks it can read the item, and stores it in `~/.config/op/service-account-token` for the agent to read at every start.

The agent is a LaunchAgent, `io.github.timche.ssh-agent`, running `~/.ssh/agent.sh`, which is a symlink to `launchd/agent.sh` in the checkout: it starts an `ssh-agent` on a fixed socket at `~/.ssh/agent.sock`, loads the key into it, and then waits on it, so that launchd restarting the pair is also what re-reads the key. The socket is fixed because the one launchd hands out belongs to the agent macOS starts for each session, which holds nothing of this and is not visible to an SSH login at all — `mac-mini-dotfiles`' `.zshenv` and its own LaunchAgents name `~/.ssh/agent.sock` instead. At boot the key cannot be read until the network is up, so the agent comes up empty and keeps trying with a widening delay.

Both halves of it are links into the checkout rather than copies, so a pull is all a change to either needs — `~/Library/LaunchAgents/io.github.timche.ssh-agent.plist` points at the plist in `launchd/` and `~/.ssh/agent.sh` at the wrapper beside it. launchd accepts a symlinked agent plist and hands the job `HOME`, so that plist holds no absolute path at all: `/bin/sh -c 'exec "$HOME/.ssh/agent.sh"'` is what expands one, and the wrapper derives the socket, the log and the token file from `$HOME` itself. It reads a plist only when the job is bootstrapped, though, so `claude/ssh-agent.sh` still boots the job out and back in when the plist changed, and restarts it with `launchctl kickstart -k` when only the wrapper did — which it knows from a hash of the wrapper kept in `~/.ssh/agent.sh.sha256`.

To see where it is:

```sh
launchctl print gui/$(id -u)/io.github.timche.ssh-agent   # loaded, and what launchd makes of it
SSH_AUTH_SOCK=~/.ssh/agent.sock ssh-add -l                # the key, or nothing yet
tail -f ~/Library/Logs/ssh-agent.log                      # why it is nothing yet
git commit --allow-empty -m test && git log --format='%G?' -1
```

`error: user.signingKey needs to be set for ssh signing` from that commit means exactly one thing: `ssh-add -L` answered with nothing, so the agent is not up or is not holding the key yet. It is the honest failure — before the key came from the agent alone, an empty agent produced an `invalid format` instead, git having tried to read the public-key file as a private one.

## Two things worth knowing

There are two ways in and different things govern them. Over the tailnet it is Tailscale SSH, which tailscaled answers itself: the policy file decides who may connect and as whom, and nothing in the sshd configuration has any say over those sessions. On the LAN it is sshd, which `harden-ssh.sh` locks down to keys, no root and one user, and which is what is left when Tailscale is down. Keeping both is deliberate — a tailnet that cannot be reached is no reason to be locked out of a machine in the next room — and Screen Sharing is the third door, for whoever can walk up to it.

macOS reads `sshd_config` per connection — launchd holds port 22 and spawns an sshd per client — so there is nothing to restart after the drop-in lands, and a config it cannot parse breaks the next login rather than waiting for one. That is why the drop-in is checked with `sshd -t` and taken straight back out if it does not hold up.

## Environment knobs

`MAC_MINI_SETUP_REPO`, `MAC_MINI_SETUP_DIR`, `DOTFILES_REPO`, `DOTFILES_DIR`, `SIGNING_KEY_OP_ITEM`, `OP_SERVICE_ACCOUNT_TOKEN_FILE`, `FORCE_HARDEN`, `TS_ADVERTISE_ROUTES`.

`CLAUDE.md` has the details: the order the scripts run in, the constraints that are not obvious from reading them, and how to test.
