# CLAUDE.md

The Mac mini Claude Code runs on and controls, machine and account in one repository: shell scripts, one `Brewfile`, one `mise.toml`, the `home/` tree linked into `$HOME`, and a folder at the root per tool — the Go ones that run on the Mac, `hachiko/` and `mochi/`, and the Cloudflare Worker that deliberately does not, `shibuya/`. macOS and Apple Silicon only, refused up front rather than half-done. Public, which is what makes CI free, and it holds no secret: those are 1Password references, and the tokens are files `.gitignore` never sees.

Two files here are named CLAUDE.md. This one is guidance for working in the repo. `home/.claude/CLAUDE.md` is the global instruction file being version-controlled — content, not guidance. Do not merge them.

## Where things are written

- A script's header comment: why it exists, what it does and what would break if it ran elsewhere in the order. `bootstrap.sh`, `machine.sh` and `claude.sh` are the entry points and name the order.
- `README.md`: the shape of the whole and the reasoning that spans more than one script, plus what only a person can do. Read it before changing structure.
- `<tool>/README.md`: one tool — what it is, its commands, setup, state and logs, how it fails. Its tests are the specification; the README is not a prose copy of them.
- `$PROJECT_DOCS_DIR/mac-mini/decisions.md`: why something is the way it is and when it changed, by topic. The other files there are procedures and status, and can go stale against this repo; correct them when you touch the same subject.

A change updates the one place whose text it makes wrong: a tool's behaviour its README, a script's its header comment, a decision `decisions.md`. Writing the same thing into two of them, or a changelog into a README, is how a README grows past what anyone maintains. Counts, flags and wordings a test already pins stay out of the prose.

## Edits here land on main

The session that changes this repo commits and pushes to main itself, once the change is whole: no branch and no PR, one commit per logical change. Standing permission, an exception to the global rules on branching, worktrees and asking before a push.

An edit is live before it is committed: `$HOME` and `~/.claude` are symlinks into the checkout, so an edit there is what the next session reads, and a tool's wrapper builds from the working tree, so a broken edit to `hachiko/` is what the next agent run tries to build within minutes. Make an edit whole in one write where it matters.

`hachiko sync` commits and pushes `home/.claude/` and nothing else here, two minutes after the last write to it, since Tim, his tools and Claude Code write it through the symlink. It is not a session's to commit or to trigger, nor Tim's to be asked to; `git status` finding it uncommitted means it was written in the last couple of minutes, and staging part of a file there holds nothing back. Your own commits in this checkout go up underneath sync's next push, so a commit left here is on its way to main. A worktree is all yours, since sync reads only this checkout's tree.

## Rules

