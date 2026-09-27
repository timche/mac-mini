# Mac mini Dotfiles

Everything personal on the Mac mini Claude runs on: the shell, the runtimes and the Claude Code configuration. The machine itself is [timche/mac-mini-setup](https://github.com/timche/mac-mini-setup), which builds it. Both are public. This one was private until 2026-09-27 and was then published with a fresh history, after the synced claude.ai skills left the tree and a secrets scan guarded every commit — see Betterleaks below — so that it could run on GitHub's free macOS runners. What it still describes of private infrastructure is kept to how the machine is reached, not where: no tailnet name or address.

macOS only. `install.sh` and `test/assert.sh` each refuse another `uname` outright rather than doing half a job on it: this repo also ran a Debian VM until 2026-09-26, and every `os` selector, `uname` branch and rc-file guard that kept the two in step went with it. What `mac-mini-setup` owes this repo is Homebrew, `gh` with a token, git from the Xcode command line tools, `jq`, tailscale and the commit-signing key in an ssh-agent. Neither half of that key is on the Mac's disk: `mac-mini-setup` reads the private half out of 1Password into an ssh-agent it keeps at `~/.ssh/agent.sock`, which is the socket `.zshenv` and boswell's LaunchAgent name, and `.gitconfig` names no key of its own — `gpg.ssh.defaultKeyCommand = ssh-add -L` is git's documented way of asking that agent, and with no `user.signingkey` it signs with the first key the agent answers with. 1Password is then the one place the key is kept, and the only file derived from it is the `~/.ssh/allowed_signers` git verifies against, which `mac-mini-setup` writes.

`.gitconfig` also runs [Betterleaks](https://github.com/betterleaks/betterleaks) before every commit in every repository on the Mac, boswell's auto-commits included, so a token pasted into a file stops at the commit rather than at a push nobody reviews. It is a hook defined in config, `[hook "betterleaks"]`, which git 2.54 runs beside a repository's own `.git/hooks` or `core.hooksPath`; a global `core.hooksPath`, the older way to hook every repository, would have switched off each project's lefthook or husky instead. It scans only what is staged and redacts what it finds, a missing betterleaks warns rather than refusing every commit, and `git commit --no-verify` is the deliberate way past a false positive. Betterleaks rather than gitleaks because gitleaks' author declared it feature complete and moved to this successor. `.github/workflows/secrets.yml` is the second line: it scans the full history of every push with the same config, from Betterleaks' own image pinned to a version (there is no official action), so a commit that skipped the hook or came from anywhere but this Mac is still read.

`.gitconfig` includes `~/.gitconfig.local` last, which is where anything machine-local goes. It is untracked and written by hand, and `test/assert.sh` asserts that nothing in it names a signing key — the agent answers for that.

`mac-mini-setup`'s `claude.sh` clones this repo and runs `install.sh` as soon as its `claude/login.sh` has a GitHub token. By hand:

```sh
gh repo clone mac-mini-dotfiles ~/.mac-mini-dotfiles
~/.mac-mini-dotfiles/install.sh
```

This clone has to stay on disk — `install.sh` symlinks out of it. The public one does not. Hidden, because `$HOME` holds what is worked on and this is what makes the account itself; `DOTFILES_DIR` moves it.

## Layout

`home/` mirrors `$HOME`, and everything is under it, `~/.claude` included.

| Path | |
| --- | --- |
| `home/.claude/CLAUDE.md` `settings.json` | Global instructions; hooks, effort, permission mode |
| `home/.claude/skills/` `hooks/` | Personal skills, and the scripts the hooks call |
| `home/.claude/agents/` | Custom subagents: the model, effort and instructions each role runs with |
| `home/.zshrc` `.zshenv` `.zprofile` `.bashrc` `.gitconfig` | Shell, PATH, prompt, signed commits |
| `home/.config/starship.toml` | The prompt |
| `home/.config/herdr/config.toml` `home/.terminfo/x/xterm-ghostty` | herdr config, Ghostty terminfo |
| `home/.config/mise/config.toml` | Every runtime and CLI mise installs globally |
| `home/.config/boswell/config.toml` `home/Library/LaunchAgents/` | The repositories boswell watches, and the agents launchd runs |
| `home/.local/bin/` | The machine's own scripts: the worktree sweep, the signed-build wrapper, the memory log, the CI dispatcher |
| `home/.config/tart-runner/repos` | The repositories whose CI jobs this Mac answers |

`.claude` sits under `home/` rather than at the repo root because a `.claude` directory at a repo root is *project* configuration to Claude Code — this repo would load its own global settings and skills a second time whenever it was the working directory.

`install.sh` also installs the tooling those files configure — oh-my-zsh, mise, Claude Code, the `Brewfile`'s packages and every tool below — and makes zsh the login shell if anything has moved the account off it. A Mac logs in to `/bin/zsh` already, and the check is `dscl . -read /Users/<name> UserShell` rather than `getent`, which macOS does not have — read rather than assumed, because a `chsh` that has nothing to change would still ask for a password nobody is there to type.

The Homebrew packages are the `Brewfile` at the repo root, applied by `install.sh` with `brew bundle --no-upgrade`: the zsh syntax highlighting, since macOS ships zsh itself, `fd`, `ffmpeg`, `glow`, `herdr`, `ripgrep`, `shellcheck`, `starship`, `zoxide`, Google Chrome and UTM as casks, Tart from `openai/tools`, and boswell from `timche/tap`. One declarative file with `brew bundle check` to read it back, which is what `mise.toml`'s `[bootstrap.packages]` could not offer: no `--no-upgrade`, nothing to assert the installed set against, and no way to fetch a formula from a plain tap — which is why boswell used to be a `brew install timche/tap/boswell` line of `install.sh`'s own. `--no-upgrade` because a re-run installs what is missing and moves no version, the same promise `mise install --locked` makes for the tool list; without it every install would be an unasked-for `brew upgrade` of the whole list, and `brew bundle upgrade` is the deliberate version. Nothing declares `adopt` any more either: `brew bundle` passes `--adopt` to every cask install of its own accord, which is what takes over the Chrome a macOS runner's image already has rather than refusing to lay a cask over an app that arrived by another route.

`mise.toml` has no packages phase left at all. The packages are here rather than in the machine repo because that one installs only what has to exist before this one can be reached — `gh`, `git`, `curl`, `jq` — and a shell and its highlighting are no use without the files in `home/` that configure them. Homebrew itself is `mac-mini-setup`'s, and `install.sh` stops with a message rather than carrying on without it.

mise itself is the exception to the rule it enforces, and it is the same exception Claude Code is: the authors' own installer beats a package of it. `install.sh` runs `curl -fsSL https://mise.run | sh` into `~/.local/bin` when that binary is missing. [mise's install page](https://mise.jdx.dev/installing-mise.html) calls that the recommended route and Homebrew's formula not the preferred one — Homebrew builds mise separately from the official release binaries, the formula trails upstream, and only a standalone install supports `mise self-update`. It trailed by a fortnight the week this changed, 2026.9.1 against 2026.9.13.

Neither rc file names a path for mise. `.zshenv` puts the shims, then `~/.local/bin`, then Homebrew's prefix in front of the system directories, and both shells activate whichever `mise` that finds; `.bashrc` builds the same order for itself, since bash reads none of the zsh files. `~/.local/bin` ahead of Homebrew's prefix is what keeps a `brew install mise` from answering first, and `install.sh` asks whether `~/.local/bin/mise` exists rather than asking PATH for the same reason. `test/assert.sh` checks the binary every kind of zsh resolves, through `whence -p` rather than `command -v`, since `mise activate` leaves a shell function of that name in an interactive one. `~/.local/bin` moved into `.zshenv` from `.zshrc`, which left it off every non-interactive shell and put it ahead of the shims in the ones that had it.

Homebrew is also why there is a `.zprofile`. Its prefix is on no default PATH, so `.zshenv` puts `/opt/homebrew/bin` behind the mise shims and `~/.local/bin` and in front of the system directories; but macOS runs `path_helper` from `/etc/zprofile` in every login shell, which rebuilds PATH with the system directories first. `.zprofile` runs after that and re-reads `.zshenv`, and `typeset -U` there keeps the second pass from listing anything twice.

herdr is in the `Brewfile`, and it replaces its own binary given the chance — so `herdr update` gives way to `brew upgrade herdr`, which `brew bundle --no-upgrade` will not do behind your back.

Claude Code is nobody's package. Anthropic's installer puts it at `~/.local/bin/claude` and it updates itself in the background on the default `latest` channel, which is the point of taking it from there: a pinned Claude Code is a day behind by the afternoon, and mise would spend every install putting the pin back over the updater's work. `install.sh` runs `curl -fsSL https://claude.ai/install.sh | bash` when that binary is missing and leaves it alone afterwards. Nothing sets `DISABLE_AUTOUPDATER`.

ccstatusline is `"npm:ccstatusline"` in mise's tool list, and `settings.json` runs it through its shim. Installed once rather than fetched per draw: `npx -y ccstatusline@latest` measured about 710ms a draw, and `npx --offline` still 625ms, because the cost is npm starting node twice rather than the registry, while an installed copy draws in about 320ms. mise's npm backend keeps it on npm and node like portless, pins it in the lockfile, and `mise up` moves it.

mise is a version manager for language runtimes and nothing else. On the Mac its tool list is `node`, `bun` and `python`, the three whose version can differ per project, and a tool only one project wants is declared in that project's own `mise.toml`, which is why nothing here installs `go` or `rust`. Everything else the Mac has is in the `Brewfile` — `fd`, `glow`, `herdr`, `shellcheck`, `starship`, `zoxide`, `ffmpeg`, `ripgrep` and boswell — while mise and Claude Code are their authors', above, and portless and ccstatusline are npm packages mise's npm backend keeps on the node it already provides.

Nothing here needs root, Homebrew wanting none of it: the only `sudo` left is the `chsh` that would move the login shell, and a Mac already on zsh never reaches it.

Safe to re-run. The links themselves are declared in `mise.toml` under `[dotfiles]` and applied by `mise bootstrap dotfiles apply`, which recognises links that already point at the right source and replaces anything else at a target, which is how a real file left at a link's target gets moved aside to `<name>.backup`.

A tool that edits a linked file by writing a new one and renaming it over the path replaces the link with a regular file, and from then on its edits and every later one stay off GitHub. `moshi-hook install` did this to `~/.claude/settings.json` on 2026-09-19, and nothing synced for four days. Re-running the bootstrap at that point discards the tool's edits, so fold them in first: `cp ~/.claude/settings.json ~/.mac-mini-dotfiles/home/.claude/settings.json`, then `ln -sfn ~/.mac-mini-dotfiles/home/.claude/settings.json ~/.claude/settings.json`. `test/assert.sh` fails on any link that has become a file.

## The account

Nothing under `home/` spells a home directory out, so the account can be called anything: `timche` on the Mac, `runner` under CI. The hook commands in `settings.json` go through `$HOME` — they run through a shell, so it expands — the statusLine command names no path at all, and the two LaunchAgents, which launchd expands neither a `~` nor a `$HOME` in, each answer for themselves: boswell's runs `/bin/sh -c` and leaves every path to the shell, and worktree-gc's is rendered from a mise template because launchd reads its `WatchPaths` itself. `test/assert.sh` fails on a `/home/<name>` or `/Users/<name>` anywhere under `home/`, because a path written for one account is a hook that never fires for another, and it fails silently rather than loudly.

`env` in `settings.json` is the one place a `$HOME` would not work: Claude Code reads those values straight out of the file, so `CLAUDE_CODE_TMPDIR` is exported from `.zshenv` instead.

## Auto-sync

[boswell](https://github.com/timche/boswell) gets everything written during a session upstream on its own. One process watches every repository `~/.config/boswell/config.toml` lists, and two are listed: this repo and the project docs at `~/projects/docs`. It runs as a LaunchAgent labelled `io.github.timche.boswell`:

```sh
launchctl print gui/$(id -u)/io.github.timche.boswell      # loaded, and what launchd makes of it
tail -f ~/Library/Logs/boswell.log                         # why something did not land
launchctl kickstart -k gui/$(id -u)/io.github.timche.boswell  # after editing the config
```

The config is read once at startup, so a third repository is two lines in it and that restart — there is nothing else to enable.

The agent is a symlink into this checkout like everything else, which launchd accepts: it resolves the link when the job is bootstrapped and reads the file behind it. It has nothing to render because it names no path of its own — launchd expands neither `~` nor `$HOME` inside a plist, and a `$HOME` in `StandardOutPath` is an `EX_CONFIG` failure at load with nothing anywhere saying which key was wrong, but launchd does hand a gui-domain agent a `HOME`. So `ProgramArguments` is `/bin/sh -c`, and the shell exports the PATH, exports the socket and opens the log before `exec`ing boswell: the PATH because launchd hands a job almost none and both git's credential helper and boswell's issue reporting shell out to `gh`, the socket because boswell's own commits are signed, and `exec` because the status `KeepAlive` reads has to be boswell's rather than the shell's. `test/boswell-agent.sh` proves all of it against launchd itself, under a label of its own and with a stand-in for boswell, because an assert that only reads the file passes on a plist launchd would refuse.

`install.sh` loads it with `launchctl bootstrap gui/$(id -u)`, behind the same gate the unit has, and boots an already-loaded one out and back in when the plist has changed — `kickstart` restarts the job from the copy launchd read rather than re-reading the file, so a changed plist would otherwise wait for a reboot. The agent being a link, comparing the target across the bootstrap would say nothing — what the link points at is a file a pull already changed — so `install.sh` keeps a copy of the definition the daemon was last loaded from under `~/.local/state/mac-mini-dotfiles/`, written only once the load succeeded, and compares against that. A boswell that fails on every start retries every ten seconds for as long as the Mac is up, `KeepAlive` and `ThrottleInterval` being the whole of what a LaunchAgent can say about it, and the log is the only place that shows.

Five seconds after the last write to a watched tree, a burst of edits lands as one commit, subject `Update a.md, b.md` and a count once there are more than three files. Five seconds rather than a minute because both repositories are read from other devices through GitHub, and the gap between writing and reading is the only thing the interval controls. What it means while editing is in `CLAUDE.md`.

This was a Claude Code `PostToolUse` hook, then a `git-sync@` timer per repository. The hook committed on every write and made syncing something Claude had to remember to do; the timer fixed both but could only fail by leaving a unit failed and firing a second unit off `OnFailure=` to say so. boswell retries the push itself with backoff, rebases when one is rejected, and files the issue from inside the same process.

An agent lives in a GUI login session, so what keeps boswell running with nobody at the Mac is the Mac logging itself in, by auto-login or by whoever unlocks FileVault, plus `pmset autorestart` to bring it back after power loss. Both are `mac-mini-setup`'s to arrange. Until a session exists there is no `gui/<uid>` domain to load the agent into, so an `install.sh` run over SSH against a Mac sitting at its login window says that and leaves the agent unloaded.

A push that cannot land — retries exhausted, or a rebase that conflicts — opens a GitHub issue titled `Auto-sync failed` on that repository, which arrives as email; nobody is reading the journal on a machine with no session attached. An already-open issue suppresses the next report, so an outage files one issue rather than one per attempt, and closing it after the fix is what re-arms the reporting. Two causes cannot report themselves, because filing needs the same network and token the push just failed on: an expired credential and a GitHub outage stay silent.

## Finding the project docs

A `SessionStart` hook, `project-docs.sh`, prints the project's docs folder and its shape — stdout from `SessionStart` goes into the session context, so Claude starts already knowing the docs are there. It looks only at `~/projects/docs/<repo>`, the project's folder in the separate docs repository; a checkout's own `docs/` is the project's published documentation, not its working docs, so it is never read. A project with no folder there produces no output at all.

The repository name comes from the `origin` remote rather than the directory, because a worktree under `~/.herdr/worktrees/<repo>/<branch>` is named for the branch while every branch shares one docs folder. Subdirectories collapse to a name and a count: listing every file put fifty lines into every session on the largest project, and the shape is the part worth injecting.

It prints one line and the file list, and stops there. What a project's docs are and how they are meant to be used differs per project and belongs in its own `README.md`; how the folder syncs, and that they should be read before the work, is the same everywhere and belongs in the global `CLAUDE.md`. A hook that repeated either would be asserting something it has no way to know is still true, and paying tokens on every session start to restate what is already in context.

This replaced nothing — it makes a rule into a mechanism. Symlinking `~/projects/docs/<repo>` into each checkout as `docs/` was the alternative, and it lost: it needs recreating per worktree, ignoring per clone, and it puts the docs somewhere the layout deliberately keeps them out of.

## The project docs

Every project's docs live in `timche/docs`, one folder per project. Putting them under `docs/` in each project's own repository was belay's shape, so that a change and the doc describing it landed in one pull request; belay was dropped on 2026-09-16 and the docs moved back. `timche/docs` decisions records that move.

Project checkouts go in `~/projects`, which `install.sh` creates and `home/.claude/CLAUDE.md` names, so a session knows where to clone without being told. The docs clone is one of them, at `~/projects/docs`, since it is written in every session like any other work; it sat at `~/docs` until 2026-09-26, and moved from the `zoidsh` organisation to `timche` the same day. The two repositories at the top of `$HOME` — this one and the machine's own — stay there deliberately: they are the machine rather than work done on it, `mac-mini-setup` puts them there on every run, and it is public, so its default has no business imposing this layout on someone else's home directory. `DOTFILES_DIR` and `MAC_MINI_SETUP_DIR` move them for anyone who disagrees.

It is private, cloned to `~/projects/docs` by `install.sh` and watched by the same daemon. Plain markdown throughout, cross-linked with relative links rather than wikilinks so it renders wherever it is read.

It was an Obsidian vault on Obsidian Sync until git replaced both, which retired the `ob sync --continuous` daemon, the watchdog timer that restarted it when it went quiet, and the subscription. Read the docs on github.com or the GitHub mobile app — mermaid renders there, and there is nothing to pull.

## Worktree isolation

Several sessions run at once, a worktree each — herdr puts its own at `~/.herdr/worktrees/<repo>/<branch>` and Claude Code puts its own at `<repo>/.claude/worktrees/<name>` — and every name two of them agree on is shared state: one compose project is one database, one host port belongs to whichever session bound it first, one Electron profile is one lock file.

portless is the machine's rather than a project's, `"npm:portless"` in mise's global tool list. Its own README asks for the global install, and the reason is that all three of the things it owns exist once: the proxy holding port 443, the certificate authority signing for every `.localhost` name, and the routes registered in `~/.portless`. mise's npm backend is that global install without `npm install -g`'s flaw: the package lands in mise's own install directory rather than inside whichever node was current, so a node upgrade does not take it along, and the lockfile pins it like any other tool. It is one of the two CLI tools mise carries at all, because it is an npm package that runs on the node mise already provides, where Homebrew's formula would have pulled in a second node of its own. `install.sh` trusts the CA, once: the check is `security verify-cert` against `~/.portless/ca.pem` rather than `portless doctor`, which reports trust in prose with no machine-readable form, and a missing certificate means portless has never run at all. Doing it there is what keeps portless from asking for a sudo password in whichever session starts a dev server first; it reads from `/dev/null` so a sudo that wants a password fails instead of holding an unattended provision open, and it is never fatal — an untrusted CA costs a browser warning, not a shell. Over SSH it is not attempted at all. Adding a certificate to the system trust store puts a confirmation dialog on the Mac's own screen as well as a sudo prompt on the terminal, so the attempt cannot succeed from the far end of an SSH session however the sudo goes — `install.sh` sees `SSH_CONNECTION` set and prints the one line to run in Terminal over Screen Sharing, `portless trust`, rather than failing on a prompt nobody can answer.

Port 443 on `127.0.0.1` is root's on macOS, and a session has no terminal to answer sudo from, so a proxy that is not already running is one the first dev server of a session cannot start: portless exits rather than falling back. The proxy therefore runs as portless's own LaunchDaemon, `sh.portless.proxy`, installed once by hand with `sudo portless service install` and the TLD list below. It is the one root daemon this repo depends on and does not own, because launchd will not take a system job from a symlink in a user's checkout. The daemon records the absolute paths of the node and the portless that installed it, both under mise's versioned install directories, so a `mise up` of either leaves it pointing at a file that is gone; `install.sh` reads both paths back and prints the reinstall command when one is missing.

Dev servers open from Tim's MacBook under a name of their own, `https://<branch>.<app>.timche.dev`, the same shape as the `.localhost` one. `.zshenv` sets `PORTLESS_TLD=localhost,timche.dev`, so the one proxy serves every route under both; `.localhost` stays first because `PORTLESS_URL` takes the first TLD. A wildcard record at Cloudflare points `*.timche.dev` at this Mac's tailnet address, DNS only, which is harmless in public DNS since nothing off the tailnet can reach it. Resolvers with DNS-rebinding protection, NextDNS among them, return nothing for a public name pointing at such an address, so the tailnet's DNS settings route `timche.dev` to Cloudflare's `1.1.1.1` as split DNS, and a tailnet device gets the answer on any network. And and `install.sh` has Tailscale Serve hand the tailnet's port 443 to the proxy as raw TCP. portless keeps TLS and routes by hostname, and the MacBook trusts portless's CA once.

A name per app rather than a port per app is the point of the arrangement. Browsers key cookies by host and ignore the port, so portless's own `--tailscale`, which puts every app on the Mac's own `<node>.<tailnet>.ts.net` name and a port each, gives every dev server one cookie jar on the MacBook: two worktrees of one app overwrite each other's session, and an app inherits the localStorage and service worker of whichever app held its port last. Nothing Tailscale offers gives a name per app today. Wildcard MagicDNS is in the client since about 1.96 but not enabled on the coordination server for any tailnet ([tailscale/tailscale#1196](https://github.com/tailscale/tailscale/issues/1196)), Tailscale Services stop at ten per user, and a tsnet node per dev server makes every worktree a device. Wildcard MagicDNS landing would not retire this on its own either: Tailscale's wildcard certificate covers `*.<node>` one label deep, and a worktree's name is two. A Let's Encrypt certificate was the other way to spare the MacBook the CA and lost twice: portless serves a `--cert` for every hostname, `.localhost` included, and a worktree's name is two labels below the domain, which one wildcard does not cover, so each project would need a certificate of its own and would be named in the public certificate logs.

`COMPOSE_PROJECT_NAME` is the one that bites with nothing configured at all, so `.zshenv` sets it. Compose names a project after the directory it was started from, which for a herdr worktree is the branch alone — so `fix-ui` in two repositories would share one project, and with it one set of containers, one network and one database volume. `<repo>-<branch>` instead: the repository from the `origin` remote, for the same reason `project-docs.sh` takes it from there, the branch from `HEAD` or the worktree folder when HEAD is detached, lowercased with everything outside `[a-z0-9_-]` replaced, which is compose's own rule for a name it would otherwise reject. Recomputed on every `chpwd`, because a shell outlives the directory it started in, and unset outside a work tree, which leaves compose on its own default. In `.zshenv` rather than `.zshrc` so that a script, a hook and an agent get the same name a prompt does.

`worktree-info.sh`, a second `SessionStart` hook beside `project-docs.sh`, prints the names the worktree owns and nothing else: the compose project, with the reminder that `docker compose down -v` takes the database with it and that a fixed host port in a compose file is a collision waiting for the second session; portless's URL, `https://<branch>.<name>.localhost` in a linked worktree and `https://<name>.localhost` in the main one, which is portless's own rule; and, for an Electron project, that a dev run takes a user-data profile named for the branch and a debug port of `0`, read back from `DevToolsActivePort` in that profile. Each line only when the project has the thing — a compose file at the worktree root, a `package.json` script that runs portless, `electron` among its dependencies — and a project with none of the three prints nothing at all, as `project-docs.sh` does without a docs folder.

Where the name cannot be derived with certainty it is not guessed at: portless resolves a workspace root against the packages under it and rewrites a branch name that is not a hostname label, so for either the hook says that `portless list` prints the URL rather than printing a URL that might not exist.

It reads the worktree from the `cwd` field of its own stdin. `CLAUDE_PROJECT_DIR` is documented as staying at the directory the session started in rather than following it into a worktree, and `CLAUDE_ENV_FILE`, the other way to hand a session a name, is unreliable across `/clear`.

Cleaning up after a removed worktree is a garbage collector, `~/.local/bin/worktree-gc`, rather than a hook on the removal, because no removal reliably hands us one: herdr removes worktrees, so does Claude Code, so does `git worktree remove` by hand, git has no post-remove hook at all, and Claude Code's `WorktreeRemove` does not fire for a git worktree ([anthropics/claude-code#94212](https://github.com/anthropics/claude-code/issues/94212), open). Registering `WorktreeCreate` and `WorktreeRemove` would be the wrong trade even if it did: the docs say configuring them *replaces* Claude Code's own `git worktree` behaviour rather than adding to it, so a hook that cleaned up would also own creating the worktree.

So the trigger is time. It sweeps two things, both only when what they belong to is gone: a compose project whose `com.docker.compose.project.working_dir` label points inside a worktree root that no longer exists, with `com.docker.compose.project.config_files` as the second opinion, removed with `docker compose -p <name> down -v --remove-orphans`; and a process whose current directory is a removed worktree, SIGTERM and then SIGKILL ten seconds later. The label rather than `docker compose ls`, which reports the config files alone and so misses a project started with `-f` or from a file named something else. Inside a worktree root — `~/.herdr/worktrees/<repo>/<branch>` or `<repo>/.claude/worktrees/<name>` — and nowhere else, which is not the same as a folder that does not exist: a daemon shared with another machine reports projects whose working directory was never on this disk, and `down -v` on one of those deletes a database over a path that only looks missing. It never touches pid 1 or its own process tree, holds a `mkdir` lock so a manual run and the agent cannot overlap, takes over a lock older than the interval, and answers `--dry-run` by printing what it would do.

A LaunchAgent runs it, `io.github.timche.worktree-gc`, with `RunAtLoad` for what the machine lost while it was off, `StartInterval` 600 and `WatchPaths` on `~/.herdr/worktrees`. The interval is what does most of the work: `WatchPaths` is not recursive, so a worktree removed one level down is invisible to it, and Claude Code's worktree roots are per repository, which no single path watches. `install.sh` creates that root if herdr has not yet, because launchd wants a watched path to exist at load, and loads or reloads the agent as it does boswell's. This is the one plist still rendered from a mise template: launchd reads `WatchPaths` itself rather than handing it to a shell that could expand a `$HOME`, so that one path has to arrive absolute — which also means the file itself changes under the bootstrap, and `install.sh` can compare it against the copy it took aside beforehand rather than keeping one of its own. The log is `~/Library/Logs/worktree-gc.log`, and a sweep that finds nothing writes nothing to it.

## The herdr server

Every Claude Code session is a pane of one herdr server, and that server is started by launchd inside the logged-in GUI session, `io.github.timche.herdr`, rather than by the first `herdr` a remote client runs. Tim attaches from the MacBook with `herdr --remote`, which runs `herdr remote-client-bridge` over Tailscale SSH and only talks to the server's socket, so where the server was started decides what its panes are. Started over SSH, every pane carries `SSH_CONNECTION`, `SSH_CLIENT` and `SSH_TTY`, and Claude Code withholds computer use from such a session: the built-in `computer-use` server is missing from `/mcp` although every documented condition holds, and appears once those three variables are unset. Anthropic documents none of this — not in the computer use docs, the changelog or the safety guide — and the strings in the binary put the three names among its terminal-identity checks, beside "[computer-use] terminal not detected; falling back to sentinel host", which fits computer use needing to find the terminal it keeps out of its screenshots. Unsetting the variables for `claude` was tried and dropped: it depends on a check nobody has promised to keep, and it hides a remote terminal rather than making the session local. From launchd, a pane is genuinely a local session, and macOS asks its privacy grants of herdr rather than of `tailscaled`.

`KeepAlive` is unconditional, unlike boswell's, because a missing server — even one stopped on purpose — would otherwise be replaced by the next remote client starting its own over SSH. `install.sh` loads the agent but never boots it out or restarts it: it usually runs inside one of that server's panes, and restarting the server ends every session on it, the one running the script included. A server already running outside launchd, or a plist changed since launchd read it, is reported with the commands to run from a shell outside herdr, a plain `ssh timche@mac-mini`, once no session needs the server. The log is `~/Library/Logs/herdr.log`.

What stays unsolved either way is approval: computer use asks for each app once per session, in the session's own terminal, and nothing pre-approves apps for a session nobody is watching ([anthropics/claude-code#47796](https://github.com/anthropics/claude-code/issues/47796), closed without it).

## Signed macOS builds

A signed test build of an Electron app runs through `~/.local/bin/with-apple-signing`, which resolves Tim's Apple Developer ID certificate out of 1Password into the environment of that one build:

```sh
with-apple-signing bun run build:mac
```

The certificate never lands on disk and never enters a keychain of Tim's. electron-builder reads a base64 `.p12` from `CSC_LINK` and its password from `CSC_KEY_PASSWORD`, builds a temporary keychain of its own and deletes it when the build ends, so a `.p12` in `~/Downloads` and a `security import` into the login keychain buy nothing that a secret reference does not — and the same trade is already how this machine signs commits, with the private half of the SSH key living in an agent and nowhere else. `~/.config/op/apple-signing.env` holds the two references rather than two values, which is why it is tracked here like any other config: `op://Mac Mini/Apple Developer ID Application Certificate/base64` and `.../password`, quoted because the vault name has a space in it. `APPLE_SIGNING_ENV_FILE` points the wrapper at another file when an item or a field label moves.

The service-account token is the one `mac-mini-setup`'s `claude/signing-key.sh` stored at `~/.config/op/service-account-token`, read into the environment of `op run` alone — not exported by the wrapper, and taken back off with `env -u` before the build is exec'd, so a build script cannot read the vault it was signed from. Masking stays on: electron-builder is verbose, and `op run` replaces a resolved value with a placeholder wherever the build prints one.

One signed build at a time. electron-builder adds its temporary keychain to the login keychain's search list and restores the list afterwards, so two builds overlapping can each put back a list the other had already changed and lose the login keychain from it. The wrapper therefore takes a `mkdir` lock in the cache directory, says out loud that it is waiting rather than looking hung, releases it on exit or interrupt, and takes over a lock whose holding process is gone — by pid, since a build that was killed outright reached no trap and would otherwise block every build after it.

Check the reference once on the Mac, with the token in the environment for that one command:

```sh
OP_SERVICE_ACCOUNT_TOKEN="$(cat ~/.config/op/service-account-token)" \
  op read "op://Mac Mini/Apple Developer ID Application Certificate/password" >/dev/null && echo ok
```

`APPLE_TEAM_ID` is in the same file as a plain value, `SUR5MVXA4C`, because a team ID is public — every signed app carries it. Meru's `beforePack` hook only writes the Touch ID keychain entitlement when it is set.

What one app alone needs is in a file of its own, `~/.config/op/apple-signing.d/<repo>.env`, which the wrapper adds to `op run` when the checkout's `origin` remote names that repository — the remote rather than the directory, for the reason `project-docs.sh` gives, so every worktree of it gets the file. Meru's holds `APPLE_PROVISIONING_PROFILE`, a reference to its provisioning profile in the Meru vault, under the name meru's release workflow gives the same base64. electron-builder wants the profile as a file, and a project keeps that file out of git, so a fresh checkout has none: inside `op run`, where the resolved value exists, the wrapper decodes it to the path `package.json`'s `build.mac.provisioningProfile` names, only when nothing is there yet. The profile is not a secret — every signed Meru.app carries a copy — so the file is left in place for the next build. The service account reads both the Mac Mini and the Meru vault; 1Password cannot add a vault to a service account after it is made, so one that needs another vault is a new service account and a new token.

Notarisation is two more references in the same file, `APPLE_ID` and `APPLE_APP_SPECIFIC_PASSWORD`, both from the Mac Mini vault's "Apple App Specific Password" item, since the password belongs to that Apple ID; the team ID is a field of the certificate item instead, because it names the certificate's team. electron-builder notarises a signed macOS build whenever the three are set, and nothing about the wrapper changed.

## Preferences

Tim's own preferences, split by what mise's declarative `[bootstrap.macos.defaults]` can say: a plain user default is declared in `mise.toml` and written by the same `mise bootstrap` that applies the dotfiles, and everything else is in `install.sh`.

| Preference | Applied by | |
| --- | --- | --- |
| Tap to click | `mise.toml` | `[bootstrap.macos.trackpad]`'s `tap_to_click`, one friendly key for both `Clicking` domains, plus the per-machine `com.apple.mouse.tapBehavior` as an explicit `host = "current"` entry |
| Finder: hidden files, every extension, status bar, path bar, no extension-change warning | `mise.toml` | `[bootstrap.macos.finder]` covers four of the five; `AppleShowAllExtensions` is global rather than Finder's own and has no friendly key, so it is a raw entry |
| Screenshots in `~/Pictures` | `install.sh` | The value has to be an absolute path — neither mise nor the screenshot service expands a `~`, and mise does not template a defaults value — so declaring it would mean spelling the account name out, which nothing here does |
| `~/Library` visible in Finder | `install.sh` | A flag on the folder, and a Finder info xattr beside it, rather than a preference at all |
| A Dock with none of the icons macOS ships with | `install.sh` | Once, recorded under `~/.local/state/mac-mini-dotfiles` |

Every one of them reads before it writes, and nothing restarts an application that had no reason to notice. That last part is why the restarts are `install.sh`'s rather than a `post-defaults` hook: mise deliberately kills no application, and a hook there would relaunch Finder on every install whether a preference changed or not. So `install.sh` takes the whole `com.apple.finder` domain aside before the bootstrap — the domain rather than the four keys, which is one fewer list to keep in step with `mise.toml` — and relaunches Finder only if the bootstrap or the `~/Library` flag moved something. The screenshot location relaunches `SystemUIServer` and `screencaptureui` both: every guide still names the first, and the process actually found holding the stale copy since Monterey is the second.

The Dock is emptied once and then left alone. mise could own `persistent-apps` with `apps = []`, but from then on every install would wipe whatever Tim had pinned since the last one, and what he asked for was the factory set gone rather than a Dock that refuses to hold anything. A marker file is what says it has been done, and nothing reads `persistent-apps` again afterwards.

One of them is believed rather than proven. Tap to click's two `Clicking` domains look to be a mirror System Settings keeps rather than what the input stack reads — forcing them to 0 and the per-machine key to 1 still taps to click — so the `host = "current"` entry is the one doing the work and the other two are there because System Settings shows them. Tap to click at the login screen was tried and dropped: its key has been reported ignored by the login window for several releases, and nothing found says it works on macOS 26.

CI reads every one of these back on the `macos-latest` runner and asserts that a second pass finds nothing to write, which is what `mise bootstrap macos defaults status --missing` answers.

## A macOS runner of last resort

Built and kept switched off. It answers a queued GitHub Actions job with a macOS VM cloned for that one job and deleted when it ends, and it exists for the day a *private* repository runs out of macOS minutes — a private repository's count tenfold against the free allowance, and this one spent September 2026's 300 on 247 auto-sync commits before the paths filter went on the workflow. Nothing uses it today. This repository is public, so its own CI is GitHub's hosted runners and free, and a self-hosted runner must never serve a public repository: anyone who can open a pull request can then run code on the Mac it is attached to.

Running such jobs on the Mac's own account was never on the table either — `install.sh` rewrites the account it runs as, and that account is Tim's. A VM per job is the answer, and it is the better one on its own merits: every job starts on a macOS install nothing has ever touched, which is exactly the claim this suite makes.

Tart runs the VM, installed from the Brewfile as `openai/tools/tart`. It has no GitHub Actions integration of its own to lean on. Its documentation points at Cirrus Runners, a hosted service at $150 a month per concurrent runner and closed to new customers since the project moved to OpenAI; its one real CI executor is GitLab's, and Orchard is a cluster orchestrator with no Actions support either. So the dispatching is this repository's, in `~/.local/bin/tart-runner`.

**How a job gets answered.** `io.github.timche.tart-runner`, a LaunchAgent in the GUI session, keeps `tart-runner` polling. Every 45 seconds it asks each repository in `~/.config/tart-runner/repos` for its queued and in-progress runs and then those runs' jobs, because GitHub has no endpoint that lists a repository's queued jobs directly. A job whose labels are all labels this Mac registers with — `self-hosted`, `macOS`, `tart` — is one to answer. It then clones `gha-runner-base`, sets the clone to 4 CPUs and 8 GB, boots it with `--no-graphics --net-softnet`, mints a just-in-time runner config with `POST …/actions/runners/generate-jitconfig`, and starts `./run.sh --jitconfig` in the guest over SSH. A JIT runner is ephemeral by construction: it takes exactly one job, exits, and removes its own registration. When it exits, the VM is stopped and deleted.

The config is the only credential the VM is ever handed, and it never reaches an argument list on this Mac — it goes to the guest on stdin. Nothing on this disk holds a registration token, and a VM destroyed mid-job leaves no runner sitting offline in the repository's settings.

**The token it polls with.** Not the account's `gh` login. That is an OAuth token with the `repo` scope, which is every private repository Tim has, and handing it to the thing that runs whatever a workflow file asks for is the wrong way round. Instead each owner gets a fine-grained personal access token of its own, scoped to the repositories in the list and to two permissions:

| Permission | Level | What it is for |
| --- | --- | --- |
| Administration | write | `POST /repos/{owner}/{repo}/actions/runners/generate-jitconfig`, `GET …/actions/runners`, `DELETE …/actions/runners/{id}` — minting the per-job registration and removing one the runner failed to remove itself |
| Actions | read | `GET /repos/{owner}/{repo}/actions/runs` and `GET …/actions/runs/{id}/jobs` — the poll |
| Metadata | read | mandatory alongside any repository permission; GitHub's token form selects it for you |

Those are the levels GitHub's [permissions required for fine-grained personal access tokens](https://docs.github.com/en/rest/authentication/permissions-required-for-fine-grained-personal-access-tokens) lists for exactly those five endpoints. Nothing gives read access to a repository's *contents*, which is what a fine-grained token's ordinary `Contents: read` would be, because the dispatcher never clones anything — the job does that, inside the VM, with the token GitHub mints for the run.

One token per **owner**, because a fine-grained token is "limited to access resources owned by a single user or organization": repositories under `timche` and under an organization need one each. `~/.config/op/tart-runner.env` holds one `op://` reference per owner, named `TART_RUNNER_TOKEN_<OWNER>` with the owner uppercased and anything but a letter or a digit turned into an underscore, and the owner half of a line in the repository list picks the one to use. The reference is resolved from 1Password at the moment it is first needed, with the service-account token in `op`'s environment and nowhere else, into a shell variable that is set for a single `gh` call rather than exported — so it is not in the dispatcher's environment, not in a file, not in an argument list, and not in anything the VM can see. If a reference is missing or will not resolve, the dispatcher says so and stops. It never falls back.

**What the VM can reach.** Tart's default is NAT, where a guest has the run of whatever the host does: this Mac's own services, the LAN, and the tailnet. `--net-softnet` puts [Softnet](https://github.com/cirruslabs/softnet), a userspace packet filter, between the VM and the bridge instead. A VM behind it may send only from its own MAC and its own DHCP address, and only to globally routable IPv4 addresses — so GitHub, Homebrew and everything else a job checks out or installs still works, while `192.168.178.0/24` and every other private range is gone. The tailnet is `100.64.0.0/10`, which is carrier-grade NAT rather than a private range and so not obviously covered by a rule phrased the other way round, so the dispatcher names it in `--net-softnet-block` rather than trusting the inference.

What stays reachable is the vmnet bridge's gateway address, which is this Mac's own address on that bridge and also the VM's DNS server — blocking it would leave the guest unable to resolve anything. So a service bound to `0.0.0.0` on this Mac is still reachable from a job. Nothing here binds one today (portless is on loopback, Tailscale on the tailnet address), and the way to close it for good would be a guest pointed at a public resolver and the gateway blocked too.

Softnet runs as root. Tart sets the setuid bit for it the first time `--net-softnet` is used **from a terminal**, by asking for a sudo password — a LaunchAgent has nowhere to ask, so on this Mac it is one command, once:

```sh
sudo chown root /opt/homebrew/Cellar/softnet/*/bin/softnet
sudo chmod u+s /opt/homebrew/Cellar/softnet/*/bin/softnet
```

That is the Cellar path rather than the `/opt/homebrew/bin/softnet` symlink, and it is what Tart itself runs; a `brew upgrade` that moves softnet to a new version directory undoes it, and the dispatcher will say so. Until it is done the dispatcher refuses to start a VM and logs the exact command rather than quietly falling back to NAT — `tart run` would fail anyway, with `root privileges are required to run and passwordless sudo was not available` in the VM log.

**What it refuses to do.** One VM at a time, and none at all while nothing is queued: 8 GB of a 32 GB Mac is not something to hold overnight for a repository that sees a handful of pushes a week. It also waits while UTM has a guest running, since the Windows VM holds 8 GB of its own and two guests plus a working machine is how everything starts swapping. Apple's licence caps a host at two macOS guests in any case, and the Virtualization framework enforces it, so the headroom above one was never large. A dispatcher that was killed outright leaves a VM behind, which the next one sweeps by name at startup; the lock is `with-apple-signing`'s, taken over by pid when its holder is gone.

The cost of all this is that a run waits for the Mac. Nothing answers a queued job while the Mac is off or already running one, and a job that queues at night sits there until morning.

**The image.** `gha-runner-base` is `ghcr.io/cirruslabs/macos-golden-gate-base` — macOS 27, the release this Mac runs — plus what a GitHub-hosted macOS image has and a bare Mac does not: the command line tools, Homebrew, `gh` and `jq`, and the actions runner itself. The Brewfile is deliberately *not* baked in. `brew bundle` doing its work inside the job is a third of what the suite proves, and an image that arrived with the packages would only ever prove the re-run.

Building or refreshing it is one command, which is idempotent and replaces whatever is there:

```sh
tart-runner --build-image
```

It generates `~/.ssh/tart-runner` if there is none, clones the base image, seeds the public half into the guest through Tart's guest agent — `tart exec`, because the base image answers SSH only for the password it ships with and there is no `sshpass` on this Mac — then installs the tools over SSH and shuts the VM down. Refresh it when the actions runner release the job needs has moved far enough that the runner refuses to start, when Homebrew's bootstrap changes, or when a new macOS base image is worth moving to; nothing about it expires on a schedule. The first build pulls tens of gigabytes, at about ten minutes per 5 GB on this connection.

**Switching it on for a repository.** The list at `~/.config/tart-runner/repos` ships empty and the LaunchAgent ships unloaded, which is what "off" means here. To point it at a private repository:

1. In 1Password, in the `Mac Mini` vault the service account can read, make an item named `GitHub - tart-runner <owner>` with a field called `token`. It holds a fine-grained personal access token whose **resource owner** is that owner, whose **repository access** is *Only select repositories* — the ones in the list and no others — and whose permissions are the three in the table above. Give it the shortest expiry you are willing to renew; a runner that stops answering because a token expired says so in its log. If the owner is an organization (`zoidsh`, `repeekgg`), the organization has to allow fine-grained tokens at all, under Settings → Personal access tokens → Fine-grained tokens, and by default an owner must approve each one a member creates.
2. Add the reference to `home/.config/op/tart-runner.env` as `TART_RUNNER_TOKEN_<OWNER>="op://Mac Mini/GitHub - tart-runner <owner>/token"`, a reference and never a value.
3. Add the `owner/name` as a line in `home/.config/tart-runner/repos`, and put `runs-on: [self-hosted, macOS, tart]` in that repository's workflow.
4. Give softnet the setuid bit if it has none, build the image if there is none, and load the agent.

```sh
sudo chown root /opt/homebrew/Cellar/softnet/*/bin/softnet
sudo chmod u+s /opt/homebrew/Cellar/softnet/*/bin/softnet
tart-runner --build-image
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/io.github.timche.tart-runner.plist
```

`launchctl bootout gui/$(id -u)/io.github.timche.tart-runner` switches it off again, and emptying the list stops it answering a repository without stopping the agent. `tart-runner --dry-run` says what it can see queued without starting anything, and `tart-runner --once` takes a single job in the foreground, which is the way to watch one go through. Only ever a private repository.

## Testing

CI, on a `macos-latest` runner: there is no macOS container to put any of this in, and the suite changes the machine it runs on. It runs `install.sh` and then `test/assert.sh` twice over — the repeat is what keeps the whole of it honest about being safe to re-run, which is the only way a change ever reaches a Mac that is already built. The runner's account is called `runner`, which is what proves nothing here assumes an account name. Hosted runners are free to a public repository, which is the reason this one is public; the Tart runner above is what a private repository would fall back to.

A local run is `./install.sh && test/assert.sh` on the Mac itself, which is the machine the suite is for and the quick check after each change. What it cannot answer is the promise the suite actually makes, since it runs against an account that is already built: `test/tart.sh` does that, in a Tart VM cloned for the run and deleted after it. It takes the working tree as it stands, uncommitted changes and untracked files included, copies it in, and runs exactly the five steps the workflow runs, so a change can be tried on a fresh Mac before it is pushed. `--keep` leaves the VM up when something fails. It wants the image `tart-runner --build-image` makes, and refuses for the same reasons the dispatcher does — a Windows guest up, another Tart VM running, a run already going.

Until 2026-09-26 there was a Debian container to stand in for a Mac; it went with the VM, and until `test/tart.sh` a change was pushed and read in CI instead.

What the runner cannot reach: its token cannot read `timche/docs`, so the docs clone fails, boswell stays unloaded, and the asserts check the wiring rather than a running daemon. What it does have is the console login an agent needs, which is what `test/boswell-agent.sh` uses: it loads boswell's plist under a label of its own with a stand-in for boswell, and proves the three things only launchd can answer — that a symlinked plist is accepted, that the `HOME` it hands the job is what the shell expands, and that the status reaching `KeepAlive` is the program's.

The asserts for `tart-runner` read its wiring and never start a VM. They have to pass on a hosted runner, which has no Tart and no image, and they would have to pass inside a Tart VM too, which has Tart from the Brewfile and still no image — nesting a macOS guest is not something the hypervisor offers.
