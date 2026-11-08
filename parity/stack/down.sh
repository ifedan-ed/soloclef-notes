#!/usr/bin/env bash
# Take the world down and leave nothing behind.
#
# It is safe to run at any time, including when nothing is up and including
# twice, because run.sh calls it from a trap: a comparison that fails, or an
# interrupted run, still has to clean up after itself. It removes only
# containers carrying this suite's label.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/world.sh"

if [[ -f "$PARITY_STATE_DIR/api.pid" ]]; then
  kill "$(cat "$PARITY_STATE_DIR/api.pid")" >/dev/null 2>&1 || true
  rm -f "$PARITY_STATE_DIR/api.pid"
fi

for name in "$PARITY_PYTHON" "$PARITY_ANALYTICS" "$PARITY_DB" "$PARITY_REDIS"; do
  labelled=$(docker container inspect "$name" \
    --format '{{index .Config.Labels "parity.disposable"}}' 2>/dev/null || true)
  if [[ "$labelled" == "true" ]]; then
    docker rm --force --volumes "$name" >/dev/null 2>&1 || true
  fi
done

docker network rm "$PARITY_NETWORK" >/dev/null 2>&1 || true
echo "parity: the world is down"
