#!/bin/bash

# The signing key and its agent, driven end to end with 1Password stubbed out: a
# key generated here, a stub op that serves its two halves, and then the real
# signing-key.sh, the real plist and the real wrapper. What it proves is the part
# that matters — that a commit signs with a key no file on the machine holds.
#
# Everything runs against a throwaway HOME. launchd hands a gui-domain agent the
# account's home directory rather than this one, and the plist leaves every path to
# it — so ssh-agent.sh installs its links here and deliberately loads nothing,
# and the wrapper is run the same way launchd runs it, with a HOME of its own.
# test/agent-links.sh is the other half of this, and covers what launchd does with
# those links for the account it really belongs to.
#
# To add a case, add a check line: a description and a shell snippet that exits
# non-zero when the expectation is not met.

set -uo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export PATH="/opt/homebrew/bin:/opt/homebrew/sbin:$PATH"

label=io.github.timche.ssh-agent
uid="$(id -u)"

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

# Everything else here lands in the throwaway HOME below, and ssh-agent.sh loads no
# job for a HOME that is not the account's — but signing-key.sh also calls
# register-signing-key.sh, and on a Mac with gh logged in that would put a throwaway
# key on the real GitHub account.
if [ "${CI:-}" != true ] && [ "${MAC_MINI_TEST_ANYWAY:-}" != 1 ]; then
  echo "signing-agent.sh runs the real signing-key.sh, which would register its" >&2
  echo "throwaway key on whatever GitHub account gh is logged in to — run it on a" >&2
  echo "throwaway machine, or set MAC_MINI_TEST_ANYWAY=1 if you are certain." >&2
  exit 1
fi

# Outside TMPDIR and short, because the agent socket lives inside this directory
# and a unix socket path runs out at about a hundred characters — the per-user
# TMPDIR macOS hands out is long enough for that to matter.
work="$(mktemp -d /tmp/mac-mini-test.XXXXXX)"
home="$work/home"
socket="$home/.ssh/agent.sock"
agent_log="$home/Library/Logs/ssh-agent.log"

was_loaded=false
launchctl print "gui/$uid/$label" >/dev/null 2>&1 && was_loaded=true

wrapper_pid=""

cleanup() {
  if [ -n "$wrapper_pid" ]; then
    kill "$wrapper_pid" >/dev/null 2>&1
  fi

  # Only what this run loaded. A Mac that had the agent already is one this suite
  # refused to run on, but the check costs nothing and the mistake is expensive.
  if [ "$was_loaded" = false ]; then
    launchctl bootout "gui/$uid/$label" >/dev/null 2>&1
  fi

  rm -rf "$work"
}
trap cleanup EXIT

mkdir -p "$home"

# The key 1Password is standing in for, which nothing may end up writing to disk
# inside $home.
ssh-keygen -q -t ed25519 -N '' -C 'test signing key' -f "$work/key"

mkdir -p "$work/bin"
cat >"$work/bin/op" <<STUB
#!/bin/bash

# Two fields of one item, and a service-account token it insists on being handed:
# carrying that token from the file through launchd into op is half of what this
# suite is testing, and a stub that ignored it would pass without it.
if [ -z "\${OP_SERVICE_ACCOUNT_TOKEN:-}" ]; then
  echo "stub op: no service account token in the environment" >&2
  exit 1
fi

case "\${1:-} \${2:-}" in
  "read op://dev/ssh-commit-signing/private key?ssh-format=openssh") exec cat "$work/key" ;;
  "read op://dev/ssh-commit-signing/public key") exec cat "$work/key.pub" ;;
esac

echo "stub op: unexpected: \$*" >&2
exit 1
STUB
chmod 755 "$work/bin/op"

# In front of Homebrew's, which is why nothing in the scripts re-prepends the brew
# prefix to a PATH that already reaches it.
export PATH="$work/bin:$PATH"

install -d -m 700 "$home/.config/op"
printf 'ops_stub_token' >"$home/.config/op/service-account-token"
chmod 600 "$home/.config/op/service-account-token"

# What home/.gitconfig carries on the real machine: no key named at
# all, git asking the agent for one instead, and a principal in allowed_signers that
# has to be the address the commits are authored under.
HOME="$home" git config --global user.name "Test Signer"
HOME="$home" git config --global user.email signer@example.com
HOME="$home" git config --global gpg.format ssh
HOME="$home" git config --global gpg.ssh.defaultKeyCommand "ssh-add -L"
HOME="$home" git config --global gpg.ssh.allowedSignersFile "$home/.ssh/allowed_signers"
HOME="$home" git config --global commit.gpgsign true

echo "--- claude/signing-key.sh"
if ! HOME="$home" "$root/claude/signing-key.sh"; then
  echo "  FAIL  signing-key.sh exited non-zero"
  failures=$((failures + 1))
fi

export root home socket agent_log work label uid

