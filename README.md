# mac-mini

[![test](https://github.com/timche/mac-mini/actions/workflows/test.yml/badge.svg)](https://github.com/timche/mac-mini/actions/workflows/test.yml)

The Mac mini Claude Code runs on and controls: a headless Apple Silicon Mac that exists for agentic coding, several sessions at a time, reached over the tailnet from somewhere else. This repository is the whole of it — the machine and the account on it — and one command puts it there. As the account the machine is for, which unlike a VM's already exists:

```sh
curl -fsSL https://raw.githubusercontent.com/timche/mac-mini/main/bootstrap.sh | bash
```

A fresh Mac has no git and no package manager, so there is nothing to clone this with — which is what `bootstrap.sh` is for. It installs Homebrew, which brings the Xcode command line tools through `softwareupdate` rather than the dialog nobody is in front of, clones this repo to `~/.mac-mini` and runs its two phases: `machine.sh` builds the Mac, `claude.sh` makes it the one Claude Code works from.

The clone stays, because a Mac is a machine you pull and re-run rather than one you reprovision from a URL — hidden because it is machinery rather than work:

```sh
git -C ~/.mac-mini pull
~/.mac-mini/machine.sh
~/.mac-mini/claude.sh
```

`MAC_MINI_DIR` puts it somewhere else. It is not a clone to delete: everything in `$HOME` is a symlink into it, and the LaunchAgents launchd loads are links into it too, so moving the checkout means re-running `claude.sh` to point the links at the new place.

Both phases are safe to re-run, and both are careful about what is already there. That is the difference from provisioning a VM: a VM is thrown away and built again, while this Mac is re-run in place and is reached over the sshd and the tailnet the run itself configures, so a rewritten sshd or a bounced tailscaled would cut the run off from the machine it is running on.

## A fresh Mac

What the scripts cannot do happens at the Mac itself, with a screen and a keyboard, before the first run:

1. Setup Assistant: the account the machine is for, as an administrator. An Apple ID only matters for Xcode, which `xcodes` downloads with it; the iCloud features can all stay off.
2. FileVault off, then Automatic login for that account in System Settings > Users & Groups. Auto-login needs FileVault off, and everything that lives in the login session waits for it after every restart.
3. Remote Login and Screen Sharing on, in System Settings > General > Sharing: the first is how the bootstrap is run from another machine over the LAN, the second is how everything that needs a click gets done once the screen is gone, including the [privacy permissions](#privacy-permissions) below. Screen Sharing is the one of the two no script can turn on, because that pane is also what registers the rights the sharing agent needs.

Then, over SSH on the LAN, the command above. It asks, in order, for the sudo password, a tailnet login URL, the Apple ID and a 2FA code for Xcode, then a GitHub device code, a Claude Code login and the 1Password service-account token.

Afterwards, over Screen Sharing: the [privacy permissions](#privacy-permissions) below, System Settings > Lock Screen > "Require password after screen saver begins or display is turned off" set to Never, and OrbStack opened once — its welcome screen is where OrbStack's terms are accepted and where Docker is chosen, and no script can click either. `machine.sh` names all three every time it runs, so a Mac that is missing one says so rather than being quietly unable to take a screenshot or start a container.

And in the Tailscale admin console: approve the advertised subnet and exit node, add an `ssh` rule for whoever should reach the Mac, and disable key expiry for it, since a node whose key expires drops off the tailnet after 180 days until somebody logs in at it again.

## The machine

`machine.sh` installs Homebrew and the `Brewfile`'s packages, turns Remote Login on if it is off, brings tailscale up as a system daemon serving Tailscale SSH, puts docker on the Mac as OrbStack, hardens sshd down to keys only, no root, one user, and — only where there is a terminal to type an Apple ID at — installs Xcode.

Every package is the `Brewfile` at the repo root, applied with `brew bundle --no-upgrade`: by `bootstrap-system.sh` here, and by `install.sh` in the account phase, one declarative list for both. `--no-upgrade` because this Mac is re-run in place over the very sshd and tailnet a run touches; `brew bundle upgrade` is the deliberate version of that. A failed package is not fatal — the exception is `gh`, `jq` and `op`, which `bootstrap-system.sh` stops on, since nothing after it works at all without them.

`xcodes` and `aria2` are the two packages the `Brewfile` deliberately leaves out. Both exist only for the Xcode download, which happens only where there is a terminal, so declaring them would install them on every unattended provision for a step it is never going to run — and `aria2` is optional even there, a faster download rather than a dependency. `xcode.sh` installs both itself, once it has decided it is going ahead.

`unattended.sh` is the part that makes several parallel sessions on it possible, and all of it is one idea: nothing on this Mac may stop and wait for a click that nobody is there to make.

- **Restart after power loss, and never sleep.** `pmset autorestart 1`, `sleep 0`, `disksleep 0`, `displaysleep 0`. A Mac nobody can walk up to is otherwise gone until somebody does. `displaysleep` is in that list although there is no display, because a `screencapture` taken while the display is asleep comes back a black frame, which is exactly what a missing Screen Recording grant looks like.
- **No crash dialogs.** `com.apple.CrashReporter DialogType none`, per user. The Electron apps under development here crash as part of the work, and each crash otherwise leaves Problem Reporter's window on the screen with the crashed process waiting behind it. The report still lands in `~/Library/Logs/DiagnosticReports`, which is where it is any use. Per user rather than per machine because the two `ReportCrash` processes read the domain of whoever owns the session the crash happened in.
- **No screensaver.** `defaults -currentHost write com.apple.screensaver idleTime -int 0`, in the ByHost domain even though the engine that reads it runs in a sandbox. This is the half of the screen lock a script can still write; the lock itself is in the pane-only list below.
- **macOS updates download but never install.** `AutomaticDownload` on and `AutomaticallyInstallMacOSUpdates` off, in `/Library/Preferences/com.apple.SoftwareUpdate`, plus `softwareupdate --schedule on` for the check itself. An update that installs itself restarts the Mac, and a restart takes every session's worktree state, every running build and every browser with it, then sits at the login window if auto-login is off. Downloaded means installing it is a decision somebody makes, and takes minutes rather than an hour. `ConfigDataInstall` and `CriticalUpdateInstall` stay on — XProtect and the security data files install in place, with no restart and no dialog. Background Security Improvement is not settable here at all: it lives in declarative device management, which needs a supervised MDM enrollment. It is on, and it can restart the Mac.
- **No Spotlight indexing.** `sudo mdutil -a -i off`. A checkout is a few thousand files; the same checkout with `node_modules` in it is a few hundred thousand, and every worktree is another copy, so `mds` spends a core walking files nobody on a headless Mac will ever search for by name — again after every install. `mdutil -s /System/Volumes/Data` reads the result back without sudo, so `assert-machine.sh` asserts it rather than reporting it. What goes with it: ⌘Space and Finder search at the Mac itself, `mdfind`, and the dSYM lookup that symbolicates a native crash report.

Three things it deliberately does not do, because each needs somebody looking at the screen, which on a headless Mac means Screen Sharing. All three are named on every run rather than assumed:

- **Auto-login.** This matters more than it looks: a LaunchAgent lives in the `gui/<uid>` domain, which exists only while somebody is logged in at the console, so a Mac sitting at its login window is a Mac where neither docker nor any of the agents below are running. Tailscale is deliberately not in that list — its daemon is a LaunchDaemon and comes up without a session, which is why the Mac stays reachable even when this goes wrong. Turning auto-login on means writing the account password to `/etc/kcpassword`, obfuscated rather than encrypted, and that is a decision for whoever owns the Mac rather than for a script. It needs FileVault off. System Settings > Users & Groups > Automatic login.
- **Screen Sharing.** `launchctl enable system/com.apple.screensharing` clears the `Disabled` flag exactly as `remote-login.sh` does for sshd, and for this job that is only half of it: the screen recording rights the sharing agent needs are registered by the Sharing pane itself, so a Mac enabled from a script comes out with the job loaded, the toggle still reading off and nothing answering on port 5900. So `unattended.sh` looks at the port rather than at launchd's opinion of the job, and asks for System Settings > General > Sharing. It is the one to fix first, since it is how the screen lock below and the privacy permissions get done.
- **The screen lock.** System Settings > Lock Screen > "Require password after screen saver begins or display is turned off", set to Never. A locked session keeps running — sshd answers, launchd keeps its jobs, builds finish — but every screenshot and recording taken over SSH comes out black, so a session cannot show that what it changed works. On macOS 27 this is the pane's alone: the setting is in no preference domain, `com.apple.screensaver` has no `askForPassword` and its ByHost half holds `idleTime` and nothing else. `sysadminctl` is not a way in either: it answers "screenLock delay is immediate" with the pane set to Never, so its reading cannot be trusted and nothing asserts it, and `sudo sysadminctl -screenLock off -password -` over Tailscale SSH fails with `MKBDeviceSetGracePeriod error -17` and changes nothing. So `unattended.sh` names the pane setting and neither writes it nor reads it back. What the pane actually did is checked by watching the session: `pmset displaysleepnow`, and a `screencapture` that comes back a real desktop rather than a login window.

`harden-ssh.sh` refuses to disable password logins unless the user has a key sshd could actually let them in with, because on a Mac there is no provider console to fall back to. A Mac reached with a password comes out of a run unhardened and told what to do about it. `FORCE_HARDEN=true` overrides that if you are certain of another way in.

## Privacy permissions

Three grants that no script can make, granted once each over Screen Sharing and then good until somebody revokes them. TCC only ever asks the screen, and there is no supported way in from an SSH session: the consent dialog cannot appear in a session with no window server of its own, and writing the grant into `TCC.db` by hand needs SIP off.

Which process is asked depends on how the session arrived. Every Claude Code session is a pane of the herdr server launchd runs in the GUI session, so the grants that matter day to day are herdr's, at `~/.local/bin/herdr`. herdr's release binary is ad-hoc signed and macOS pins a grant to an ad-hoc binary's exact contents, so a `herdr update` drops them and they are granted again. Over the tailnet without herdr the process TCC asks about is `tailscaled`, which answers Tailscale SSH itself; on the LAN it is sshd, where the entries are `/usr/libexec/sshd-keygen-wrapper` and `com.apple.sshd-session`, either of which can be the one that gets asked. The panes take no drag from a remote session, so use the **+** button and ⇧⌘G to type a path.

- **Screen Recording** — System Settings > Privacy & Security > Screen & System Audio Recording. For `screencapture` and every recording taken over SSH, which is how a session shows a change working rather than asserting it, and for herdr, which reads the panes it drives. Without it `screencapture` still exits 0 and still writes a PNG; the PNG is black.
- **Accessibility** — System Settings > Privacy & Security > Device Control and Data Access, the pane macOS 27 folds Accessibility into; the Accessibility link opens it. The same entries. This is what lets a session reach into another app's windows — focus, keystrokes, clicks — which is how herdr drives anything with a GUI.
- **Full Disk Access** — for herdr, so that a session can write Claude Code's native messaging host into `~/Library/Application Support/Google/Chrome` and read the profile back. Without it both are "Operation not permitted".
- **Notifications** — System Settings > Notifications, per app rather than per binary. For the Electron app under development, so that a notification it posts is delivered rather than dropped. Its entry only exists once the app is signed and has asked for permission at least once, so the order is run it, let it ask, then allow it; before that there is nothing in the list to switch on.

To check them afterwards, over SSH:

```sh
screencapture -x /tmp/shot.png && ls -l /tmp/shot.png
```

A real desktop is hundreds of kilobytes. A black frame is a few, whatever the resolution — which is the tell, since the exit status and the file are the same either way. Copy it off and look at it if the size is ambiguous. Nothing has to wake the display first: `unattended.sh` sets `displaysleep 0`, because a capture of a sleeping display is the same black frame a missing grant gives.

```sh
osascript -e 'tell application "System Events" to count processes'
```

Accessibility: a count means the grant is there, and `-25211 osascript is not allowed assistive access` means it is not.

macOS also puts up a "requesting to bypass the system private window picker" dialog every so often, even with the grant in place. It is the one modal this repo cannot switch off, and it needs Screen Sharing and a click — worth knowing about when screenshots start coming back wrong for no reason.

A session shows its own app rather than the display, which carries every other session's windows: `window-shot <owner> <out.png> [title]` captures one window without its shadow, Electron's child views included. It is `screencapture -x -o -l <window id>`, and the id is the part macOS has no command for, so the script asks `CGWindowListCopyWindowInfo` through the Command Line Tools' `swift`, about a quarter of a second with nothing to compile or install. The owner is the process name the window list uses — `Electron` for an unpackaged Electron app, the product name for a packaged one — and the list comes front to back, so the frontmost of that owner's on-screen windows wins unless a title substring picks another. No match exits 1 and names what is on screen instead.

## Tailscale

`tailscale.sh` runs the open-source `tailscaled` the `Brewfile` installed as a root system daemon of this repo's own, `io.github.timche.tailscaled`, and sets the three prefs this Mac is on the tailnet for: Tailscale SSH, its LAN advertised as a subnet, and itself offered as an exit node.

The daemon rather than the standalone app, even though both can serve Tailscale SSH. A system daemon runs before anybody logs in, so a Mac whose auto-login fails or whose GUI session dies is still on the tailnet and still reachable — where the app is a login item inside a session, which is the dependency that already makes docker and the signing agent wait for one.

Homebrew is where the binaries come from and what root executes is a copy of them, `/usr/local/bin/tailscaled` and `/usr/local/bin/tailscale`, root-owned at 755 under root-owned directories. Homebrew's prefix is not: `/opt/homebrew/opt` and the Cellar below it belong to the account, so a root daemon pointed at `/opt/homebrew/opt/tailscale/bin/tailscaled` — which is what `brew services` generates — is root for anything running as the account at the next daemon start, and the same goes for the log it appends to under `/opt/homebrew/var/log` and for every `sudo tailscale` this script runs. `tailscale.sh` replaces a copy only when it differs from the formula's binary, so a re-run with nothing new restarts nothing.

The plist is `system/launchd/io.github.timche.tailscaled.plist` in this repo, installed as a root-owned *copy* in `/Library/LaunchDaemons` rather than a symlink into the checkout, for the same reason as the sshd drop-in: it is root's job description, and a link would leave it in a directory the account can write. It is `KeepAlive`, `RunAtLoad`, logs to `/var/log/tailscaled.log`, and passes no arguments, which is what the `brew services` plist did too — so tailscaled keeps its macOS defaults, the state in `/Library/Tailscale` and the socket at `/var/run/tailscaled.socket`. Naming either would move it, and moving the state logs this Mac out of the tailnet.

Upgrading is `brew upgrade tailscale` and then `tailscale.sh` again, which copies the new binaries and restarts the daemon. **Run it from the LAN**, `ssh timche@<the Mac's 192.168.x.x address>` rather than over the tailnet, because the restart drops every tailnet connection including an SSH session over one; the script says so and asks first where there is a terminal to answer at. `tailscale.sh` also migrates a Mac still on the `brew services` daemon: it stops `sh.brew.tailscale` and bootstraps this one in the same step, so the gap is seconds.

The subnet comes from the interface the default route leaves by — its address and netmask, turned into a network and a prefix — so a Mac moved to another LAN needs a re-run rather than an edit. `TS_ADVERTISE_ROUTES` overrides it, and set-but-empty advertises no subnet at all. Nothing here touches IP forwarding: on macOS Tailscale enables it itself when routes are advertised. An exit node on macOS routes in userspace and only while the machine is awake, which is what `unattended.sh`'s `pmset sleep 0` is for.

A node that has never logged in needs `tailscale up`, which prints a URL to open on a machine that has a browser and then waits for it — so `tailscale.sh` runs it only where there is a terminal to wait at, and prints the command when there is not. Everything after that is `tailscale set`, which changes prefs without starting a login, and which only runs where the prefs differ from what the script asks for. A node already advertising what this asks for comes out of a run untouched.

MagicDNS is the daemon's own: tailscaled writes `/etc/resolver/<tailnet>.ts.net` and a file per reverse zone, each marked `# Added by tailscaled`, which send tailnet names to 100.100.100.100 and leave every other lookup with the resolvers the Mac already had. `tailscale.sh` leaves them alone, since tailscaled rewrites and removes the files it recognises as its own.

Two things only the tailnet can do, both in [the admin console](https://login.tailscale.com/admin): approve this machine's advertised subnet and its exit node, unless `autoApprovers` in the policy file already covers them, and allow Tailscale SSH to it with an `ssh` rule saying who may connect and as whom. Until that rule exists nothing reaches the SSH server tailscaled is running.

To see where it is:

```sh
tailscale status                       # the node, the tailnet, and who else is on it
sudo tailscale debug prefs             # RunSSH, and AdvertiseRoutes with the subnet and 0.0.0.0/0, ::/0
sudo launchctl print system/io.github.timche.tailscaled   # whether the daemon is loaded, and on what
tail -f /var/log/tailscaled.log        # what it has to say
sysctl net.inet.ip.forwarding          # 1 once routes are advertised, and Tailscale's doing
scutil --dns | grep -B2 -A2 100.100.100.100   # tailscaled's resolver files, as macOS reads them
```

## Docker

Docker here is OrbStack, the one cask `docker.sh` configures: it runs the Linux VM under Virtualization.framework and links its own `docker`, `docker compose` and `docker buildx` into `/usr/local/bin` and `~/.docker/cli-plugins`, so no docker formula is installed beside it. Its memory is dynamic and returns to macOS as the VM stops using it, on a Mac whose parallel sessions run browsers and Electron *outside* the VM and want that memory for themselves — so `memory_mib` is a ceiling rather than a reservation and `docker.sh` leaves it at OrbStack's default of half the machine.

The one thing it costs: OrbStack is free for personal use, and commercial use needs a paid licence bought per seat. A Mac that builds anything sold from it needs one, and nothing in this repo can buy or apply it — an unlicensed install runs on a Pro trial and then asks.

The first run is the app's own and needs somebody at the screen, which on this Mac means Screen Sharing: a welcome screen whose Next accepts OrbStack's terms, then a choice between Docker and Linux machines. `docker.sh` stops at `orb status` — the only thing it asks of OrbStack until that answers `Running`, since an install with no first run behind it has no settings to read and `orb config show` against one hangs rather than failing — and says what to click; `machine.sh` lists it under what is left. Everything OrbStack links — the CLIs, the plugins, the `orbstack` docker context, the `Include ~/.orbstack/ssh/config` in `~/.ssh/config` — it does for itself at that first run. It also appends a `# Added by OrbStack` block to `~/.zprofile`, which sources `~/.orbstack/shell/init.zsh` for the completions and `~/.orbstack/bin`; `~/.zprofile` is a link into this checkout, so that block is tracked here and stays.

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

`xcode.sh` installs the full Xcode, which this Mac needs for signing builds of an Electron app with a Developer ID certificate — the command line tools Homebrew brought are enough to compile but not the whole toolchain electron-builder reaches for. It is the one step of the machine `machine.sh` will not do unattended: Apple hands nobody an Xcode without an Apple ID, a 2FA code typed in while it is still valid, and the account password for the privileged end of the install. `machine.sh` runs it when there is a terminal and lists it under what is left when there is not.

```sh
~/.mac-mini/xcode.sh
```

It stops before downloading anything if `/` has less than 40GB free, since the xip is around 11GB and unpacks to more than twice that before the copy into `/Applications`. The download comes through [`xcodes`](https://github.com/XcodesOrg/xcodes) — `--latest`, so a release and never a beta — and then the script selects what it installed, accepts the licence and runs the first launch, each one guarded so that a re-run asks for nothing.

`xcodes` remembers the Apple ID and keeps its password in the login keychain. This Mac is reached over SSH, where a keychain that needs to ask for anything cannot, so that write may be refused — in which case xcodes says so and asks for the Apple ID again next time, `XCODES_USERNAME` and `XCODES_PASSWORD` in the environment answer it without a keychain at all, and `xcodes signout` clears whatever is stored.

To see where it is:

```sh
xcode-select -p        # /Applications/Xcode-<version>.app/Contents/Developer
xcodebuild -version
xcodes installed       # every Xcode on the Mac, and which one is selected
```

Nothing here installs the Developer ID certificate, and nothing should: electron-builder takes it from `CSC_LINK` and `CSC_KEY_PASSWORD` and imports it into a keychain of its own for the length of a build. A project resolves both from 1Password through varlock at that point, as below, so the login keychain never holds it and a Mac reprovisioned from here has nothing to re-import.

## The account

`claude.sh` is the second phase and the part that needs an account. It installs no packages of its own — every package is the `Brewfile`'s — and runs three steps in order:

1. `install.sh` links this checkout into `$HOME` and `~/.claude` and installs the tooling those files configure.
2. `claude/login.sh` logs in to GitHub and to Claude Code, both of which want a browser on some other machine, and reruns `install.sh` for the half that wanted a token.
3. `claude/signing-key.sh` puts the commit-signing key into an agent and registers its public half.

`install.sh` first, because `login.sh` logs in to a Claude Code that `install.sh` is what installs, and `signing-key.sh` writes the `user.email` from the `.gitconfig` `install.sh` links. Run `claude.sh` again when a token expires; run `install.sh` alone after a pull that changed a link or a tool.

The GitHub token goes in a file rather than the login keychain (`gh auth login --insecure-storage`). This Mac is reached over SSH and does its work from LaunchAgents, and both of those meet a keychain that will not answer with a prompt nobody sees. Claude Code keeps its own credential in the keychain and falls back to `~/.claude/.credentials.json` when the write is refused, which is what an SSH session usually gets; if the login does not take, `claude setup-token` prints a token that lasts a year and `CLAUDE_CODE_OAUTH_TOKEN` carries it instead.

## Layout

`home/` mirrors `$HOME`, and everything the account is made of is under it, `~/.claude` included.

| Path | |
| --- | --- |
| `home/.claude/CLAUDE.md` `settings.json` | Global instructions; hooks, effort, permission mode |
| `home/.claude/skills/` `hooks/` | Personal skills, and the scripts the hooks call |
| `home/.claude/agents/` | Custom subagents: the model, effort and instructions each role runs with |
| `home/.zshrc` `.zshenv` `.zprofile` `.bashrc` `.gitconfig` | Shell, PATH, prompt, signed commits |
| `home/.config/starship.toml` | The prompt |
| `home/.config/git/worktree-install` | The post-checkout hook that installs a new worktree's dependencies |
| `home/.config/herdr/config.toml` `home/.terminfo/x/xterm-ghostty` | herdr config, Ghostty terminfo |
| `home/.config/mise/config.toml` | Every runtime mise installs globally |
| `home/.config/boswell/config.toml` `home/Library/LaunchAgents/` | The repositories boswell watches, and the agents launchd runs |
| `home/.local/bin/` | The machine's own scripts: the worktree sweep, the disk and CPU watch, the `op` wrapper, the memory log, the window screenshot |
| `hachiko/` | A compiled tool, one folder per tool: the Go module the wrapper above builds |

`.claude` sits under `home/` rather than at the repo root because a `.claude` directory at a repo root is *project* configuration to Claude Code — this repo would load its own global settings and skills a second time whenever it was the working directory.

Everything else here is a shell script, and the tools at the root are the exception: a folder each, Go, the standard library and `CGO_ENABLED=0`, built with the `go` the root `mise.toml` declares. There is no release pipeline and nothing to publish, because there is nowhere to publish to — boswell commits every edit to this repository within seconds and the Mac pulls it, so the wrapper in `home/.local/bin/<tool>` is what turns a landed edit into a running binary. It hashes the tool's sources, and when that hash has moved it rebuilds into `~/Library/Caches/<tool>` and moves the result into place in one step; when the sources do not compile it logs one line and keeps the last binary that did, which is the whole point on a repository where main is what lands and a half-written edit reaches the Mac before the next one finishes it. Every other way the build can fail ends the same way and says which: a folder a pull has moved away, a mise that is not there, a go that is not installed — the last binary that worked keeps running, and the wrapper never downloads a toolchain from inside a LaunchAgent, which is `install.sh`'s job. A build that hangs is killed after two minutes. A run with nothing to build never reaches for mise at all, and the binary it execs is static: only the rebuild depends on the tooling, so a LaunchAgent keeps watching while mise or Homebrew is the thing that broke.

The links themselves are declared in `mise.toml` under `[dotfiles]` and applied by `mise bootstrap dotfiles apply`, which recognises links that already point at the right source and replaces anything else at a target, so a real file left at a link's target is moved aside to `<name>.backup`.

A tool that edits a linked file by writing a new one and renaming it over the path replaces the link with a regular file, and from then on its edits and every later one stay off GitHub. Re-running the bootstrap at that point discards the tool's edits, so fold them in first: `cp ~/.claude/settings.json ~/.mac-mini/home/.claude/settings.json`, then `ln -sfn ~/.mac-mini/home/.claude/settings.json ~/.claude/settings.json`. `test/assert.sh` fails on any link that has become a file.

## The account, and no account name

Nothing under `home/` spells a home directory out, so the account can be called anything: `timche` on the Mac, `runner` under CI. The hook commands in `settings.json` go through `$HOME` — they run through a shell, so it expands — the statusLine command names no path at all, and the LaunchAgents, in which launchd expands neither a `~` nor a `$HOME`, each answer for themselves: boswell's and the ssh-agent's run `/bin/sh -c` and leave every path to the shell, and worktree-gc's is rendered from a mise template because launchd reads its `WatchPaths` itself. `test/assert.sh` fails on a `/home/<name>` or `/Users/<name>` anywhere under `home/` or in `mise.toml`, because a path written for one account is a hook that never fires for another, and it fails silently rather than loudly.

`env` in `settings.json` is the one place a `$HOME` would not work: Claude Code reads those values straight out of the file, so `CLAUDE_CODE_TMPDIR` is exported from `.zshenv` instead.

## The shell and the tooling

`install.sh` makes zsh the login shell if anything has moved the account off it. A Mac logs in to `/bin/zsh` already, and the check is `dscl . -read /Users/<name> UserShell` rather than `getent`, which macOS does not have — read rather than assumed, because a `chsh` that has nothing to change would still ask for a password nobody is there to type. Nothing else here needs root, Homebrew wanting none of it.

mise is a version manager for language runtimes and nothing else. The global tool list in `home/.config/mise/config.toml` is `node`, `bun` and `python`, the three whose version can differ per project, and a runtime a repository needs for itself is declared in that repository's own root `mise.toml` — which is why `go` is in this one's, for the compiled tools under their own folders, and why another project's `go` or `rust` stays in that project's. `install.sh` runs `mise install --locked` from inside the checkout so that both lists are installed, and says so when the `go` it asks for could not be: a Mac without it keeps running the binary each wrapper already built and quietly stops picking up changes. Everything else the Mac has is in the `Brewfile`, while mise, Claude Code and herdr are their authors' and portless, ccstatusline and chrome-devtools-mcp are npm packages mise's npm backend keeps on the node it already provides.

mise itself is the exception to the rule it enforces, and it is the same exception Claude Code is: the authors' own installer beats a package of it. `install.sh` runs `curl -fsSL https://mise.run | sh` into `~/.local/bin` when that binary is missing. [mise's install page](https://mise.jdx.dev/installing-mise.html) calls that the recommended route and Homebrew's formula not the preferred one — Homebrew builds mise separately from the official release binaries, the formula trails upstream, and only a standalone install supports `mise self-update`.

Claude Code is nobody's package. Anthropic's installer puts it at `~/.local/bin/claude` and it updates itself in the background on the default `latest` channel, which is the point of taking it from there: a pinned Claude Code is a day behind by the afternoon, and mise would spend every install putting the pin back over the updater's work. `install.sh` runs `curl -fsSL https://claude.ai/install.sh | bash` when that binary is missing and leaves it alone afterwards. Nothing sets `DISABLE_AUTOUPDATER`.

Neither rc file names a path for mise. `.zshenv` puts the shims, then `~/.local/bin`, then Homebrew's prefix in front of the system directories, and both shells activate whichever `mise` that finds; `.bashrc` builds the same order for itself, since bash reads none of the zsh files. `~/.local/bin` ahead of Homebrew's prefix is what keeps a `brew install mise` from answering first, and `install.sh` asks whether `~/.local/bin/mise` exists rather than asking PATH for the same reason. `test/assert.sh` checks the binary every kind of zsh resolves, through `whence -p` rather than `command -v`, since `mise activate` leaves a shell function of that name in an interactive one.

Homebrew is also why there is a `.zprofile`. Its prefix is on no default PATH, so `.zshenv` puts `/opt/homebrew/bin` behind the mise shims and `~/.local/bin` and in front of the system directories; but macOS runs `path_helper` from `/etc/zprofile` in every login shell, which rebuilds PATH with the system directories first. `.zprofile` runs after that and re-reads `.zshenv`, and `typeset -U` there keeps the second pass from listing anything twice.

herdr is the third: its installer puts it at `~/.local/bin/herdr` when that binary is missing, and `herdr update` replaces it in place. That is why it is not Homebrew's — `herdr update` overwrites whichever binary it runs from, which under Homebrew is a Cellar path the formula then no longer matches.

ccstatusline is `"npm:ccstatusline"` in mise's tool list, and `settings.json` runs it through its shim. Installed once rather than fetched per draw: `npx -y ccstatusline@latest` measured about 710ms a draw and `npx --offline` still 625ms, because the cost is npm starting node twice rather than the registry, while an installed copy draws in about 320ms.

Every session has two MCP servers whichever project it is in, registered at user scope by `install.sh`. `chrome-devtools` is Google's Chrome DevTools MCP, `"npm:chrome-devtools-mcp"` in mise's tool list and run through its shim: it drives a page and also reads what a screenshot cannot — performance traces, network requests with their bodies, source-mapped console output, throttling. It runs `--headless --isolated`, so each session launches a Chrome of its own with a throwaway profile rather than sharing one signed-in profile in windows on the Mac's screen; the installed Chrome trusts portless's CA from the system store, so `https://<name>.localhost` loads without a warning. It writes a screenshot, snapshot or recording only inside the session's workspace and refuses any other path. `--browser-url` pointed at a debugging port attaches the same server to a running browser or an Electron app instead, which a project declares in its own `.mcp.json` under a name of its own, since a project server shadows a user one of the same name. `context7` is Context7's hosted server, current documentation for the version of a library a project uses, with no key and so at the free tier's rate limit. Claude Code keeps user-scope servers in `~/.claude.json`, which is its state as much as its configuration, so they are written with `claude mcp add-json` rather than linked, and only when the definition in `install.sh` differs from what is there.

`.gitconfig` runs [Betterleaks](https://github.com/betterleaks/betterleaks) before every commit in every repository on the Mac, boswell's auto-commits included, so a token pasted into a file stops at the commit rather than at a push nobody reviews. It is a hook defined in config, `[hook "betterleaks"]`, which git 2.54 runs beside a repository's own `.git/hooks` or `core.hooksPath`; a global `core.hooksPath` would have switched off each project's lefthook or husky instead. It scans only what is staged and redacts what it finds, a missing betterleaks warns rather than refusing every commit, and `git commit --no-verify` is the deliberate way past a false positive. `.github/workflows/secrets.yml` is the second line: it scans the full history of every push with the same config, from Betterleaks' own image pinned to a version, so a commit that skipped the hook or came from anywhere but this Mac is still read.

A second config hook, `[hook "worktree-install"]`, installs a JS project's dependencies into a worktree as it is added, because a worktree with no `node_modules` is one where the repository's own lefthook or husky pre-commit finds no linter and passes having checked nothing. `~/.config/git/worktree-install` runs on `post-checkout` and acts only on `git worktree add`: a null previous HEAD in a repository whose git directory is not the common one. A clone reports the same null HEAD from its main worktree and is deliberately left alone — installing there would run the lifecycle scripts of a repository nobody has read yet. It also wants a `package.json` at the new worktree's top level, no `node_modules` there, and `node_modules` in the main worktree, which is what says a person has already installed and so trusted this project by hand. The package manager comes from the lockfile — `bun.lock` or `bun.lockb`, `pnpm-lock.yaml`, `package-lock.json`, `yarn.lock`, and nothing at all without one — and the tools come from the mise shims on PATH. An install that fails prints the command to run by hand and exits 0, since the worktree exists either way.

`.gitconfig` includes `~/.gitconfig.local` last, which is where anything machine-local goes. It is untracked and written by hand, and `test/assert.sh` asserts that nothing in it names a signing key — the agent answers for that.

## The signing key

The key is the same one every machine here signs with, and it lives in 1Password. Neither half is written to this Mac. The private half is read out of 1Password straight into an ssh-agent, over a pipe, every time that agent starts; the public half is read only to trust it and to register it, and `.gitconfig` names no key at all — it sets `gpg.ssh.defaultKeyCommand` to `ssh-add -L`, which is git's own way of asking the agent, and with no `user.signingkey` git signs with the first key it answers with. There is no file to leak and nothing to paste.

The one file derived from the key is `~/.ssh/allowed_signers`, a principal and a public key so that git can verify what it signs. It is not a second source: `claude/signing-key.sh` rewrites it from 1Password on every run, so a rotated key takes the line for the old one with it.

What it expects:

- A 1Password item with the key in it, at `op://dev/ssh-commit-signing`, whose `private key` and `public key` fields are the two halves. `SIGNING_KEY_OP_ITEM` names a different one.
- A 1Password service account with read access to that vault. `claude/op-token.sh`, which `claude/signing-key.sh` calls, asks for its token once, without echoing it, checks it can read the item, and stores it in `~/.config/op/service-account-token`, where the agent reads it at every start and the `op` wrapper for every `op run`. After rotating the token in 1Password, run `claude/op-token.sh --replace` at a terminal to store the new one.

The agent is a LaunchAgent, `io.github.timche.ssh-agent`, running `~/.ssh/agent.sh`, which is a symlink to `launchd/agent.sh` in the checkout: it starts an `ssh-agent` on a fixed socket at `~/.ssh/agent.sock`, loads the key into it, and then waits on it, so that launchd restarting the pair is also what re-reads the key. The socket is fixed because the one launchd hands out belongs to the agent macOS starts for each session, which holds nothing of this and is not visible to an SSH login at all — `.zshenv` and the other LaunchAgents name `~/.ssh/agent.sock` instead. At boot the key cannot be read until the network is up, so the agent comes up empty and keeps trying with a widening delay.

Both halves of it are links into the checkout rather than copies, so a pull is all a change to either needs — `~/Library/LaunchAgents/io.github.timche.ssh-agent.plist` points at the plist in `launchd/` and `~/.ssh/agent.sh` at the wrapper beside it. launchd accepts a symlinked agent plist and hands the job `HOME`, so that plist holds no absolute path at all: `/bin/sh -c 'exec "$HOME/.ssh/agent.sh"'` is what expands one, and the wrapper derives the socket, the log and the token file from `$HOME` itself. It reads a plist only when the job is bootstrapped, though, so `claude/ssh-agent.sh` still boots the job out and back in when the plist changed, and restarts it with `launchctl kickstart -k` when only the wrapper did — which it knows from a hash of the wrapper kept in `~/.ssh/agent.sh.sha256`.

To see where it is:

```sh
launchctl print gui/$(id -u)/io.github.timche.ssh-agent   # loaded, and what launchd makes of it
SSH_AUTH_SOCK=~/.ssh/agent.sock ssh-add -l                # the key, or nothing yet
tail -f ~/Library/Logs/ssh-agent.log                      # why it is nothing yet
git commit --allow-empty -m test && git log --format='%G?' -1
```

`error: user.signingKey needs to be set for ssh signing` from that commit means exactly one thing: `ssh-add -L` answered with nothing, so the agent is not up or is not holding the key yet.

## Auto-sync

[boswell](https://github.com/timche/boswell) gets everything written during a session upstream on its own. One process watches every repository `~/.config/boswell/config.toml` lists, and two are listed: this repo and the project docs at `~/projects/docs`. It runs as a LaunchAgent labelled `io.github.timche.boswell`:

```sh
launchctl print gui/$(id -u)/io.github.timche.boswell      # loaded, and what launchd makes of it
tail -f ~/Library/Logs/boswell.log                         # why something did not land
launchctl kickstart -k gui/$(id -u)/io.github.timche.boswell  # after editing the config
```

The config is read once at startup, so a third repository is two lines in it and that restart — there is nothing else to enable.

The agent is a symlink into this checkout like everything else, which launchd accepts: it resolves the link when the job is bootstrapped and reads the file behind it. It has nothing to render because it names no path of its own — launchd expands neither `~` nor `$HOME` inside a plist, and a `$HOME` in `StandardOutPath` is an `EX_CONFIG` failure at load with nothing anywhere saying which key was wrong, but launchd does hand a gui-domain agent a `HOME`. So `ProgramArguments` is `/bin/sh -c`, and the shell exports the PATH, exports the socket and opens the log before `exec`ing boswell: the PATH because launchd hands a job almost none and both git's credential helper and boswell's issue reporting shell out to `gh`, the socket because boswell's own commits are signed, and `exec` because the status `KeepAlive` reads has to be boswell's rather than the shell's. `test/boswell-agent.sh` proves all of it against launchd itself, under a label of its own and with a stand-in for boswell, because an assert that only reads the file passes on a plist launchd would refuse.

`install.sh` loads it with `launchctl bootstrap gui/$(id -u)`, behind the same gate the unit has, and boots an already-loaded one out and back in when the plist has changed — `kickstart` restarts the job from the copy launchd read rather than re-reading the file, so a changed plist would otherwise wait for a reboot. The agent being a link, comparing the target across the bootstrap would say nothing — what the link points at is a file a pull already changed — so `install.sh` keeps a copy of the definition the daemon was last loaded from under `~/.local/state/mac-mini/`, written only once the load succeeded, and compares against that. A boswell that fails on every start retries every ten seconds for as long as the Mac is up, `KeepAlive` and `ThrottleInterval` being the whole of what a LaunchAgent can say about it, and the log is the only place that shows.

Five seconds after the last write to a watched tree, a burst of edits lands as one local commit, subject `Update a.md, b.md` and a count once there are more than three files. The push waits for `push_delay`, counted from the oldest unpushed commit, and then takes everything unpushed at once: an hour for this repository, since every push starts a CI run, and a minute for the docs, which other devices read through GitHub. A steady stream of edits cannot postpone a push past its delay. What it means while editing is in `CLAUDE.md`.

An agent lives in a GUI login session, so what keeps boswell running with nobody at the Mac is the Mac logging itself in, by auto-login or by whoever unlocks FileVault, plus `pmset autorestart` to bring it back after power loss. Until a session exists there is no `gui/<uid>` domain to load the agent into, so an `install.sh` run over SSH against a Mac sitting at its login window says that and leaves the agent unloaded.

A push that cannot land — retries exhausted, or a rebase that conflicts — opens a GitHub issue titled `Auto-sync failed` on that repository, which arrives as email; nobody is reading the log on a machine with no session attached. An already-open issue suppresses the next report, so an outage files one issue rather than one per attempt, and closing it after the fix is what re-arms the reporting. Two causes cannot report themselves, because filing needs the same network and token the push just failed on: an expired credential and a GitHub outage stay silent.

## Finding the project docs

A `SessionStart` hook, `project-docs.sh`, prints the project's docs folder and its shape — stdout from `SessionStart` goes into the session context, so Claude starts already knowing the docs are there. It looks only at `~/projects/docs/<repo>`, the project's folder in the separate docs repository; a checkout's own `docs/` is the project's published documentation, not its working docs, so it is never read. A project with no folder there produces no output at all.

The repository name comes from the `origin` remote rather than the directory, because a worktree under `~/.herdr/worktrees/<repo>/<branch>` is named for the branch while every branch shares one docs folder. Subdirectories collapse to a name and a count: listing every file put fifty lines into every session on the largest project, and the shape is the part worth injecting.

It prints one line and the file list, and stops there. What a project's docs are and how they are meant to be used differs per project and belongs in its own `README.md`; how the folder syncs, and that they should be read before the work, is the same everywhere and belongs in the global `CLAUDE.md`.

## The project docs

Every project's docs live in `timche/docs`, one folder per project, cloned to `~/projects/docs` by `install.sh` and watched by the same daemon. It is private, because the folder names alone say which projects exist. Plain markdown throughout, cross-linked with relative links rather than wikilinks so it renders wherever it is read — on github.com or the GitHub mobile app, where mermaid renders and there is nothing to pull.

Project checkouts go in `~/projects`, which `install.sh` creates and `home/.claude/CLAUDE.md` names, so a session knows where to clone without being told. The docs clone is one of them, since it is written in every session like any other work. This repository is the one thing at the top of `$HOME` that is not a project: it is the machine rather than work done on it, and `MAC_MINI_DIR` moves it for anyone who disagrees.

## Worktree isolation

Several sessions run at once, a worktree each — herdr puts its own at `~/.herdr/worktrees/<repo>/<branch>` and Claude Code puts its own at `<repo>/.claude/worktrees/<name>` — and every name two of them agree on is shared state: one compose project is one database, one host port belongs to whichever session bound it first, one Electron profile is one lock file.

portless is the machine's rather than a project's, `"npm:portless"` in mise's global tool list. Its own README asks for the global install, and the reason is that all three of the things it owns exist once: the proxy holding port 443, the certificate authority signing for every `.localhost` name, and the routes registered in `~/.portless`. mise's npm backend is that global install without `npm install -g`'s flaw: the package lands in mise's own install directory rather than inside whichever node was current, so a node upgrade does not take it along, and the lockfile pins it like any other tool. `install.sh` trusts the CA, once: the check is `security verify-cert` against `~/.portless/ca.pem` rather than `portless doctor`, which reports trust in prose with no machine-readable form, and a missing certificate means portless has never run at all. Doing it there is what keeps portless from asking for a sudo password in whichever session starts a dev server first; it reads from `/dev/null` so a sudo that wants a password fails instead of holding an unattended provision open, and it is never fatal — an untrusted CA costs a browser warning, not a shell. Over SSH it is not attempted at all: adding a certificate to the system trust store puts a confirmation dialog on the Mac's own screen as well as a sudo prompt on the terminal, so `install.sh` sees `SSH_CONNECTION` set and prints the one line to run in Terminal over Screen Sharing, `portless trust`.

Port 443 on `127.0.0.1` is root's on macOS, and a session has no terminal to answer sudo from, so a proxy that is not already running is one the first dev server of a session cannot start: portless exits rather than falling back. The proxy therefore runs as portless's own LaunchDaemon, `sh.portless.proxy`, installed once by hand with `sudo portless service install` and the TLD list below. It is the one root daemon this repo depends on and does not own, because launchd will not take a system job from a symlink in a user's checkout. The daemon records the absolute paths of the node and the portless that installed it, both under mise's versioned install directories, so a `mise up` of either leaves it pointing at a file that is gone; `install.sh` reads both paths back and prints the reinstall command when one is missing.

Dev servers open from the MacBook under a name of their own, `https://<branch>.<app>.timche.dev`, the same shape as the `.localhost` one. `.zshenv` sets `PORTLESS_TLD=localhost,timche.dev`, so the one proxy serves every route under both; `.localhost` stays first because `PORTLESS_URL` takes the first TLD. A wildcard record at Cloudflare points `*.timche.dev` at this Mac's tailnet address, DNS only, which is harmless in public DNS since nothing off the tailnet can reach it. Resolvers with DNS-rebinding protection, NextDNS among them, return nothing for a public name pointing at such an address, so the tailnet's DNS settings route `timche.dev` to Cloudflare's `1.1.1.1` as split DNS, and a tailnet device gets the answer on any network. And `install.sh` has Tailscale Serve hand the tailnet's port 443 to the proxy as raw TCP: portless keeps TLS and routes by hostname, and the MacBook trusts portless's CA once.

A name per app rather than a port per app is the point of the arrangement. Browsers key cookies by host and ignore the port, so portless's own `--tailscale`, which puts every app on the Mac's own `<node>.<tailnet>.ts.net` name and a port each, gives every dev server one cookie jar on the MacBook: two worktrees of one app overwrite each other's session, and an app inherits the localStorage and service worker of whichever app held its port last. Nothing Tailscale offers gives a name per app today — wildcard MagicDNS is in the client but not enabled on the coordination server for any tailnet ([tailscale/tailscale#1196](https://github.com/tailscale/tailscale/issues/1196)), Tailscale Services stop at ten per user, and a tsnet node per dev server makes every worktree a device.

`COMPOSE_PROJECT_NAME` is the one that bites with nothing configured at all, so `.zshenv` sets it. Compose names a project after the directory it was started from, which for a herdr worktree is the branch alone — so `fix-ui` in two repositories would share one project, and with it one set of containers, one network and one database volume. `<repo>-<branch>` instead: the repository from the `origin` remote, for the same reason `project-docs.sh` takes it from there, the branch from `HEAD` or the worktree folder when HEAD is detached, lowercased with everything outside `[a-z0-9_-]` replaced, which is compose's own rule for a name it would otherwise reject. Recomputed on every `chpwd`, because a shell outlives the directory it started in, and unset outside a work tree, which leaves compose on its own default. In `.zshenv` rather than `.zshrc` so that a script, a hook and an agent get the same name a prompt does.

`worktree-info.sh`, a second `SessionStart` hook beside `project-docs.sh`, prints the names the worktree owns and nothing else: the compose project, with the reminder that `docker compose down -v` takes the database with it and that a fixed host port in a compose file is a collision waiting for the second session; portless's URL, `https://<branch>.<name>.localhost` in a linked worktree and `https://<name>.localhost` in the main one, which is portless's own rule; and, for an Electron project, that a dev run takes a user-data profile named for the branch and a debug port of `0`, read back from `DevToolsActivePort` in that profile. Each line only when the project has the thing — a compose file at the worktree root, a `package.json` script that runs portless, `electron` among its dependencies — and a project with none of the three prints nothing at all. Where the name cannot be derived with certainty it is not guessed at: portless resolves a workspace root against the packages under it and rewrites a branch name that is not a hostname label, so for either the hook says that `portless list` prints the URL rather than printing a URL that might not exist.

It reads the worktree from the `cwd` field of its own stdin. `CLAUDE_PROJECT_DIR` is documented as staying at the directory the session started in rather than following it into a worktree, and `CLAUDE_ENV_FILE`, the other way to hand a session a name, is unreliable across `/clear`.

Cleaning up after a removed worktree is a garbage collector, `~/.local/bin/worktree-gc`, rather than a hook on the removal, because no removal reliably hands us one: herdr removes worktrees, so does Claude Code, so does `git worktree remove` by hand, git has no post-remove hook at all, and Claude Code's `WorktreeRemove` does not fire for a git worktree ([anthropics/claude-code#94212](https://github.com/anthropics/claude-code/issues/94212), open). Registering `WorktreeCreate` and `WorktreeRemove` would be the wrong trade even if it did: the docs say configuring them *replaces* Claude Code's own `git worktree` behaviour rather than adding to it, so a hook that cleaned up would also own creating the worktree.

So the trigger is time. Two of its four sweeps are about a worktree that is gone, and neither runs unless it is: a compose project whose `com.docker.compose.project.working_dir` label points inside a worktree root that no longer exists, with `com.docker.compose.project.config_files` as the second opinion, removed with `docker compose -p <name> down -v --remove-orphans`; and a process whose current directory is a removed worktree, SIGTERM and then SIGKILL ten seconds later. The label rather than `docker compose ls`, which reports the config files alone and so misses a project started with `-f` or from a file named something else. Inside a worktree root — `~/.herdr/worktrees/<repo>/<branch>` or `<repo>/.claude/worktrees/<name>` — and nowhere else, which is not the same as a folder that does not exist: a daemon shared with another machine reports projects whose working directory was never on this disk, and `down -v` on one of those deletes a database over a path that only looks missing. It never touches pid 1 or its own process tree, holds a `mkdir` lock so a manual run and the agent cannot overlap, takes over a lock older than the interval, and answers `--dry-run` by printing what it would do.

The other two are about a session that is over rather than a folder that is gone. Claude Code gives every session a scratch folder, `${CLAUDE_CODE_TMPDIR:-~/.cache/claude-tmp}/claude-<uid>/<project-slug>/<session-id>/`, and removes none of them, so the sweep removes one whose name is a session id, that no running session claims, and that nothing was written under for a week. Live is what `~/.claude/sessions/<pid>.json` says, by the pid in its name rather than by the record being there, since the record outlives the process; the id inside it comes out with `sed`, because a sweep may not depend on `jq`. The week of grace is for `claude --resume`, which reuses the folder, and herdr resumes every session it had after a restart. A project folder goes with `rmdir` once the last session folder in it has, so anything else left in it keeps it, and anything not named like a session id is left alone — `bash-edit-diff` and `tasks` beside the session folders, `cc-socks` and `tart` a level above them.

The last is `git worktree prune` in every repository directly under `~/projects`, which is `WORKTREE_GC_PROJECTS` where a check needs it somewhere else. git's own prune rather than a rule of this script's: it drops the metadata of a worktree whose folder git cannot find and nothing more, so a session still working keeps its entry, and it reports what it pruned on stderr with `-v`, which is where each line of the log comes from. A `.git` directory rather than a `.git` file, since the file is what a worktree has.

A LaunchAgent runs it, `io.github.timche.worktree-gc`, with `RunAtLoad` for what the machine lost while it was off, `StartInterval` 600 and `WatchPaths` on `~/.herdr/worktrees`. The interval is what does most of the work: `WatchPaths` is not recursive, so a worktree removed one level down is invisible to it, and Claude Code's worktree roots are per repository, which no single path watches. `install.sh` creates that root if herdr has not yet, because launchd wants a watched path to exist at load, and loads or reloads the agent as it does boswell's. This is the one plist rendered from a mise template rather than linked: launchd reads `WatchPaths` itself rather than handing it to a shell that could expand a `$HOME`, so that one path has to arrive absolute — which also means the file itself changes under the bootstrap, and `install.sh` compares it against the copy it took aside beforehand. The log is `~/Library/Logs/worktree-gc.log`, and a sweep that finds nothing writes nothing to it.

## The herdr server

Every Claude Code session is a pane of one herdr server, and that server is started by launchd inside the logged-in GUI session, `io.github.timche.herdr`, rather than by the first `herdr` a remote client runs. Tim attaches from the MacBook with `herdr --remote`, which runs `herdr remote-client-bridge` over Tailscale SSH and only talks to the server's socket, so where the server was started decides what its panes are. Started over SSH, every pane carries `SSH_CONNECTION`, `SSH_CLIENT` and `SSH_TTY`, and Claude Code withholds computer use from such a session: the built-in `computer-use` server is missing from `/mcp` although every documented condition holds, and appears once those three variables are unset. Anthropic documents none of this, and the strings in the binary put the three names among its terminal-identity checks, beside "[computer-use] terminal not detected; falling back to sentinel host", which fits computer use needing to find the terminal it keeps out of its screenshots. From launchd, a pane is genuinely a local session, and macOS asks its privacy grants of herdr rather than of `tailscaled`.

`KeepAlive` is unconditional, unlike boswell's, because a missing server — even one stopped on purpose — would otherwise be replaced by the next remote client starting its own over SSH. `install.sh` loads the agent but never boots it out or restarts it: it usually runs inside one of that server's panes, and restarting the server ends every session on it, the one running the script included. A server already running outside launchd, or a plist changed since launchd read it, is reported with the commands to run from a shell outside herdr, a plain `ssh timche@mac-mini`, once no session needs the server. The log is `~/Library/Logs/herdr.log`.

What stays unsolved either way is approval: computer use asks for each app once per session, in the session's own terminal, and nothing pre-approves apps for a session nobody is watching ([anthropics/claude-code#47796](https://github.com/anthropics/claude-code/issues/47796), closed without it).

## varlock

A project that resolves its secrets with `op run --env-file .env.op -- <cmd>` gets the same token from `~/.local/bin/op`, a wrapper ahead of Homebrew's `op` on PATH, so the project never learns how `op` signs in and the same command works on a Mac where the 1Password app does it. When `OP_SERVICE_ACCOUNT_TOKEN` is unset and the token file is readable, it sets the variable for that one `op` process and nothing else, where exporting it from `.zshenv` would put it in every process and so in transcripts and logs. `op run` hands its own environment to the command it starts, so for `op run` the wrapper puts `env -u OP_SERVICE_ACCOUNT_TOKEN` right after the first `--`; a command that calls `op` by name comes back through the wrapper and is signed in again. This keeps the token out of projects and build tools' environments rather than away from the account, which can read the file anyway. The scripts here and `launchd/agent.sh` read the file themselves and do not depend on it.

A project that loads its environment through varlock is handed no token from here: where it resolves a reference through the `op` CLI, that `op` is the wrapper above and is signed in already, and anything else is the project's own schema to declare. `.zshenv` and `.bashrc` set `DO_NOT_TRACK=1`, the cross-tool opt-out at donottrack.sh that varlock honours, since varlock otherwise sends anonymous usage analytics and writes an id to `~/.config/varlock/config.json` for any project without an opt-out of its own.

Signing and notarising a macOS build is the project's own configuration, in its `.env.schema`, rather than a wrapper here: meru resolves its Developer ID certificate, its notarisation credentials and its provisioning profile that way.

## hachiko

`hachiko` watches the two failures a Mac with no screen cannot notice, every five minutes from the `io.github.timche.hachiko` LaunchAgent. Named for Hachikō, the Akita who waited at Shibuya station every day.

The first is a process that lost something it retries on and is logging the mistake into a file nothing bounds — a worker whose Redis went away writes the same connection error a few hundred thousand times a second, which is tens of gigabytes an hour into a path somebody chose because it was convenient. On a machine somebody is sitting at, the fans say so; on this one the first symptom is a build failing for want of space, hours later, with the disk already full. The second is a process still spending a core on work nobody is waiting for, which is what a worker left behind by a session that ended looks like.

It is the repository's one compiled piece, a Go module in `hachiko/` built in place by the wrapper in `~/.local/bin/hachiko`; [Layout](#layout) has how that works and why there is nothing to install. Standard library only, so the binary the agent runs depends on nothing — not mise, not Homebrew, not a runtime — which matters because a Mac whose tooling has broken is exactly a Mac worth watching. `go test ./...` in `hachiko/` is its behaviour: the clock, the free-space reading, the file walk, the process sample, `lsof`, herdr and the sender are all injected, so every rule below is checked without filling a disk, waiting an hour or opening a session.

What it reads: free space on the Data volume from `statfs`, which is what `df` reads without the parsing, and every regular file over a gigabyte under `/private/tmp` and `$HOME`, staying on one volume and pruning `~/Library/Containers`, the Group Containers beside it, `.git` and `node_modules` — about 370,000 entries, walked sixteen directories at a time; the walk and the process sample together take about a second. A file that has gained two gigabytes since the last sample is growing fast, and `lsof` names the process writing it. Sizes are the allocated ones, from the block count rather than the apparent length, because a VM's sparse disk image is apparently hundreds of gigabytes it is not occupying.

CPU is the difference in cumulative CPU time between two samples over the wall clock between them, from one `ps -A` for the whole machine, taken in the C locale because the start date it prints is half of a process's identity. Never ps's own `%cpu`, which on macOS is a decaying average over the process's whole life: it understates a worker that has just gone wrong and overstates one that has finished. A process is reported when it has held half a core or more across every interval for an hour, which is twelve intervals of five minutes and so thirteen samples; a sample that comes in low starts the hour again, and because the window is a span of time rather than a count of runs, an interval the agent missed costs nothing. Identity is the pid and its start time together, since a pid is reused and the history behind one belongs to whoever held it. `~/.config/hachiko/cpu-allow` is a glob per line for the things expected to burn a core here — the OrbStack VM, a UTM guest, Spotlight, Time Machine, a macOS update, the window server — and hachiko never reports its own process tree. A hot process whose parent is launchd and whose working directory is inside `~/projects` or a worktree root is called out as orphaned inside a checkout, which is the shape of a worker whose session is gone.

The thresholds: under 100 GB free, under 20 GB free, a gigabyte to be worth watching, two gigabytes of growth between samples, half a core for an hour. Each is an environment variable (`HACHIKO_LOW_GB` and the rest), which is what lets the tests trip the same arithmetic with megabytes and minutes rather than filling a disk to prove it.

One message per incident per state change. A file still growing is the same incident until it stops, low space stays one alert until free space is back over 100 GB, and 20 GB is an escalation that is allowed to say so once. A hot process is one incident for as long as it stays hot, and a disk and a CPU incident in the same run are one message and one session.

Under 20 GB free, hachiko truncates the fastest growing file, and only that one: the biggest offender or nothing, since a check working its way down the list would reach a file that is somebody's. Only under `/private/tmp`, the Claude Code scratch root or `~/Library/Logs`, and only when the name ends `.log`, `.out`, `.err` or `.output` or contains `.log.`. Never an `rm` and never a `kill`: the writer keeps its descriptor and its offset, so a log it appends to goes on working and the space comes back at once, where an unlinked file frees nothing until the writer exits and a killed worker takes a session's work with it. What to do about the writer is a decision for a person.

Alerts go to a private Discord channel through a webhook whose URL is the `url` field of the `hachiko-discord` item in the `dev` vault, referenced from `home/.local/bin/hachiko.env.op` beside the wrapper and resolved by `op run --env-file` only when there is something to send, since every run counts against the service account's daily limit. The URL reaches one process and one request body: `op run` re-enters the binary with `--send`, which reads the message on stdin and posts it with `net/http`, so it is in no command line `ps` shows the machine and in no file — and net/http's own error text, which names the URL it failed on, is redacted before it reaches the log. A send that fails leaves the incident unraised, so the next check tries again rather than going quiet about it.

`~/Library/Logs/hachiko.log` is the log, one line per thing that happened and nothing at all on a quiet check. `hachiko --dry-run` reports what a check sees and changes nothing, `hachiko --test-alert` proves the webhook, and `hachiko --help` has the rest.

### The on-call session

An alert that says a file is growing is a page in the middle of the night that still needs somebody to read a process listing. So hachiko opens a Claude Code session to do the reading: `hachiko oncall <name> <brief-file>` puts a tab in the herdr workspace for this repository, with this checkout as its directory so the session starts with the machine's own instructions loaded, and starts an agent called `oncall-<name>` in it. herdr rather than a `claude -p` of its own, because Tim attaches to the same server from anywhere with `herdr --remote`, the session is still there hours later, and an agent waiting on `AskUserQuestion` shows in his sidebar as `blocked` — which is the point, since the session is told to ask before it changes anything. Never focused, since he may be in the middle of something, and one session per incident name: a second alert while the first is still being worked is an update to that session rather than a second agent on the same disk.

That update does not always land. herdr refuses a prompt to an agent that is already waiting on a question, and waiting on a question is exactly where the standing orders leave an on-call session — so the commonest second alert of an incident reaches nobody. hachiko treats that as a session that is up but has not heard this: the message names the tab the agent is waiting in, says the update was not delivered to it, and carries the whole of the detail, because nothing in that tab is going to read it for him. The same goes for a session that started but that the brief never reached: the tab is named rather than left unmentioned, and nothing waits ten minutes for a report that cannot come.

What reaches the channel either way is one message per state change. A truncate is the exception that always sends: it is the one thing here that changes somebody's disk, so it is an incident of its own even on a run where the file was already flagged and the threshold was already crossed.

What the session is handed is a prompt whose orders come first, then the findings between two markers carrying a nonce that prompt alone knows, then a line saying that was the data. Everything in that block — a path, a command line, a log line quoted back by `lsof` — was chosen by whatever filled the disk, so it is the one part of the prompt an attacker writes: the fence is what keeps a file named "ignore your orders" from reading as a turn in the conversation, backticks go out of it, and nothing inside it can spell the end of it. Paths and arguments are clipped on the way in for the same reason Discord's 2,000 characters are respected.

The order the alert goes out in is the session first, then one short line from hachiko — what fired, and that an agent is looking into it in a named tab. The session investigates read-only, and its first duty is to send its own message: what is happening, the cause as far as it knows it, the options it is about to offer and which one it recommends. It sends that with `hachiko notify <incident> <file>`, which is the only way anything but hachiko reaches the channel and which is handed no URL of its own. That command takes no lock and writes no state: a sweep holds its lock across a herdr call and an `op run`, which together can outlast the five minutes the session is told to report within, so the report leaves a marker and the next sweep is what clears the incident. Then it waits in `AskUserQuestion` with two to four options, recommendation first, and takes no destructive or outward action — no kill, no delete, no truncate, no push, no restarting a service — until Tim picks one. Afterwards it sends the outcome the same way and writes a note under `$PROJECT_DOCS_DIR/mac-mini/incidents/`.

The fallback is why hachiko speaks first and keeps its own details. The session depends on herdr being up, on a Claude login and on usage being left, and a watch that only ever spoke through an agent would be silent exactly when that chain broke. So: if the session cannot be started, the first message carries the full raw details and says so; if a session started but has not reported ten minutes later, hachiko sends the details itself and says the agent did not report; and under 20 GB free, or after a truncate, the first message carries the whole of it immediately rather than waiting for anybody to finish reading.

## Preferences

Tim's own preferences, split by what mise's declarative `[bootstrap.macos.defaults]` can say: a plain user default is declared in `mise.toml` and written by the same `mise bootstrap` that applies the links, and everything else is in `install.sh`.

| Preference | Applied by | |
| --- | --- | --- |
| Tap to click | `mise.toml` | `[bootstrap.macos.trackpad]`'s `tap_to_click`, one friendly key for both `Clicking` domains, plus the per-machine `com.apple.mouse.tapBehavior` as an explicit `host = "current"` entry |
| Finder: hidden files, every extension, status bar, path bar, no extension-change warning | `mise.toml` | `[bootstrap.macos.finder]` covers four of the five; `AppleShowAllExtensions` is global rather than Finder's own and has no friendly key, so it is a raw entry |
| Screenshots in `~/Pictures` | `install.sh` | The value has to be an absolute path — neither mise nor the screenshot service expands a `~`, and mise does not template a defaults value — so declaring it would mean spelling the account name out, which nothing here does |
| `~/Library` visible in Finder | `install.sh` | A flag on the folder, and a Finder info xattr beside it, rather than a preference at all |
| A Dock with none of the icons macOS ships with | `install.sh` | Once, recorded under `~/.local/state/mac-mini` |

Every one of them reads before it writes, and nothing restarts an application that had no reason to notice. That last part is why the restarts are `install.sh`'s rather than a `post-defaults` hook: mise deliberately kills no application, and a hook there would relaunch Finder on every install whether a preference changed or not. So `install.sh` takes the whole `com.apple.finder` domain aside before the bootstrap — the domain rather than the four keys, which is one fewer list to keep in step with `mise.toml` — and relaunches Finder only if the bootstrap or the `~/Library` flag moved something. The screenshot location relaunches `SystemUIServer` and `screencaptureui` both: every guide still names the first, and the process actually found holding the stale copy is the second.

The Dock is emptied once and then left alone. mise could own `persistent-apps` with `apps = []`, but from then on every install would wipe whatever Tim had pinned since the last one, and what he asked for was the factory set gone rather than a Dock that refuses to hold anything. A marker file is what says it has been done, and nothing reads `persistent-apps` again afterwards.

One of them is believed rather than proven. Tap to click's two `Clicking` domains look to be a mirror System Settings keeps rather than what the input stack reads — forcing them to 0 and the per-machine key to 1 still taps to click — so the `host = "current"` entry is the one doing the work and the other two are there because System Settings shows them.

CI reads every one of these back on the `macos-latest` runner and asserts that a second pass finds nothing to write, which is what `mise bootstrap macos defaults status --missing` answers.

## Testing

`.github/workflows/test.yml`, on a `macos-latest` runner and nowhere else: there is no macOS container to put any of this in, and the suite changes the machine it runs on. Hosted runners are free to a public repository, which is the reason this one is public.

The job runs `bootstrap.sh` first, against a clone of the branch under test rather than of main, which is the only path that clones anything. Then each phase by hand, with the asserts after each: `machine.sh` twice, `xcode.sh` twice, then `claude.sh` and after it the `install.sh` that is its own first step, which is the second pass over the account half. The repeat is what keeps the whole of it honest about being safe to re-run, which is the only way a change ever reaches a Mac that is already built. The runner's account is called `runner`, which is what proves nothing here assumes an account name. Then the three tests that need more than a file read: `signing-agent.sh`, `agent-links.sh` and `boswell-agent.sh`. Then `harden-ssh.sh` with a key seeded the way a real Mac has one, last, because a runner has nothing after it that needs to log in.

There are three assert scripts, one per phase: `test/assert-machine.sh` for what `machine.sh` left, `test/assert.sh` for what `install.sh` left, and `test/assert-claude.sh` for the logins and the signing key.

The compiled tools are the one part the assert scripts deliberately do not test. `go vet` and `go test ./...` run for each of them in the same job, on the go `install.sh` has just installed, and that is where every rule a tool decides by is checked — against injected seams rather than against the Mac, so nothing has to fill a disk or wait an hour. What `test/assert.sh` adds is the wiring Go cannot see: that the wrapper is linked and runs, that the agent is a valid plist and loaded, and that a source tree which does not compile leaves the last binary that did in place.

`test/assert-markdown.sh` is the fourth, and the only one that is about the repository rather than the Mac: the word budget on the always-loaded `home/.claude/CLAUDE.md`, and prose that is never hard-wrapped. `test/assert.sh` runs it and adds the number it failed on to its own, and `.github/workflows/markdown.yml` runs it alone on `ubuntu-latest` in a few seconds. That second job is the point of it being a script of its own: `test.yml`'s paths filter deliberately keeps a markdown-only push off a macOS runner, so a rule that lived only in `assert.sh` would be checked by nothing on exactly the commits it is about. Which is also why nothing in it may reach for a command only macOS has.

`test/signing-agent.sh` is the one worth reading. It generates a key, puts a stub `op` in front of the real one on PATH, and runs the real `claude/signing-key.sh` against a throwaway `HOME` — then asserts that the agent holds the key, that `.ssh` holds neither half of it, and that a commit signs and verifies through the agent and cannot sign without it. The agent is run the way launchd runs it rather than by launchd, since launchd would hand it the account's `HOME` and not that one; `test/agent-links.sh` is the half that does use launchd, against the account itself and with no token anywhere, and asserts what only a real `bootstrap` can — that a symlinked plist loads, and that a wrapper changed in the checkout restarts the job. `test/boswell-agent.sh` does the same for boswell's plist under a label of its own and with a stand-in for boswell. The first two refuse to run outside CI unless `MAC_MINI_TEST_ANYWAY=1`: the first would register its throwaway key on whatever GitHub account `gh` is logged in to, and the second bounces the agent holding the real key, since launchd keys a job by label per account.

A local run is `./install.sh && test/assert.sh` on the Mac itself, which is the quick check after each change. What it cannot answer is the promise the suite actually makes, since it runs against a Mac that is already built: only CI does that, where `bootstrap.sh` runs against a runner on every push. Not from nothing, though — a hosted runner arrives with Homebrew and the command line tools already, so the install that a fresh Mac starts with is the one step skipped there, and what is exercised is the clone and both phases against a machine that has never had them. So a change to the fresh-Mac path is tried by pushing the branch and reading that job.

What CI cannot reach: anything that needs a click. Screen Sharing needs the Sharing pane to register the sharing agent's screen recording rights, so `assert-machine.sh` reports where it stands instead of asserting it. The screen lock is worse than unreachable, for the reason above, so nothing checks it at all. Nor a service account and a vault, a tailnet to log in to — the runner installs tailscaled and configures its prefs, which works logged out, but nothing authenticates and the asserts say so — a machine that loses power, and TCC, since the runners have SIP disabled and anything gated on Full Disk Access behaves more permissively there than on a real Mac. That last one is why `remote-login.sh` goes through launchd rather than `systemsetup`, which is gated. Nor a hypervisor: a runner is a VM itself and Virtualization.framework inside one refuses outright, and OrbStack's first run is a welcome screen on the Mac's own display besides. So everything docker itself answers — the context, `docker info`, a container, the plugins, the two-service compose file — is skipped on a runner, and only a Mac with OrbStack actually running asserts any of it. The docs clone fails too, the runner's token not reaching `timche/docs`, so boswell stays unloaded and the asserts check its wiring rather than a running daemon.

## Environment knobs

`MAC_MINI_REPO`, `MAC_MINI_DIR` (the clone, `~/.mac-mini` by default), `PROJECT_DOCS_DIR`, `SIGNING_KEY_OP_ITEM`, `OP_SERVICE_ACCOUNT_TOKEN_FILE`, `FORCE_HARDEN`, `TS_ADVERTISE_ROUTES`, `MAC_MINI_TEST_ANYWAY`, and hachiko's own thresholds, roots and paths: `HACHIKO_LOW_GB`, `HACHIKO_CRITICAL_GB`, `HACHIKO_BIG_KB`, `HACHIKO_GROWTH_KB`, `HACHIKO_CPU_SHARE`, `HACHIKO_CPU_WINDOW`, `HACHIKO_CPU_ALLOW`, `HACHIKO_ONCALL_DEADLINE`, `HACHIKO_TMP`, `HACHIKO_PROJECTS`, `HACHIKO_STATE_DIR`, `HACHIKO_ENV_FILE`, `HACHIKO_CACHE_DIR` (the wrapper's build cache), `HACHIKO_NOW`.

`CLAUDE.md` has the rest: the order the scripts run in, the constraints that are not obvious from reading them, and how to work in this repo.
