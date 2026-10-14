#!/usr/bin/env bash
# Step 3: install our FreeRADIUS config (from radius/*.tmpl), check it, and
# restart FreeRADIUS. Run again whenever you change a template or .env.
# Usage: ./scripts/wsl/03-configure-freeradius.sh
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

require_sudo
ensure_env

# Only these variables are replaced; FreeRADIUS's own ${...} stay untouched.
# Single quotes are intentional: envsubst wants the literal names.
# shellcheck disable=SC2016
VARS='${RADIUS_DB_HOST} ${RADIUS_DB_PORT} ${RADIUS_DB_NAME} ${RADIUS_DB_USER} ${RADIUS_DB_PASSWORD} ${MIKROTIK_LAB_SUBNET} ${MIKROTIK_LAB_SECRET}'

# render <template> <destination>
render() {
  local src="$REPO_ROOT/$1" dest="$RADDB/$2"
  [[ -f "$src" ]] || die "Missing template $src"
  # Keep the distro's original file once, so you can always go back.
  if root_file_exists "$dest" && ! root_file_exists "$dest.orig"; then
    sudo cp -p "$dest" "$dest.orig"
  fi
  envsubst "$VARS" < "$src" | sudo tee "$dest" > /dev/null
  sudo chown root:freerad "$dest"
  sudo chmod 640 "$dest"
  echo "  installed $dest"
}

step "Installing config files"
render radius/clients.conf.tmpl        clients.conf
render radius/mods-available/sql.tmpl  mods-available/sql

step "Enabling the SQL module"
sudo ln -sfn ../mods-available/sql "$RADDB/mods-enabled/sql"
echo "  linked mods-enabled/sql"

step "Checking IPv6 support"
# FreeRADIUS listens on IPv6 by default. If IPv6 is switched off in this WSL,
# it refuses to start, so we comment out its IPv6 "listen" blocks.
SITE="$RADDB/sites-available/default"
if [[ -f /proc/net/if_inet6 ]]; then
  echo "  IPv6 available — leaving defaults."
# (grep without -q reads the whole file; -q stops early and can fail with error 141)
elif root_cat "$SITE" | grep -E '^\s*ipv6addr' > /dev/null; then
  root_file_exists "$SITE.orig" || sudo cp -p "$SITE" "$SITE.orig"
  root_cat "$SITE" | awk '
    /^listen[ \t]*\{/ { inblk=1; depth=0; buf="" }
    inblk {
      buf = buf $0 "\n"
      depth += gsub(/\{/, "{") - gsub(/\}/, "}")
      if (depth == 0) {
        if (buf ~ /\n[ \t]*ipv6addr/) { gsub(/\n/, "\n#", buf); printf "#%s", substr(buf, 1, length(buf)-1) }
        else printf "%s", buf
        inblk=0
      }
      next
    }
    { print }
  ' | sudo tee "$SITE.new" > /dev/null
  sudo mv "$SITE.new" "$SITE"
  sudo chown root:freerad "$SITE"; sudo chmod 640 "$SITE"
  yellow "  IPv6 is off in this WSL — disabled FreeRADIUS IPv6 listeners (IPv4 is all we need)."
else
  echo "  IPv6 listeners already disabled."
fi

step "Checking the configuration"
# A fresh private temp file each run, owned by you so you can read it.
CHECK_LOG="$(mktemp)"
trap 'rm -f "$CHECK_LOG"' EXIT
# shellcheck disable=SC2024
if ! sudo freeradius -C > "$CHECK_LOG" 2>&1; then
  red "Config check FAILED. Last lines:"
  tail -n 25 "$CHECK_LOG"
  die "Fix the error above, or run: sudo freeradius -X"
fi
green "Config is valid."

step "Restarting FreeRADIUS"
start_service postgresql
sudo service freeradius restart
sleep 1
if sudo service freeradius status > /dev/null 2>&1; then
  green "FreeRADIUS is running."
else
  die "FreeRADIUS did not start. Run 'make radius-debug' to see why."
fi

green "Done. Next: ./scripts/wsl/04-test-radius.sh"
