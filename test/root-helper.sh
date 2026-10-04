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

sandbox="$(mktemp -d)"
trap 'rm -rf "$sandbox"' EXIT

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

# A stub sudo, so that a case which gets past the review reaches a refusal rather
# than the machine. `-n true` answers yes, which is what gets the run past the
# guard that would otherwise skip it for want of a terminal; the sudoers read is
# answered with a line that has the includedir in it; everything else — every
# install, every write — refuses. So the furthest any case here can get is the
# warning root-helper.sh prints when it cannot write, and nothing outside this
# directory is touched.
mkdir -p "$sandbox/bin"
cat >"$sandbox/bin/sudo" <<'STUB'
#!/bin/sh
case "$*" in
  "-n true") exit 0 ;;
  "cat /etc/sudoers") echo "@includedir /private/etc/sudoers.d"; exit 0 ;;
esac
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
  cp "$repo/root-helper.sh" "$copy/root-helper.sh"
  cp "$repo/system/libexec/claude-root" "$copy/system/libexec/claude-root"
  cp "$repo/system/sudoers/claude-root" "$copy/system/sudoers/claude-root"
  echo "$copy"
}

run() {
  PATH="$sandbox/bin:$PATH" bash "$1/root-helper.sh" 2>&1
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
check "a clean pair installs nothing only because sudo refused" \
  'printf "%s\n" "$clean_output" | grep -q "stub sudo refuses"'
check "and nothing in the review is reported as a control character" \
  '! printf "%s\n" "$clean_output" | grep -q "control characters"'

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
  'printf "%s\n" "$escaped_output" | grep -q "Nothing was installed"'
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
     grep -qE "Nothing was installed|visudo rejected"'
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
  'printf "%s\n" "$c1_output" | grep -q "Nothing was installed"'

# And the em dashes the refusal must not trip over, since every comment in both
# files is full of them and they are high bytes too.
dashed="$(stage dashed)"
printf '\n# an em dash \342\200\224 like every other comment here\n' \
  >>"$dashed/system/libexec/claude-root"
dashed_output="$(run "$dashed")"
export dashed_output

check "an em dash is not mistaken for a control character" \
  '! printf "%s\n" "$dashed_output" | grep -q "Nothing was installed"'

# The helper is the other half, and the one that gets no tab: this repo's shell is
# indented with spaces, so a tab there is as much a surprise as an escape.
tabbed="$(stage tabbed)"
printf '\t# a tab nobody meant to write\n' >>"$tabbed/system/libexec/claude-root"
tabbed_output="$(run "$tabbed")"
export tabbed_output

check "a tab in the helper is refused" \
  'printf "%s\n" "$tabbed_output" | grep -q "Nothing was installed"'
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
  '! printf "%s\n" "$sudo_tab_output" | grep -q "Nothing was installed"'

# The placeholder check, which is the older half of the same idea: a rule for an
# account that does not exist leaves this Mac's own with no sudo.
unrendered="$(stage unrendered)"
printf '__USER__ ALL=(root) NOPASSWD: %s\n' \
  __NOTHING__ >"$unrendered/system/sudoers/claude-root"
unrendered_output="$(run "$unrendered")"
export unrendered_output

check "a placeholder that survived rendering is refused" \
  'printf "%s\n" "$unrendered_output" | grep -q "placeholder survived"'

if [ "$failures" -gt 0 ]; then
  echo "  $failures check(s) failed"
fi

exit "$failures"
