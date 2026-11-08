#!/usr/bin/env bash
# Stand up the whole disposable world: a throwaway Postgres migrated by this
# checkout's own migrations, a throwaway Redis, the real FastAPI app under
# uvicorn, the Go tier on loopback, and an analytics stand-in so neither tier
# talks to anything off this host.
#
# Nothing of the running stack is touched and nothing here is reused between
# runs: the database is created, migrated and seeded from scratch every time,
# because a comparison is only worth anything if both tiers were asked their
# question of identical rows.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/world.sh"
STACK="$PARITY_STACK_DIR"

bash "$STACK/refuse_live_targets.sh"

mkdir -p "$PARITY_STATE_DIR"
docker network create "$PARITY_NETWORK" >/dev/null 2>&1 || true

# Only ever our own, and only after refuse_live_targets.sh has agreed that
# anything of these names carries our label.
docker rm --force "$PARITY_DB" "$PARITY_REDIS" "$PARITY_PYTHON" \
  "$PARITY_ANALYTICS" >/dev/null 2>&1 || true

echo "parity: starting Postgres, Redis and the analytics stand-in"
docker run --detach --name "$PARITY_DB" --network "$PARITY_NETWORK" \
  --label "$PARITY_LABEL" \
  --publish "127.0.0.1:$PARITY_DB_PORT:5432" \
  --env POSTGRES_USER="$PARITY_DB_USER" \
  --env POSTGRES_PASSWORD="$PARITY_DB_PASSWORD" \
  --env POSTGRES_DB="$PARITY_DB_NAME" \
  pgvector/pgvector:pg17 >/dev/null

docker run --detach --name "$PARITY_REDIS" --network "$PARITY_NETWORK" \
  --label "$PARITY_LABEL" \
  --publish "127.0.0.1:$PARITY_REDIS_PORT:6379" \
  redis:8-alpine >/dev/null

docker run --detach --name "$PARITY_ANALYTICS" --network "$PARITY_NETWORK" \
  --label "$PARITY_LABEL" \
  --publish "127.0.0.1:$PARITY_ANALYTICS_PORT:$PARITY_ANALYTICS_PORT" \
  --volume "$PARITY_REPO/parity/stack:/stack:ro" \
  --env PARITY_ANALYTICS_PORT="$PARITY_ANALYTICS_PORT" \
  --entrypoint python soloclef-backend-fast:latest \
  /stack/analytics_standin.py >/dev/null

psql() { docker exec --interactive "$PARITY_DB" psql --quiet \
  --username="$PARITY_DB_USER" --dbname="$PARITY_DB_NAME" "$@"; }

ready=no
for _ in $(seq 1 90); do
  if psql --command='SELECT 1' >/dev/null 2>&1; then ready=yes; break; fi
  sleep 1
done
[[ "$ready" == yes ]] || { echo "parity: the throwaway Postgres never answered" >&2; exit 1; }

# The second half of refusing a live target, which needs a connection: this
# database must be one nobody has used, and this Redis must hold no keys at all
# — least of all one of the app's own queues, which is what
# soloclef_worker/internal/beat/redis_test.go refuses on.
tables=$(psql --tuples-only --no-align --command \
  "SELECT count(*) FROM information_schema.tables WHERE table_schema='public'")
if [[ "${tables//[[:space:]]/}" != "0" ]]; then
  echo "parity: $PARITY_DB already holds $tables tables; refusing to migrate over them" >&2
  exit 1
fi
keys=$(docker exec "$PARITY_REDIS" redis-cli DBSIZE | tr -d '[:space:]')
if [[ "$keys" != "0" ]]; then
  echo "parity: $PARITY_REDIS holds $keys keys; refusing to touch it" >&2
  exit 1
fi
for queue in "$PARITY_CELERY_QUEUE" "$PARITY_CELERY_QUEUE.connectors" "$PARITY_CELERY_QUEUE.heavy"; do
  held=$(docker exec "$PARITY_REDIS" redis-cli EXISTS "$queue" | tr -d '[:space:]')
  if [[ "$held" != "0" ]]; then
    echo "parity: $PARITY_REDIS holds the live queue $queue; refusing to touch it" >&2
    exit 1
  fi
done

# The migrations, run the way internal/schema/dump_from_migrations.sh runs them
# and under AUTH_TYPE=GOOGLE for the same reason: oauth_account is mapped only
# there.
echo "parity: migrating"
docker run --rm --network "$PARITY_NETWORK" \
  --volume "$PARITY_REPO/soloclef_backend:/app:ro" --workdir /app \
  --env DATABASE_URL="postgresql+asyncpg://$PARITY_DB_USER:$PARITY_DB_PASSWORD@$PARITY_DB:5432/$PARITY_DB_NAME" \
  --env EMBEDDING_MODEL="litellm://openai/schema-only" \
  --env EMBEDDING_BASE_URL="http://127.0.0.1:1" \
  --env EMBEDDING_DIMENSION=1024 --env OPENAI_API_KEY=unused \
  --env AUTH_TYPE=GOOGLE \
  --entrypoint alembic soloclef-backend-fast:latest upgrade head >/dev/null 2>&1 \
  || { echo "parity: alembic upgrade head failed" >&2; exit 1; }

echo "parity: seeding"
psql --file=/dev/stdin < "$STACK/seed.sql" >/dev/null

PARITY_SESSION_SECRET="$PARITY_SESSION_SECRET" \
  python3 "$STACK/mint_credentials.py" "$PARITY_STATE_DIR/credentials.json"

bash "$STACK/python_tier.sh"
bash "$STACK/go_tier.sh"

echo "parity: the world is up — python $PARITY_PYTHON_BASE, go $PARITY_GO_BASE"
