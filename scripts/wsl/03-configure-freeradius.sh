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
  # A file that already came from one of our templates (it names this script)
  # is not an original, so it is never saved as .orig.
  if root_file_exists "$dest" && ! root_file_exists "$dest.orig" \
     && ! root_cat "$dest" | grep -F '03-configure-freeradius.sh' > /dev/null; then
    sudo cp -p "$dest" "$dest.orig"
  fi
  envsubst "$VARS" < "$src" | sudo tee "$dest" > /dev/null
  sudo chown root:freerad "$dest"
  sudo chmod 640 "$dest"
  echo "  installed $dest"
}

step "Installing config files"
render radius/clients.conf.tmpl            clients.conf
render radius/mods-available/sql.tmpl      mods-available/sql
render radius/dictionary.tmpl              dictionary
render radius/mods-available/data_quota.tmpl mods-available/data_quota
render radius/policy.d/hotspot.tmpl        policy.d/hotspot

step "Enabling the SQL and data-quota modules"
sudo ln -sfn ../mods-available/sql        "$RADDB/mods-enabled/sql"
sudo ln -sfn ../mods-available/data_quota "$RADDB/mods-enabled/data_quota"
echo "  linked mods-enabled/sql and mods-enabled/data_quota"

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

step "Hooking the quota policy into the default site"
# The policy must run in "authorize" AFTER the sql module (which loads the
# user's limits) and AFTER the built-in expiration module. Both run before
# "logintime", so we add one line right after it. Safe to run again: the
# marker line "hotspot_quota" is only added once.
if root_cat "$SITE" | grep -E '^[[:space:]]*hotspot_quota[[:space:]]*$' > /dev/null; then
  echo "  already hooked."
else
  root_file_exists "$SITE.orig" || sudo cp -p "$SITE" "$SITE.orig"
  root_cat "$SITE" | awk '
    { print }
    !done && /^[ \t]*logintime[ \t]*$/ {
      print ""
      print "\t# hotspot-platform: data quota (radius/policy.d/hotspot.tmpl)"
      print "\thotspot_quota"
      done = 1
    }
  ' | sudo tee "$SITE.new" > /dev/null
  if ! sudo grep -E '^[[:space:]]*hotspot_quota[[:space:]]*$' "$SITE.new" > /dev/null; then
    sudo rm -f "$SITE.new"
    die "Could not find the 'logintime' line in $SITE to hook after. Is this FreeRADIUS 3.x?"
  fi
  sudo mv "$SITE.new" "$SITE"
  sudo chown root:freerad "$SITE"; sudo chmod 640 "$SITE"
  echo "  added 'hotspot_quota' after 'logintime' in sites-available/default"
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