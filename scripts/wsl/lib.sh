#!/usr/bin/env bash
# Shared helpers for the WSL setup scripts. Sourced, not run directly.

set -euo pipefail

# Repo root = two folders up from this file.
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ENV_FILE="$REPO_ROOT/.env"
export RADDB="/etc/freeradius/3.0"

green() { printf '\033[32m%s\033[0m\n' "$*"; }
yellow() { printf '\033[33m%s\033[0m\n' "$*"; }
red() { printf '\033[31m%s\033[0m\n' "$*"; }
step() { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }
die() { red "ERROR: $*"; exit 1; }

# Random secret made of letters and digits only (safe inside config files).
random_secret() { openssl rand -base64 48 | tr -dc 'A-Za-z0-9' | head -c "${1:-24}"; }

# Create .env from .env.example the first time, filling in random secrets.
ensure_env() {
  if [[ ! -f "$ENV_FILE" ]]; then
    [[ -f "$REPO_ROOT/.env.example" ]] || die ".env.example is missing"
    cp "$REPO_ROOT/.env.example" "$ENV_FILE"
    sed -i "s/^RADIUS_DB_PASSWORD=.*/RADIUS_DB_PASSWORD=$(random_secret)/" "$ENV_FILE"
    sed -i "s/^MIKROTIK_LAB_SECRET=.*/MIKROTIK_LAB_SECRET=$(random_secret)/" "$ENV_FILE"
    chmod 600 "$ENV_FILE"
    green "Created .env with fresh random secrets (it is git-ignored)."
  fi
  set -a
  # shellcheck disable=SC1090
  source "$ENV_FILE"
  set +a
}

require_sudo() {
  sudo -v || die "This script needs sudo (your WSL password)."
}

start_service() {
  # 'service' works whether or not systemd is enabled in WSL.
  sudo service "$1" start >/dev/null 2>&1 || sudo service "$1" restart >/dev/null
}
