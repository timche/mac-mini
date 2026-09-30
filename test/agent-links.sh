#!/bin/bash

# The agent as launchd actually loads it: claude/ssh-agent.sh against the account
# this runs as, with no service-account token anywhere — which is every Mac between
# machine.sh and the first signing-key.sh, and enough to prove the plumbing. The
# agent comes up empty and stays up; the key itself is signing-agent.sh's, with a
# stub 1Password and a HOME of its own.
#
# What it is here for is the two things the links have to make true: that launchd
# accepts a plist that is a symlink into the checkout, and that a wrapper which
# changed in the checkout restarts the job rather than being ignored.
#
# It refuses to run outside CI unless MAC_MINI_TEST_ANYWAY=1, for the same reason
# signing-agent.sh does — launchd keys a job by label per account, so this bounces
# the agent holding the real key — and because it edits launchd/agent.sh in the
# checkout to make the restart happen.
#
# To add a case, add a check line: a description and a shell snippet that exits
# non-zero when the expectation is not met.

set -uo pipefail

export PATH="/opt/homebrew/bin:/opt/homebrew/sbin:$PATH"
export root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

export label=io.github.timche.ssh-agent
export uid="$(id -u)"
export plist="$HOME/Library/LaunchAgents/$label.plist"
export agent="$HOME/.ssh/agent.sh"
export stamp="$HOME/.ssh/agent.sh.sha256"
export socket="$HOME/.ssh/agent.sock"
export log="$HOME/Library/Logs/ssh-agent.log"

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

if [ "${CI:-}" != true ] && [ "${MAC_MINI_TEST_ANYWAY:-}" != 1 ]; then
  echo "agent-links.sh loads the LaunchAgent labelled $label, which is the label" >&2
  echo "the real agent uses, and edits launchd/agent.sh while it does — run it on" >&2
  echo "a throwaway machine, or set MAC_MINI_TEST_ANYWAY=1 if you are certain." >&2
  exit 1
fi

was_loaded=false
launchctl print "gui/$uid/$label" >/dev/null 2>&1 && was_loaded=true

wrapper="$(mktemp)"
cp "$root/launchd/agent.sh" "$wrapper"

# The stamp is what claude/ssh-agent.sh compares a wrapper against, and the edit
# below moves it: left as it is, the next run would kickstart the job over a
# wrapper this script has already put back.
stamp_before="$(mktemp)"
had_stamp=false
if [ -f "$stamp" ]; then
  cp "$stamp" "$stamp_before"
  had_stamp=true
fi

cleanup() {
  cp "$wrapper" "$root/launchd/agent.sh"
  rm -f "$wrapper"

  if [ "$had_stamp" = true ]; then
    cp "$stamp_before" "$stamp"
  else
    rm -f "$stamp"
  fi
  rm -f "$stamp_before"

  if [ "$was_loaded" = false ]; then
    launchctl bootout "gui/$uid/$label" >/dev/null 2>&1
  fi
}
trap cleanup EXIT

job_pid() {
  launchctl print "gui/$uid/$label" 2>/dev/null | awk '$1 == "pid" { print $3 }'
}

echo "--- claude/ssh-agent.sh"
if ! "$root/claude/ssh-agent.sh"; then
  echo "  FAIL  ssh-agent.sh exited non-zero"
  failures=$((failures + 1))
fi

check "the wrapper in ~/.ssh is a link into the checkout" \
  '[ "$(readlink "$agent")" = "$root/launchd/agent.sh" ]'
check "the plist in ~/Library/LaunchAgents is a link into the checkout" \
  '[ "$(readlink "$plist")" = "$root/launchd/$label.plist" ]'
check "the plist lints through the link" 'plutil -lint "$plist"'
check "the agent is loaded" 'launchctl print "gui/$uid/$label"'
check "launchd read the plist in the checkout" \
  'launchctl print "gui/$uid/$label" |
     grep -qE "path = .*/launchd/io\.github\.timche\.ssh-agent\.plist"'
check "the hash of the wrapper was recorded" \
  '[ "$(cat "$stamp")" = "$(shasum -a 256 "$root/launchd/agent.sh" | awk "{ print \$1 }")" ]'

# The wrapper resolved $HOME for itself, which is the whole of what the plist
# leaves to it: it found the socket, and it opened the log.
waited=0
while { [ ! -S "$socket" ] || [ ! -s "$log" ]; } && [ "$waited" -lt 20 ]; do
  sleep 0.5
  waited=$((waited + 1))
done

check "the agent is listening on the socket the dotfiles name" '[ -S "$socket" ]'
check "the wrapper opened the log itself" '[ -s "$log" ]'

export first_pid="$(job_pid)"

echo "--- claude/ssh-agent.sh again, with nothing changed"
export said="$(mktemp)"
"$root/claude/ssh-agent.sh" >"$said" 2>&1
sed 's/^/  /' "$said"

export second_pid="$(job_pid)"

check "a second run says so rather than reloading" 'grep -q "already loaded" "$said"'
check "a second run left the job alone" \
  '[ -n "$first_pid" ] && [ "$first_pid" = "$second_pid" ]'
rm -f "$said"

# A pull that moves the checkout to a commit touching the wrapper, which is the one
# thing a link cannot show by itself.
echo "--- claude/ssh-agent.sh after the wrapper changed in the checkout"
printf '\n# changed by test/agent-links.sh\n' >>"$root/launchd/agent.sh"
"$root/claude/ssh-agent.sh" 2>&1 | sed 's/^/  /'

# kickstart returns before the replacement has a pid of its own, and between the
# two the job has none at all — so an empty reading is the restart in progress
# rather than the end of the wait.
waited=0
third_pid="$(job_pid)"
while { [ -z "$third_pid" ] || [ "$third_pid" = "$second_pid" ]; } &&
      [ "$waited" -lt 20 ]; do
  sleep 0.5
  waited=$((waited + 1))
  third_pid="$(job_pid)"
done

export third_pid

check "a changed wrapper restarted the job" \
  '[ -n "$third_pid" ] && [ "$third_pid" != "$second_pid" ]'
check "the recorded hash moved with it" \
  '[ "$(cat "$stamp")" = "$(shasum -a 256 "$root/launchd/agent.sh" | awk "{ print \$1 }")" ]'

if [ "$failures" -gt 0 ]; then
  echo "  $failures check(s) failed"
  sed 's/^/  /' "$log" 2>/dev/null
  exit 1
fi
