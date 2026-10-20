#!/usr/bin/env python3
# PreToolUse on Bash, in every session. Several Claude sessions share this Mac, one
# worktree each, and each runs its own copy of the same dev server, so a process named
# by its name, a pattern or the port it listens on is every session's copy of it: a
# `pkill -f portless` meant for one session's server stopped another's. A kill is
# allowed by pid alone, and a pid read off pgrep, lsof, ps or fuser is a pid chosen by
# name or port all the same.
#
# The command is read the way the shell splits it, so a pkill inside quotes, a commit
# message or a grep pattern is text and goes through. Anything this cannot read goes
# through as well: a guard that refused what it could not parse would cost every
# session its shell.

import json
import os
import re
import sys

KILLERS_BY_NAME = {"pkill", "killall", "killall5", "skill"}
FINDERS = {"pgrep", "pidof", "lsof", "fuser", "ps", "netstat", "ss"}
SHELLS = {"sh", "bash", "zsh", "dash", "ksh"}

# Commands that run the command after them, each with the flags of its own that take a
# value, since `sudo -u x pkill` runs pkill and not x.
PREFIXES = {
    "sudo": {"-u", "-g", "-C", "-D", "-h", "-p", "-r", "-t", "-U", "-T"},
    "doas": {"-u", "-C"},
    "env": {"-u", "-C", "-P", "-S"},
    "xargs": {"-I", "-n", "-P", "-L", "-l", "-d", "-E", "-e", "-s", "-a", "-R", "-S", "-J"},
    "nohup": set(),
    "time": set(),
    "command": set(),
    "builtin": set(),
    "exec": {"-a"},
    "nice": {"-n"},
    "timeout": {"-s", "-k", "--signal", "--kill-after"},
    "caffeinate": {"-t", "-w"},
    "stdbuf": set(),
}
KEYWORDS = {"do", "then", "else", "elif", "!", "{", "while", "until", "if"}
OPENERS = {"while", "until", "if", "for", "select", "case", "{"}
CLOSERS = {"done", "fi", "esac", "}"}

OPERATORS = ("&&", "||", "|&", ";;", "|", ";", "&", "(", ")", "\n")
PIPES = {"|", "|&"}
PLAIN_VAR = re.compile(r'^"?\$(\w+|\{\w+\})"?$')

REASON = (
    "Killing by name, pattern or port reaches every session's copy of that process on this "
    "Mac, not only yours: several Claude sessions share it, one worktree each. Stop only what "
    "you started: `kill <pid>` with the PID you recorded when you started it (`$!`), or "
    "TaskStop on the background task that runs it."
)


class Denied(Exception):
    pass


class Word:
    def __init__(self, text, raw, subs):
        self.text, self.raw, self.subs = text, raw, subs


# What a segment reads from besides its arguments: a process substitution, or the
# substitutions in a redirect's target such as a here-string.
class Feed:
    def __init__(self, subs):
        self.subs = subs


def closing_paren(s, i):
    depth = 0
    while i < len(s):
        c = s[i]
        if c == "\\":
            i += 2
            continue
        if c == "'":
            i = s.index("'", i + 1)
        elif c == '"':
            i += 1
            while s[i] != '"':
                i += 2 if s[i] == "\\" else 1
        elif c == "(":
            depth += 1
        elif c == ")":
            depth -= 1
            if depth == 0:
                return i
        i += 1
    raise ValueError("unclosed $(")


def closing_backtick(s, i):
    i += 1
    while s[i] != "`":
        i += 2 if s[i] == "\\" else 1
    return i


def word_end(s, i):
    while i < len(s) and s[i] not in " \t\n;&|()<>":
        if s.startswith("$(", i):
            i = closing_paren(s, i + 1)
        elif s[i] == "`":
            i = closing_backtick(s, i)
        elif s[i] == "'":
            i = s.index("'", i + 1)
        elif s[i] == '"':
            i += 1
            while s[i] != '"':
                if s.startswith("$(", i):
                    i = closing_paren(s, i + 1)
                i += 2 if s[i] == "\\" else 1
        elif s[i] == "\\":
            i += 1
        i += 1
    return i


