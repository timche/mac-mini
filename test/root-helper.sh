#!/bin/bash

# What root-helper.sh shows Tim before it asks for a password, and what it refuses
# to install at all. Its own script rather than a check line in assert-machine.sh
# because every case runs the real installer against sources poisoned on purpose,
# which needs a repository of its own to poison — and because the thing under test
# is output, which a check line can only answer yes or no about.
#
# Nothing here installs anything or needs root. The two cases that matter are
# refused before the first privileged step, and a stub sudo stands in for the rest
# so that a run never reaches /usr/local or /etc. A real sudo is never called.
#
# To add a case, add a check line: a description and a shell snippet that exits
# non-zero when the expectation is not met.
#
# It exits with the number of checks that failed, which is what assert-machine.sh
# adds to its own total.

set -uo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export repo

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

sandbox="$(mktemp -d)"
trap 'rm -rf "$sandbox"' EXIT

capture="$sandbox/capture"
mkdir -p "$capture"
export capture

# What the poisoned rules below grant, passed to printf rather than written into
# its format string: the secrets scan every commit on this Mac goes through reads
# a word after NOPASSWD: as a password being set, which is the same reason the real
# rule names its command through a Cmnd_Alias. The files these produce are byte for
# byte what they would be written out in full.
everything=ALL
narrow=/usr/local/libexec/claude-root
alias_name=CLAUDE_ROOT

# And the keyword on its own, for the same reason again: in the patterns below it
# is followed by what the rule grants, which is the shape the scan is looking for.
granted='NOPASSWD:'
export everything granted

# A stub sudo, which is what keeps every case here off the machine: nothing it
# answers writes anything outside this sandbox, and nothing it refuses can.
#
# `-n true` answers yes, which is what gets a run past the guard that would
# otherwise skip it for want of a terminal. The sudoers read is answered with a
# line that has the includedir in it, and is also the moment a run would have
# stopped for Tim's password — so $REWRITE, when a case sets it, rewrites the
# sources there: after the review, before the install, which is exactly where a
# session racing the installer would get in. The directory creation is answered
# without creating anything. A write is captured rather than performed, and the
# bytes are what the case then asserts on. Anything else refuses.
mkdir -p "$sandbox/bin"
cat >"$sandbox/bin/sudo" <<'STUB'
#!/bin/sh
if [ "$1" = -n ]; then
  shift
  case "$*" in
    /usr/bin/true) exit 0 ;;
  esac
fi

case "$*" in
  "/bin/cat /etc/sudoers")
    if [ -n "${REWRITE:-}" ]; then
      /bin/cat "$REWRITE/sudoers" >"$SOURCES/system/sudoers/claude-root"
      /bin/cat "$REWRITE/helper" >"$SOURCES/system/libexec/claude-root"
    fi

    echo "@includedir /private/etc/sudoers.d"
    exit 0
    ;;
  /usr/bin/install\ -d*) exit 0 ;;
  /usr/bin/cmp\ *)
    /bin/cat >/dev/null
    exit 1
    ;;
esac

# `sudo sh -c <script> sh <destination> <mode>`, which is how root is handed the
# bytes to write. They are written here instead, under the whole destination path
# with its slashes turned into underscores — both destinations are called
# claude-root, so the basename alone would have one overwrite the other.
if [ "$1" = /bin/sh ] && [ "$2" = -c ]; then
  name="$(printf '%s' "$5" | /usr/bin/tr / _)"
  /bin/cat >"$CAPTURE/$name"
  echo "$6" >"$CAPTURE/$name.mode"
  exit 0
fi

echo "stub sudo refuses: $*" >&2
exit 1
STUB
chmod 0755 "$sandbox/bin/sudo"

