#!/usr/bin/env bash
# Puts the command that names this Herdr tab in front of every prompt.
#
# The tab strip is Tim's overview of what every session is doing, and an
# instruction in CLAUDE.md alone was followed only some of the time. A prompt
# is where a task starts or changes, so UserPromptSubmit is where the session
# is told, more firmly while the tab still has herdr's default label, which is
# its number.
#
# The tab id is spelled out rather than left as $HERDR_TAB_ID because
# EnterWorktree's isolation guard refuses any command that expands a variable.
#
# A mochi session is left alone: mochi names that tab after its Discord thread.

set -uo pipefail

[ "${HERDR_ENV:-}" = "1" ] || exit 0
[ -n "${HERDR_TAB_ID:-}" ] || exit 0
[ -z "${MOCHI_THREAD:-}" ] || exit 0

herdr="${HERDR_BIN_PATH:-herdr}"
command -v "$herdr" >/dev/null 2>&1 || exit 0
command -v python3 >/dev/null 2>&1 || exit 0

label="$("$herdr" tab get "$HERDR_TAB_ID" 2>/dev/null | python3 -c '
import json, sys

try:
    print(json.load(sys.stdin)["result"]["tab"]["label"])
except Exception:
    pass
' 2>/dev/null)"

command="herdr tab rename $HERDR_TAB_ID <label>"

case "$label" in
  '' | *[!0-9]*)
    echo "This herdr tab is named \"$label\". If this prompt changes the task, rename it in two or three kebab-case words before anything else: \`$command\`."
    ;;
  *)
    echo "This herdr tab still has its default label. Before anything else, and once you know what the task is, name it in two or three kebab-case words: \`$command\`."
    ;;
esac
