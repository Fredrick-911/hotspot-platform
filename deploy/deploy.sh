#!/usr/bin/env bash
set -euo pipefail

APP_DIR="/opt/hotspot-platform"
RELEASES_DIR="${APP_DIR}/releases"
CURRENT_LINK="${APP_DIR}/bin/api"
TIMESTAMP="$(date +%Y%m%d%H%M%S)"
TARGET_DIR="${RELEASES_DIR}/${TIMESTAMP}"

echo "==> Starting deployment: ${TIMESTAMP}"

# 1. Prepare target release folder
mkdir -p "${TARGET_DIR}"

# 2. Move staged artifact to the release directory
mv /tmp/hotspot-api "${TARGET_DIR}/api"
chmod +x "${TARGET_DIR}/api"

# 3. Atomically switch symlink
mkdir -p "${APP_DIR}/bin"
ln -sfn "${TARGET_DIR}/api" "${CURRENT_LINK}"

# 4. Sync radius templates and migrations
if [ -d "/tmp/radius-updates" ]; then
    cp -r /tmp/radius-updates/* "${APP_DIR}/radius/" 2>/dev/null || true
    rm -rf /tmp/radius-updates
fi

if [ -d "/tmp/migrations-updates" ]; then
    cp -r /tmp/migrations-updates/* "${APP_DIR}/migrations/" 2>/dev/null || true
    rm -rf /tmp/migrations-updates
fi

# 5. Restart the Go service
echo "==> Restarting hotspot-api service..."
sudo systemctl restart hotspot-api

# 6. Verify healthcheck
echo "==> Verifying service health..."
ATTEMPTS=0
MAX_ATTEMPTS=10
HEALTHY=0

until [ "${ATTEMPTS}" -ge "${MAX_ATTEMPTS}" ]; do
    if curl -s -f "http://127.0.0.1:8080/health" > /dev/null 2>&1; then
        HEALTHY=1
        break
    fi
    ATTEMPTS=$((ATTEMPTS + 1))
    sleep 1
done

if [ "${HEALTHY}" -eq 1 ]; then
    echo "==> Healthcheck PASSED!"
else
    echo "==> ERROR: Healthcheck FAILED! Service not responding on :8080/health"
    echo "==> Dumping logs:"
    sudo journalctl -u hotspot-api -n 25 --no-pager
    exit 1
fi

# 7. Keep only the last 5 releases
cd "${RELEASES_DIR}"
# shellcheck disable=SC2012
ls -1dt ./* | tail -n +6 | xargs rm -rf 2>/dev/null || true

echo "==> Deployment completed successfully!"
