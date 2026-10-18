#!/usr/bin/env python3
# PreToolUse on Bash, Edit and Write, and nothing at all in any session but one hachiko
# opened on an incident: those run under bypassPermissions, so the never list in their
# standing orders was words alone, and a fix-first session acts before anybody has read
# what it decided to do. The marker is HACHIKO_ONCALL_SESSION, which `hachiko oncall`
# sets on the pane it starts the session in, so it reaches the session, every subagent it
# spawns and every hook run for one — all of them one process tree.
#
# A guard against this session's own mistakes rather than a sandbox, and it says so here
# because the difference decides what belongs in it: the account already has root by other
# routes and writes this file, so anything deliberate goes round it. What it has to catch is
# the plausible wrong move at four in the morning — a `sudo` that is not the one root helper
# command, a force-push, an `rm` outside the three roots an on-call session may empty, an
# edit to the configuration every other session on this Mac loads. Where a rule cannot be
# read off the command, the orders and the oncall-partner hold it instead: whether the
# incident data really gave the daemon that is being restarted, and whether the one fix-first
# attempt has already been spent, are both outside anything this can see.
#
# Fail closed, and only for a marked session: an internal error there is a denial, since a
# session acting with nobody awake is the case the whole of this is for. Unmarked, every
# path out of here is exit 0 before stdin is even read.
#
# Exit 2 with the reason on stderr rather than a `deny` decision in JSON on stdout: the
# documented difference is that exit 2 stops the call before the permission rules are
# evaluated at all, and the permission rules in one of these sessions are
# bypassPermissions. The reason reaches the agent either way, which is the point — a
# refusal it cannot read is one it tries again in another shape.

import json
import os
import re
import shlex
import sys

MARKER = "HACHIKO_ONCALL_SESSION"
if not os.environ.get(MARKER):
    sys.exit(0)

HOME = os.path.expanduser("~")
MACHINE = os.environ.get("MAC_MINI_DIR") or os.path.join(HOME, ".mac-mini")
HELPER = os.environ.get("HACHIKO_ROOT_HELPER") or "/usr/local/libexec/claude-root"
SCRATCH = os.environ.get("CLAUDE_CODE_TMPDIR") or os.path.join(HOME, ".cache", "claude-tmp")

# The three roots the standing orders let a session empty, plus the one the shell's own
# temporary files are in. Everything else holds somebody's work.
#
# ~/Library/Caches is deliberately not among them, although it looks like the obvious place
# to free a gigabyte: hachiko's own state directory is in it, and so is the binary the
# wrapper builds, so an `rm -rf` there takes the record of this session's own attempt, the
# thread its messages go into, the sweep's lock and the stamp that says the night's update
# has run.
DELETE_ROOTS = [
    "/private/tmp",
    "/private/var/folders",
    "/var/folders",
    SCRATCH,
    os.path.join(HOME, "Library", "Logs"),
]
if os.environ.get("TMPDIR"):
    DELETE_ROOTS.append(os.environ["TMPDIR"])

# A project's own log or tmp folder, and in it only the files whose name says they are a
# log, which is the same rule hachiko's own truncate obeys: a tmp folder holds a build
# somebody is waiting on as readily as a log.
LOG_NAME = re.compile(r"\.(log|out|err|output)$|\.log\.\d+$")
LOG_DIRS = {"log", "logs", "tmp", "temp"}

# Only the configuration every session on this Mac loads, which includes this guard and the
# permissions the on-call session is running under. hachiko sync publishes it within minutes
# of a write, so an edit here is an edit to every other session as well.
#
# `home/.claude` wherever it is rather than in the checkout alone: the fix an update incident
# is allowed to make is made in a worktree, and that worktree holds a copy of this whole tree
# under a path of its own that lands on main like any other.
CONFIG_DIRS = [os.path.join(HOME, ".claude")]
CONFIG_PATH = re.compile(r"(^|/)home/\.claude(/|$)")

