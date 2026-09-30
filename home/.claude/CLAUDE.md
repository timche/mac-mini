# Comments

- A comment says only what the code cannot: why a constraint exists, why the obvious approach was rejected, what an external system does that the code works around. Never what the code does; a name or a smaller function says that. Strip such comments from any code you touch, and keep the one that earns its place trimmed to the essentials.

# Commits and pull requests

- One commit per logical change, a move or rename ahead of the feature that needs it. While a pull request is open, answer review with new commits.
- A pull request whose work can be seen carries the proof, attached with `gh pr create --attach './shot.png#alt text'` or `gh pr edit <n> --attach`, so the reviewer does not have to reproduce it. A fix shows the before as well as the after.
- A screenshot proves a state: a layout, a label, a setting, what a screen looks like once it settles. A recording proves a sequence: focus, order, timing, a transition, a hover or drag, what follows a click. The test is whether one frame could look right while the bug is still there; if it could, record. On the Mac, `screencapture -v -k -x -V <seconds> out.mov` records the screen with clicks shown, and `ffmpeg -i out.mov -vf scale=1280:-2 -c:v libx264 -crf 26 -pix_fmt yuv420p out.mp4` makes it small enough to attach. A video takes no alt text, so the body says above it what it shows.
- When the design changes while a pull request is open, the title and body change with it in the same step as the push, since a squash merge takes the title as the commit message and a stale one lands in history for good. Before merging, reread the title against the final diff.
- The design changing under an open pull request also invalidates any published brief for it; republish the brief when the body changes.
- An issue, comment or pull request on someone else's repository shows none of our code: no snippets, file paths, package, variable or project names, or counts from our repositories. Reproduce with a minimal example under neutral names (`@acme/db`, `FEATURE_X`), written and run fresh.
- A repository may have its own pre-commit hooks (lefthook, husky); they run beside the global Betterleaks scan, which is a config-defined git hook, so never avoid or remove them on that account.

# Installing and privileged commands

- Never install software or change the machine outside the project without being asked, even into a user path. Recommend, then wait.
- sudo on this Mac asks for Tim's password, and a session has no terminal to type it at. When a step needs it, print the exact command in a code block and ask Tim to run it; he reviews it first and can run it himself. A change to the system trust store or to a privacy permission also puts a dialog on the Mac's own screen, which only Screen Sharing reaches, so say that as well.

# Secrets

- A repository that commits `.env.op` files keeps its secrets in 1Password as `op://` references. A command that needs them runs as `op run --env-file <path> -- <cmd>`, with the `.env.op` beside the code it runs, and one `--env-file` per file when it needs several; a command that needs none runs without it, since every run counts against the service account's daily limit. Never `op read` a value into the shell, a file or an argument, and never write a resolved value anywhere. A reference that does not resolve is a question for Tim, not a `.env` to fill in.

# Blockers

- When a verification or a step cannot be done from this machine (a site behind a login or bot protection, a device, a display, a credential), do not report it as unverified and move on. Say what is missing, propose the concrete ways to get it with a recommendation, and ask Tim for the one thing only he can supply, such as a login, a port forward or a cookie export. Once it works, write the procedure into the project's docs so the next session starts unblocked.

# Where things live

