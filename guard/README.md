# guard

`guard` is the `PreToolUse` hook on `Bash` in every Claude session on this Mac. It reads the command a session is about to run and refuses the two kinds that reach out of the session that ran them, each because many sessions share one machine:

- **A kill by name, pattern or port.** Each session runs its own copy of the same dev server in its own worktree, so a process named by its name, a pattern or the port it listens on is every session's copy of it: a `pkill -f portless` meant for one session's server stopped another's. A kill is allowed by pid alone, and a pid read off `pgrep`, `pidof`, `lsof`, `fuser`, `ps`, `netstat` or `ss` is a pid chosen by name or port all the same.
- **Taking the Mac's one foreground without the lock.** The GUI session has one frontmost app, one key window and one pointer, so `cliclick` and an `osascript` that sends a System Events keystroke, raises a window or clicks the screen reach whatever another session has in front. Those run under [`focus-lock`](../home/.local/bin/focus-lock), which serialises them, and this refuses them outside it. What reaches an app without the foreground — CDP, `perform action "AXPress"`, UTM's `input keystroke`, VNC — is left alone, which is why the check reads what a script does rather than refusing `osascript`. `$PROJECT_DOCS_DIR/shared/macos-gui-automation.md` says which call is which.

It is a compiled binary rather than a script because a hook that cannot run is a hook that is silently off: a Python that a mise or a Homebrew upgrade moved takes the guard with it and nothing in a session says so. This depends on a kernel and nothing else.

## The command

`guard pre-tool-use` reads the hook event as JSON on stdin and prints a deny decision — `hookSpecificOutput`, `permissionDecision` `deny`, and the reason the refusing check gives — or nothing at all, which is how a command is allowed. It exits 0 either way, and 1 when it was called wrong; never 2, which is how a `PreToolUse` hook blocks a command, so no bug here can cost a session its shell.

An event it cannot read is allowed, and so is a command it cannot read: `internal/shell` splits a command the way Bash would — quotes, `$()`, backticks, process substitutions, redirects, heredocs, `bash -c`, the prefixes that run another command such as `sudo`, `env`, `xargs` and `timeout` — and an unterminated quote, an unclosed substitution or a nesting deeper than anyone writes comes back as unreadable. A guard on every session's shell that refused what it could not parse would cost them all their shell. So a `pkill` in a commit message, a grep pattern or a heredoc body is an argument and goes through, and `internal/killbypid` and `internal/focus` decide only on a word a shell would run as a command.

## How it is wired

`home/.claude/settings.json` registers it once for every Bash command, with no `if` filter. A filter would be a second list of the programs guard reads, kept in step by hand with the first, and Claude Code's matching sees through only its own fixed set of wrappers, so any runner missing from the filter would be a command guard never saw. The timeout is 30 seconds, since a rebuild after an edit runs inside the hook.

What it registers is the built binary, `~/Library/Caches/guard/guard`, and the wrapper that builds it only when there is none: a shell, a `find`, a `shasum` and a `mise` before every Bash call in every session cost several times the check they are there for. So keeping an edit live is the binary's own job. The wrapper writes the folder it built from, and its own path, into the binary at build time; the binary, before it reads the event, compares the times of that folder's `*.go`, `go.mod` and `go.sum` and of the folders themselves — a deletion or a rename shows on the folder alone — against the newer of itself and `attempted`, and execs the wrapper with the event unread when the sources have moved. What answers the command is then a build of the sources as they are now. `GUARD_FRESH` says that handoff has happened, so there is only ever one.

## Setup, state and logs

Nothing to install: `mise.toml` links `home/.local/bin/guard` into `~/.local/bin`, and that wrapper builds the binary from this checkout's working tree into `~/Library/Caches/guard`, exactly as [hachiko's](../hachiko/README.md) does and for the same reasons. A wrapper run from a linked worktree builds into `~/Library/Caches/guard/worktrees/<name>` instead, since the hook runs the default cache's binary and that binary follows the sources it was built from. Sources whose times moved but whose contents did not are not rebuilt, and the wrapper touches `attempted` so the binary stops handing off. `go test ./...` is the gate between an edit and every session's shell, and a tree that does not build or does not pass leaves the last binary that did. The wrapper's one-line failures go to the session's stderr, which Claude Code reports as a hook error; there is no log file.

The state is the binary and `attempted`, which holds the hash of the sources at the last build attempt whether that attempt worked or not, and in its time when it was made. So a tree that does not compile is built once rather than before every command the session runs while it is fixing it, and the binary has something to measure the sources against. A cache with no binary in it is built from whatever `attempted` says, that being the one case where the hook reaches the wrapper at all.

`GUARD_CACHE_DIR` moves the cache, which is how a session tries a change without touching the one every other session is running.

## The tests are the specification

What this refuses is what `internal/killbypid` and `internal/focus` refuse in their tests, case by case. `test/assert.sh` checks only that it is linked, registered as the one command above, answering, and that an edit reaches a session: the handoff, a tree that does not compile, and a cache with no binary in it. A new way of killing by name or taking the foreground is a case in the table, not a line there.