# Where herdr keeps this repository's worktrees, which are outside the checkout and are
# where the orders have the session make its fix.
HERDR_WORKTREES = os.path.join(HOME, ".herdr", "worktrees", os.path.basename(MACHINE))

# Prefixes a command hides behind, which the real command follows, with their own flags: the
# command in `env -i sudo rm -rf /` is the sudo.
PREFIXES = {"command", "builtin", "exec", "nohup", "nice", "stdbuf", "time", "env"}

SHELLS = {"sh", "bash", "zsh", "dash", "ksh"}

# `sed -i` is the likeliest shape of all for changing one line of a configuration file from a
# shell, so it belongs here however much of sed is a reader.
WRITERS = {
    "tee", "cp", "mv", "rm", "rmdir", "unlink", "install", "ln", "truncate", "chmod",
    "chown", "touch", "dd", "mkdir", "rsync", "patch", "ed", "shred", "python3", "python",
    "node", "perl", "ruby", "bun", "sed", "gsed", "awk", "sponge", "plutil", "defaults",
}

DELETERS = {"rm", "rmdir", "unlink", "shred", "srm", "truncate"}

LAUNCHCTL_READS = {"print", "list", "print-cache", "print-disabled", "getenv", "dumpstate", "examine"}


def deny(reason):
    sys.stderr.write(reason + "\n")
    sys.exit(2)


# The two variables a path is written with often enough that refusing them would cost an
# incident its fix, and whose value this process has as well.
VARS = re.compile(r"\$\{?(HOME|TMPDIR)\}?")


# A token that cannot be read as a path is one of two things: a variable or a command
# substitution the shell expands to something this cannot see, or a glob whose literal part
# is all there is to go on. The first is refused; the second is judged on the part that is
# spelled out.
def resolve(token, cwd):
    token = VARS.sub(lambda m: os.environ.get(m.group(1)) or m.group(0), token)
    if "$" in token or "`" in token:
        return None
    path = os.path.expanduser(token)
    if not os.path.isabs(path):
        path = os.path.join(cwd, path)
    return os.path.normpath(path)


def under(path, root):
    root = os.path.normpath(root)
    return path == root or path.startswith(root + os.sep)


def deletable(path):
    if any(under(path, root) for root in DELETE_ROOTS):
        return True
    parts = path.split(os.sep)
    return bool(LOG_NAME.search(parts[-1])) and any(part in LOG_DIRS for part in parts[:-1])


def is_config(path):
    return any(under(path, root) for root in CONFIG_DIRS) or bool(CONFIG_PATH.search(path))


# Where one command ends and the next begins. Everything below reads the command at the head
# of a segment, because that is what actually runs: a `sudo` quoted into a grep pattern is a
# string, and refusing it would cost an incident its reading.
OPERATORS = {"&&", "||", "|", ";", "&", "\n", "(", ")", "{", "}", "|&"}
# The output ones alone: `cat < file` reads, and refusing it would cost a reading.
WRITE_REDIRECTS = {">", ">>", "2>", "&>", ">|"}


def segments(command):
    lexer = shlex.shlex(command, posix=True, punctuation_chars=True)
    lexer.whitespace_split = True
    tokens = list(lexer)

    out, current = [], []
    for token in tokens:
        if token in OPERATORS:
            out.append(current)
            current = []
            continue
        current.append(token)
    out.append(current)
    return [s for s in out if s]


# An assignment or a prefix in front of the command is the shell's and not the command's, so
# `FOO=bar sudo rm -rf /` has to read as the sudo it is. A prefix's own flags go with it,
# which is what `env -i sudo …` is.
def words(segment):
    rest, prefixed = list(segment), False
    while rest:
        head = rest[0]
        if "=" in head and not head.startswith("=") and "/" not in head.split("=")[0]:
            rest, prefixed = rest[1:], True
            continue
        if os.path.basename(head) in PREFIXES:
            rest, prefixed = rest[1:], True
            continue
        if prefixed and head.startswith("-"):
            rest = rest[1:]
            continue
        break
    return rest


