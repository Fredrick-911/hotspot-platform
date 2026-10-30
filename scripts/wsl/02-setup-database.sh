#!/usr/bin/env bash
# Step 2: create the "radius" PostgreSQL database, load FreeRADIUS's tables,
# and add test users. Safe to run again.
# Usage: ./scripts/wsl/02-setup-database.sh
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

require_sudo
ensure_env
start_service postgresql

SCHEMA="$RADDB/mods-config/sql/main/postgresql/schema.sql"
root_file_exists "$SCHEMA" || die "Schema not found at $SCHEMA. Did step 1 finish?"

# Run SQL as the PostgreSQL superuser (no password needed inside WSL).
as_postgres() { sudo -u postgres psql -v ON_ERROR_STOP=1 -q "$@"; }

step "Creating database user '$RADIUS_DB_USER'"
as_postgres -v user="$RADIUS_DB_USER" -v pass="$RADIUS_DB_PASSWORD" <<'SQL'
SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', :'user', :'pass')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'user') \gexec
SELECT format('ALTER ROLE %I PASSWORD %L', :'user', :'pass') \gexec
SQL

step "Creating database '$RADIUS_DB_NAME'"
as_postgres -v db="$RADIUS_DB_NAME" -v user="$RADIUS_DB_USER" <<'SQL'
SELECT format('CREATE DATABASE %I OWNER %I', :'db', :'user')
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = :'db') \gexec
SQL

# Connect as the radius user so it owns every table it creates.
export PGPASSWORD="$RADIUS_DB_PASSWORD"
as_radius() {
  psql -v ON_ERROR_STOP=1 -q -h "$RADIUS_DB_HOST" -p "$RADIUS_DB_PORT" \
    -U "$RADIUS_DB_USER" -d "$RADIUS_DB_NAME" "$@"
}

step "Loading FreeRADIUS tables"
if as_radius -tAc "SELECT to_regclass('public.radcheck')" | grep radcheck > /dev/null; then
  yellow "Tables already exist — skipping schema load."
else
  # The schema file is root-only, so read it with sudo and pipe it into psql.
  root_cat "$SCHEMA" | as_radius
  green "Tables created."
fi

# The seed creates users with the well-known password "pass1234". It must never
# reach a real server, so it only runs for local development (or CI).
step "Adding test users"
if [[ "${APP_ENV:-development}" == "development" || "${CI:-}" == "true" || "${FORCE_SEED:-}" == "1" ]]; then
  as_radius -f "$REPO_ROOT/radius/sql/seed-dev.sql"
else
  yellow "APP_ENV=${APP_ENV} - skipping test users (set FORCE_SEED=1 to load them anyway)."
fi

step "Tables in '$RADIUS_DB_NAME'"
as_radius -c '\dt'

green "Database ready. Next: ./scripts/wsl/03-configure-freeradius.sh"