# Words and operators, with quotes taken off and every command substitution kept apart as
# the command it runs. Heredoc bodies and comments are dropped: neither is run as a command.
def lex(s):
    tokens, i, n = [], 0, len(s)
    text, subs, start = None, [], 0
    heredocs = []

    def flush():
        nonlocal text, subs
        if text is not None:
            tokens.append(Word("".join(text), s[start:i], subs))
        text, subs = None, []

    def begin():
        nonlocal text, start
        if text is None:
            text, start = [], i

    while i < n:
        c = s[i]
        if text is None and c == "#":
            while i < n and s[i] != "\n":
                i += 1
            continue
        if c in " \t":
            flush()
            i += 1
            continue
        if c == "\\" and i + 1 < n:
            begin()
            if s[i + 1] != "\n":
                text.append(s[i + 1])
            i += 2
            continue
        if c == "'":
            begin()
            end = s.index("'", i + 1)
            text.append(s[i + 1:end])
            i = end + 1
            continue
        if c == '"':
            begin()
            i += 1
            while s[i] != '"':
                if s[i] == "\\":
                    text.append(s[i + 1])
                    i += 2
                elif s.startswith("$(", i):
                    end = closing_paren(s, i + 1)
                    subs.append(s[i + 2:end])
                    text.append(s[i:end + 1])
                    i = end + 1
                elif s[i] == "`":
                    end = closing_backtick(s, i)
                    subs.append(s[i + 1:end])
                    text.append(s[i:end + 1])
                    i = end + 1
                else:
                    text.append(s[i])
                    i += 1
            i += 1
            continue
        if s.startswith("$(", i):
            begin()
            end = closing_paren(s, i + 1)
            subs.append(s[i + 2:end])
            text.append(s[i:end + 1])
            i = end + 1
            continue
        if c == "`":
            begin()
            end = closing_backtick(s, i)
            subs.append(s[i + 1:end])
            text.append(s[i:end + 1])
            i = end + 1
            continue
        if (s.startswith("<(", i) or s.startswith(">(", i)) and text is None:
            end = closing_paren(s, i + 1)
            tokens.append(Feed([s[i + 2:end]]))
            i = end + 1
            continue
        if c in "<>" or s.startswith("&>", i):
            # A redirect's fd (`2>`) is not an argument, and nor is what it points at.
            if text is not None and "".join(text).isdigit():
                text, subs = None, []
            flush()
            m = re.match(r"&>>?|<<<|<<-?|<>|<&|>&|>>|>\||<|>", s[i:])
            op = m.group(0)
            i += len(op)
            while i < n and s[i] in " \t":
                i += 1
            if s.startswith("<(", i):
                continue
            target = s[i:word_end(s, i)]
            if op in ("<<", "<<-"):
                heredocs.append((target.replace("'", "").replace('"', "").replace("\\", ""), op == "<<-"))
            elif "$(" in target or "`" in target:
                tokens.append(Feed([sub for word in lex(target) if isinstance(word, Word)
                                    for sub in word.subs]))
            i += len(target)
            continue
        op = next((o for o in OPERATORS if s.startswith(o, i)), None)
        if op:
            flush()
            tokens.append(op)
            i += len(op)
            if op == "\n" and heredocs:
                for delimiter, tabs in heredocs:
                    while i < n:
                        end = s.find("\n", i)
                        end = n if end == -1 else end
                        line = s[i:end]
                        i = end + 1
                        if (line.lstrip("\t") if tabs else line) == delimiter:
                            break
                heredocs = []
            continue
        begin()
        text.append(c)
        i += 1
    flush()
    return tokens


# The command a segment runs, past assignments, shell keywords and the prefixes that run
# another command, or None when the segment runs nothing.
def command_of(words):
    i = 0
    while i < len(words):
        word = words[i].text
        if re.match(r"^\w+=", word) or word in KEYWORDS:
            i += 1
            continue
        name = os.path.basename(word)
        if name in PREFIXES:
            if name == "command" and any(w.text in ("-v", "-V") for w in words[i + 1:i + 2]):
                return None
            value_flags = PREFIXES[name]
            i += 1
            while i < len(words):
                arg = words[i].text
                if name == "env" and re.match(r"^\w+=", arg):
                    i += 1
                elif arg.startswith("-") and len(arg) > 1:
                    i += 2 if arg in value_flags else 1
                else:
                    break
            if name == "timeout" and i < len(words):
                i += 1
            continue
        return i
    return None


