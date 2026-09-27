# CLAUDE.md

The personal half of the Mac mini Claude runs on; the machine itself is the public `mac-mini-setup`, where the Claude Code half is its `claude.sh` overlay. macOS only, since 2026-09-26: the Debian VM this also ran on is being retired, and `install.sh` and `test/assert.sh` each refuse another `uname` rather than doing half a job on it. README has the layout, the split between the two repos and the reasoning behind all of it — read it before changing structure.

Two files here are named CLAUDE.md. This one is guidance for working in the repo. `home/.claude/CLAUDE.md` is the global instruction file being version-controlled — content, not guidance. Do not merge them.

## Edits here publish themselves

The boswell daemon commits and pushes anything dirty in this repo five seconds after the last write, through the `$HOME` and `~/.claude` symlinks too since git sees the realpaths. Nothing about it is yours to trigger: never offer to commit or push here, and never run `boswell once` to hurry it along. It watches the project docs the same way; see README.

Five seconds is short, so a half-finished edit can be upstream before the next write finishes it, and what lands is main with no review step. Make an edit whole in one write where it matters.

Commit and push to main yourself when something must land now, no branch and no PR. Standing permission, and an exception to the global rules on branching and asking before a push.

A sync that cannot push opens a GitHub issue titled `Auto-sync failed`, on whichever repository failed. One already open is what stops the next attempt filing a second, so close it once the push lands — an open one means the next outage goes unreported.

## Rules

- Editing the main checkout is fine here, an exception to the global worktree rule: boswell publishes main, and a worktree would only delay that.
- Nothing under `home/` spells a home directory out: the account is `timche` on the Mac and `runner` under CI, and `assert.sh` fails on a `/Users/<name>` or `/home/<name>` anywhere under it. Hook, statusLine and service paths go through `$HOME`, a mise template or a `~` the reading program expands itself.
- `.claude` stays under `home/`; at the repo root it would load as this repo's own project configuration.
- `home/.claude/CLAUDE.md` is loaded whole into every session, and adherence drops past about 200 lines of ordinary markdown. `assert.sh` enforces that budget in words, about 2,400, since a paragraph here is one line however long.
- mise is a version manager for language runtimes and nothing else: `node`, `bun` and `python` in `home/.config/mise/config.toml`, and `go` or `rust` in the `mise.toml` of the one project that wants them, which is why nothing global installs either. Claude Code comes from Anthropic's installer into `~/.local/bin` and updates itself on the default channel, because pinning it means turning its updater off and then owning what the updater was doing. mise comes from its own installer into the same place, `curl -fsSL https://mise.run | sh` behind one guard on that path existing, for the reason mise's install page gives: Homebrew builds it separately, the formula trails upstream, and only a standalone install supports `mise self-update`. Those two are the exceptions to a CLI tool being Homebrew's, and they are the same exception — the authors ship the installer. `~/.local/bin` sits ahead of Homebrew's prefix in `.zshenv` and `.bashrc` so a `brew install mise` cannot shadow it. portless is the one exception: an npm package, `"npm:portless"` in that same tool list, installed by mise's npm backend and run on mise's node, where Homebrew's formula would bring a second node. ccstatusline is the other npm package, `"npm:ccstatusline"` in the same list, which the status line runs through its shim — installed once, because `npx` paid for npm starting node twice on every draw. Every other tool is Homebrew's, a line in the `Brewfile` at the repo root that `install.sh` applies with `brew bundle --no-upgrade` — boswell included, through `tap "timche/tap"`, which is the one thing mise's bootstrap could not do: it fetches a tap's own `api/formula/<name>.json`, a plain tap publishes none, and it refuses to proxy to the brew CLI. `--no-upgrade` so a re-run installs what is missing and moves no version, and `brew bundle check --no-upgrade` in `assert.sh` is what keeps the file and the Mac in step. `mise.toml` has no packages phase at all any more, and no entry anywhere carries an `os` selector — there is one OS.
- `mac-mini-setup` installs only what has to exist before `install.sh` can be reached — `gh` and the token it carries, `git` from the Xcode command line tools, `curl`, `jq` — plus the machine itself: Homebrew, Xcode, tailscaled as a system daemon serving Tailscale SSH, docker as OrbStack, and the login session a LaunchAgent needs. Everything after the handover is this repo's, Homebrew packages included. `gh` is the one tool that could not move even if it were only ours: boswell calls `gh auth token` for the issue it files, and a LaunchAgent has no mise shims on its PATH.
## Testing

`./install.sh` then `test/assert.sh`, on the Mac. The `macos` job in CI runs that pair twice on a `macos-latest` runner, which is what proves it is safe to re-run, and it runs on every branch push — so a change is pushed and read there rather than reasoned about. There is no container to test in: there is no macOS container, and the suite changes the machine it runs on.

`~/.local/bin/tart-runner` is a self-hosted runner for a private repository that runs out of macOS minutes, built 2026-09-27 and deliberately switched off: empty repository list, and a LaunchAgent whose plist is neither linked into `~/Library/LaunchAgents` nor loaded — the link is the switch, made by hand rather than by `mise.toml`, and `install.sh` reads it. Never point it at a public repository. README has the rest.

`test/run.sh` and its Debian container were removed with the VM on 2026-09-26.
