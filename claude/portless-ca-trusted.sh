#!/bin/bash

# Exits 0 when this account or the admin domain trusts portless's CA, and 1 when
# it does not or there is none. Its own script because install.sh decides from it
# whether to trust the CA and claude.sh whether to list that as left to do.
#
# Looked up in the trust settings by SHA-1 rather than with `security
# verify-cert`, whose default policy never counts the SSL-only trust portless
# sets, so a trusted CA read as untrusted.

set -euo pipefail

ca="$HOME/.portless/ca.pem"
[ -f "$ca" ] || exit 1

sha1="$(openssl x509 -in "$ca" -noout -fingerprint -sha1 2>/dev/null | sed 's/.*=//; s/://g')"
[ -n "$sha1" ] || exit 1

settings="$(mktemp)"
trap 'rm -f "$settings"' EXIT

for domain in "" -d; do
  if security trust-settings-export ${domain:+"$domain"} "$settings" >/dev/null 2>&1 &&
    plutil -extract "trustList.$sha1" xml1 -o - "$settings" >/dev/null 2>&1; then
    exit 0
  fi
done
exit 1
