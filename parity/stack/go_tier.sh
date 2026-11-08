#!/usr/bin/env bash
# The Go API tier, built from this checkout and run on loopback against the
# same throwaway Postgres and Redis as the Python tier.
#
# Every setting mirrors the Python container's, because a difference in
# configuration is a difference in answer that says nothing about the port. It
# runs on the host rather than in a container so that a rebuild is a second,
# which is what makes the suite usable while a route is being written.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/world.sh"

binary="$PARITY_STATE_DIR/api"
mkdir -p "$PARITY_STATE_DIR"

( cd "$PARITY_REPO/soloclef_api" && go build -o "$binary" ./cmd/api )

if [[ -f "$PARITY_STATE_DIR/api.pid" ]]; then
  kill "$(cat "$PARITY_STATE_DIR/api.pid")" >/dev/null 2>&1 || true
  rm -f "$PARITY_STATE_DIR/api.pid"
fi

export SOLOCLEF_API_LISTEN_ADDRESS="127.0.0.1:$PARITY_GO_PORT"
export SOLOCLEF_API_DATABASE_URL="postgres://$PARITY_DB_USER:$PARITY_DB_PASSWORD@127.0.0.1:$PARITY_DB_PORT/$PARITY_DB_NAME"
export SOLOCLEF_API_SESSION_SECRET="$PARITY_SESSION_SECRET"
export SOLOCLEF_API_PYTHON_UPSTREAM="$PARITY_PYTHON_BASE"
export SOLOCLEF_API_ERROR_REPORT_URL="$PARITY_PUBLIC_URL/contact"
export SOLOCLEF_API_REDIS_URL="redis://127.0.0.1:$PARITY_REDIS_PORT/0"
export SOLOCLEF_API_CELERY_BROKER_URL="redis://127.0.0.1:$PARITY_REDIS_PORT/0"
export SOLOCLEF_API_CELERY_DEFAULT_QUEUE="$PARITY_CELERY_QUEUE"
export SOLOCLEF_API_ALLOWED_ORIGINS="$PARITY_PUBLIC_URL,http://localhost:3000,http://127.0.0.1:3000"
export SOLOCLEF_API_PUBLIC_URL="$PARITY_PUBLIC_URL"
export SOLOCLEF_API_FRONTEND_URL="$PARITY_PUBLIC_URL"
export SOLOCLEF_API_POSTHOG_API_KEY="$PARITY_ANALYTICS_KEY"
export SOLOCLEF_API_POSTHOG_HOST="http://127.0.0.1:$PARITY_ANALYTICS_PORT"
export SOLOCLEF_API_LOG_LEVEL=error

nohup "$binary" >"$PARITY_STATE_DIR/api.log" 2>&1 &
echo $! > "$PARITY_STATE_DIR/api.pid"

for _ in $(seq 1 60); do
  if curl --silent --output /dev/null "$PARITY_GO_BASE/healthz"; then
    echo "parity: go tier answers"
    exit 0
  fi
  sleep 0.25
done
echo "parity: the Go tier never answered" >&2
cat "$PARITY_STATE_DIR/api.log" >&2
exit 1