def uses_finder(command):
    for words, _, feeds in segments(lex(command)):
        at = command_of(words)
        if at is not None and os.path.basename(words[at].text) in FINDERS:
            return True
        if any(uses_finder(sub) for part in words + feeds for sub in part.subs):
            return True
    return False


def fed_by_finder(feeds):
    return any(uses_finder(sub) for feed in feeds for sub in feed.subs)


def segments(tokens):
    out, words, feeds = [], [], []
    for token in tokens:
        if isinstance(token, str):
            out.append((words, token, feeds))
            words, feeds = [], []
        elif isinstance(token, Feed):
            feeds.append(token)
        else:
            words.append(token)
    out.append((words, None, feeds))
    return out


def check_kill(args, tainted, piped):
    if piped:
        raise Denied
    targets, i = [], 0
    while i < len(args):
        arg = args[i].text
        if arg in ("-l", "-L"):
            return
        if arg == "--":
            targets += args[i + 1:]
            break
        if arg in ("-s", "-n"):
            i += 2
            continue
        if arg.startswith("-") and not targets and i == 0:
            i += 1
            continue
        targets.append(args[i])
        i += 1
    for target in targets:
        if target.text == "-1":
            raise Denied
        if any(uses_finder(sub) for sub in target.subs):
            raise Denied
        var = PLAIN_VAR.match(target.raw)
        if var and var.group(1).strip("{}") in tainted:
            raise Denied


def check(command, piped=False, tainted=None):
    tainted = set() if tainted is None else tainted
    parts = segments(lex(command))

    # `done < <(pgrep x)` feeds a loop whose kill comes before the feed is read, so a
    # finder feeding a closing keyword counts from the start of the command.
    if any(fed_by_finder(feeds) for words, _, feeds in parts
           if not words or words[0].text in CLOSERS):
        piped = True
    depth, piped_at = 0, (0 if piped else None)

    for words, end, feeds in parts:
        if words:
            first = words[0].text
            if first in OPENERS:
                depth += 1
            elif first in CLOSERS:
                depth -= 1

        fed = piped_at is not None or fed_by_finder(feeds)
        for feed in feeds:
            for sub in feed.subs:
                check(sub, piped_at is not None)

        for word in words:
            for sub in word.subs:
                check(sub, piped_at is not None)
            assignment = re.match(r"^(\w+)=", word.text)
            if assignment and any(uses_finder(sub) for sub in word.subs):
                tainted.add(assignment.group(1))

        if len(words) > 3 and words[0].text in ("for", "select") and words[2].text == "in":
            if any(uses_finder(sub) for w in words[3:] for sub in w.subs):
                tainted.add(words[1].text)

        at = command_of(words)
        if at is not None:
            name = os.path.basename(words[at].text)
            args = words[at + 1:]
            if name in KILLERS_BY_NAME:
                raise Denied
            if name == "fuser" and any(re.match(r"^-\w*k", a.text) for a in args):
                raise Denied
            if name == "kill":
                check_kill(args, tainted, fed)
            if name == "read" and fed:
                tainted.update(a.text for a in args if not a.text.startswith("-"))
            if name in SHELLS:
                for j, arg in enumerate(args[:-1]):
                    if re.match(r"^-\w*c\w*$", arg.text):
                        check(args[j + 1].text, fed, tainted)
                        break
            if name in FINDERS and end in PIPES and piped_at is None:
                piped_at = depth

        if end == "(":
            depth += 1
        elif end == ")":
            depth -= 1
        elif end not in PIPES and piped_at is not None and depth <= piped_at:
            piped_at = None


def main():
    try:
        event = json.load(sys.stdin)
        command = (event.get("tool_input") or {}).get("command") or ""
        check(command)
    except Denied:
        print(json.dumps({
            "hookSpecificOutput": {
                "hookEventName": "PreToolUse",
                "permissionDecision": "deny",
                "permissionDecisionReason": REASON,
            }
        }))
    except Exception:
        pass


main()
