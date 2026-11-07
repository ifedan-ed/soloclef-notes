#!/usr/bin/env bash
#
# Prints the schema Soloclef's Alembic migrations produce, normalised so that
# two runs of this script on the same migrations give identical bytes.
#
# The schema this tier generates against cannot be read from the repository:
# there is no model file and no schema file, only the migration chain. So it is
# produced here — a throwaway Postgres, `alembic upgrade head` against it in the
# backend image, `pg_dump --schema-only`, and the container destroyed. Nothing
# touches a running database; `internal/schema/drift_test.go` compares this
# output against the checked-in `schema.sql`.
#
# Usage: internal/schema/dump_from_migrations.sh > internal/schema/schema.sql
#
# The database image is pinned because the dump's text is the dumping server's:
# a different major version words the same schema differently.

set -euo pipefail

readonly POSTGRES_IMAGE="${SOLOCLEF_SCHEMA_POSTGRES_IMAGE:-pgvector/pgvector:pg17}"
readonly BACKEND_IMAGE="${SOLOCLEF_SCHEMA_BACKEND_IMAGE:-soloclef-backend-fast:latest}"

# The Python tier's settings module builds its embedding clients at import, and
# alembic/env.py imports it to reach the models. These name a model on a port
# nothing answers on, and a fixed vector length, so the import succeeds without
# reaching any model host: the migrations need no embeddings.
readonly EMBEDDING_MODEL="${SOLOCLEF_SCHEMA_EMBEDDING_MODEL:-litellm://openai/schema-dump-only}"
readonly EMBEDDING_BASE_URL="${SOLOCLEF_SCHEMA_EMBEDDING_BASE_URL:-http://127.0.0.1:1}"

readonly backend_source="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)/surfsense_backend"
# Not prefixed soloclef-: nothing this script makes is part of the running
# stack, and a name that looks like one invites someone to leave it alone.
readonly run_id="go-api-schema-dump-$$"

cleanup() {
	docker rm -f "$run_id" >/dev/null 2>&1 || true
	docker network rm "$run_id" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM HUP

if [[ ! -d "$backend_source/alembic/versions" ]]; then
	echo "no migrations at $backend_source/alembic" >&2
	exit 1
fi

docker network create "$run_id" >/dev/null
docker run --detach --name "$run_id" --network "$run_id" \
	--env POSTGRES_USER=schema --env POSTGRES_PASSWORD="$run_id" --env POSTGRES_DB=schema \
	"$POSTGRES_IMAGE" >/dev/null

# A query, not pg_isready: the server accepts connections once during initdb
# and then restarts, so the first "ready" is not the one that lasts.
ready=no
for _ in $(seq 1 60); do
	if docker exec "$run_id" psql --username=schema --dbname=schema --quiet \
		--command='SELECT 1' >/dev/null 2>&1; then
		ready=yes
		break
	fi
	sleep 1
done
if [[ "$ready" != yes ]]; then
	echo "the throwaway Postgres never answered a query" >&2
	exit 1
fi

# The migrations run from this checkout, not from whatever the image was built
# with, so the dump answers for the source the Go tier is committed beside.
#
# Its output is held back and printed only if it fails: importing the Python
# tier's settings writes a page of warnings about the model host it is told not
# to reach, and those are not news.
if ! migration_output="$(docker run --rm --network "$run_id" \
	--volume "$backend_source:/app:ro" --workdir /app \
	--env DATABASE_URL="postgresql+asyncpg://schema:$run_id@$run_id:5432/schema" \
	--env EMBEDDING_MODEL="$EMBEDDING_MODEL" \
	--env EMBEDDING_BASE_URL="$EMBEDDING_BASE_URL" \
	--env EMBEDDING_DIMENSION=1024 \
	--env OPENAI_API_KEY=unused \
	--entrypoint alembic "$BACKEND_IMAGE" upgrade head 2>&1)"; then
	echo "$migration_output" >&2
	echo "alembic upgrade head failed against the throwaway database" >&2
	exit 1
fi

docker exec "$run_id" pg_dump --username=schema --dbname=schema \
	--schema-only --schema=public --no-owner --no-privileges --no-comments |
	# pg_dump 17 brackets its output with \restrict and \unrestrict lines
	# carrying a fresh random nonce, and names the server's patch version in a
	# comment. Neither says anything about the schema; both would make every
	# run differ from the last.
	grep -vE '^\\(un)?restrict ' |
	grep -vE '^-- Dumped (from database|by pg_dump) version '
