#!/bin/bash

# The two rules that are about the markdown in this repo rather than about the
# Mac: the always-loaded instruction file's word budget, and prose that is never
# hard-wrapped. Its own script because test.yml's paths filter keeps a
# markdown-only push off the macOS runner, so CI runs these on a Linux runner
# instead while test/assert.sh runs the same script on the Mac — which is why
# nothing here may reach for a command only macOS has.
#
# It exits with the number of checks that failed, which is what assert.sh adds to
# its own total.
#
# To add a case, add a check line: a description and a shell snippet that exits
# non-zero when the expectation is not met.

set -uo pipefail

# Exported because the snippets run in a child bash that inherits nothing else.
export repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

failures=0

check() {
  local description="$1" snippet="$2"

  if bash -c "$snippet" >/dev/null 2>&1; then
    echo "  ok    $description"
  else
    echo "  FAIL  $description"
    failures=$((failures + 1))
  fi
}

# CLAUDE.md is loaded whole into every session and adherence drops past about
# 200 lines of ordinary markdown. Counted in words, since a paragraph here is one
# line however long: 2,400 is 200 lines at the dozen words a wrapped line holds.
# What reaches the context, not what is in the file: YAML frontmatter is
# configuration and block-level HTML comments are stripped before injection, so
# counting either would charge rent on words Claude never sees. Computed here
# rather than inside the snippet, which runs through two levels of quoting.
export loaded_words="$(
  cat "$repo/home/.claude/CLAUDE.md" 2>/dev/null |
    awk '/^---$/ { fm = !fm; next } fm { next }
         /<!--/ { c = 1 } c { if (/-->/) c = 0; next }
         { print }' |
    wc -w
)"

check "the always-loaded instructions stay under 2,400 words" \
  '[ -f "$repo/home/.claude/CLAUDE.md" ] && [ "$loaded_words" -gt 0 ] &&
   [ "$loaded_words" -lt 2400 ]'

# Markdown prose is never hard-wrapped: a paragraph or list item is one line.
# Reports each line that continues the one before it, which is exactly what an
# unwrap would join; code fences, tables, headings, frontmatter and hard breaks
# are left alone. The synced skills are Anthropic's and written their own way, and
# node_modules is a Worker's dependencies — thousands of READMEs nobody here wrote,
# present on the Mac after a `npm ci` and absent on the Linux runner, which would
# make this pass or fail by where it ran.
export wrapped_prose="$(
  find "$repo" -name node_modules -prune -o -name '*.md' \
    -not -path '*/.git/*' -not -path '*/synced/*' \
    -exec awk '
      function starts_block(s) {
        return s ~ /^[ \t]*$/ || s ~ /^[ \t]*[#|><]/ || s ~ /^[ \t]*(```|~~~)/ ||
               s ~ /^[ \t]*([-*+]|[0-9]+[).])[ \t]/ || s ~ /^[ \t]*(---+|\*\*\*+|___+)[ \t]*$/
      }
      FNR == 1 { fm = ($0 == "---"); prev = ""; fence = 0; if (fm) next }
      fm { if ($0 == "---") fm = 0; next }
      /^[ \t]*(```|~~~)/ { fence = !fence; prev = ""; next }
      fence { next }
      {
        if (prev != "" && prev !~ /(  |\\)$/) {
          if (prev ~ /^>/) {
            body = $0; sub(/^> ?/, "", body)
            if ($0 ~ /^>/ && !starts_block(body)) { print FILENAME ":" FNR; found = 1 }
          } else if (!starts_block($0)) { print FILENAME ":" FNR; found = 1 }
        }
        prev = ($0 ~ /^[ \t]*$/ || $0 ~ /^[ \t]*[#|<]/ ||
                $0 ~ /^[ \t]*(---+|\*\*\*+|___+)[ \t]*$/) ? "" : $0
      }
      END { exit found }
    ' {} + 2>/dev/null
)"

check "markdown prose is not hard-wrapped" '[ -z "$wrapped_prose" ]'

# Named rather than only counted, because which line continues which is the whole
# of what makes this fixable.
[ -z "$wrapped_prose" ] || printf '%s\n' "$wrapped_prose" | sed 's/^/        /'

if [ "$failures" -gt 0 ]; then
  echo "  $failures check(s) failed"
fi

exit "$failures"
