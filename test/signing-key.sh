#!/bin/bash

# The signing key driven end to end with 1Password stubbed out: a key generated
# here, a stub op that serves its two halves, and then the real signing-key.sh.
# What it proves is the part that matters — that the key lands in ~/.ssh as a
# private file, that a commit signs with it and verifies, and that nothing else
# can sign once the file is gone.
#
# Everything runs against a throwaway HOME, since what the script writes is two
# files under it and a trust list beside them.
#
# To add a case, add a check line: a description and a shell snippet that exits
# non-zero when the expectation is not met.

set -uo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export PATH="/opt/homebrew/bin:/opt/homebrew/sbin:$PATH"

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

# Everything else here lands in the throwaway HOME below, but signing-key.sh also
# calls register-signing-key.sh, and on a Mac with gh logged in that would put a
# throwaway key on the real GitHub account.
if [ "${CI:-}" != true ] && [ "${MAC_MINI_TEST_ANYWAY:-}" != 1 ]; then
  echo "signing-key.sh runs the real claude/signing-key.sh, which would register its" >&2
  echo "throwaway key on whatever GitHub account gh is logged in to — run it on a" >&2
  echo "throwaway machine, or set MAC_MINI_TEST_ANYWAY=1 if you are certain." >&2
  exit 1
fi

work="$(mktemp -d)"
home="$work/home"

cleanup() {
  rm -rf "$work"
}
trap cleanup EXIT

mkdir -p "$home"

ssh-keygen -q -t ed25519 -N '' -C 'test signing key' -f "$work/key"

mkdir -p "$work/bin"
cat >"$work/bin/op" <<STUB
#!/bin/bash

# Two fields of one item, and a service-account token it insists on being handed:
# carrying that token from the file into op is half of what this suite is testing,
# and a stub that ignored it would pass without it.
if [ -z "\${OP_SERVICE_ACCOUNT_TOKEN:-}" ]; then
  echo "stub op: no service account token in the environment" >&2
  exit 1
fi

case "\${1:-} \${2:-}" in
  "read op://dev/ssh-commit-signing/private key?ssh-format=openssh")
    echo private >>"$work/reads"
    exec cat "$work/key"
    ;;
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

# What home/.gitconfig carries on the real machine, `~` and all, since git is what
# expands that and the principal in allowed_signers has to be the address the
# commits are authored under.
HOME="$home" git config --global user.name "Test Signer"
HOME="$home" git config --global user.email signer@example.com
HOME="$home" git config --global gpg.format ssh
HOME="$home" git config --global user.signingkey '~/.ssh/id_signing.pub'
HOME="$home" git config --global gpg.ssh.allowedSignersFile '~/.ssh/allowed_signers'
HOME="$home" git config --global commit.gpgsign true

echo "--- claude/signing-key.sh"
if ! HOME="$home" "$root/claude/signing-key.sh"; then
  echo "  FAIL  signing-key.sh exited non-zero"
  failures=$((failures + 1))
fi

export root home work

# GitHub and the trust list both keep the type and the body and drop the comment.
export signer_line="signer@example.com $(awk '{print $1" "$2}' "$work/key.pub")"

check "the key is trusted under the authoring address" \
  'grep -qxF "$signer_line" "$home/.ssh/allowed_signers"'

check "the directory holding the key is the account's alone" \
  '[ "$(stat -f %Lp "$home/.ssh")" = 700 ]'
check "the private half is a file nobody else can read" \
  '[ -f "$home/.ssh/id_signing" ] &&
   [ "$(stat -f %Lp "$home/.ssh/id_signing")" = 600 ]'
check "the private half is the key 1Password served" \
  '[ "$(ssh-keygen -y -f "$home/.ssh/id_signing" | awk "{print \$1\" \"\$2}")" = \
     "$(awk "{print \$1\" \"\$2}" "$work/key.pub")" ]'
check "the public half is beside it, and is the same key" \
  '[ -f "$home/.ssh/id_signing.pub" ] &&
   [ "$(awk "{print \$1\" \"\$2}" "$home/.ssh/id_signing.pub")" = \
     "$(awk "{print \$1\" \"\$2}" "$work/key.pub")" ]'

# Nothing is left behind in the directory the key is staged in, which is ~/.ssh
# itself: a temporary file there holding a private key would be a second copy
# nothing rotates.
check "no staging file was left in .ssh" \
  '[ -z "$(find "$home/.ssh" -name ".id_signing.*" -print -quit)" ]'

# The assertion the rest is for: git finds the key through the `~` in its own
# config, signs with no agent anywhere, and verifies against the trust list
# signing-key.sh wrote.
check "a commit signs from the key file and verifies" \
  'mkdir -p "$work/repo" && cd "$work/repo" && HOME="$home" git init -q . &&
   HOME="$home" SSH_AUTH_SOCK= git commit --allow-empty -q -m signed &&
   [ "$(HOME="$home" SSH_AUTH_SOCK= git log --format=%G? -1)" = G ]'

# The other side of the same claim: with the private half out of reach there is
# nothing left to sign with, which is what proves the signature came from that file
# and not from an agent this suite failed to notice.
check "a commit cannot sign with the private half out of reach" \
  'cd "$work/repo" && mv "$home/.ssh/id_signing" "$work/moved" &&
   ! HOME="$home" SSH_AUTH_SOCK= git commit --allow-empty -q -m unsigned;
   status=$?; mv "$work/moved" "$home/.ssh/id_signing"; [ $status -eq 0 ]'

# Documented as safe to re-run, so prove it: nothing may be rewritten, and the key
# has to come out of it still signing.
echo "--- claude/signing-key.sh again"
if ! HOME="$home" "$root/claude/signing-key.sh"; then
  echo "  FAIL  signing-key.sh is not idempotent"
  failures=$((failures + 1))
fi

check "the key still verifies after a second run" \
  'cd "$work/repo" &&
   HOME="$home" SSH_AUTH_SOCK= git commit --allow-empty -q -m again &&
   [ "$(HOME="$home" SSH_AUTH_SOCK= git log --format=%G? -1)" = G ]'
check "the trust list did not collect a second copy of the key" \
  '[ "$(wc -l <"$home/.ssh/allowed_signers")" -eq 1 ]'

# Every `op read` spends part of the service account's day, so a re-run against a
# key that is already there reads the public field to compare against and stops.
check "the second run did not read the private half again" \
  '[ "$(wc -l <"$work/reads")" -eq 1 ]'

if [ "$failures" -gt 0 ]; then
  echo "  $failures check(s) failed"
  exit 1
fi