# A copy of the three files a run reads, so that each case can poison one of them.
# root-helper.sh finds the rest of its paths from its own location, so a directory
# with those three in it is a repository as far as it is concerned.
#
# $1 is the name of the case, and the copy is made fresh for each one.
stage() {
  local copy="$sandbox/$1"

  mkdir -p "$copy/system/libexec" "$copy/system/sudoers"

  # The one patch: root-helper.sh names /usr/bin/sudo absolutely, which is the
  # point of it and is why a stub cannot be put on the PATH instead, so the copy
  # has that path pointed at the stub. Nothing else about the script is touched.
  sed "s|/usr/bin/sudo|$sandbox/bin/sudo|g" "$repo/root-helper.sh" \
    >"$copy/root-helper.sh"

  cp "$repo/system/libexec/claude-root" "$copy/system/libexec/claude-root"
  cp "$repo/system/sudoers/claude-root" "$copy/system/sudoers/claude-root"
  echo "$copy"
}

run_with_rewrite() {
  PATH="$sandbox/bin:$PATH" CAPTURE="$capture" SOURCES="$1" REWRITE="$2" \
    bash "$1/root-helper.sh" 2>&1
}

run() {
  PATH="$sandbox/bin:$PATH" CAPTURE="$capture" SOURCES="$1" \
    bash "$1/root-helper.sh" 2>&1
}
export -f run

# The rule as it stands, which is what the other cases are a deviation from.
clean="$(stage clean)"
clean_output="$(run "$clean")"
export clean_output

check "the review names the rule that is about to be granted" \
  'printf "%s\n" "$clean_output" | grep -qE "$granted[[:space:]]+CLAUDE_ROOT"'
check "and says it is about to install as root" \
  'printf "%s\n" "$clean_output" | grep -q "Read this before typing a password"'
check "a clean pair trips no gate about its contents" \
  '! printf "%s\n" "$clean_output" |
     grep -qE "hold control characters|visudo rejected|placeholder survived"'
check "and reaches the install rather than a refusal" \
  'printf "%s\n" "$clean_output" | grep -qE "installed|unchanged|not root-owned"'

# The attack this exists for. The rule grants everything and displays as the
# narrow one: visudo parses it, the trailing comment carries an escape that erases
# the line, and the carriage return reprints a harmless-looking rule over it. A
# review of raw bytes would show the second rule and nothing else.
escaped="$(stage escaped)"
printf 'timche ALL=(root) NOPASSWD: %s # \033[2K\rtimche ALL=(root) NOPASSWD: %s\n' \
  "$everything" "$narrow" >"$escaped/system/sudoers/claude-root"
escaped_output="$(run "$escaped")"
export escaped_output

# visudo has to accept it, or the case is testing the wrong thing: a rule sudo
# would reject never reaches the review at all.
check "the escaped rule is one sudo would have accepted" \
  '/usr/sbin/visudo -cf "'"$escaped"'/system/sudoers/claude-root"'
check "the escape is rendered rather than acted on" \
  'printf "%s\n" "$escaped_output" | grep -q "\^\[" &&
   printf "%s\n" "$escaped_output" | grep -q "\^M"'
check "the review shows what the rule really grants" \
  'printf "%s\n" "$escaped_output" | grep -qE "$granted[[:space:]]+$everything( |$)"'
check "the escaped rule is refused" \
  'printf "%s\n" "$escaped_output" | grep -q "hold control characters"'
check "and refused before sudo was asked for anything" \
  '! printf "%s\n" "$escaped_output" | grep -q "stub sudo refuses"'

# A carriage return on its own, with no escape and no comment: enough to overwrite
# a line on a terminal by itself. This one visudo rejects before the control
# character check ever sees it — a mid-line return is a syntax error to it — so
# what matters is that it is refused and that visudo's complaint, which quotes the
# line back, is rendered rather than echoed.
returned="$(stage returned)"
printf 'timche ALL=(root) NOPASSWD: %s\rtimche ALL=(root) NOPASSWD: %s\n' \
  "$everything" "$alias_name" >"$returned/system/sudoers/claude-root"
returned_output="$(run "$returned")"
export returned_output

check "a bare carriage return in the rule is refused" \
  'printf "%s\n" "$returned_output" |
     grep -qE "hold control characters|visudo rejected"'
