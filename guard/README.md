# guard

`guard` is the `PreToolUse` hook on `Bash` in every Claude session on this Mac. It reads the command a session is about to run and refuses the two kinds that reach out of the session that ran them, each because many sessions share one machine:

- **A kill by name, pattern or port.** Each session runs its own copy of the same dev server in its own worktree, so a process named by its name, a pattern or the port it listens on is every session's copy of it: a `pkill -f portless` meant for one session's server stopped another's. A kill is allowed by pid alone, and a pid read off `pgrep`, `pidof`, `lsof`, `fuser`, `ps`, `netstat` or `ss` is a pid chosen by name or port all the same.
- **Taking the Mac's one foreground without the lock.** The GUI session has one frontmost app, one key window and one pointer, so `cliclick` and an `osascript` that sends a System Events keystroke, raises a window or clicks the screen reach whatever another session has in front. Those run under [`focus-lock`](../home/.local/bin/focus-lock), which serialises them, and this refuses them outside it. What reaches an app without the foreground — CDP, `perform action "AXPress"`, UTM's `input keystroke`, VNC — is left alone, which is why the check reads what a script does rather than refusing `osascript`. `$PROJECT_DOCS_DIR/shared/macos-gui-automation.md` says which call is which.

It is a compiled binary rather than a script because a hook that cannot run is a hook that is silently off: a Python that a mise or a Homebrew upgrade moved takes the guard with it and nothing in a session says so. This depends on a kernel and nothing else.

## The command

`guard pre-tool-use` reads the hook event as JSON on stdin and prints a deny decision — `hookSpecificOutput`, `permissionDecision` `deny`, and the reason the refusing check gives — or nothing at all, which is how a command is allowed. It exits 0 either way, and 1 when it was called wrong; never 2, which is how a `PreToolUse` hook blocks a command, so no bug here can cost a session its shell.

An event it cannot read is allowed, and so is a command it cannot read: `internal/shell` splits a command the way Bash would — quotes, `$()`, backticks, process substitutions, redirects, heredocs, `bash -c`, the prefixes that run another command such as `sudo`, `env`, `xargs` and `timeout` — and an unterminated quote, an unclosed substitution or a nesting deeper than anyone writes comes back as unreadable. A guard on every session's shell that refused what it could not parse would cost them all their shell. So a `pkill` in a commit message, a grep pattern or a heredoc body is an argument and goes through, and `internal/killbypid` and `internal/focus` decide only on a word a shell would run as a command.

## How it is wired

`home/.claude/settings.json` registers it once per `if` filter — one permission rule each, `Bash(pkill *)` through `Bash(op *)` — so no process starts for a command that cannot carry either thing. The filter is checked against every subcommand, inside `$()` and backticks, and after Claude Code strips `timeout`, `nice`, `nohup` and the rest; it does not look inside `bash -c`, which is why the shells are filters of their own, and it strips neither `sudo` nor `env` nor `op run`, which is why those are too. The timeout is 30 seconds, since a rebuild after an edit runs inside the hook.

## Setup, state and logs

Nothing to install: `mise.toml` links `home/.local/bin/guard` into `~/.local/bin`, and that wrapper builds the binary from this checkout's working tree into `~/Library/Caches/guard` when the sources' hash moves, exactly as [hachiko's](../hachiko/README.md) does and for the same reasons. `go test ./...` is the gate between an edit and every session's shell, and a tree that does not build or does not pass leaves the last binary that did. The wrapper's one-line failures go to the session's stderr, which Claude Code reports as a hook error; there is no log file and no state beyond the binary and `source.sha256`.

`GUARD_CACHE_DIR` moves the cache, which is how a session tries a change without touching the one every other session is running.

## The tests are the specification

What this refuses is what `internal/killbypid` and `internal/focus` refuse in their tests, case by case. `test/assert.sh` checks only that it is linked, registered under every filter, and answering; a new way of killing by name or taking the foreground is a case in the table, not a line there.
