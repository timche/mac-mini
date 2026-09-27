#!/usr/bin/env bash
# Show a markdown file in glow, in a Herdr pane beside the caller, or close
# that pane again.
# Usage: preview.sh <file.md> [width]
#        preview.sh close
set -euo pipefail

mode="${1:?usage: preview.sh <file.md> [width] | preview.sh close}"

if [ "${HERDR_ENV:-}" != 1 ] || [ -z "${HERDR_PANE_ID:-}" ]; then
  if [ "$mode" = close ]; then exit 0; fi
  echo "not inside Herdr; ask the user to run: ! glow -p -w ${2:-100} \"$mode\"" >&2
  exit 2
fi

glow_running() {
  herdr pane process-info --pane "$1" \
    | jq -e '.result.process_info.foreground_processes[]? | select(.name == "glow")' >/dev/null
}

find_glow_pane() {
  for candidate in $(herdr pane list --workspace "$HERDR_WORKSPACE_ID" \
    | jq -r --arg tab "$HERDR_TAB_ID" --arg me "$HERDR_PANE_ID" \
      '.result.panes[] | select(.tab_id == $tab and .pane_id != $me) | .pane_id'); do
    if glow_running "$candidate"; then
      echo "$candidate"
      return
    fi
  done
}

if [ "$mode" = close ]; then
  pane="$(find_glow_pane)"
  if [ -z "$pane" ]; then exit 0; fi
  herdr pane send-keys "$pane" q >/dev/null
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    if ! glow_running "$pane"; then break; fi
    sleep 0.2
  done
  herdr pane close "$pane" >/dev/null
  echo "closed $pane"
  exit 0
fi

file="$mode"
width="${2:-\$((\$(tput cols)-4))}"

if [ ! -f "$file" ]; then
  echo "no such file: $file" >&2
  exit 1
fi
file="$(realpath "$file")"

if ! command -v glow >/dev/null; then
  echo "glow is not installed; ask the user to install it" >&2
  exit 3
fi

# Reuse a pane in this tab that is already running glow, so repeat previews
# don't stack columns.
pane="$(find_glow_pane)"

if [ -n "$pane" ]; then
  herdr pane send-keys "$pane" q >/dev/null
  # The shell needs a moment to reclaim the terminal after less exits.
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    if ! glow_running "$pane"; then break; fi
    sleep 0.2
  done
else
  # Split so the new pane gets the longer axis; glow reads best in a column.
  layout="$(herdr pane layout --pane "$HERDR_PANE_ID")"
  read -r w h < <(echo "$layout" | jq -r --arg me "$HERDR_PANE_ID" \
    '.result.layout.panes[] | select(.pane_id == $me) | "\(.rect.width) \(.rect.height)"')
  direction=right
  if [ "$w" -lt $((h * 2)) ]; then direction=down; fi
  pane="$(herdr pane split --current --direction "$direction" --cwd "$PWD" --no-focus \
    | jq -r '.result.pane.pane_id')"
  # A command sent before the shell prints its prompt lands as pasted text.
  herdr pane wait-output "$pane" --regex '\S' --timeout 10000 >/dev/null
  sleep 0.3
fi

herdr pane run "$pane" "clear; glow -p -w $width $(printf '%q' "$file")" >/dev/null
for _ in $(seq 1 50); do
  if glow_running "$pane"; then
    echo "$pane"
    exit 0
  fi
  sleep 0.2
done
echo "glow did not start in $pane" >&2
exit 4
