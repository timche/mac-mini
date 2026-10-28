# Comments

- A comment says only what the code cannot: why a constraint exists, why the obvious approach was rejected, what an external system does that the code works around. Never what the code does. Strip such comments from code you touch, and trim the rest.

# Commits and pull requests

- One commit per logical change, a move or rename ahead of the feature that needs it. While a pull request is open, answer review with new commits.
- Never create a merge commit. A branch catches up by rebasing onto its base and pushing with `--force-with-lease`; a pull request lands squashed or rebased, never with `gh pr merge --merge`.
- A pull request whose work can be seen carries the proof, attached with `gh pr create --attach './shot.png#alt text'` or `gh pr edit <n> --attach`; a fix shows the before as well as the after. A screenshot proves a state; a recording proves a sequence (focus, order, timing, a transition, what follows a click), so record whenever one frame could look right while the bug is still there. `screencapture -v -k -x -V <seconds> out.mov` records with clicks shown, and `ffmpeg -i out.mov -vf scale=1280:-2 -c:v libx264 -crf 26 -pix_fmt yuv420p out.mp4` makes it small enough to attach; the body says above a video what it shows.
- Before-and-after screenshots, for a pull request, a review or a question, go in an HTML artifact that overlays each pair at the same width with a draggable slider, a key or click that flips between them, side by side as a fallback, and pickers for page, viewport and theme. Capture each pair under identical conditions: same width and device pixel ratio, a fresh browser context per theme so a stored toggle can't override it. Check that each capture shows what it claims. The pull request links the artifact and still attaches one representative pair.
- When the design changes under an open pull request, its title and body change in the same step as the push, since a squash merge takes the title as the commit message, and any brief published for it is republished. Before merging, reread the title against the final diff.
- Every mention of a pull request gives its link beside its number: `#<id> (<url>)`.
- An issue, comment or pull request on someone else's repository shows none of our code: no snippets, paths, package, variable or project names, or counts from our repositories. Reproduce with a minimal example under neutral names (`@acme/db`, `FEATURE_X`), written and run fresh.
- Never skip or remove a repository's own pre-commit hooks (lefthook, husky) because the global Betterleaks scan also runs.

# Installing and privileged commands

- A tool a task needs and this machine lacks gets installed, not worked around: a CLI into a user path with mise, brew or a global bun install, or a one-off run through `bunx`/`npx`. Say what was installed. Any other change to the machine outside the project, such as its configuration or a launchd job, is recommended and waits until Tim asks.
- A subagent that cannot reach a tool the task needs is the wrong agent type, or its definition lacks the tool; fix that rather than accept an unverified result.
- sudo needs Tim's password and a session has no terminal to type it at: print the exact command in a code block for him to review and run. A change to the trust store or a privacy permission also puts a dialog on the Mac's own screen, which only Screen Sharing reaches; say so.
- The exception is `sudo /usr/local/libexec/claude-root <action>`, which needs no password. Run `--list` for its actions rather than guessing one.

# Secrets

- A repository's `.env.op` files hold its secrets as 1Password `op://` references. A command that needs them runs as `op run --env-file <path> -- <cmd>`, one `--env-file` per file; one that needs none runs without it, since every run counts against the service account's daily limit. Never `op read` a value into the shell, a file or an argument, nor write a resolved value anywhere, bar the SSH keys `claude/ssh-key.sh` writes to `~/.ssh`: the commit-signing key `id_signing` and the VM key `id_vm`. A reference that does not resolve is a question for Tim, not a `.env` to fill in.
- A credential Tim creates for any service goes into 1Password before it is used anywhere, and every later step reads it from there, never from the clipboard or a paste: `op read "op://<vault>/<item>/<field>" | gh secret set <NAME> …` for a GitHub secret, `op run` for a command, an `op://` reference in `.env.op` for the rest. A guide for creating one ends with the exact `op item create` command, taking the value from `pbpaste` and clearing the clipboard afterwards, then the commands that read it back. Vault, title, type, field and tag conventions are in `$PROJECT_DOCS_DIR/mac-mini/secrets-handoff.md`; a write credential goes in the project's prod vault, which only Tim reads, so those commands run on his side.

# Blockers

- Ask Tim only for what this Mac cannot do: a decision that is his, a login, a device, a display, a credential, a site behind bot protection. Do everything else yourself, such as a pull, a restart or discarding changes you have read, and report it done. For a blocker, say what is missing, propose ways to get it with a recommendation, and ask for the one thing only he can supply. Once it works, write the procedure into the project's docs.
- Once its change has merged, a session removes its worktree and its local and remote branch, and stops what it started. Its last reply says the session can be closed, or names what is still open: an uncommitted change, an open pull request, a running process, a question for Tim.

# Where things live

