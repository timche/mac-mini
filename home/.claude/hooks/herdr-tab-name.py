#!/usr/bin/env python3
"""Names this Herdr tab after the title Claude Code gives its session.

Claude Code writes that title into the transcript as an `ai-title` line early in
the first turn, and writes a new one when the session's subject moves, so the
last one is current. Asking the session to rename its own tab was followed only
some of the time; this needs nothing from the model.

A tab is ours to name while it has herdr's default label, its number, or still
carries the label this hook last gave it. Anything else was named by somebody
else, by hand, by the lead skill or by mochi, and is left alone until /clear puts
the number back (herdr-tab-reset.sh). Herdr keeps no record of who set a label,
so the one this hook set is kept per tab in CACHE.

Silent and always exits 0: it runs on every tool call, and stdout from some of
its events would land in the session's context.
"""

import json
import os
import re
import subprocess
import sys

CACHE = os.path.expanduser("~/Library/Caches/herdr-tab-name")
# The title is near the end of a long transcript, and this runs on every tool call.
TAIL_BYTES = 1 << 20
MAX_WORDS = 4


def herdr(*args):
    binary = os.environ.get("HERDR_BIN_PATH") or "herdr"
    return subprocess.run([binary, *args], capture_output=True, text=True, timeout=5)


def latest_title(path):
    try:
        with open(path, "rb") as f:
            f.seek(0, os.SEEK_END)
            f.seek(max(0, f.tell() - TAIL_BYTES))
            lines = f.read().splitlines()
    except OSError:
        return ""
    for line in reversed(lines):
        if b'"ai-title"' not in line:
            continue
        try:
            entry = json.loads(line)
        except ValueError:
            continue
        if entry.get("type") == "ai-title" and isinstance(entry.get("aiTitle"), str):
            return entry["aiTitle"]
    return ""


def kebab(title):
    words = re.sub(r"[^a-z0-9]+", " ", title.lower().replace("'", "")).split()
    return "-".join(words[:MAX_WORDS])


def main():
    if os.environ.get("HERDR_ENV") != "1" or os.environ.get("MOCHI_THREAD"):
        return
    tab = os.environ.get("HERDR_TAB_ID", "")
    if not tab:
        return
    try:
        event = json.load(sys.stdin)
    except ValueError:
        return

    label = json.loads(herdr("tab", "get", tab).stdout)["result"]["tab"]["label"]
    record = os.path.join(CACHE, re.sub(r"[^A-Za-z0-9_-]", "_", tab))
    try:
        with open(record) as f:
            ours = f.read()
    except OSError:
        ours = None
    if not label.isdigit() and label != ours:
        return

    name = kebab(latest_title(event.get("transcript_path") or ""))
    if not name or name == label:
        return
    if herdr("tab", "rename", tab, name).returncode != 0:
        return
    os.makedirs(CACHE, exist_ok=True)
    with open(record, "w") as f:
        f.write(name)


try:
    main()
except Exception:
    pass
