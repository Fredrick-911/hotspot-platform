#!/usr/bin/env bash
# Step 4: prove FreeRADIUS works end to end, the same way a MikroTik would use it:
#   1. a correct login is accepted and gets speed + TIME LEFT back
#   2. a wrong password is rejected
#   3. accounting (usage reports) is saved in the database
#   4. an expired plan is rejected
#   5. the DATA LEFT shrinks as the user consumes data, and the user is
#      rejected once it is gone (so reconnecting never resets a plan)
#   6. plans bigger than 4 GiB get Mikrotik-Total-Limit-Gigawords
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

# value_of "<Attribute-Name>" -> the number after "Attribute-Name = " in $OUT
# (exact attribute name only, so Mikrotik-Total-Limit does not match
# Mikrotik-Total-Limit-Gigawords)
value_of() {
  awk -v a="$1" '$1 == a && $2 == "=" { print $3; exit }' <<<"$OUT"
}

# expect_value "<Attribute-Name>" <expected> "<description>"
expect_value() {
  local got
  got="$(value_of "$1")"
  if [[ "$got" == "$2" ]]; then pass "$3"; else fail "$3 (expected $2, got '${got:-nothing}')"; fi
}

# login <user> <password> -> puts radtest's output in $OUT
login() { OUT=$(radtest "$1" "$2" "$SERVER" 0 "$SECRET" 2>&1 || true); }
show() { printf '    %s\n' "${OUT//$'\n'/$'\n'    }"; }

# send_acct <user> <session-id> <attribute lines...>  (one accounting packet)
send_acct() {
  local user="$1" sid="$2"
  shift 2
  printf '%s\n' \
    "User-Name = \"$user\"" \
    "Acct-Session-Id = \"$sid\"" \
    "NAS-IP-Address = 127.0.0.1" \
    "Framed-IP-Address = 192.168.88.50" \
    "Calling-Station-Id = \"AA:BB:CC:DD:EE:FF\"" \
    "$@" | radclient -q "$SERVER:1813" acct "$SECRET"
}

# A finished session that used <in> bytes up and <out> bytes down.
# extra attributes (e.g. Acct-Input-Gigawords = 1) can follow.
report_usage() {
  local user="$1" sid="$2" in="$3" out="$4"
  shift 4
  send_acct "$user" "$sid" "Acct-Status-Type = Start" || fail "Start packet not answered"
  send_acct "$user" "$sid" "Acct-Status-Type = Stop" "Acct-Session-Time = 60" \
    "Acct-Input-Octets = $in" "Acct-Output-Octets = $out" "$@" || fail "Stop packet not answered"
}

TEST_USERS="'test-hour','test-quota','test-bigdata'"
clean_usage() { db "DELETE FROM radacct WHERE username IN ($TEST_USERS)" > /dev/null; }
trap clean_usage EXIT

# The seed data sets "ends in 1 hour" when it is loaded. Re-set it to exactly 1 hour
# from NOW so the time-left check below does not depend on when you ran the seed.
db "UPDATE radcheck
       SET value = floor(EXTRACT(EPOCH FROM now() + interval '1 hour'))::bigint::text
     WHERE username = 'test-hour' AND attribute = 'Expiration'" > /dev/null
clean_usage

step "Test 1: correct password is accepted with speed limit and TIME LEFT"
login test-hour pass1234
show
expect "Access-Accept"                    "login accepted (Access-Accept)"
expect 'Mikrotik-Rate-Limit = "2M/5M"'    "speed limit 2M/5M sent to router"
TIMEOUT="$(value_of Session-Timeout)"
if [[ "$TIMEOUT" =~ ^[0-9]+$ ]] && (( TIMEOUT >= 3590 && TIMEOUT <= 3600 )); then
  pass "time left sent as Session-Timeout = $TIMEOUT s (about 1 hour)"
else
  fail "Session-Timeout should be 3590-3600, got '${TIMEOUT:-nothing}'"
fi

step "Test 2: wrong password is rejected"
login test-hour wrong-password
expect "Access-Reject"                    "wrong password rejected (Access-Reject)"

step "Test 3: accounting (usage) is recorded"
SID="test-$(date +%s)"
report_usage test-hour "$SID" 1048576 5242880
ROW=$(db "SELECT username, acctsessiontime, acctinputoctets, acctoutputoctets
          FROM radacct WHERE acctsessionid = '$SID'")
if [[ "$ROW" == "test-hour|60|1048576|5242880" ]]; then
  pass "session saved in radacct: user=test-hour, 60s, 1 MB up, 5 MB down"
else
  fail "radacct row not as expected (got: '${ROW:-nothing}')"
fi

step "Test 4: an expired plan is rejected"
login test-expired pass1234
expect "Access-Reject"                    "expired plan rejected (Access-Reject)"

step "Test 5: data left shrinks with use, and the user is rejected when it is gone"
# test-quota has a 10 MiB allowance = 10485760 bytes.
SID="test-quota-$(date +%s)"
login test-quota pass1234
expect "Access-Accept"                    "fresh plan accepted"
expect_value Mikrotik-Total-Limit 10485760 "router told: 10485760 bytes left (the full 10 MiB)"

report_usage test-quota "$SID-a" 4194304 3145728          # 7340032 bytes used
login test-quota pass1234
expect "Access-Accept"                    "still accepted after using 7 MiB"
expect_value Mikrotik-Total-Limit 3145728 "router told: only 3145728 bytes left (3 MiB), not a fresh 10 MiB"

report_usage test-quota "$SID-b" 4194304 0                # 11534336 bytes used in total
login test-quota pass1234
expect "Access-Reject"                    "rejected once the allowance is used up"

step "Test 6: plans over 4 GiB also send Mikrotik-Total-Limit-Gigawords"
# test-bigdata has 10 GiB = 10737418240 = 2 x 4294967296 + 2147483648
login test-bigdata pass1234
show
expect "Access-Accept"                    "10 GiB plan accepted"
expect_value Mikrotik-Total-Limit           2147483648 "low 32 bits of the data left"
expect_value Mikrotik-Total-Limit-Gigawords 2          "upper bits: 2 whole 4 GiB blocks"

# Used 4 GiB: Acct-Input-Gigawords = 1 means "1 x 4294967296 bytes", so 6 GiB are left
# = 1 x 4294967296 + 2147483648.
report_usage test-bigdata "test-big-$(date +%s)" 0 0 "Acct-Input-Gigawords = 1"
login test-bigdata pass1234
expect_value Mikrotik-Total-Limit           2147483648 "low 32 bits after using 4 GiB"
expect_value Mikrotik-Total-Limit-Gigawords 1          "upper bits after using 4 GiB: 1 block"

echo
if [[ $FAILED -eq 0 ]]; then
  green "All RADIUS tests passed. FreeRADIUS is ready for the MikroTik."
else
  die "Some tests failed. Run 'make radius-debug' in another terminal and re-run this script to see why."
fi