- Project checkouts go in `~/projects`, and project docs in `$PROJECT_DOCS_DIR` (`~/projects/docs`); a repository refers to `$PROJECT_DOCS_DIR/<repo>` rather than a path. Edit the docs with Read, Edit and Write only, never shell redirection or `sed -i`: several sessions share them, and only the file tools refuse a write to a file that changed since it was read. `hachiko sync` commits and pushes the docs and `~/.claude`, so never commit either or ask Tim to.
- `$PROJECT_DOCS_DIR/<repo>` holds a project's settled decisions and `$PROJECT_DOCS_DIR/shared` what applies across projects, such as the Cloudflare account's conventions; a session-start hook lists both. Read what bears on the task before starting, so a recorded decision is not argued again. A convention more than one project needs goes in `shared`; a project's own folder wins where they disagree.
- A repository's README, CLAUDE.md and code comments describe how it works now: no dates, no history, no "since" or "until", no alternative that lost; a constraint the code obeys can still say why. The decision behind it, and when it changed, goes in `$PROJECT_DOCS_DIR/<repo>`; text that carries history is moved there when touched, not deleted.
- A hook names the herdr tab after Claude Code's own session title and follows that title as it changes, so leave the tab's name to it. A tab renamed any other way keeps that name until `/clear`.
- Give Tim a dev server as `https://<branch>.<app>.timche.dev`, the second URL the session-start hook prints. Never use `portless --tailscale`, which puts every app on one hostname and so in one cookie jar. Anything portless does not serve binds to the tailnet address, `tailscale ip -4`, rather than an SSH tunnel.
- Tim reaches this Mac as `timche@mac-mini` over Tailscale SSH, or on the LAN at the address `ipconfig getifaddr en0` gives, which is faster and works when his tailscale is down. Files cross with `scp` run from his side: `scp ./file timche@mac-mini:<path>` to send one here, `scp timche@mac-mini:<path> .` to take one away. A file for him stays where it was made and he gets its absolute path; the folder holding it stays until he has pulled it. When a file is needed from him, name the path to send it to. Taildrop refuses his devices.
- This Mac has no monitor but is always logged in to a GUI session, which Orca runs in, so a session's macOS privacy grants (Screen Recording, Accessibility, Automation, Full Disk Access) are Orca Terminal Host's, and a missing one puts a prompt on the Mac's screen that only Tim can answer. A browser that has to be seen or driven runs there as an ordinary window with Chrome's remote debugging port; Tim watches and clicks it over Screen Sharing. To show an app, capture its window, not the display, which carries every other session's windows: `window-shot <owner> <out.png> [title]`, the owner as the window list names it (`Electron` for a dev build). A capture that fails or comes back black means a grant is missing, a blocker for Tim. Close each window and tab you open once done with it.
- Every session shares that one desktop's frontmost app and pointer, so drive an app without them: CDP for Chromium and Electron, accessibility actions for native menus, UTM's AppleScript input or VNC for a VM, each launched so it does not come to the front. What still needs the foreground, such as a raised window, a System Events keystroke or `cliclick`, runs as one command under `focus-lock`. `$PROJECT_DOCS_DIR/shared/macos-gui-automation.md` has which call is which.
- The public web and anything signed in as Tim go through Claude in Chrome, the extension in his own Chrome profile: search and shopping sites block the headless browser `chrome-devtools` starts, and it has none of his sessions. `chrome-devtools` is for his own apps on a local or dev URL. When Claude in Chrome reports no connected browser, `$PROJECT_DOCS_DIR/mac-mini/README.md`, under Claude in Chrome, says how to start it.

# Markdown

- Never hard-wrap prose in markdown files. One paragraph or list item is one line, however long.
- Never draw a Mermaid diagram in a terminal reply; it shows as raw source there. Use an ASCII diagram in a `text` fence, or an HTML artifact when the picture needs more than boxes and arrows. Mermaid is fine where GitHub renders it: PR bodies, issues, comments and markdown files.
- A list in a reply that Tim may answer item by item, such as questions, blockers or options, is numbered, not bulleted.

# Worktrees

- Code changes happen in a worktree, never in a repository's main checkout, which other sessions share; a project's CLAUDE.md can lift this. A worktree is herdr's or Claude Code's to make, never a `git worktree add` anywhere else: `hachiko gc` sweeps those two roots alone.
- Use the names the session-start hook prints for this worktree, its compose project, dev URL and Electron profile, and hardcode none of them, nor a host port. A dev server takes the port it is given, portless's `PORT` or `0`, and whatever needs its URL gets it as an environment variable. A compose file maps a container port with no host port, `"5432"` not `"5432:5432"`, and `docker compose port <service> 5432` reads back the one it got.
- The output of anything long-running goes to a Claude Code background task or a size-capped log, never an unbounded file in `/tmp` or elsewhere: a process that lost what it retries on writes tens of gigabytes an hour.
- Never stop, reuse or clean up another worktree's processes, containers, volumes or profiles; `hachiko gc` sweeps what a removed worktree leaves behind. Stop your own by PID or TaskStop, and leave shared processes and scratch files as you found them.

# Delegation

- Delegate work whose output or reading is large: code, tests, codebase exploration. Make a change yourself only when the spec would be as long as the diff and no further reading is needed. Short actions that depend on context you hold, such as commits, PRs and review fixes, are yours.
