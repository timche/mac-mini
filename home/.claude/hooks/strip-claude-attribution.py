#!/usr/bin/env python3
# PreToolUse on Bash. A pull request, issue or comment body goes from gh straight to
# GitHub and never passes through git's commit-msg hook, and Claude Code's attribution
# setting only changes what the model is asked to write. So the attribution is taken
# out of any gh command and any body file it names before the command runs. Rewritten
# rather than refused, since a refused command is one an agent retries and works around.

import json
import re
import shlex
import sys

ATTRIBUTION = re.compile(
    r"[ \t]*(?:🤖[ \t]*)?Generated with \[?Claude Code\]?(?:\([^)\s]*\))?[ \t]*"
    r"|[ \t]*Co-Authored-By:[^\n'\"]*(?:Claude|anthropic\.com)[^\n'\"]*",
    re.IGNORECASE,
)
GH = re.compile(r"(?:^|[\s;&|(])gh\s")
BODY_FILE_FLAGS = ("--body-file", "-F", "--input")


def strip(text):
    cleaned = ATTRIBUTION.sub("", text)
    cleaned = re.sub(r"(?:\\n){3,}", r"\\n\\n", cleaned)
    return re.sub(r"\n[ \t]*\n(?:[ \t]*\n)+", "\n\n", cleaned)


def body_files(command):
    try:
        words = shlex.split(command, posix=True)
    except ValueError:
        return []
    paths = []
    for i, word in enumerate(words):
        for flag in BODY_FILE_FLAGS:
            if word == flag and i + 1 < len(words):
                paths.append(words[i + 1])
            elif word.startswith(flag + "="):
                paths.append(word[len(flag) + 1:])
    return [p.split("=@", 1)[-1].lstrip("@") for p in paths if p not in ("-", "")]


def main():
    event = json.load(sys.stdin)
    tool_input = event.get("tool_input") or {}
    command = tool_input.get("command") or ""
    if not GH.search(command):
        return

    for path in body_files(command):
        try:
            with open(path, encoding="utf-8") as f:
                text = f.read()
        except OSError:
            continue
        cleaned = strip(text)
        if cleaned != text:
            with open(path, "w", encoding="utf-8") as f:
                f.write(cleaned)

    cleaned = strip(command)
    if cleaned == command:
        return
    print(json.dumps({
        "hookSpecificOutput": {
            "hookEventName": "PreToolUse",
            "permissionDecision": "allow",
            "permissionDecisionReason": "Claude attribution removed from the gh command",
            "updatedInput": {**tool_input, "command": cleaned},
        }
    }))


main()