# The flags that take a value, among the commands below: without them `truncate -s 0 x`
# reads 0 as a second path and refuses a command that is inside the limits.
VALUE_FLAGS = {"-s", "--size", "-n", "--iterations", "--reference"}


def paths_of(args, cwd):
    out, flags_over, skip = [], False, False
    for arg in args:
        if skip:
            skip = False
            continue
        if arg == "--":
            flags_over = True
            continue
        if not flags_over and arg.startswith("-"):
            skip = arg in VALUE_FLAGS
            continue
        out.append((arg, resolve(arg, cwd)))
    return out


def check_sudo(args, cwd):
    # Exactly the root helper, and exactly the two actions Tim's list leaves open. The
    # daemon has to be one the installed helper itself names, read out of that file rather
    # than listed here, since the list that decides is the one sudoers reaches.
    if not args or os.path.normpath(os.path.expanduser(args[0])) != os.path.normpath(HELPER):
        deny("The never list for an on-call session allows no sudo but the root helper's own "
             "restart-daemon and reinstall-portless, and no approval code changes that. Put the "
             "exact command to Tim for him to run himself.")

    action = args[1:]
    if action[:1] == ["reinstall-portless"] and len(action) == 1:
        return
    if action[:1] != ["restart-daemon"] or len(action) != 2:
        deny("The root helper's only actions an on-call session may run are "
             "`restart-daemon <name>` and `reinstall-portless`. Everything else under sudo is "
             "Tim's, including this.")
    if action[1] not in helper_daemons():
        deny("%s is not a daemon the installed root helper restarts, so this would be refused "
             "by it anyway. Run the command the incident data gives after **You can run:** and "
             "nothing you spelled yourself." % action[1])


def helper_daemons():
    names, inside = set(), False
    try:
        with open(HELPER) as helper:
            for line in helper:
                entry = line.strip()
                if not inside:
                    inside = entry == "allowed_daemons=("
                elif entry == ")":
                    break
                else:
                    names.add(entry.strip('"').split(" ")[0])
    except OSError:
        return set()
    return names


def check_git(args, cwd):
    where = cwd
    rest = list(args)
    while rest:
        if rest[0] in ("-C", "--git-dir", "--work-tree") and len(rest) > 1:
            resolved = resolve(rest[1], cwd)
            if resolved is None:
                deny("This git command names a directory the guard cannot read, so it cannot "
                     "say which repository it would change. Spell the path out.")
            where, rest = resolved, rest[2:]
            continue
        if rest[0].startswith("-"):
            rest = rest[1:]
            continue
        break

    if not rest:
        return
    verb, rest = rest[0], rest[1:]

    if verb == "branch" and any(flag in rest for flag in ("-d", "-D", "--delete", "-M")):
        deny("Deleting or moving a branch is on the never list for an on-call session. Leave "
             "the branch and say in the thread what you would do with it.")

    if verb != "push":
        return

    # The checkout, and the worktrees of it that are not inside it: Claude Code puts its own
    # under the repository, herdr puts its under ~/.herdr/worktrees/<repo>, and the fix the
    # orders describe is made in either.
    if not under(where, MACHINE) and not under(where, HERDR_WORKTREES):
        deny("An on-call session pushes to this machine's own repository and to nothing else. "
             "Put the change to Tim instead.")

    for flag in ("--force", "-f", "--force-with-lease", "--force-if-includes", "--delete",
                 "-d", "--mirror", "--prune"):
        if flag in rest or any(arg.startswith(flag + "=") for arg in rest):
            deny("A force-push, a prune or a branch deletion is on the never list for an "
                 "on-call session, whatever a handover says. A commit that has to be undone is "
                 "undone with `git revert`.")

    refs = [arg for arg in rest if not arg.startswith("-")]
    if refs and refs[0] not in ("origin",):
        deny("An on-call session pushes to `origin` and to nothing else. %s is somewhere else, "
             "so the change is Tim's to make." % refs[0])
    for ref in refs[1:]:
        if ref.startswith("+") or ref.startswith(":"):
            deny("A `+` refspec is a force-push and a leading `:` deletes a branch, both on the "
                 "never list for an on-call session.")
        target = ref.split(":")[-1]
        if target not in ("main", "refs/heads/main", "HEAD"):
            deny("An on-call session pushes to `main` and to nothing else, and this names %s."
                 % target)


