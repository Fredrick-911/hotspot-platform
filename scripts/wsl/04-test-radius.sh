#!/usr/bin/env bash
# Step 4: prove FreeRADIUS works end to end, the same way a MikroTik would use it:
#   1. a correct login is accepted and gets speed/time limits back
#   2. a wrong password is rejected
#   3. accounting (usage reports) is saved in the database
# Usage: ./scripts/wsl/04-test-radius.sh
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

ensure_env
export PGPASSWORD="$RADIUS_DB_PASSWORD"
db() { psql -tA -h "$RADIUS_DB_HOST" -p "$RADIUS_DB_PORT" -U "$RADIUS_DB_USER" -d "$RADIUS_DB_NAME" -c "$1"; }

SERVER=127.0.0.1
SECRET=testing123   # the "localhost" client in clients.conf
FAILED=0
pass() { green "  PASS: $*"; }
fail() { red   "  FAIL: $*"; FAILED=1; }

# expect "<text that must appear in \$OUT>" "<description>"
expect() {
  if grep -qF -- "$1" <<<"$OUT"; then pass "$2"; else fail "$2"; fi
}

step "Test 1: correct password is accepted with plan limits"
OUT=$(radtest test-hour pass1234 "$SERVER" 0 "$SECRET" 2>&1 || true)
printf '    %s\n' "${OUT//$'\n'/$'\n'    }"
expect "Access-Accept"                    "login accepted (Access-Accept)"
expect 'Mikrotik-Rate-Limit = "2M/5M"'    "speed limit 2M/5M sent to router"
expect "Session-Timeout = 3600"           "1-hour time limit sent to router"

step "Test 2: wrong password is rejected"
OUT=$(radtest test-hour wrong-password "$SERVER" 0 "$SECRET" 2>&1 || true)
expect "Access-Reject"                    "wrong password rejected (Access-Reject)"

step "Test 3: accounting (usage) is recorded"
SID="test-$(date +%s)"
send_acct() {
  printf '%s\n' \
    "User-Name = \"test-hour\"" \
    "Acct-Session-Id = \"$SID\"" \
    "NAS-IP-Address = 127.0.0.1" \
    "Framed-IP-Address = 192.168.88.50" \
    "Calling-Station-Id = \"AA:BB:CC:DD:EE:FF\"" \
    "$@" | radclient -q "$SERVER:1813" acct "$SECRET"
}
send_acct "Acct-Status-Type = Start" || fail "Start packet not answered"
send_acct "Acct-Status-Type = Stop" "Acct-Session-Time = 60" \
          "Acct-Input-Octets = 1048576" "Acct-Output-Octets = 5242880" || fail "Stop packet not answered"

ROW=$(db "SELECT username, acctsessiontime, acctinputoctets, acctoutputoctets
          FROM radacct WHERE acctsessionid = '$SID'")
if [[ "$ROW" == "test-hour|60|1048576|5242880" ]]; then
  pass "session saved in radacct: user=test-hour, 60s, 1 MB up, 5 MB down"
else
  fail "radacct row not as expected (got: '${ROW:-nothing}')"
fi
db "DELETE FROM radacct WHERE acctsessionid = '$SID'" > /dev/null

echo
if [[ $FAILED -eq 0 ]]; then
  green "All RADIUS tests passed. FreeRADIUS is ready for the MikroTik."
else
  die "Some tests failed. Run 'make radius-debug' in another terminal and re-run this script to see why."
fi