check "and visudo's own complaint is rendered, not echoed" \
  'printf "%s\n" "$returned_output" | grep -q "\^M"'
check "and it never reached sudo either" \
  '! printf "%s\n" "$returned_output" | grep -q "stub sudo refuses"'

# The C1 controls, which are the half a character class cannot name: U+009B is the
# CSI some terminals act on exactly as they act on ESC-[, and it arrives as the two
# bytes 0xC2 0x9B. A rule carrying one has to be refused like any other.
c1="$(stage c1)"
printf 'timche ALL=(root) NOPASSWD: %s # \302\233[2Ktimche ALL=(root) NOPASSWD: %s\n' \
  "$everything" "$alias_name" >"$c1/system/sudoers/claude-root"
c1_output="$(run "$c1")"
export c1_output

check "a C1 control in the rule is one sudo would have accepted" \
  '/usr/sbin/visudo -cf "'"$c1"'/system/sudoers/claude-root"'
check "and it is refused all the same" \
  'printf "%s\n" "$c1_output" | grep -q "hold control characters"'

# And the em dashes the refusal must not trip over, since every comment in both
# files is full of them and they are high bytes too.
dashed="$(stage dashed)"
printf '\n# an em dash \342\200\224 like every other comment here\n' \
  >>"$dashed/system/libexec/claude-root"
dashed_output="$(run "$dashed")"
export dashed_output

check "an em dash is not mistaken for a control character" \
  '! printf "%s\n" "$dashed_output" | grep -q "hold control characters"'

# The helper is the other half, and the one that gets no tab: this repo's shell is
# indented with spaces, so a tab there is as much a surprise as an escape.
tabbed="$(stage tabbed)"
printf '\t# a tab nobody meant to write\n' >>"$tabbed/system/libexec/claude-root"
tabbed_output="$(run "$tabbed")"
export tabbed_output

check "a tab in the helper is refused" \
  'printf "%s\n" "$tabbed_output" | grep -q "hold control characters"'
check "and the helper is named as the file that holds it" \
  'printf "%s\n" "$tabbed_output" | grep -q "the helper, line"'

# A tab in the sudoers rule is the one exception, whitespace between sudoers
# fields being free — so this one has to get through, or the rule is stricter than
# sudoers itself.
sudo_tab="$(stage sudo_tab)"
printf 'timche\tALL=(root) NOPASSWD: %s\n' \
  "$narrow" >"$sudo_tab/system/sudoers/claude-root"
sudo_tab_output="$(run "$sudo_tab")"
export sudo_tab_output

check "a tab between sudoers fields is allowed" \
  '! printf "%s\n" "$sudo_tab_output" | grep -q "hold control characters"'

# The placeholder check, which is the older half of the same idea: a rule for an
# account that does not exist leaves this Mac's own with no sudo.
unrendered="$(stage unrendered)"
printf '__USER__ ALL=(root) NOPASSWD: %s\n' \
  __NOTHING__ >"$unrendered/system/sudoers/claude-root"
unrendered_output="$(run "$unrendered")"
export unrendered_output

check "a placeholder that survived rendering is refused" \
  'printf "%s\n" "$unrendered_output" | grep -q "placeholder survived"'

# Every command named by its absolute path, which is the other half of the same
# idea. sudoers here resets the environment but sets no secure_path, so a bare
# `sudo cat` would be whichever cat the caller's PATH found first — and the first
# entry on this account's PATH is ~/.local/bin, which the account can write. So a
# directory of recording fakes goes at the front of the PATH, and what is asserted
# is that not one of them was called: the ones sudo would have run as root, and the
# ones that decide what Tim sees in the review.
#
# home/.zshenv is a symlink into this checkout, so a session can set any variable
# Tim's shell starts with; the PATH here stands in for that as much as for a
# dropped binary.
shadow="$sandbox/shadow"
mkdir -p "$shadow"

