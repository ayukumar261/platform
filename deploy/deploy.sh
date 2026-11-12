#!/usr/bin/env bash

# Exit on error, unset variable, or failed pipe
set -euo pipefail

# Switch to deployment directory
cd /opt/platform

# Start the applications alongside the infrastructure services
export COMPOSE_PROFILES=apps

# Print a UTC-timestamped "started" line
echo "[deploy] $(date -u +%FT%TZ) started"

# Mirror origin/main exactly, then re-exec the freshly synced script so this
# run uses the new deploy logic
if [[ "${1:-}" != "--synced" ]]; then
  git fetch origin && git reset --hard origin/main || echo "[deploy] git sync failed (continuing)"
  exec ./deploy/deploy.sh --synced
fi

# Download the latest image tags
docker compose pull

# Recreate any containers that changed, without building on the host
docker compose up -d --no-build --remove-orphans

# Delete any untagged images to reclaim disk space
docker image prune -f

# Print a UTC-timestamped "finished" line
echo "[deploy] $(date -u +%FT%TZ) finished"