def check_kill(args):
    for arg in args:
        if re.search(r"claude|herdr", arg, re.IGNORECASE):
            deny("Stopping a Claude session or herdr is on the never list for an on-call "
                 "session: herdr holds every session on this Mac, and one of them may be "
                 "working. Kill the process causing the incident by its pid.")


def check_delete(args, cwd):
    for token, path in paths_of(args, cwd):
        if path is None:
            deny("This would delete something the guard cannot read the path of (%s). Spell the "
                 "path out, so what is being deleted is in the record." % token)
        if is_config(path):
            deny("%s is the configuration every session on this Mac loads, which is on the "
                 "never list for an on-call session." % token)
        if not deletable(path):
            deny("An on-call session deletes under /private/tmp, the Claude Code scratch "
                 "directory and ~/Library/Logs, and in a project's own log or tmp folder only "
                 "the files whose name says they are a log. %s is none of those, so it is "
                 "somebody's work: say in the thread what you would delete and wait for "
                 "Tim." % token)


# What a redirect in a segment is pointed at, which is the token after each of them.
def redirect_targets(segment, cwd):
    return [resolve(token, cwd)
            for before, token in zip(segment, segment[1:])
            if before in WRITE_REDIRECTS]


def check_config_writes(segment, args, verb, cwd):
    # A writing command is judged on all of its arguments, since which of them it writes to
    # differs per command. A redirect is judged on what it points at alone: `cat
    # ~/.claude/settings.json > /private/tmp/copy` reads that file, and refusing an ordinary
    # diagnostic would cost an incident its reading.
    if os.path.basename(verb) in WRITERS:
        suspect = [path for _, path in paths_of(args, cwd)]
        suspect += [resolve(token, cwd) for token in segment]
    else:
        suspect = redirect_targets(segment, cwd)

    for path in suspect:
        if path is not None and is_config(path):
            deny("Nothing on this Mac edits ~/.claude or home/.claude from an on-call session: "
                 "it is every session's configuration, including this one's permissions, and "
                 "hachiko sync publishes it within minutes. Put the change to Tim.")


# A redirect over a file that is already there is an erase, and "empty or delete" is what the
# limits are written in terms of: `: > x` and `rm x` cost the same thing. One pointed at a
# file that does not exist yet is a write, so a message file and an incident note go through,
# and so does anything inside this machine's own checkout — where what the session may be
# doing is the fix, and a heredoc over a file it is editing is not an erase of anybody's data.
def check_erase(segment, cwd):
    for path in redirect_targets(segment, cwd):
        if path is None or not os.path.exists(path) or os.path.isdir(path):
            continue
        if deletable(path) or under(path, MACHINE) or under(path, HERDR_WORKTREES):
            continue
        deny("This empties a file that is there, which the limits count as deleting it: an "
             "on-call session empties files under /private/tmp, the Claude Code scratch "
             "directory and ~/Library/Logs, and a project's own log files. Say in the thread "
             "what you would empty and wait for Tim.")


