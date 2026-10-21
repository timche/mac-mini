#!/bin/bash

# Turn the mochi plugin on in each repository mochi serves, and nowhere else. Its
# skill and its session hooks are for sessions in those repositories alone, so
# settings.json declares the marketplace it comes from and turns it off for every
# session; a repository's own .claude/settings.local.json, which Claude Code reads
# above the user's settings, turns it back on there. That file is per repository
# and per person, and gitignored where Claude Code made it, so the plugin reaches
# nobody else who clones the repository.
#
# The repositories are the ones ~/.config/mochi/config.json maps a server to,
# which is what mochi itself starts sessions in. Run by install.sh, after the link
# to that file is in place.
#
# Merged into the file with jq rather than written whole, since Claude Code keeps
# its own keys there, such as the permissions a session was granted; a file jq
# cannot read is reported and left alone. A repository that is not checked out
# yet, or whose ignore rules do not cover the file, is reported and not fatal:
# the plugin is on the next time this runs after the checkout exists.
#
# Safe to re-run: a repository that already has it on is left alone.

set -euo pipefail

plugin="mochi@mac-mini"
config="${MOCHI_CONFIG:-$HOME/.config/mochi/config.json}"

if [ ! -f "$config" ]; then
  echo "there is no $config, so the mochi plugin is on in no repository" >&2
  exit 0
fi

repos="$(jq -r '.server[]?.repo // empty' "$config")" || {
  echo "$config could not be read, so the mochi plugin is on in no repository" >&2
  exit 0
}

while IFS= read -r repo; do
  [ -n "$repo" ] || continue
  case $repo in
    "~") repo=$HOME ;;
    "~/"*) repo="$HOME/${repo#"~/"}" ;;
  esac
  if ! git -C "$repo" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    echo "mochi serves $repo, which is not a checkout yet, so the mochi plugin is not on there" >&2
    continue
  fi

  settings="$repo/.claude/settings.local.json"
  if [ -f "$settings" ] &&
     jq -e --arg id "$plugin" '.enabledPlugins[$id] == true' "$settings" >/dev/null 2>&1; then
    echo "the mochi plugin is already on in $repo"
  else
    mkdir -p "$repo/.claude"
    tmp="$(mktemp "$settings.XXXXXX")"
    if { if [ -e "$settings" ]; then cat "$settings"; else echo '{}'; fi; } |
       jq -e --arg id "$plugin" 'if type == "object" then .enabledPlugins[$id] = true else error end' >"$tmp" 2>/dev/null; then
      chmod 644 "$tmp"
      mv "$tmp" "$settings"
      echo "turned the mochi plugin on in $repo"
    else
      rm -f "$tmp"
      echo "$settings is not a JSON object jq can add to, so the mochi plugin is not on in $repo" >&2
    fi
  fi

  # Asked of git itself, since the rule can be in the repository's own ignore files or
  # in the global one this repository installs at ~/.config/git/ignore.
  if ! git -C "$repo" check-ignore -q .claude/settings.local.json; then
    echo "warning: .claude/settings.local.json is not gitignored in $repo, so the" \
         "mochi plugin's setting there could be committed for everyone" >&2
  fi
done <<<"$repos"
