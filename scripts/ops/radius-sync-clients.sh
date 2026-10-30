#!/usr/bin/env bash
# Restart FreeRADIUS when the list of routers (the "nas" table) has changed.
#
# Why: FreeRADIUS reads the router list from the database when it STARTS and
# does not pick up new rows on a reload (verified with 3.2.5: "HUP - No files
# changed"). Every router an owner registers in the dashboard adds a row, so
# this script (run by a systemd timer every minute) restarts FreeRADIUS - about
# one second - only when that list is different from the last time.
#
# Run as root. Settings come from /opt/hotspot-platform/.env (or ENV_FILE).
set -euo pipefail

ENV_FILE="${ENV_FILE:-/opt/hotspot-platform/.env}"
STATE_FILE="${STATE_FILE:-/var/lib/hotspot-radius-sync/nas.md5}"
RESTART_CMD="${RESTART_CMD:-systemctl restart freeradius}"

if [[ -f "$ENV_FILE" ]]; then
  set -a
  # shellcheck disable=SC1090
  source "$ENV_FILE"
  set +a
fi
: "${RADIUS_DB_HOST:=127.0.0.1}" "${RADIUS_DB_PORT:=5432}" "${RADIUS_DB_NAME:=radius}" "${RADIUS_DB_USER:=radius}"
export PGPASSWORD="${RADIUS_DB_PASSWORD:-}"

current="$(psql -h "$RADIUS_DB_HOST" -p "$RADIUS_DB_PORT" -U "$RADIUS_DB_USER" -d "$RADIUS_DB_NAME" -X -A -t -c \
  "SELECT COALESCE(md5(string_agg(nasname || '|' || secret, ',' ORDER BY nasname)), 'empty') FROM nas")"
[[ -n "$current" ]] || { echo "radius-sync: could not read the nas table" >&2; exit 1; }

mkdir -p "$(dirname "$STATE_FILE")"
previous="$(cat "$STATE_FILE" 2>/dev/null || true)"
if [[ "$current" == "$previous" ]]; then
  exit 0
fi

echo "radius-sync: router list changed ($previous -> $current), restarting FreeRADIUS"
$RESTART_CMD
# Record the new state only after a successful restart, so a failure is retried next minute.
echo "$current" > "$STATE_FILE"