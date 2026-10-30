# mac-mini

[![test](https://github.com/timche/mac-mini/actions/workflows/test.yml/badge.svg)](https://github.com/timche/mac-mini/actions/workflows/test.yml)

The Mac mini Claude Code runs on and controls: a headless Apple Silicon Mac that exists for agentic coding, several sessions at a time, reached over the tailnet from somewhere else. This repository is the whole of it, the machine and the account on it.

## Installing

This section is written for Tim. Everything after it is written for the sessions that work on the repository.

### At the Mac, before the first run

1. Setup Assistant: create the account the Mac is for, as an administrator. An Apple ID is only needed for Xcode; the iCloud features can stay off.
2. System Settings > Users & Groups: turn FileVault off, then Automatic login on for that account. Automatic login needs FileVault off, and everything here waits for a logged-in session after a restart.
3. System Settings > General > Sharing: turn on Remote Login (to run the install over SSH) and Screen Sharing (for every click later on).

### The install

Over SSH on the LAN, as that account:

```sh
curl -fsSL https://raw.githubusercontent.com/timche/mac-mini/main/bootstrap.sh | bash
```

It asks, in order, for the sudo password, a tailnet login URL, the Apple ID and a 2FA code for Xcode, a GitHub device code, a Claude Code login and the 1Password service-account token.

### Afterwards, over Screen Sharing

1. System Settings > Privacy & Security: give `~/.local/libexec/herdr-launcher` (add it with **+**, then ⇧⌘G to type the path) **Screen & System Audio Recording**, **Device Control and Data Access** and **Full Disk Access**.
2. System Settings > Lock Screen: "Require password after screen saver begins or display is turned off" to **Never**.
3. Open OrbStack once, accept its terms and choose Docker.

`machine.sh` lists whichever of these is still missing every time it runs.

### In the Tailscale admin console

1. Add an `ssh` rule for whoever should reach the Mac.
2. Disable key expiry for the Mac, or it drops off the tailnet after 180 days.

### Keeping it up to date

```sh
git -C ~/.mac-mini pull
~/.mac-mini/machine.sh
~/.mac-mini/claude.sh
```

Re-running changes nothing that is already right and moves no version; hachiko's nightly update is what moves versions.

### What only you do later

- **A prompt on the Mac's screen:** answer it over Screen Sharing. A session that causes one says what it is for.
- **The root helper:** when a session says it changed, run `~/.mac-mini/machine/root-helper.sh`. It asks for your password and shows a diff first.
- **The permissions above, again,** when a session says `herdr-launcher` was rebuilt.
- **A sudo command a session prints for you:** read it, then run it.

## How it is put together

A fresh Mac has no git and no package manager, so there is nothing to clone this with — which is what `bootstrap.sh` is for. It installs Homebrew, which brings the Xcode command line tools through `softwareupdate` rather than the dialog nobody is in front of, clones this repo to `~/.mac-mini` and runs its two phases: `machine.sh` builds the Mac, `claude.sh` makes it the one Claude Code works from. The clone stays, because a Mac is a machine you pull and re-run rather than one you reprovision from a URL.

`MAC_MINI_DIR` puts it somewhere else. It is not a clone to delete: everything in `$HOME` is a symlink into it, and the LaunchAgents launchd loads are links into it too, so moving the checkout means re-running `claude.sh` to point the links at the new place.

Every script opens with a comment saying why it exists and what would break if it ran elsewhere in the order. That comment is where what a script does is written down; this file is the shape of the whole and the reasoning that spans more than one script, and `CLAUDE.md` is how to work in the repository.

Why each of the steps under [Installing](#installing) is needed: auto-login, because the herdr server, the LaunchAgents and every privacy grant live in the login session; Screen Sharing, because it is the one sharing setting no script can turn on, that pane also registering the rights the sharing agent needs; the three privacy grants, for the reasons [Privacy permissions](#privacy-permissions) gives; the Lock Screen setting, so the display can sleep without a password standing between Screen Sharing and the desktop; OrbStack, whose terms and Docker choice are on a welcome screen no script can click; and the key expiry, since a node whose key expires drops off the tailnet until somebody logs in at it again.

## The principles

- **Re-run in place, never reprovisioned.** A VM is thrown away and built again. This Mac is re-run over the very sshd and tailnet the run is reaching it on, so a step that rewrites a working sshd, installs a second tailscale or restarts the machine is a regression even when it is idempotent. Everything checks before it changes, and says where it stands when there is nothing to do.
- **A re-run moves no version.** `brew bundle --no-upgrade` and `mise install --locked` install what is missing and leave what is there, which is what makes both phases safe after every pull. [`hachiko update`](tools/hachiko/README.md#updates) is the one thing here that moves one, once a night.
- **Nothing waits for a click.** Several sessions run unattended on a Mac with no screen, so anything that would prompt checks for a terminal first and says what it skipped instead. What genuinely needs a click is named on every run rather than assumed done.
- **Nothing is written for one account.** The Mac's is `timche` and CI's is `runner`. Paths go through `$HOME`, a mise template, or a `~` the reading program expands itself.

## The machine

`machine.sh` installs Homebrew and the `Brewfile`'s packages, turns Remote Login on if it is off, brings tailscale up as a system daemon serving Tailscale SSH, puts docker on the Mac as OrbStack, installs Rosetta for the x86_64 tools a Windows build of an Electron app runs, hardens sshd down to keys only, no root, one user, installs the one [root helper](#the-root-helper) a session may run under sudo, and — only where there is a terminal to type an Apple ID at — installs Xcode.

Every package is the `Brewfile` at the repo root, applied with `brew bundle --no-upgrade` by `bootstrap-system.sh` here and by `install.sh` in the account phase, one declarative list for both. A failed package is not fatal, bar `gh`, `jq` and `op`, which `bootstrap-system.sh` stops on since nothing after them works at all. `xcodes` and `aria2` are deliberately left out and installed by `xcode.sh` itself, both existing only for a download that happens only where there is a terminal.

`unattended.sh` is what makes a Mac nobody is sitting at usable: restart after power loss and never sleep, no crash dialogs, no screensaver, macOS updates downloaded but never installed, no Spotlight indexing and no media analysis. The script has the reasoning for each, and two are worth knowing from outside it. `displaysleep 0` is in the list although there is no display, because a `screencapture` taken while the display is asleep comes back the same black frame a missing Screen Recording grant gives. And an update that installs itself restarts the Mac, taking every session's worktree state, every running build and every browser with it — so installing one is Tim's decision, and [the nightly update](tools/hachiko/README.md#updates) says what is waiting and why nothing here can install it. Background Security Improvement is the exception that is not settable at all: it lives in declarative device management, which needs a supervised MDM enrollment, it is on, and it can restart the Mac.

The three settings under [Installing](#installing) that stay Tim's are Tim's because each needs the pane itself, and `unattended.sh` names all three on every run rather than assuming them done. Auto-login, because turning it on writes the account password to `/etc/kcpassword`, obfuscated rather than encrypted — and a Mac sitting at its login window has no `gui/<uid>` domain, so neither docker nor any of the agents is running, tailscaled being the exception that keeps it reachable. Screen Sharing, because `launchctl enable` clears the `Disabled` flag but the screen recording rights the sharing agent needs are registered by the Sharing pane itself, so a Mac enabled from a script comes out with the job loaded, the toggle reading off and nothing answering on port 5900 — which is why the check looks at the port rather than at launchd's opinion of the job, and why this is the one to fix first. And the screen lock, which on macOS 27 is in no preference domain and whose `sysadminctl` reading cannot be trusted, so nothing writes it, reads it back or asserts it; what the pane actually did is checked by watching, `pmset displaysleepnow` and then a `screencapture` that comes back a real desktop rather than a login window.

`harden-ssh.sh` refuses to disable password logins unless the user has a key sshd could actually let them in with, because on a Mac there is no provider console to fall back to. A Mac reached with a password comes out of a run unhardened and told what to do about it; `FORCE_HARDEN=true` overrides that if you are certain of another way in. `root-helper.sh` runs after it, because nothing in the way this Mac is reached depends on the helper.

## The root helper

sudo on this Mac asks for Tim's password, and a session has no terminal to type it at, so every privileged step is a command printed for him to run. `root-helper.sh` carves out one exception: `/usr/local/libexec/claude-root`, a script that takes a fixed set of actions and refuses everything else, and a sudoers rule letting the account run that one path with no password.

Narrow rather than blanket, and narrow against mistakes rather than against the account. Nothing here defends root from a session that set out to have it: sessions run with permissions bypassed, `home/.zshenv` is a symlink into a checkout the account writes with `~/.local/bin` first on the PATH it sets, and both portless's root daemon and Homebrew's `tailscaled` run as root out of paths the account owns. What the narrow rule buys is that an agent acting on its own can bounce `dasd` or flush DNS and nothing else, and that what it is trusted with is a list somebody can read.

Two rules decide what may be an action. The helper takes no path, no command, no signal and no pid from its caller; the only argument any action takes is a name matched against a list compiled into the script, by string equality and never as a pattern. And no action touches a path the account can write, even one the helper spells out itself — a root command aimed at a file in a home directory is a root command the account aims wherever it likes, by replacing that file with a symlink. An action that needs root needs it for a path only root can write, and `assert-machine.sh` reads the helper back to say so. `reinstall-portless` is the one named exception and nothing else may follow it, because portless's root daemon already runs the account's own node and JavaScript as root by portless's own design; the assert cuts that one function out of the file before it reads the rule off everything else.

`sudo /usr/local/libexec/claude-root --list` prints the actions and the daemons:

- `restart-daemon <name>` — `launchctl kickstart -k system/<label>` for a daemon on the list, `dasd` and `tailscale` so far, falling back to a `kill -TERM` of the pid launchd is watching where SIP refuses the kickstart, and reporting the old pid and the new one, because a kickstart that returned 0 has only queued the work. tailscale is on the list for [the nightly update](tools/hachiko/README.md#updates); a session should leave it alone, since the restart drops every tailnet connection including the one somebody is typing in.
- `flush-dns` — `dscacheutil -flushcache` and `killall -HUP mDNSResponder`.
- `reinstall-portless` — `portless service install` again, on whichever node and portless the account's mise has now, serving the TLDs the helper spells out, checked against the `PORTLESS_TLD` in `home/.zshenv` so that a reinstall cannot quietly take a dev URL off the Mac.
- `install-apple-updates` — `softwareupdate --install` for every update waiting whose details line names no `Action` and whose title is not macOS's, with no `--restart`. An update with an Action restarts or shuts the Mac down over every session running on it, and a macOS update asks for a volume owner's password even when root asks, so both are left to Tim. The labels come out of softwareupdate's own listing rather than from the caller, which is the rule above from the other side; what went in is read back off a second listing, `softwareupdate --install` exiting zero for a label it could not find; and each call has a deadline of its own, macOS shipping no `timeout` and softwareupdate having none. [The nightly update](tools/hachiko/README.md#updates) is what calls it.

Every action writes one `logger -t claude-root` line with its name and its argument, after validation and before anything privileged runs. That line is the only trace a run leaves, since sudo records an allowed run of it nowhere on this Mac; `/usr/bin/log show --predicate 'eventMessage beginswith "claude-root:"'` reads them back, with the full path because zsh's own `log` builtin shadows it and silently prints nothing. `CLAUDE_ROOT_DRY_RUN=1` makes an action say what it would do and change nothing; it reaches the helper only from a shell, never through sudo, and it is checked after the validation, so it is no way past the allowlist.

Adding a daemon to the allowlist is an edit to `machine/system/libexec/claude-root` followed by `~/.mac-mini/machine/root-helper.sh`, which asks for Tim's password. That is the point: what a session is trusted with changes only when he says so. The installer shows a `diff -u` of each of the two files against what is installed and asks before it writes — a session cannot run it, but it can write the two files a run of it copies, and one of them is a command sudo will then run as root without asking again, so read the diff rather than typing through it. Both files are root-owned copies and never links into the checkout, which is what makes an edit to either reach the Mac only when Tim runs the installer; the rule is checked with `visudo -cf` first, since a sudoers file sudo cannot parse takes sudo away from Tim as well.

## Privacy permissions

Four grants that no script can make, given once each over Screen Sharing and then good until somebody revokes them. TCC only ever asks the screen: the consent dialog cannot appear in a session with no window server of its own, and writing the grant into `TCC.db` by hand needs SIP off.

Which process is asked depends on how the session arrived. Every Claude Code session is a pane of the herdr server launchd runs in the GUI session, and that job starts `~/.local/libexec/herdr-launcher`, so the launcher is the path every grant below is given to; [The herdr server](#the-herdr-server) has why. Over the tailnet without herdr the process TCC asks about is `tailscaled`; on the LAN it is sshd, where the entries are `/usr/libexec/sshd-keygen-wrapper` and `com.apple.sshd-session`, either of which can be the one that gets asked. The panes take no drag from a remote session, so use the **+** button and ⇧⌘G to type a path.

- **Screen Recording** — System Settings > Privacy & Security > Screen & System Audio Recording. For `screencapture` and every recording taken over SSH, which is how a session shows a change working rather than asserting it, and for herdr, which reads the panes it drives. Without it `screencapture` still exits 0 and still writes a PNG; the PNG is black.
- **Accessibility** — System Settings > Privacy & Security > Device Control and Data Access, the pane macOS 27 folds Accessibility into. The same entries. This is what lets a session reach into another app's windows — focus, keystrokes, clicks — which is how herdr drives anything with a GUI.
- **Full Disk Access** — for the launcher, so that a session can write Claude Code's native messaging host into `~/Library/Application Support/Google/Chrome` and read the profile back. Without it both are "Operation not permitted".
- **Notifications** — System Settings > Notifications, per app rather than per binary, for the Electron app under development. Its entry only exists once the app is signed and has asked for permission at least once, so the order is run it, let it ask, then allow it.

To check them afterwards, over SSH:

```sh
screencapture -x /tmp/shot.png && ls -l /tmp/shot.png
osascript -e 'tell application "System Events" to count processes'
```

A real desktop is hundreds of kilobytes; a black frame is a few, whatever the resolution, and the exit status and the file are the same either way. Nothing has to wake the display first. For Accessibility, a count means the grant is there and `-25211 osascript is not allowed assistive access` means it is not.

macOS also puts up a "requesting to bypass the system private window picker" dialog every so often, even with the grant in place. It is the one modal this repo cannot switch off, and it needs Screen Sharing and a click — worth knowing about when screenshots start coming back wrong for no reason.

A session shows its own app rather than the display, which carries every other session's windows, through `window`: `window list <pid>` prints the process's on-screen windows as id, size and title; `window shot <pid> <out.png> [--window <id>]` captures one without its shadow, Electron's child views included; `window record <pid> <out.mp4|out.mov> [--window <id>] [--max <seconds>] [--no-cursor] [--full] [-- <command>]` records one. The window is named only by values no other session's window can share: the pid of the app's main process, and the window id when that process has several. A process name or a title can belong to another session's copy of the app, or, for UTM, whose one process owns every VM's window, to another session's VM. A pid with several on-screen windows is refused with each one's id and title, and a pid with none exits 1 and names, with their pids, what is on screen instead. One script does all three so that rule is written once.

`shot` is `screencapture -l` given the window id. `record` is ScreenCaptureKit rather than `screencapture -v`, which records the display or a rectangle of it and so needs the window raised and uncovered for the whole recording: a desktop-independent window filter records the window's own pixels while others cover it, raising nothing. Given `-- <command>`, it runs the command once the first frame is in, stops when it exits and exits with its status, so a recording ends with the script that drives it rather than running on to `--max` when the session that started it is gone; the command gets `WINDOW_RECORD_START_MS`, the epoch milliseconds the `recording` line on stderr also carries, to time its steps against the video. Without one it runs until SIGINT or SIGTERM, which it passes on to the command, or `--max` seconds (300 by default). It prints the file once it is finished, H.264 at most 1280 wide with the pointer and its clicks drawn, ready for `gh pr create --attach`. A label goes on afterwards with `ffmpeg-full`'s `drawtext`, which the plain `ffmpeg` bottle lacks. A minimised window gives no frames and fails the recording, and while it records macOS draws its sharing indicator over the window's traffic lights. UTM stops drawing a VM window that is entirely covered, so a recording of a guest leaves a few pixels of its window showing.

`focus-lock [-t seconds] <command>` runs a command holding the session's one foreground, so two sessions that each raise a window and type or click into it take turns instead of typing into each other's apps. It waits up to five minutes by default, naming the holder, and the lock is a `lockf` on `~/.focus-lock/lock`, released by the kernel however the holder ends. Input that needs no foreground takes no lock; `$PROJECT_DOCS_DIR/shared/macos-gui-automation.md` says which input that is.

## Tailscale

`tailscale.sh` runs the open-source `tailscaled` the `Brewfile` installed as a root system daemon and sets the prefs this Mac is on the tailnet with: Tailscale SSH on, and no advertised routes — neither a subnet nor an exit node. Agents run unattended here, so a route through this Mac would put the LAN, and all of a client's traffic while it used the Mac as an exit, within reach of whatever one of them ran. A subnet router or exit node belongs on a machine that runs nothing else.

The daemon rather than the standalone app, even though both serve Tailscale SSH. A system daemon runs before anybody logs in, so a Mac whose auto-login fails or whose GUI session dies is still reachable, where the app is a login item inside a session — the dependency that already makes docker and the herdr server wait for one. Root runs Homebrew's `tailscaled` under the LaunchDaemon `brew services` writes, which is [Tailscale's own documented install](https://github.com/tailscale/tailscale/wiki/Tailscaled-on-macOS#installing-tailscaled-from-homebrew). The label is Homebrew's to choose, so `tailscale.sh` and `test/assert-machine.sh` both ask `brew services info` what it is rather than naming it, and the daemon passes no arguments, so tailscaled keeps its macOS defaults — state in `/Library/Tailscale`, socket at `/var/run/tailscaled.socket` — naming either being what would move the state and log this Mac out of the tailnet. What that accepts is a root daemon whose binary sits under a prefix the account owns; `$PROJECT_DOCS_DIR/timche/mac-mini/decisions.md` has the trade. The account has read-write access to tailscaled's own API without any of it in any case, tailscaled granting that to the macOS `admin` group, which is why nothing in `tailscale.sh` reaches the prefs through `sudo`.

Upgrading is `brew upgrade tailscale` and then `sudo brew services restart tailscale`. **Run the restart from the LAN**, `ssh timche@<the Mac's 192.168.x.x address>` rather than over the tailnet, because it drops every tailnet connection including an SSH session over one. The restart is a step of its own because launchd reads a plist at bootstrap and not again, and the plist names the opt symlink rather than a version, so the formula moving under it leaves the running process on the binary it already exec'd. `sudo brew services start` also takes root ownership of the keg it runs out of, so the keg left behind by a later `brew upgrade` is root's, and clearing it is `sudo rm -rf /opt/homebrew/Cellar/tailscale/<old version>`.

A session over Tailscale SSH is a child of tailscaled, so macOS asks tailscaled for any privacy grant something in it needs: the first `osascript` to System Events, which `test/assert-machine.sh` runs to read the login items, puts an Automation dialog on the Mac's screen and waits for it. Homebrew's tailscaled is ad-hoc signed, so the grant is tied to that one build and every upgrade asks again. Over the tailnet, a suite that stops after the docker checks is waiting for that click, over Screen Sharing.

A node that has never logged in needs `tailscale up`, which prints a URL and then waits for it — so `tailscale.sh` runs it only where there is a terminal and prints the command when there is not. Everything after that is `tailscale set`, which changes prefs without starting a login and only runs where they differ. MagicDNS is the daemon's own, written into `/etc/resolver/` and marked `# Added by tailscaled`, and left alone here. One thing only the tailnet can do, in [the admin console](https://login.tailscale.com/admin): an `ssh` rule saying who may connect and as whom. Until that rule exists nothing reaches the SSH server tailscaled is running.

```sh
tailscale status                       # the node, the tailnet, and who else is on it
tailscale debug prefs                  # RunSSH true, and AdvertiseRoutes empty
sudo brew services info tailscale      # whether it is started, and as what
sudo launchctl print system/sh.brew.tailscale   # whether the daemon is loaded, and on what
tail -f /opt/homebrew/var/log/tailscaled.log    # what it has to say
scutil --dns | grep -B2 -A2 100.100.100.100   # tailscaled's resolver files, as macOS reads them
```

## Docker

Docker here is OrbStack, the one cask `docker.sh` configures: it runs the Linux VM under Virtualization.framework and links its own `docker`, `docker compose` and `docker buildx`, so no docker formula is installed beside it. Its memory is dynamic and returns to macOS as the VM stops using it, on a Mac whose parallel sessions run browsers and Electron *outside* the VM and want that memory for themselves — so `memory_mib` is a ceiling rather than a reservation and `docker.sh` leaves it at OrbStack's default. The one thing it costs: OrbStack is free for personal use and commercial use needs a paid licence per seat, which nothing in this repo can buy or apply.

The first run is the app's own and needs somebody at the screen: a welcome screen whose Next accepts OrbStack's terms, then a choice between Docker and Linux machines. `docker.sh` stops at `orb status` and says what to click, since an install with no first run behind it has no settings to read and `orb config show` against one hangs rather than failing. Everything OrbStack links — the CLIs, the plugins, the docker context, the `Include ~/.orbstack/ssh/config` — it does for itself at that first run, including a `# Added by OrbStack` block in `~/.zprofile`, which is a link into this checkout, so that block is tracked here and stays.

After it, `docker.sh` settles two settings and only where `orb config show` disagrees: start at login, and every core but two, read from the hardware so that a different Mac needs no edit. It restarts the VM only when the core count changed, that being the one of the two the VM reads when it boots. Start at login means a login item, and a login item lives in the GUI session — so a Mac at its login window has no docker, exactly as it has no hachiko and no herdr server.

```sh
orb status                       # whether the VM is up
orb config show                  # every setting, including the two docker.sh sets
docker context show              # orbstack, the context OrbStack sets for itself
docker run --rm hello-world
docker compose version           # the plugins OrbStack links into ~/.docker/cli-plugins
```

The VM, its disk, its images and its volumes are all under `~/.orbstack`. `orb config set cpu <n>` and `orb config set memory_mib <n>` change the shape of it and take effect at the next `orb stop && orb start`; re-running `docker.sh` puts the core count back to what this Mac works out to.

## Xcode

`xcode.sh` installs the full Xcode, which this Mac needs for signing builds of an Electron app with a Developer ID certificate — the command line tools Homebrew brought are enough to compile but not the whole toolchain electron-builder reaches for. It is the one step of the machine `machine.sh` will not do unattended: Apple hands nobody an Xcode without an Apple ID, a 2FA code typed in while it is still valid, and the account password for the privileged end of the install.

```sh
~/.mac-mini/machine/xcode.sh
```

It stops before downloading anything if `/` has less than 40GB free, since the xip is around 11GB and unpacks to more than twice that. The download comes through [`xcodes`](https://github.com/XcodesOrg/xcodes) at `--latest`, so a release and never a beta, and the select, the licence and the first launch after it are each guarded so that a re-run asks for nothing. `xcodes` keeps the Apple ID password in the login keychain, which over SSH may refuse the write — in which case it asks for the Apple ID again next time, `XCODES_USERNAME` and `XCODES_PASSWORD` in the environment answer it without a keychain at all, and `xcodes signout` clears whatever is stored.

```sh
xcode-select -p        # /Applications/Xcode-<version>.app/Contents/Developer
xcodebuild -version
xcodes installed       # every Xcode on the Mac, and which one is selected
```

`rosetta.sh` is the other machine step a Developer ID build leans on, and it needs nothing typed: `softwareupdate --install-rosetta --agree-to-license` under the sudo `machine.sh` already holds, so running `machine.sh` is what agrees to Apple's Rosetta licence.

Nothing here installs the Developer ID certificate, and nothing should: electron-builder takes it from `CSC_LINK` and `CSC_KEY_PASSWORD` and imports it into a keychain of its own for the length of a build, which a project resolves from 1Password with `op run` at that point, so the login keychain never holds it. The intermediate that issued it is another matter — the certificate is from Apple's G2 Developer ID authority, macOS ships only the original, and codesign may not build the chain from the copy inside the `.p12`, so `install.sh` adds the G2 intermediate to the login keychain from apple.com with its SHA-256 pinned. An intermediate changes no trust, so this takes no sudo and no dialog; both authorities share a common name, so the check before it is by the OU.

## The account

`claude.sh` is the second phase and the part that needs an account. It installs no packages of its own — every package is the `Brewfile`'s — and runs four steps in order:

1. `install.sh` links this checkout into `$HOME` and `~/.claude` and installs the tooling those files configure.
2. `account/login.sh` logs in to GitHub and to Claude Code, both of which want a browser on some other machine, and reruns `install.sh` for the half that wanted a token.
3. `account/signing-key.sh` writes the commit-signing key into `~/.ssh` and registers its public half, and `account/vm-key.sh` writes `~/.ssh/id_vm`, the key sessions reach the local VMs with, from `op://dev/vm-ssh`. Both go through `account/ssh-key.sh`.
4. [shibuya](tools/shibuya/README.md) is deployed, and its ping token made if the Mac has none, so a fresh Mac checks in rather than being reported offline a quarter of an hour after its first sweep.

`install.sh` first, because `login.sh` logs in to a Claude Code that `install.sh` is what installs, and `signing-key.sh` writes the `user.email` from the `.gitconfig` `install.sh` links. shibuya last, because both of its scripts reach 1Password through `op run` with the service-account token `signing-key.sh` stores — a Mac that has no token file yet has shibuya skipped and said so, and a deploy that fails is reported rather than fatal. This is the only deploy nobody asks for; `install.sh` runs neither step. Run `claude.sh` again when a token expires; run `install.sh` alone after a pull that changed a link or a tool.

The GitHub token goes in a file rather than the login keychain (`gh auth login --insecure-storage`), because this Mac is reached over SSH and does its work from LaunchAgents, and both of those meet a keychain that will not answer with a prompt nobody sees. Claude Code keeps its own credential in the keychain and falls back to `~/.claude/.credentials.json` when the write is refused, which is what an SSH session usually gets; if the login does not take, `claude setup-token` prints a token that lasts a year and `CLAUDE_CODE_OAUTH_TOKEN` carries it instead.

## Layout

`home/` mirrors `$HOME`, and everything the account is made of is under it, `~/.claude` included.

| Path | |
| --- | --- |
| `home/.claude/CLAUDE.md` `settings.json` | Global instructions; hooks, effort, permission mode |
| `home/.claude/skills/` `hooks/` | Personal skills, and the one hook script herdr installs for itself |
| `home/.claude/agents/` | Custom subagents: the model, effort and instructions each role runs with |
| `home/.zshrc` `.zshenv` `.zprofile` `.gitconfig` | Shell, PATH, prompt, signed commits |
| `home/.config/starship.toml` | The prompt |
| `home/.config/git/worktree-install` | The post-checkout hook that installs a new worktree's dependencies |
| `home/.config/git/strip-claude-attribution` | The commit-msg hook that takes Claude's attribution out of every commit message |
| `home/.config/git/refuse-claude-attribution` | The pre-push hook that refuses a commit still carrying it |
| `home/.config/herdr/config.toml` `home/.terminfo/x/xterm-ghostty` | herdr config, Ghostty terminfo |
| `home/.config/mise/config.toml` | Every runtime mise installs globally |
| `home/.hachiko/` | Linked into hachiko's own folder in `HOME`: where it alerts, whose replies it takes, where it checks in, the repositories sync publishes, and its secrets' `op://` references |
| `home/.mochi/` | Linked into mochi's own folder in `HOME`: who mochi takes instructions from, which Discord server maps to which herdr workspace, and its bot token's `op://` reference |
| `home/Library/LaunchAgents/` | The agents launchd runs |
| `home/.local/bin/` | The machine's own scripts: an entry per Go tool — hachiko's, mochi's and claude-hooks' — the `op` wrapper, the window screenshot, the foreground lock |
| `home/.local/libexec/build-tool` | The build-in-place arrangement those three entries share, sourced out of the checkout and linked nowhere |
| `account/` | The account half's scripts: what `claude.sh` runs in the order its comments give, and the two `install.sh` reaches for |
| `account/claude-plugins/` | A local plugin marketplace: the `mochi` plugin, on only in the repositories mochi serves |
| `machine/` | The machine half's scripts, which `machine.sh` runs in the order its comments give |
| `machine/system/` | What lands outside `$HOME` as a root-owned copy: the claude-root helper, the sudoers rule naming it, the sshd drop-in |
| `machine/launchd/` | The herdr launcher's source, which is what that agent starts |
| `tools/hachiko/` | The watch over this Mac, its worktree sweep, its auto-sync and the nightly update — [tools/hachiko/README.md](tools/hachiko/README.md) |
| `tools/mochi/` | Tim's Claude Tag for Discord — [tools/mochi/README.md](tools/mochi/README.md) |
| `tools/claude-hooks/` | Every Claude Code hook as a subcommand of one binary, three of them on every session's Bash calls: the guard, no kill by name and no foreground without the lock; the attribution stripped out of what `gh` sends GitHub; the worktree a session moved to, named in herdr's sidebar; and the project's docs printed into a session as it starts. The status line's tap is there too, the one command that is no hook: it keeps what is left of the account's usage limits where hachiko reads it — [tools/claude-hooks/README.md](tools/claude-hooks/README.md) |
| `tools/lib/discord/` | Not a tool but a library the tools share: a Discord client, REST and gateway, as a Go module of its own |
| `tools/shibuya/` | The one tool that does not run on this Mac: hachiko's dead man's switch, a Cloudflare Worker — [tools/shibuya/README.md](tools/shibuya/README.md) |

`.claude` sits under `home/` rather than at the repo root because a `.claude` directory at a repo root is *project* configuration to Claude Code — this repo would load its own global settings and skills a second time whenever it was the working directory.

Everything else here is a shell script, and the tools under `tools/` are the exception. A compiled tool is Go on the standard library alone, run from this Mac; a Worker is TypeScript with a deploy script of its own, and the only one there is runs nowhere near this Mac on purpose. There is no release pipeline and nothing to publish, because there is nowhere to publish to: the entry in `home/.local/bin/<tool>` builds the tool from this checkout's working tree and runs its tests as the gate, so an edit is a running binary minutes later and a tree that does not compile or does not pass leaves the last one that did. All of that is one script, `home/.local/libexec/build-tool`, which each entry sources after setting what is its tool's own; `CLAUDE.md` has the rules a change to one of the tools obeys, that script's comment has how the building works, and each entry's own what that tool adds to it.

`machine/launchd/herdr-launcher.c` is the one compiled thing that is neither a tool nor a folder under `tools/`, and the one that is C. It is built once by `install.sh` rather than by a wrapper, for the reason [The herdr server](#the-herdr-server) gives: its bytes are an identity macOS pins privacy grants to, and a rebuild costs them.

The links themselves are declared in `mise.toml` under `[dotfiles]` and applied by `mise bootstrap dotfiles apply`, which recognises links that already point at the right source and replaces anything else at a target — with the `--force-dotfiles` `install.sh` passes, a real file left at a link's target is overwritten and no backup is kept. A tool that edits a linked file by writing a new one and renaming it over the path replaces the link with a regular file, and from then on its edits stay off GitHub; re-running the bootstrap discards them, so fold them in first:

```sh
cp ~/.claude/settings.json ~/.mac-mini/home/.claude/settings.json
ln -sfn ~/.mac-mini/home/.claude/settings.json ~/.claude/settings.json
```

`test/assert.sh` fails on any link that has become a file, and on a `/home/<name>` or `/Users/<name>` anywhere under `home/` or in `mise.toml`, because a path written for one account is a hook that never fires for another and fails silently rather than loudly. The hook commands in `settings.json` go through `$HOME`, since they run through a shell; the LaunchAgents, in which launchd expands neither a `~` nor a `$HOME`, each run `/bin/sh -c` and leave every path to the shell, which is the one thing launchd hands a `HOME`. `env` in `settings.json` is the one place a `$HOME` would not work, so `CLAUDE_CODE_TMPDIR` is exported from `.zshenv` instead.

## The shell and the tooling

`install.sh` makes zsh the login shell if anything has moved the account off it, reading `dscl` rather than assuming, because a `chsh` with nothing to change would still ask for a password nobody is there to type. Nothing else in the account phase needs root.

mise is a version manager for language runtimes and nothing else. The global list in `home/.config/mise/config.toml` is `node`, `bun` and `python`; a runtime a repository needs for itself is declared in that repository's own root `mise.toml`, which is why `go` is in this one's. `install.sh` runs `mise install --locked` from inside the checkout so that both lists are installed, and says so when the `go` it asks for could not be — a Mac without it keeps running the binary each wrapper already built and quietly stops picking up changes. Everything else the Mac has is in the `Brewfile`, bar the npm packages mise's npm backend keeps on the node it already provides (portless, ccstatusline, chrome-devtools-mcp) and Chrome for Testing, which has no cask and no updater of its own and so comes through mise's `http` backend, pinned and moved like any other tool.

Three tools are their authors' installers rather than a package, and for one reason each: mise, because [its install page](https://mise.jdx.dev/installing-mise.html) calls the standalone install the recommended route and only that one supports `mise self-update`; Claude Code, because it updates itself in the background on the default channel and pinning it would mean turning that off and owning what it was doing; and herdr, because `herdr update` overwrites whichever binary it runs from, which under Homebrew is a Cellar path the formula then no longer matches. `install.sh` fetches each only when the binary is missing and leaves it alone afterwards. `~/.local/bin` sits ahead of Homebrew's prefix in `.zshenv` so that a `brew install` of any of them cannot answer first, and `install.sh` asks whether the file exists rather than asking PATH for the same reason.

No shell file names a path for mise: `.zshenv` puts the shims, then `~/.local/bin`, then Homebrew's prefix in front of the system directories, and `.zshrc` activates whichever `mise` that finds. Homebrew is also why there is a `.zprofile` — macOS runs `path_helper` from `/etc/zprofile` in every login shell, which rebuilds PATH with the system directories first, so `.zprofile` runs after it and re-reads `.zshenv`, `typeset -U` keeping the second pass from listing anything twice.

Claude Code's Bash tool runs Homebrew's bash 5, named by `CLAUDE_CODE_SHELL` in `home/.claude/settings.json`, while zsh stays the login shell for everything else. bash is what a model writes, and zsh's differences from it fail quietly: its `log` builtin shadows `/usr/bin/log`, an unmatched glob aborts the command and an unquoted variable is not split. `/bin/bash` 3.2 would trade those for every bash 4 feature a model reaches for, so the tool gets Homebrew's. Claude Code is started from zsh, so it carries the environment `.zshenv` built, PATH and mise's activation included.

Every session has two MCP servers whichever project it is in, registered at user scope by `install.sh`: `chrome-devtools`, Google's Chrome DevTools MCP, which drives a page and reads what a screenshot cannot, and `context7`, current documentation for the version of a library a project uses. chrome-devtools runs `--headless --isolated`, so each session launches a browser of its own with a throwaway profile, and that browser is Chrome for Testing rather than Tim's Chrome headless: macOS sends a Dock, Spotlight or `open -a` launch of an app to a copy already running under its bundle ID, so a headless `com.google.Chrome` would catch every launch of the real one, open no window and ignore Quit. `--browser-url` pointed at a debugging port attaches the same server to a running browser or an Electron app instead, which a project declares in its own `.mcp.json` under a name of its own, since a project server shadows a user one of the same name. Claude Code keeps user-scope servers in `~/.claude.json`, which is its state as much as its configuration, so they are written with `claude mcp add-json` rather than linked, and only when the definition in `install.sh` differs from what is there.

Four git hooks are defined in `.gitconfig` as config hooks, which git 2.54 runs beside a repository's own `.git/hooks` or `core.hooksPath` — a global `core.hooksPath` would have switched off each project's lefthook or husky instead:

- `betterleaks` on `pre-commit`, so a token pasted into a file stops at the commit rather than at a push nobody reviews. It scans only what is staged, a missing betterleaks warns rather than refusing every commit, and `git commit --no-verify` is the deliberate way past a false positive. `.github/workflows/secrets.yml` is the second line, scanning the full history of every push with the same config.
- `worktree-install` on `post-checkout`, which installs a JS project's dependencies into a worktree as it is added, because a worktree with no `node_modules` is one where the repository's own pre-commit finds no linter and passes having checked nothing. It acts only on a `git worktree add` into a project somebody has already installed in the main worktree by hand, which is what says the project has been trusted; anything else gets the commands printed instead.
- `strip-claude-attribution` on `commit-msg` and `refuse-claude-attribution` on `pre-push`, because Claude Code's own setting only changes what the model is asked to write. The first strips rather than refuses, since a refused commit is one an agent retries and works around; the second catches `--no-verify`, a cherry-pick and a commit made elsewhere. A pull request body never passes through git, so `claude-hooks strip-attribution` takes the same lines out of any `gh` command, and out of the body files it names, before the command runs, and refuses `--no-verify` on a commit or a push wherever it sits in a command.

`.gitconfig` includes `~/.gitconfig.local` last, which is where anything machine-local goes. It is untracked and written by hand, and `test/assert.sh` asserts that the signing key it leaves effective is the one the tracked config names.

## The signing key

The key is the same one every machine here signs with, and it lives in 1Password. `account/signing-key.sh` writes it to this Mac as two files: `~/.ssh/id_signing`, mode 600 in a directory at 700, and `~/.ssh/id_signing.pub` beside it, which is what `.gitconfig` names as `user.signingkey`. Nothing asks an ssh-agent for a key, so nothing has to be running for a commit to sign — there is no job to load and no socket to find, whether the session came in over SSH, out of a LaunchAgent or from a pane of the herdr server. The third derived file is `~/.ssh/allowed_signers`, a principal and a public key so that git can verify what it signs.

The private half goes from `op read` down a pipe into a temporary file in `~/.ssh` under `umask 077` and then a rename, so it is never under a world-readable `TMPDIR`, never briefly readable at its final path, and never in a variable, a trace or an argument. What bounds it is the account, which can read that file and can equally read the service-account token and fetch the same key from 1Password. The key signs commits and authenticates nothing, and GitHub revokes it in one click.

None of the three files is a second source: the script rewrites the trust list from 1Password on every run, so a rotated key takes the line for the old one with it, and it reads the private half again whenever the file is missing or holds a different key from the vault's. A run against a key that is already there spends no `op read` on the private field, every one of them counting against the service account's day.

What it expects:

- A 1Password item with the key in it, at `op://dev/ssh-commit-signing`, whose `private key` and `public key` fields are the two halves. `SIGNING_KEY_OP_ITEM` names a different one.
- A 1Password service account with read access to that vault. `account/op-token.sh`, which `signing-key.sh` calls, asks for its token once without echoing it, checks it can read the item, and stores it in `~/.config/op/service-account-token`, where `signing-key.sh` reads it and the `op` wrapper does for every `op run`. After rotating the token in 1Password, run `account/op-token.sh --replace` at a terminal to store the new one.

```sh
ls -l ~/.ssh/id_signing ~/.ssh/id_signing.pub         # the key, 600 and 644
ssh-keygen -y -f ~/.ssh/id_signing                    # the public half it carries
git commit --allow-empty -m test && git log --format='%G?' -1
```

`error: No private key found for public key "…/.ssh/id_signing.pub"` means the public half is there and the private one is not, which is a `account/signing-key.sh` away. `error: user.signingKey needs to be set for ssh signing` means the config itself is not in place, so `./install.sh` has not linked `.gitconfig`.

## The project docs

Every project's docs live in `timche/docs`, a folder per project under its GitHub owner, cloned to `~/projects/docs` by `install.sh` and committed and pushed on its own by `hachiko sync`, which also publishes this repository's `home/.claude`; [tools/hachiko/README.md](tools/hachiko/README.md#auto-sync) has it. It is private, because the folder names alone say which projects exist. Plain markdown throughout, cross-linked with relative links rather than wikilinks so it renders wherever it is read — on github.com or the GitHub mobile app, where mermaid renders and there is nothing to pull.

Project checkouts go in `~/projects`, which `install.sh` creates and `home/.claude/CLAUDE.md` names, so a session knows where to clone without being told. The docs clone is one of them, since it is written in every session like any other work. This repository is the one thing at the top of `$HOME` that is not a project: it is the machine rather than work done on it.

A `SessionStart` hook, [`claude-hooks project-docs`](tools/claude-hooks/README.md), prints the project's docs folder and its shape — stdout from `SessionStart` goes into the session context, so Claude starts already knowing the docs are there. It looks only at `~/projects/docs/<owner>/<repo>`, which is the layout checkouts have in `~/projects` too; a checkout's own `docs/` is the project's published documentation rather than its working docs, so it is never read. Before it comes `~/projects/docs/shared`, in every session whether in a repository or not. The owner and repository come from the `origin` remote rather than the directory, because a worktree is named for its branch while every branch shares one docs folder, and subdirectories collapse to a name and a count, the shape being the part worth injecting rather than fifty lines of file names.

## Worktree isolation

Several sessions run at once, a worktree each — herdr puts its own at `~/.herdr/worktrees/<repo>/<branch>` and Claude Code puts its own at `<repo>/.claude/worktrees/<name>` — and every name two of them agree on is shared state: one compose project is one database, one host port belongs to whichever session bound it first, one Electron profile is one lock file.

portless is the machine's rather than a project's, because all three of the things it owns exist once: the proxy holding port 443, the certificate authority signing for every `.localhost` name, and the routes registered in `~/.portless`. `install.sh` trusts the CA once, looking up `~/.portless/ca.pem` in the account's and admin trust settings rather than asking `portless doctor`, which reports trust in prose with no machine-readable form; doing it there is what keeps portless from asking for a sudo password in whichever session starts a dev server first, and it is never fatal, an untrusted CA costing a browser warning rather than a shell. Over SSH it is not attempted at all, since adding a certificate to the system trust store puts a confirmation dialog on the Mac's own screen as well as a sudo prompt — so `install.sh` prints the one line to run in Terminal over Screen Sharing, `portless trust`.

Port 443 on `127.0.0.1` is root's on macOS and a session has no terminal to answer sudo from, so the proxy runs as portless's own LaunchDaemon, `sh.portless.proxy`, installed once by hand with `sudo portless service install` and the TLD list below. It is the one root daemon this repo depends on and does not own, because launchd will not take a system job from a symlink in a user's checkout. The daemon records the absolute paths of the node and the portless that installed it, both under mise's versioned install directories, so a `mise up` of either leaves it pointing at a file that is gone; `install.sh` reads both paths back and prints the reinstall command when one is missing, and [the nightly update](tools/hachiko/README.md#updates) runs it through the [root helper](#the-root-helper).

Dev servers open from the MacBook under a name of their own, `https://<branch>.<app>.timche.dev`, the same shape as the `.localhost` one; `.zshenv` sets `PORTLESS_TLD=localhost,timche.dev`, so the one proxy serves both, `.localhost` staying first because `PORTLESS_URL` takes the first TLD. A wildcard record at Cloudflare points `*.timche.dev` at this Mac's tailnet address, DNS only, which is harmless in public DNS since nothing off the tailnet can reach it; resolvers with DNS-rebinding protection return nothing for such a name, so the tailnet's DNS settings route `timche.dev` to `1.1.1.1` as split DNS. `install.sh` has Tailscale Serve hand the tailnet's port 443 to the proxy as raw TCP, so portless keeps TLS and routes by hostname, and the MacBook trusts portless's CA once. A name per app rather than a port per app is the point of the whole arrangement: browsers key cookies by host and ignore the port, so one hostname with a port each would give every dev server on this Mac one cookie jar.


What a removed worktree leaves behind — its compose project, its volumes, its processes, its git metadata — is swept by `hachiko gc`; [tools/hachiko/README.md](tools/hachiko/README.md#the-worktree-sweep) has it.

## The herdr server

Every Claude Code session is a pane of one herdr server, and that server is started by launchd inside the logged-in GUI session, `io.github.timche.herdr`, rather than by the first `herdr` a remote client runs. Tim attaches from the MacBook with `herdr --remote`, which only talks to the server's socket, so where the server was started decides what its panes are. Started over SSH, every pane carries `SSH_CONNECTION`, `SSH_CLIENT` and `SSH_TTY`, and Claude Code withholds computer use from such a session — the built-in `computer-use` server is simply missing from `/mcp`, and appears once those three variables are unset. From launchd, a pane is genuinely a local session, and macOS asks its privacy grants of this job rather than of `tailscaled`.

What the job starts is not herdr but the launcher, `~/.local/libexec/herdr-launcher`, which runs `~/.local/bin/herdr server` as its child. It is there for one reason: a privacy grant is pinned to the exact contents of an ad-hoc signed binary, herdr's release binary is ad-hoc signed, and every `herdr update` therefore cost all of [the grants](#privacy-permissions) and an afternoon of Screen Sharing to give them back. TCC asks the responsible process, which for a launchd job is the job's own process and for that process's children is it again, so the grants belong to the launcher and herdr may change underneath as often as it likes.

Which is why the launcher is the parent and never execs herdr — an exec would put herdr's contents back in front of TCC — and why, being a parent nothing else needs, it has to behave as though it were not there: it forwards `SIGTERM`, `SIGINT` and `SIGHUP`, and exits with herdr's status or dies of herdr's signal, so `KeepAlive` and `launchctl bootout` see exactly what they saw when the agent ran herdr itself. `test/assert.sh` proves both halves. `install.sh` compiles it to that fixed path only when the SHA-256 of `machine/launchd/herdr-launcher.c` no longer matches `~/.local/state/mac-mini/herdr-launcher.sha256`, deliberately not through the build-in-place wrapper the Go tools use, since a rebuild for any reason at all is new bytes and so a dropped grant. A build is therefore a re-grant, which the install says out loud, and the only ones there should ever be are a fresh Mac and a deliberate change to the launcher.

`KeepAlive` is unconditional, unlike the sync agent's, because a missing server — even one stopped on purpose — would otherwise be replaced by the next remote client starting its own over SSH. `install.sh` loads the agent but never boots it out or restarts it: it usually runs inside one of that server's panes, and restarting the server ends every session on it, the one running the script included. A server already running outside launchd, or a plist changed since launchd read it, is reported with the commands to run from a plain `ssh timche@mac-mini` once no session needs the server. A Mac with no launcher built is left with the agent unloaded and reported instead, since a job pointed at a binary that is not there is one launchd retries every ten seconds for the life of the Mac. The log is `~/Library/Logs/herdr.log`.

What stays unsolved either way is approval: computer use asks for each app once per session, in the session's own terminal, and nothing pre-approves apps for a session nobody is watching ([anthropics/claude-code#47796](https://github.com/anthropics/claude-code/issues/47796), closed without it).

## Projects and 1Password

A project that resolves its secrets with `op run --env-file .env.op -- <cmd>` gets the service-account token from `~/.local/bin/op`, a wrapper ahead of Homebrew's `op` on PATH, so the project never learns how `op` signs in and the same command works on a Mac where the 1Password app does it. When `OP_SERVICE_ACCOUNT_TOKEN` is unset and the token file is readable, the wrapper sets the variable for that one `op` process and nothing else, where exporting it from `.zshenv` would put it in every process and so in transcripts and logs. `op run` hands its own environment to the command it starts, so the wrapper puts `env -u OP_SERVICE_ACCOUNT_TOKEN` right after the first `--`; a command that calls `op` by name comes back through the wrapper and is signed in again. This keeps the token out of projects and build tools' environments rather than away from the account, which can read the file anyway, and the scripts here read the file themselves and do not depend on it.

Signing and notarising a macOS build is the project's own configuration, in a `.env.signing.op` it runs the build under, rather than a wrapper here. `.zshenv` also sets `DO_NOT_TRACK=1`, the cross-tool opt-out at donottrack.sh, so no CLI a session runs sends usage analytics or writes an install id.

## Preferences

Tim's own preferences, split by what mise's declarative `[bootstrap.macos.defaults]` can say: a plain user default is declared in `mise.toml` and written by the same `mise bootstrap` that applies the links, and everything else is in `install.sh`.

| Preference | Applied by | |
| --- | --- | --- |
| Tap to click | `mise.toml` | `[bootstrap.macos.trackpad]`'s `tap_to_click`, one friendly key for both `Clicking` domains, plus the per-machine `com.apple.mouse.tapBehavior` as an explicit `host = "current"` entry, which is the one doing the work |
| Finder: hidden files, every extension, status bar, path bar, no extension-change warning | `mise.toml` | `[bootstrap.macos.finder]` covers four of the five; `AppleShowAllExtensions` is global rather than Finder's own and has no friendly key, so it is a raw entry |
| Screenshots in `~/Pictures` | `install.sh` | The value has to be an absolute path — neither mise nor the screenshot service expands a `~`, and mise does not template a defaults value — so declaring it would mean spelling the account name out, which nothing here does |
| `~/Library` visible in Finder | `install.sh` | A flag on the folder, and a Finder info xattr beside it, rather than a preference at all |
| A Dock with none of the icons macOS ships with | `install.sh` | Once, recorded under `~/.local/state/mac-mini`, and then left alone, so that whatever Tim has pinned since survives an install |

Every one of them reads before it writes, and nothing restarts an application that had no reason to notice — which is why the restarts are `install.sh`'s rather than a `post-defaults` hook, mise killing no application and a hook there relaunching Finder on every install whether anything moved or not. The screenshot location relaunches `SystemUIServer` and `screencaptureui` both: every guide still names the first, and the process actually found holding the stale copy is the second.

CI reads every one of these back on the `macos-latest` runner and asserts that a second pass finds nothing to write, which is what `mise bootstrap macos defaults status --missing` answers.

## Testing

`.github/workflows/test.yml`, on a `macos-latest` runner and nowhere else: there is no macOS container to put any of this in, and the suite changes the machine it runs on. Hosted runners are free to a public repository, which is the reason this one is public.

The job runs `bootstrap.sh` first, against a clone of the branch under test rather than of main, which is the only path that clones anything. Then each phase by hand with the asserts after each: `machine.sh` twice, then the [root helper](#the-root-helper) under sudo, which only a machine with passwordless sudo can exercise — `--list` and three refusals, and none of the actions themselves — then `xcode.sh` twice, then `claude.sh` and after it the `install.sh` that is its own first step. The repeat is what keeps the whole of it honest about being safe to re-run, which is the only way a change ever reaches a Mac that is already built, and the runner's account being called `runner` is what proves nothing here assumes an account name. Then `test/signing-key.sh` and `test/sync-agent.sh`, the two that need more than a file read, and last `harden-ssh.sh` with a key seeded the way a real Mac has one, because a runner has nothing after it that needs to log in.

There are four assert scripts. `test/assert-machine.sh`, `test/assert.sh` and `test/assert-account.sh` are one per phase — what `machine.sh` left, what `install.sh` left, and the logins and the signing key. `test/assert-markdown.sh` is the odd one out and the only one about the repository rather than the Mac: the word budgets on the always-loaded `home/.claude/CLAUDE.md` and on this repository's own `CLAUDE.md`, and prose that is never hard-wrapped. `assert.sh` runs it and adds its failures to its own, and `.github/workflows/markdown.yml` runs it alone on `ubuntu-latest` — which is the point of it being a script of its own, since `test.yml`'s paths filter deliberately keeps a markdown-only push off a macOS runner. Which is also why nothing in it may reach for a command only macOS has.

The compiled tools are the one part the assert scripts deliberately do not test. `go vet`, `go test ./...` and a pinned `govulncheck` run for each of them and for `tools/lib/discord/` in the same job, and that is where every rule a tool decides by is checked, against injected seams rather than against the Mac. What `test/assert.sh` adds is the wiring Go cannot see: that the wrapper is linked and runs, that the agents are valid plists and loaded, and that the wrapper's own gate works, against a copy of the checkout since it has to break a test on purpose and the wrapper builds from the working tree it would be broken in. [shibuya](tools/shibuya/README.md)'s `npm test` and a `wrangler deploy --dry-run` are in that job too, although nothing about them needs a Mac: they run in workerd, and this is the job that has mise's node.

Two tests do what no file read can. `test/signing-key.sh` runs the real `account/signing-key.sh` against a throwaway `HOME` with a stub `op` on PATH, and refuses to run outside CI unless `MAC_MINI_TEST_ANYWAY=1`, since it would register its throwaway key on whatever GitHub account `gh` is logged in to. `test/sync-agent.sh` loads a plist under a label of its own and asserts what only a real `bootstrap` can: that a symlinked plist loads, that the `HOME` launchd hands a gui-domain job is what the shell expands, and that the status reaching `KeepAlive` is the program's.

A local run is `./install.sh && test/assert.sh` on the Mac itself, the quick check after a change to the account half, and `./machine.sh && test/assert-machine.sh` for the other one. Both run against a Mac that is already built, so the fresh-Mac path is CI's alone and a change to it is tried by pushing the branch and reading that job — and not from nothing even there, since a hosted runner arrives with Homebrew and the command line tools already.

What CI cannot reach, where the asserts report where things stand rather than asserting them:

- Anything that needs a click: Screen Sharing needs the Sharing pane to register the sharing agent's rights, and the screen lock is not reachable from a script at all, so nothing checks it.
- A service account and a vault, which takes the shibuya deploy with it, since `op run` is what resolves the Cloudflare token: what CI exercises there is the tests and the dry run and never the upload. The docs clone fails for the same reason, so `hachiko sync` stays unloaded and only its wiring is checked.
- A tailnet to log in to. The runner installs tailscaled and configures its prefs, which works logged out, but nothing authenticates.
- A hypervisor, a runner being a VM itself. Everything docker itself answers is skipped there, and only a Mac with OrbStack running asserts any of it.
- TCC and sudo as a real Mac has them. Runners have SIP disabled, so anything gated on Full Disk Access behaves more permissively — which is why `remote-login.sh` goes through launchd rather than the gated `systemsetup`. A runner's sudoers also grants the account every command, which is what lets the helper install there at all and means the half of it that matters most, that sudo refuses everything outside the helper, is the one thing only a real Mac can assert.
- A machine that loses power.

## Environment knobs

`MAC_MINI_REPO`, `MAC_MINI_DIR` (the clone, `~/.mac-mini` by default), `PROJECT_DOCS_DIR`, `SIGNING_KEY_OP_ITEM`, `OP_SERVICE_ACCOUNT_TOKEN_FILE`, `FORCE_HARDEN`, `CLAUDE_ROOT_DRY_RUN`, `MAC_MINI_TEST_ANYWAY`. Each tool's own are in its README: [hachiko](tools/hachiko/README.md#environment-knobs), [mochi](tools/mochi/README.md#environment-knobs), [shibuya](tools/shibuya/README.md#environment-knobs).

`CLAUDE.md` has the rest: the order the scripts run in, the constraints that are not obvious from reading them, and how to work in this repo.
