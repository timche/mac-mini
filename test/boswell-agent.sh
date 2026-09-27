#!/bin/bash

# boswell's LaunchAgent as launchd actually loads it, which is the one thing
# assert.sh cannot say: it reads the plist and never loads a job, and launchd
# refuses one it dislikes with an EX_CONFIG and no word about which key was wrong.
# A plist that names a $HOME where launchd expands none passes every check in
# assert.sh and starts nothing at all.
#
# So this loads the agent for real, with two substitutions: a label of its own, and
# a stand-in for boswell that reports the environment the agent's shell built for it
# and exits with a status nothing else would produce. Everything being proved is
# launchd's side — that it resolves a symlinked plist, that the HOME it hands a
# gui-domain job is what the shell expands, that the redirect opens the log, and
# that the status reaching KeepAlive is the program's — so boswell itself is beside
# the point, and a real one refuses to start without the two clones anyway.
#
# macOS only, and it wants a console login: the gui/<uid> domain an agent lives in
# belongs to one, so an SSH session against a Mac at its login window cannot run
# this. Safe on a working Mac — the label, the plist and the log are all its own,
# and it takes the job and the files away again.
#
# To add a case, add a check line: a description and a shell snippet that exits
# non-zero when the expectation is not met.

set -uo pipefail

if [ "$(uname -s)" != Darwin ]; then
  echo "  skip  boswell's LaunchAgent (launchd is macOS's)"
  exit 0
fi

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

source_plist="$repo/home/Library/LaunchAgents/io.github.timche.boswell.plist"

export label=io.github.timche.boswell-agent-test
export uid="$(id -u)"
export link="$HOME/Library/LaunchAgents/$label.plist"
export log="$HOME/Library/Logs/$label.log"

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

# Under $HOME rather than /var/folders, whose path launchd may print resolved
# through the link /var is: the check below compares the path it read.
export work="$(mktemp -d "$HOME/.boswell-agent-test.XXXXXX")"
export plist="$work/$label.plist"
stand_in="$work/boswell"

cleanup() {
  launchctl bootout "gui/$uid/$label" >/dev/null 2>&1
  rm -f "$link" "$log"
  rm -rf "$work"
}
trap cleanup EXIT

cat >"$stand_in" <<'SH'
#!/bin/sh
# What the agent's shell handed it, and a status no shell of its own would pick.
printf 'HOME=%s\nPATH=%s\nSSH_AUTH_SOCK=%s\n' "$HOME" "$PATH" "$SSH_AUTH_SOCK"
exit 7
SH
chmod +x "$stand_in"

# The repo's plist with its label, its program and its log moved aside, and nothing
# else touched: what is under test is every other key exactly as it ships.
sed -e "s|<string>io.github.timche.boswell</string>|<string>$label</string>|" \
    -e "s|/opt/homebrew/bin/boswell|$stand_in|" \
    -e "s|Logs/boswell.log|Logs/$label.log|" \
    "$source_plist" >"$plist"

# A link, because that is half of what is being proved: launchd resolves it when the
# job is bootstrapped and reads the file behind it.
mkdir -p "$HOME/Library/LaunchAgents" "$HOME/Library/Logs"
ln -sfn "$plist" "$link"

launchctl bootout "gui/$uid/$label" >/dev/null 2>&1

echo "--- launchctl bootstrap gui/$uid (a symlinked plist)"
if ! launchctl bootstrap "gui/$uid" "$link"; then
  echo "  FAIL  launchd would not load the agent — the gui/$uid domain belongs to" >&2
  echo "        a console login, which a Mac at its login window does not have" >&2
  exit 1
fi

check "the plist lints through the link" 'plutil -lint "$link"'
check "the agent is loaded" 'launchctl print "gui/$uid/$label"'
check "launchd read the file behind the link" \
  'launchctl print "gui/$uid/$label" | grep -q "path = $plist"'

# RunAtLoad, so the bootstrap above was the whole of starting it.
waited=0
while [ ! -s "$log" ] && [ "$waited" -lt 40 ]; do
  sleep 0.5
  waited=$((waited + 1))
done

check "the shell opened the log the plist names" '[ -s "$log" ]'

# The three that would each be an unexpanded ~ or $HOME in a plist launchd rendered
# nothing for: the account's home directory reached the job, and the shell spent it.
check "launchd handed the job the account's home directory" \
  'grep -qx "HOME=$HOME" "$log"'
check "the job's PATH reaches the shims and Homebrew" \
  'grep -q "^PATH=$HOME/.local/share/mise/shims:/opt/homebrew/bin:" "$log"'
check "the job reaches the ssh-agent holding the signing key" \
  'grep -qx "SSH_AUTH_SOCK=$HOME/.ssh/agent.sock" "$log"'

# What KeepAlive { SuccessfulExit = false } reads, and the reason the command execs
# rather than calling: a status of the program's own, not a shell's. 7 raw, or the
# wait status launchctl prints on the releases that report that instead.
waited=0
while ! launchctl print "gui/$uid/$label" 2>/dev/null |
         grep -qE "last exit (code|status) = (7|1792)" && [ "$waited" -lt 40 ]; do
  sleep 0.5
  waited=$((waited + 1))
done

check "the status launchd sees is the program's" \
  'launchctl print "gui/$uid/$label" | grep -qE "last exit (code|status) = (7|1792)"'

if [ "$failures" -gt 0 ]; then
  echo "  $failures check(s) failed"
  launchctl print "gui/$uid/$label" 2>&1 | sed 's/^/  /'
  sed 's/^/  /' "$log" 2>/dev/null
  exit 1
fi
