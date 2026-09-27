#!/usr/bin/env bash
# Runs the suite the way CI does — on a Mac nothing has touched — without
# touching this one.
#
#   test/tart.sh            # clone a VM, run the suite in it, delete the VM
#   test/tart.sh --keep     # leave the VM running afterwards to look at
#
# `./install.sh && test/assert.sh` on this Mac is the quick check and stays that,
# but it is a check against an account that is already built, and install.sh
# rewrites the account it runs as. What it cannot answer is the promise the suite
# actually makes: that a Mac with none of this on it comes out of a run with all
# of it. A VM cloned for the run and deleted after it answers exactly that, and
# it is the same image `tart-runner` gives a CI job, so a failure here is a
# failure there.
#
# The working tree goes in as it stands, uncommitted changes and untracked files
# included, because the point is to try a change before pushing it.

set -uo pipefail

if [ "$(uname -s)" != Darwin ]; then
  echo "mac-mini is for a Mac; this is $(uname -s)." >&2
  exit 1
fi

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# The dispatcher's image rather than the bare macOS one: install.sh starts from
# what the machine phase owes it — Homebrew, git, curl, jq — and stops with a
# message rather than carrying on without them, so a vanilla guest would only
# ever prove that.
image="${TART_TEST_IMAGE:-gha-runner-base}"
ssh_key="${TART_RUNNER_SSH_KEY:-$HOME/.ssh/tart-runner}"
vm_user="${TART_RUNNER_USER:-admin}"
cpu="${TART_TEST_CPU:-4}"
memory="${TART_TEST_MEMORY:-8192}"
vm_prefix=mac-mini-test

keep=false

for arg in "$@"; do
  case "$arg" in
    --keep) keep=true ;;
    -h | --help)
      echo "usage: test/tart.sh [--keep]"
      echo
      echo "Runs install.sh and test/assert.sh twice, then test/boswell-agent.sh,"
      echo "in a Tart macOS VM cloned from $image and deleted afterwards."
      echo "--keep leaves the VM running so a failure can be looked at."
      exit 0
      ;;
    *)
      echo "test/tart.sh: unknown option $arg" >&2
      exit 2
      ;;
  esac
done

say() { printf 'test/tart.sh: %s\n' "$1"; }

die() {
  say "$1" >&2
  exit 1
}

for tool in tart jq git; do
  command -v "$tool" >/dev/null 2>&1 ||
    die "$tool is missing — it is Homebrew's, and the Brewfile has it"
done

vms_dir="${TART_HOME:-$HOME/.tart}/vms"

[ -d "$vms_dir/$image" ] ||
  die "no image called $image — build one with: tart-runner --build-image"

[ -f "$ssh_key" ] ||
  die "no key at $ssh_key — tart-runner --build-image makes one and puts it in the image"

# The same arithmetic tart-runner does: 8 GB for this guest, 8 for UTM's Windows
# one, on a 32 GB Mac that is also somebody's working machine.
utmctl=/Applications/UTM.app/Contents/MacOS/utmctl
if [ -x "$utmctl" ] && "$utmctl" list 2>/dev/null | tail -n +2 | grep -qi '^started'; then
  die "UTM has a VM running; two guests do not fit in 32 GB"
fi

# Not `tart list`: Tart 2.38 answers it by reading every VM's disk image, and a
# running VM holds its own, so the listing fails for exactly as long as there is
# something to find. The process table cannot be taken out by what it is asked
# about.
if pgrep -qf "tart run "; then
  die "another Tart VM is already running"
fi

# mkdir rather than flock, which macOS does not have, and taken over by pid when
# the holder is gone — a run killed outright reaches no trap.
cache="${XDG_CACHE_HOME:-$HOME/.cache}"
[ -d "$HOME/Library/Caches" ] && cache="$HOME/Library/Caches"
lock="$cache/tart-test.lock"
mkdir -p "$cache"

while ! mkdir "$lock" 2>/dev/null; do
  holder="$(cat "$lock/pid" 2>/dev/null)"

  if [ -z "$holder" ] || ! kill -0 "$holder" 2>/dev/null; then
    say "taking over the lock left behind by ${holder:+pid }${holder:-an earlier run}"
    rm -rf "$lock"
    continue
  fi

  die "a suite run is already going as pid $holder"
