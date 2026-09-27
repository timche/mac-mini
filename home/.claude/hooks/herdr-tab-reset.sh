#!/usr/bin/env bash
# Restores this Herdr tab's default label when a session is cleared.
#
# The lead skill names the tab after the task so the tab strip is Tim's
# overview of what every session is building, and marks it with a check mark
# when the work ships. Clearing the session starts a different task, so the old
# name outlives what it described; SessionStart with matcher "clear" is the
# only hook that fires there.
#
# Herdr has no unset: renaming to an empty string leaves the tab visibly blank,
# so the default has to be rebuilt. It is the tab's 1-based position in its
# workspace, which is the order `tab list` returns tabs in.
#
# Silent throughout. Stdout from a SessionStart hook lands in the session
# context, and a session that just cleared should start on an empty one.

set -uo pipefail

[ "${HERDR_ENV:-}" = "1" ] || exit 0
[ -n "${HERDR_TAB_ID:-}" ] || exit 0
[ -n "${HERDR_WORKSPACE_ID:-}" ] || exit 0

herdr="${HERDR_BIN_PATH:-herdr}"
command -v "$herdr" >/dev/null 2>&1 || exit 0
command -v python3 >/dev/null 2>&1 || exit 0

position="$("$herdr" tab list 2>/dev/null | python3 -c '
import json, os, sys

try:
    tabs = json.load(sys.stdin)["result"]["tabs"]
except Exception:
    raise SystemExit(0)

workspace = os.environ["HERDR_WORKSPACE_ID"]
tab = os.environ["HERDR_TAB_ID"]
siblings = [t for t in tabs if t.get("workspace_id") == workspace]
for index, sibling in enumerate(siblings, start=1):
    if sibling.get("tab_id") == tab:
        print(index)
        break
' 2>/dev/null)"

[ -n "$position" ] || exit 0

"$herdr" tab rename "$HERDR_TAB_ID" "$position" >/dev/null 2>&1 || true