- Edit anything under `~/projects/docs` with Read, Edit and Write, never with shell redirection or `sed -i`. Several sessions share that one folder, and the file tools refuse a write to a file that changed since the session read it — a shell command has no such check and silently drops the other session's paragraph.
- Project checkouts go in `~/projects`, and `~/projects/docs` holds the project documentation; `$PROJECT_DOCS_DIR` names it, and a repository's own files refer to `$PROJECT_DOCS_DIR/<repo>` rather than a path. This repo is cloned to `~/.mac-mini`, hidden because it is what makes the machine and the account rather than work done in them. Both `~/projects/docs` and `~/.mac-mini` are watched by boswell, which commits, pushes and pulls them automatically, so never commit in them and never ask Tim to, unless the repository's own CLAUDE.md says a change must land now; a docs edit is on GitHub within a minute, an edit here within the hour.
- A project's docs in `~/projects/docs/<repo>` carry its settled decisions, and a session-start hook prints what is there. Read what bears on the task before starting, so a decision already recorded is not argued again.
- A repository's README, CLAUDE.md and code comments describe how it works now: no dates, no history, no "since" or "until", no alternative that lost; a constraint the code obeys can still say why. The decision behind it, and when it changed, goes in `~/projects/docs/<repo>`. When touching one that carries history, move it there rather than delete it.
- Tim reaches this Mac with Tailscale SSH as `timche@mac-mini` and attaches to herdr, whose server launchd runs in the GUI session, so a session's macOS privacy grants (Screen Recording, Accessibility, Full Disk Access) are the ones given to herdr.
- A dev server reaches his MacBook as `https://<branch>.<app>.timche.dev`, the second URL the session-start hook prints: portless serves every route under that name as well as `.localhost`, over the tailnet. Give him that URL. Never use `portless --tailscale`, which puts every app on one hostname and so in one cookie jar. Anything portless does not serve is bound to the tailnet address, `tailscale ip -4`, rather than tunnelled over SSH.
- Files cross that link with `scp`, run from Tim's side in either direction: `scp ./file timche@mac-mini:~/Downloads/` to send one here, `scp timche@mac-mini:<path> .` to take one away. Anything downloaded or sent here goes in `~/Downloads`, never a folder made up for it; when a file is needed, ask for it there. This Mac cannot push to his MacBook, which runs no SSH server for it, so anything going the other way is left somewhere for him to pull. Taildrop does not work here: Tim's devices are tagged, tagged devices have no user owner, and `tailscale file cp` refuses with "peer is owned by a different user".
- Tim is often on the same LAN as this Mac, where `ipconfig getifaddr en0` gives it a `192.168.x.x` address that sshd answers on, faster than the tailnet and working when tailscale is down on his side. `who` says where he is attached from, in brackets after each ttys line: a `100.x` source is the tailnet, a LAN source means tailscale is not up on his side. A session's own environment has no `SSH_CONNECTION`, since herdr runs in the Mac's GUI session.
- This Mac has no monitor but logs itself in, so a GUI session is always running. A browser that has to be seen or driven runs there as an ordinary window, with Chrome's remote debugging port; Tim watches and clicks it over Screen Sharing, and `screencapture -x` shows it to you. To show an app, capture its window rather than the display, which carries every other session's windows: `window-shot <owner> <out.png> [title]`, with the owner as the window list names it (`Electron` for a dev build). The display never sleeps, so a capture needs no waking first; one that fails or comes back black means a privacy grant is missing, a blocker for Tim rather than something to work around. Every window and tab he sees is one he has to look past, so close each one you open as soon as you are done with it, and leave shared processes, worktrees and scratch files the way you found them.

# Markdown

- Never hard-wrap prose in markdown files. One paragraph or list item is one line, however long; editors and viewers soft-wrap it.
- Never draw a diagram in Mermaid in a terminal reply; it shows as raw source there. Use an ASCII diagram in a `text` fence, or an HTML artifact when the picture needs more than boxes and arrows. Mermaid is fine where GitHub renders it: PR bodies, issues, comments and markdown files.

# Worktrees

- Several sessions run at once, a worktree each, and the session-start hook prints the names this one owns: its compose project, its dev URL, its Electron profile. Use those, and hardcode none of them — not a host port, not a profile path, not a compose project name.
- A dev server takes the port it is given: portless's `PORT`, or `0` and the next free one. Pass the URL it settled on to whatever needs it as an environment variable rather than writing the port down anywhere.
- A compose file maps a container port with no host port — `"5432"`, not `"5432:5432"` — and `docker compose port <service> 5432` reads back the one it got.
- Never stop, reuse or clean up another worktree's processes, containers, volumes or profiles by hand. `worktree-gc` sweeps what a removed worktree leaves behind, and a running worktree is somebody's session.

# Delegation

- Delegate work whose output or reading is large: code, tests, codebase exploration. Do a change yourself only when spawning a subagent would cost more than the change, meaning the spec would be as long as the diff and no further reading is needed.
- Code changes happen in a worktree, never in a repository's main checkout, because other sessions share it. A project's CLAUDE.md can lift this.
- Short actions that depend on context you already hold, such as commits, PRs and review fixes, are yours.