done

echo $$ >"$lock/pid"

vm="$vm_prefix-$$"
ip=

# shellcheck disable=SC2329 # run from the traps below
cleanup() {
  if [ "$keep" = true ]; then
    say "leaving $vm running at ${ip:-its address}; delete it with: tart stop $vm && tart delete $vm"
  else
    tart stop "$vm" >/dev/null 2>&1
    tart delete "$vm" >/dev/null 2>&1
  fi
  rm -rf "$lock"
}

trap cleanup EXIT
trap 'cleanup; exit 130' INT TERM

# A run killed outright leaves its VM behind, and the next one is what sweeps it.
for dir in "$vms_dir/$vm_prefix-"*; do
  [ -d "$dir" ] || continue
  stale="$(basename "$dir")"
  say "sweeping $stale, left behind by an earlier run"
  tart stop "$stale" >/dev/null 2>&1
  tart delete "$stale" >/dev/null 2>&1
done

ssh_vm() {
  ssh -i "$ssh_key" \
    -o IdentitiesOnly=yes \
    -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile=/dev/null \
    -o LogLevel=ERROR \
    -o ConnectTimeout=10 \
    -o ServerAliveInterval=30 \
    "$vm_user@$ip" "$@"
}

say "cloning $image to $vm"
tart clone "$image" "$vm" || exit 1
# A fresh MAC, or `tart ip` answers from the lease the last clone of this image
# left behind and hands back an address before the VM has booted.
tart set "$vm" --cpu "$cpu" --memory "$memory" --random-mac || exit 1

tart run --no-graphics "$vm" >"${TMPDIR:-/tmp}/$vm.log" 2>&1 &

ip="$(tart ip "$vm" --wait 180 2>/dev/null)"
[ -n "$ip" ] || die "no IP from $vm after three minutes"

say "waiting for SSH on $ip"
deadline=$((SECONDS + 300))
until ssh_vm true >/dev/null 2>&1; do
  [ "$SECONDS" -lt "$deadline" ] || die "no SSH on $vm at $ip after five minutes"
  sleep 5
done

# The tree as it stands rather than a clone of the branch: a change is worth
# trying before it is pushed, and boswell pushes everything here within seconds
# anyway. `.git` goes too, because install.sh asks whether the checkout is a work
# tree before it starts boswell, which is one of the things being tested.
say "copying the working tree in"
{
  echo .git
  git -C "$repo" ls-files -co --exclude-standard
} | tar -cf - -C "$repo" -T - |
  ssh_vm 'rm -rf ~/mac-mini && mkdir -p ~/mac-mini && tar -xf - -C ~/mac-mini' ||
  die "could not copy the working tree into $vm"

say "running the suite in $vm"
echo

# The script goes over as a file and is run from there rather than down ssh's
# stdin as `bash -s`: bash reads a piped script a block at a time and interleaves
# running it, so any child that touches stdin eats the rest of it — which is how
# a `tar` in the image build once stopped a third of the way through an archive
# and still reported success. install.sh runs plenty that reads stdin.
ssh_vm 'cat >/tmp/suite.sh' <<'SUITE'
set -uo pipefail
cd "$HOME/mac-mini"
chmod +x install.sh test/*.sh

status=0
for step in "./install.sh" "test/assert.sh" "./install.sh" "test/assert.sh" "test/boswell-agent.sh"; do
  printf '\n===== %s =====\n\n' "$step"
  if ! $step; then
    printf '\n===== %s FAILED =====\n' "$step"
    status=1
    break
  fi
done

exit "$status"
SUITE

# -l, because `ssh host cmd` runs a non-login shell whose PATH is four system
# directories: the image keeps Homebrew and the rest in .zprofile, and a run that
# starts without them fails on the first `brew` install.sh reaches.
ssh_vm 'bash -l /tmp/suite.sh' </dev/null
status=$?

echo
if [ "$status" -eq 0 ]; then
  say "the suite passed on a fresh macOS install"
else
  say "the suite failed in $vm with status $status"
fi

exit "$status"