- Everything checks before it changes. This Mac is re-run in place over the very sshd and tailnet a step here would touch, so a step that installs a second tailscale, rewrites a working sshd or reboots a machine somebody is working on is a regression even when it is idempotent.
- A re-run moves no version: `brew bundle --no-upgrade` and `mise install --locked`. `hachiko update` is the one thing that moves versions, once a night, so an upgrade step belongs there and nowhere else.
- Nothing is written for one account or one machine: the account is `timche` on the Mac and `runner` in CI, and the Mac's name is never a literal. Paths go through `$HOME`, a mise template or a `~` the reading program expands, the account record through `dscl` (macOS has no `getent`). `test/assert-machine.sh` and `test/assert.sh` fail on a literal `/Users/<name>` or `/home/<name>`.
- `/bin/bash` is 3.2, which every script runs under: no associative arrays, and no reading an empty array under `set -u`.
- Prompts belong in a script that has checked for a terminal. Under the curl install stdin is the pipe feeding `bootstrap.sh`, which hands each phase its terminal by device name rather than as `/dev/tty`, which Bun cannot poll. A step that would prompt without one says what it skipped, or an unattended provision hangs.
- `.claude` stays under `home/`; at the root it would load as this repo's own project configuration.
- launchd expands neither `~` nor `$HOME` in a plist, and starts an agent in `/`, but hands a gui-domain agent the account's `HOME`. So a plist holds no absolute path and is a plain symlink from `[dotfiles]`: `/bin/sh -c` works out PATH, log and binary from `$HOME`, and every `mise` call names its directory (`-C <repo>` or a `cd`), since from `/` nothing this repo declares is active. launchd reads a plist only at bootstrap, so a changed plist needs `bootout` and `bootstrap` and a changed wrapper `kickstart -k`; the installers keep a copy or hash of what was loaded to notice. The sshd drop-in is a root-owned copy, never a link the account could rewrite.
- `/usr/local/libexec/claude-root` is the only command a session runs under sudo without Tim, so an action added to it is an action every session has. It takes no path, command, signal or pid from its caller, matches by string, refuses before anything privileged runs, writes one `logger -t claude-root` line first (sudo logs nothing of it), and touches no path the account can write, even one it spells out itself — a log-capping action under `~/Library/Logs` once made it passwordless sudo for everything. `reinstall-portless` is the one exception and nothing may follow it; `test/assert-machine.sh` holds every other action to the rule. Its two files are copies `root-helper.sh` installs only when Tim runs it.
- A private key from 1Password is one file in `~/.ssh` (600, in a 700 directory), `id_signing` or `id_vm`, written only by `claude/ssh-key.sh` from `op read` down a pipe into a temp file under `umask 077` and a rename — never printed, traced, passed as an argument or held in a variable. The service-account token is handled the same way.
- `launchd/herdr-launcher.c` is what macOS holds responsible for every session's privacy grants, pinned to its exact bytes, so every edit costs Tim the grants again by hand on the Mac's own screen. It changes only for a reason worth that, is compiled by `install.sh` only when its source hash moves, never by a wrapper, and stays herdr's parent: forwards `SIGTERM`, `SIGINT` and `SIGHUP` and exits as herdr exited.
- A compiled tool is Go, `CGO_ENABLED=0`, standard library only — it runs with the account's whole reach (`gh`, 1Password, the signing key), so a third-party module is supply chain with that reach. Settled; the exception is what is too large or risky to own, such as a database engine or cryptography beyond the standard library's, pinned in `go.mod`/`go.sum`, bumped only on purpose and covered by `govulncheck`. `main` at the folder root holds only argument parsing and dispatch; each command and each shared concern is a package under `internal/`. Shared code is a library folder at the root with no `main`, such as `discord/`, required and replaced with `../discord`. The wrapper in `home/.local/bin/<tool>` rebuilds into `~/Library/Caches/<tool>` when the source hash moves and keeps the last binary that built and passed `go test ./...`, so those tests are the only gate between an edit and the running agents and may depend on nothing but a `HOME` and a PATH with git.
- `shibuya/` is deployed by `shibuya/deploy.sh` and nothing else, and never to try something out: there is one Worker and it is the live dead man's switch for hachiko.
- mise is for language runtimes: `node`, `bun` and `python` in `home/.config/mise/config.toml` for every project, and a runtime this repo needs (`go`) in its root `mise.toml`. npm-packaged CLIs and Chrome for Testing are there too. Claude Code, mise and herdr come from their own installers into `~/.local/bin`, ahead of Homebrew on PATH, because each updates itself. Every other tool is one `Brewfile` line; a script that wants a package checks for it and reports what is missing rather than installing it, bar `xcodes` and `aria2` in `xcode.sh`.
- `home/.claude/CLAUDE.md` is loaded whole into every session; `test/assert-markdown.sh` keeps it under 2,400 words and this file under 2,000, and no markdown here is hard-wrapped. That script also runs on Linux, so it may use no macOS-only command.

## Testing

- The account half: `./install.sh` then `test/assert.sh`. The machine half: `./machine.sh` then `test/assert-machine.sh`. Both run on a Mac that is already built; the fresh-Mac path is CI's alone (`.github/workflows/test.yml`, described in README under Testing).
- A Go tool or `discord/`: `mise exec -- go test -C <folder> ./...` and `go vet` from the repo root, beside `test/assert.sh`, which checks only the wrapper, links and agent.
- The Worker: `npm test` and `npx wrangler deploy --dry-run` in `shibuya/`.
- Markdown: `bash test/assert-markdown.sh`.