# GitHub and the trust list both keep the type and the body and drop the comment.
export signer_line="signer@example.com $(awk '{print $1" "$2}' "$work/key.pub")"

check "the key is trusted under the authoring address" \
  'grep -qxF "$signer_line" "$home/.ssh/allowed_signers"'

# The whole point of the 1Password half: the private key is in a process and
# nowhere else. A grep rather than a name, because what must not be there is the
# content and not a particular path.
check "no private key was written anywhere in .ssh" \
  '! grep -rlq "PRIVATE KEY" "$home/.ssh"'

# Nor the public half. 1Password is the one place the key is kept, git asks the
# agent for it, and the trust list is the only thing derived from it — so a copy on
# disk would be a second source to rotate and to get wrong.
check "no public key file was written under .ssh either" \
  '[ -z "$(find "$home/.ssh" -name "*.pub" -print -quit)" ]'
check "nothing under .ssh is named after the account" \
  '[ ! -e "$home/.ssh/claude" ] && [ ! -e "$home/.ssh/claude.pub" ]'

export plist="$home/Library/LaunchAgents/$label.plist"

check "the agent plist is a link to the one in the repo" \
  '[ "$(readlink "$plist")" = "$root/launchd/$label.plist" ]'
check "the plist is valid through the link" 'plutil -lint "$plist"'
check "the wrapper is a link to the one in the repo" \
  '[ "$(readlink "$home/.ssh/agent.sh")" = "$root/launchd/agent.sh" ]'
check "the plist leaves every path to the wrapper and \$HOME" \
  '[ "$(plutil -extract ProgramArguments.2 raw -o - "$plist")" = \
     "exec \"\$HOME/.ssh/agent.sh\"" ] &&
   ! plutil -extract EnvironmentVariables raw -o - "$plist" &&
   ! plutil -extract StandardOutPath raw -o - "$plist"'
check "launchd restarts the agent whenever it exits" \
  'plutil -extract KeepAlive xml1 -o - "$plist" | grep -q "<true/>"'

# Run the way launchd runs it, which is with a HOME and nothing else: the socket, the
# log and the token file are all derived from it, and the stub op is on PATH.
HOME="$home" "$home/.ssh/agent.sh" &
wrapper_pid=$!

# The key comes through a network call on a real Mac, so neither path has it the
# moment the job starts.
waited=0
while [ "$waited" -lt 30 ]; do
  if SSH_AUTH_SOCK="$socket" ssh-add -l >/dev/null 2>&1; then
    break
  fi
  sleep 1
  waited=$((waited + 1))
done

export fingerprint="$(ssh-keygen -lf "$work/key.pub" | awk '{print $2}')"

check "the agent is listening on the socket the dotfiles name" '[ -S "$socket" ]'
check "the wrapper opened its own log, which the plist does not name" \
  '[ -s "$agent_log" ]'
check "the agent holds the key from 1Password" \
  'SSH_AUTH_SOCK="$socket" ssh-add -l | grep -qF "$fingerprint"'

# The assertion the rest is for: git asks the agent which key to sign with, having
# no file to be told by, and verifies against the trust list signing-key.sh wrote.
check "a commit signs through the agent and verifies" \
  'mkdir -p "$work/repo" && cd "$work/repo" && HOME="$home" git init -q . &&
   HOME="$home" SSH_AUTH_SOCK="$socket" git commit --allow-empty -q -m signed &&
   [ "$(HOME="$home" SSH_AUTH_SOCK="$socket" git log --format=%G? -1)" = G ]'

# The other side of the same claim: with the agent out of reach there is nothing
# left to sign with, which is what proves the signature came from it and not from a
# file this suite failed to notice.
check "a commit cannot sign with the agent out of reach" \
  'cd "$work/repo" &&
   ! HOME="$home" SSH_AUTH_SOCK= git commit --allow-empty -q -m unsigned'

# Documented as safe to re-run, so prove it: nothing may be rewritten, and the
# agent has to come out of it holding the same key.
echo "--- claude/signing-key.sh again"
if ! HOME="$home" "$root/claude/signing-key.sh"; then
  echo "  FAIL  signing-key.sh is not idempotent"
  failures=$((failures + 1))
fi

check "the key still verifies after a second run" \
  'cd "$work/repo" &&
   HOME="$home" SSH_AUTH_SOCK="$socket" git commit --allow-empty -q -m again &&
   [ "$(HOME="$home" SSH_AUTH_SOCK="$socket" git log --format=%G? -1)" = G ]'
check "the trust list did not collect a second copy of the key" \
  '[ "$(wc -l <"$home/.ssh/allowed_signers")" -eq 1 ]'

if [ "$failures" -gt 0 ]; then
  echo "  $failures check(s) failed"
  [ -f "$agent_log" ] && sed 's/^/  /' "$agent_log"
  exit 1
fi
