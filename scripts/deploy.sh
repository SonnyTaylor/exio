#!/usr/bin/env bash
# Deploy exiod to Proxmox LXC
# Usage: ./scripts/deploy.sh [version]
#   version: optional version string (default: reads from git tag or "dev")

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
PROXMOX_HOST="proxmox"
LXC_ID="134"
REMOTE_BIN="/usr/local/bin/exiod"
SERVICE="exiod"

# Determine version
VERSION="${1:-$(git -C "$PROJECT_DIR" describe --tags --always 2>/dev/null || echo "dev")}"
COMMIT="$(git -C "$PROJECT_DIR" rev-parse --short HEAD)"
BUILD_TIME="$(date -u '+%Y-%m-%d_%H:%M:%S')"

LDFLAGS="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildTime=${BUILD_TIME}"

echo "==> Building exiod ${VERSION} (${COMMIT}) for linux/amd64..."
cd "$PROJECT_DIR"
GOOS=linux GOARCH=amd64 go build -ldflags "$LDFLAGS" -o bin/exiod-linux ./cmd/exiod/

echo "==> Uploading to ${PROXMOX_HOST}..."
scp bin/exiod-linux "${PROXMOX_HOST}:/tmp/exiod"

echo "==> Stopping ${SERVICE}..."
ssh "$PROXMOX_HOST" "pct exec ${LXC_ID} -- systemctl stop ${SERVICE}"

echo "==> Installing binary..."
ssh "$PROXMOX_HOST" "pct push ${LXC_ID} /tmp/exiod ${REMOTE_BIN} && pct exec ${LXC_ID} -- chmod +x ${REMOTE_BIN}"

echo "==> Starting ${SERVICE}..."
ssh "$PROXMOX_HOST" "pct exec ${LXC_ID} -- systemctl start ${SERVICE}"

echo "==> Verifying..."
sleep 1
ssh "$PROXMOX_HOST" "pct exec ${LXC_ID} -- ${REMOTE_BIN} version"
ssh "$PROXMOX_HOST" "pct exec ${LXC_ID} -- curl -s http://localhost:8080/_health"
echo ""

echo "==> Deploy complete!"
