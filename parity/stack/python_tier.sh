#!/usr/bin/env bash
# The real FastAPI app — app.app:app under uvicorn — against the throwaway
# Postgres and Redis.
#
# It is the reference answer, so nothing about it is a stand-in: the same image
# the stack runs, the same ASGI app, the same middleware stack. Only its
# configuration is invented, and every address in it is at example.invalid.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/world.sh"

docker rm --force "$PARITY_PYTHON" >/dev/null 2>&1 || true

docker run --detach --name "$PARITY_PYTHON" --network "$PARITY_NETWORK" \
  --label "$PARITY_LABEL" \
  --publish "127.0.0.1:$PARITY_PYTHON_PORT:8000" \
  --volume "$PARITY_REPO/soloclef_backend:/app:ro" --workdir /app \
  --env DATABASE_URL="postgresql+asyncpg://$PARITY_DB_USER:$PARITY_DB_PASSWORD@$PARITY_DB:5432/$PARITY_DB_NAME" \
  --env REDIS_URL="redis://$PARITY_REDIS:6379/0" \
  --env REDIS_APP_URL="redis://$PARITY_REDIS:6379/0" \
  --env CELERY_BROKER_URL="redis://$PARITY_REDIS:6379/0" \
  --env CELERY_TASK_DEFAULT_QUEUE="$PARITY_CELERY_QUEUE" \
  --env EMBEDDING_MODEL="litellm://openai/comparison-only" \
  --env EMBEDDING_BASE_URL="http://127.0.0.1:1" \
  --env EMBEDDING_DIMENSION=1024 --env OPENAI_API_KEY=unused \
  --env AUTH_TYPE=GOOGLE \
  --env SECRET_KEY="$PARITY_SESSION_SECRET" \
  --env SOLOCLEF_PUBLIC_URL="$PARITY_PUBLIC_URL" \
  --env NEXT_FRONTEND_URL="$PARITY_PUBLIC_URL" \
  --env POSTHOG_API_KEY="$PARITY_ANALYTICS_KEY" \
  --env POSTHOG_HOST="http://$PARITY_ANALYTICS:$PARITY_ANALYTICS_PORT" \
  --env SANDBOX_ENABLED=FALSE \
  --env SMTP_ENABLED=FALSE \
  --env LOG_LEVEL=warning \
  --entrypoint uvicorn soloclef-backend-fast:latest \
  app.app:app --host 0.0.0.0 --port 8000 --no-access-log >/dev/null

echo "parity: waiting for the Python tier"
for _ in $(seq 1 180); do
  code=$(curl --silent --output /dev/null --write-out '%{http_code}' \
    "$PARITY_PYTHON_BASE/api/v1/pats" || true)
  if [[ -n "$code" && "$code" != "000" ]]; then
    echo "parity: python tier answers ($code)"
    exit 0
  fi
  sleep 1
done
echo "parity: the Python tier never answered" >&2
docker logs --tail 60 "$PARITY_PYTHON" >&2
exit 1