def check_segment(segment, cwd, depth=0):
    args = words(segment)
    if not args:
        return
    verb, rest = args[0], args[1:]
    name = os.path.basename(verb)

    if name in ("sudo", "doas"):
        check_sudo([a for a in rest if not a.startswith("-")], cwd)
        return

    if name == "op":
        deny("Anything with 1Password is on the never list for an on-call session. A secret an "
             "incident needs is Tim's to read.")

    if name in ("reboot", "shutdown", "halt"):
        deny("Rebooting or shutting down this Mac is on the never list for an on-call session. "
             "Tim reaches it over the tailnet and nothing would bring it back.")

    if name == "launchctl" and rest and rest[0] not in LAUNCHCTL_READS:
        deny("Stopping, starting or reloading a launchd job is on the never list for an on-call "
             "session, bar the root helper's own restart-daemon. Say which job it is and leave "
             "it to Tim.")

    # The server is every session on this Mac; a tab or a pane is one of them, and closing
    # one takes whatever that session was doing with it.
    if name == "herdr" and (rest[:1] == ["server"] or rest[1:2] == ["close"]):
        deny("Restarting herdr ends every session on this Mac, this one included, and closing "
             "a tab or a pane ends somebody's. Both are on the never list.")

    if name in ("kill", "pkill", "killall", "skill"):
        check_kill(rest)

    if name in DELETERS:
        check_delete(rest, cwd)

    # find's paths come before its first expression, and everything after one is a test
    # rather than a path — `-name '*.log'` read as a path refuses a command that deletes
    # nothing of the kind. What it would delete is the pattern under the root, which is the
    # same thing `rm dir/*.log` asks about: a log folder with thousands of files in it is
    # exactly where find gets reached for, and that one is inside the limits.
    if name == "find" and ("-delete" in rest or "-exec" in rest or "-execdir" in rest):
        roots = []
        for arg in rest:
            if arg.startswith("-"):
                break
            roots.append(arg)

        patterns = [rest[i + 1] for i, arg in enumerate(rest)
                    if arg in ("-name", "-iname") and i + 1 < len(rest)]

        if patterns:
            check_delete([os.path.join(root, pattern)
                          for root in roots for pattern in patterns], cwd)
        else:
            check_delete(roots, cwd)

    if name == "git":
        check_git(rest, cwd)

    # xargs runs its command against paths that arrive on stdin, which nothing here can
    # read. So the command itself is judged, and one that deletes is refused outright: what
    # it would delete is not in the command at all.
    if name == "xargs":
        if any(os.path.basename(arg) in DELETERS for arg in rest):
            deny("What this would delete arrives on stdin rather than in the command, so "
                 "nothing can say whether it is inside the limits. Name the files.")
        tail = [a for a in rest
                if not a.startswith("-") and a != "{}" and not a.isdigit()]
        if tail:
            check_segment(tail, cwd, depth)

    check_config_writes(segment, rest, verb, cwd)
    check_erase(segment, cwd)

    # One level into a shell of its own, since `bash -c "sudo ..."` is the same command in
    # another shape, and `-lc` is that shape with one letter in front. Deeper than that is a
    # script on disk, which nothing here reads.
    if name in SHELLS and depth == 0:
        for i, arg in enumerate(rest):
            if re.fullmatch(r"-[a-zA-Z]*c", arg) and i + 1 < len(rest):
                check_command(rest[i + 1], cwd, depth + 1)


def check_command(command, cwd, depth=0):
    try:
        parsed = segments(command)
    except ValueError:
        deny("The guard could not read this command as a shell command, so it cannot say "
             "whether it is on the never list. Write it in a plainer shape.")
    for segment in parsed:
        check_segment(segment, cwd, depth)


def main():
    raw = sys.stdin.read()
    event = json.loads(raw)

    tool = event.get("tool_name") or ""
    tool_input = event.get("tool_input") or {}
    cwd = event.get("cwd") or os.getcwd()

    if tool == "Bash":
        command = tool_input.get("command") or ""
        check_command(command, cwd)
        return

    if tool in ("Edit", "Write", "NotebookEdit", "MultiEdit"):
        path = tool_input.get("file_path") or tool_input.get("notebook_path") or ""
        if path and is_config(resolve(path, cwd) or path):
            deny("Nothing on this Mac edits ~/.claude or home/.claude from an on-call session: "
                 "it is every session's configuration, including this one's permissions, and "
                 "hachiko sync publishes it within minutes. Put the change to Tim.")


if __name__ == "__main__":
    try:
        main()
    except SystemExit:
        raise
    except Exception as err:  # noqa: BLE001 - a guard that cannot read the call denies it
        deny("The on-call guard could not read this tool call (%s), so it is refused rather "
             "than allowed: nobody is awake to check it. Hand the incident to Tim." % err)