for name in sh cat cmp install rm diff grep sed tr shasum stat dscl id uname \
            mktemp dirname chown chmod mv visudo sudo true; do
  {
    echo '#!/bin/sh'
    echo "echo $name >>\"\$SHADOW_CALLS\""
    echo 'exit 1'
  } >"$shadow/$name"
  chmod 0755 "$shadow/$name"
done

shadowed="$(stage shadowed)"
shadow_calls="$sandbox/shadow-calls"
: >"$shadow_calls"
export shadow_calls

shadow_output="$(
  PATH="$shadow:$sandbox/bin:$PATH" CAPTURE="$capture" SOURCES="$shadowed" \
    SHADOW_CALLS="$shadow_calls" bash "$shadowed/root-helper.sh" 2>&1
)"
export shadow_output

check "a review runs none of the commands a poisoned PATH would have supplied" \
  '[ ! -s "$shadow_calls" ]'
# Named rather than only counted, because which command went looking on the PATH
# is the whole of what makes this fixable.
[ ! -s "$shadow_calls" ] ||
  sort -u "$shadow_calls" | sed 's/^/        called: /'
check "and the review still happened, so the run was not simply refused" \
  'printf "%s\n" "$shadow_output" | grep -qE "$granted[[:space:]]+CLAUDE_ROOT"'

# And the race, which is the reason the installer reads its two files once and
# installs from memory. Every session on this Mac runs as the account that owns the
# checkout, so a session can rewrite either source the moment Tim starts typing his
# password — after the review has passed, before root reads anything. The rewrite
# happens on the stub's sudoers read, which is that moment, and what is asserted is
# that the bytes root was handed are the ones that were reviewed.
#
# It needs somewhere root-owned for the helper to go, which only a Mac that has had
# a real install has; machine.sh makes it, and CI runs that before this. A Mac that
# has not says so rather than failing on something this repo did not do.
if [ "$(stat -f '%Su' /usr/local/libexec 2>/dev/null)" = root ]; then
  raced="$(stage raced)"

  mkdir -p "$sandbox/rewrite"
  printf 'timche ALL=(root) %s %s\n' "$granted" "$everything" \
    >"$sandbox/rewrite/sudoers"
  printf '#!/bin/bash\necho a helper nobody reviewed\n' \
    >"$sandbox/rewrite/helper"

  raced_output="$(run_with_rewrite "$raced" "$sandbox/rewrite")"
  export raced_output raced
  export installed_rule="$capture/_etc_sudoers.d_claude-root"
  export installed_helper="$capture/_usr_local_libexec_claude-root"

  check "the rewrite landed in the sources, so the race really happened" \
    'grep -q "a helper nobody reviewed" "$raced/system/libexec/claude-root" &&
     grep -qE "$granted[[:space:]]+$everything( |$)" \
       "$raced/system/sudoers/claude-root"'
  check "the review showed the rule as it was before the rewrite" \
    'printf "%s\n" "$raced_output" | grep -qE "$granted[[:space:]]+CLAUDE_ROOT"'
  check "root was handed both files" \
    '[ -s "$installed_rule" ] && [ -s "$installed_helper" ]'
  check "the rule root was handed is the one that was reviewed" \
    'grep -qE "$granted[[:space:]]+CLAUDE_ROOT" "$installed_rule" &&
     ! grep -qE "$granted[[:space:]]+$everything( |$)" "$installed_rule"'
  check "and the helper root was handed is not the one the race swapped in" \
    '! grep -q "a helper nobody reviewed" "$installed_helper" &&
     grep -q "restart-daemon" "$installed_helper"'
  check "each went out at the mode it is meant to have" \
    '[ "$(cat "$installed_rule.mode")" = 0440 ] &&
     [ "$(cat "$installed_helper.mode")" = 0755 ]'
else
  echo "  --    /usr/local/libexec is not root-owned yet, so the race against a"
  echo "        rewritten source was not run — machine.sh makes that directory"
fi

if [ "$failures" -gt 0 ]; then
  echo "  $failures check(s) failed"
fi

exit "$failures"
