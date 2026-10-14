#!/usr/bin/env bash
# Step 1: install FreeRADIUS, PostgreSQL and helper tools inside WSL (Ubuntu).
# Usage: ./scripts/wsl/01-install.sh
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

require_sudo

step "Updating package lists"
sudo apt-get update

step "Installing FreeRADIUS, PostgreSQL and tools"
# freeradius            -> the RADIUS server
# freeradius-utils      -> radtest / radclient (test tools)
# freeradius-postgresql -> lets FreeRADIUS read users from PostgreSQL
# postgresql            -> the database
# gettext-base          -> envsubst (fills in our config templates)
# openssl               -> generates random secrets
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y \
  freeradius freeradius-utils freeradius-postgresql \
  postgresql gettext-base openssl make

step "Starting PostgreSQL"
start_service postgresql

step "Checking versions"
# awk reads ALL the output and prints line 1. ("head -n 1" stops reading early,
# which kills freeradius mid-output and makes the script fail with error 141.)
freeradius -v | awk 'NR == 1'
psql --version

green "Install complete. Next: ./scripts/wsl/02-setup-database.sh"
