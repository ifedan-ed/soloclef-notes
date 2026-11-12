#!/usr/bin/env bash
# Indexing end to end, against real services in throwaway containers:
# Postgres (pgvector/pgvector:pg17, migrated by soloclef_schema's migrate
# command), Qdrant, MinIO, Redis and the parser service. A file is stored, a
# message is queued, and the document must come out parsed, chunked,
# embedded, searchable and marked ready.
#
#   bash scripts/index_end_to_end_test.sh             # the tests
#   bash scripts/index_end_to_end_test.sh bench       # the tests, then the benchmarks
#
# Embeddings come from a stub inside the test (the OpenAI embeddings shape,
# deterministic vectors), so a run needs no network and costs nothing. To use
# the real proxy as well, name it; the key is read from the file by the test
# and is never printed:
#
#   LITELLM_URL=http://127.0.0.1:4000/v1 LITELLM_KEY_FILE=/path/to/key \
#       bash scripts/index_end_to_end_test.sh
#
# The tokenizer is the one the backend image bakes. Point SOLOCLEF_TOKENIZER
# at a copy of its tokenizer.json:
#
#   docker compose cp backend:/app/tokenizer/tokenizer.json /tmp/tokenizer.json
#   SOLOCLEF_TOKENIZER=/tmp/tokenizer.json bash scripts/index_end_to_end_test.sh
#
# Every container has a name unique to this run, is published on a loopback
# port Docker picks, and is removed when the script ends, however it ends.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repository="$(cd "$here/.." && pwd)"
run="soloclef-index-e2e-$$-$RANDOM"

: "${SOLOCLEF_TOKENIZER:?set SOLOCLEF_TOKENIZER to a copy of the tokenizer.json the backend image bakes, as the top of this script says}"
[[ -r "$SOLOCLEF_TOKENIZER" ]] || { echo "cannot read $SOLOCLEF_TOKENIZER" >&2; exit 1; }

postgres_image="${SOLOCLEF_E2E_POSTGRES_IMAGE:-pgvector/pgvector:pg17}"
qdrant_image="${SOLOCLEF_E2E_QDRANT_IMAGE:-qdrant/qdrant:v1.19.2}"
minio_image="${SOLOCLEF_E2E_MINIO_IMAGE:-minio/minio:RELEASE.2025-09-07T16-13-09Z}"
redis_image="${SOLOCLEF_E2E_REDIS_IMAGE:-redis:7-alpine}"
parse_image="soloclef-parse-service:index-e2e"

scratch="$(mktemp -d)"
cleanup() {
	docker rm --force "$run-postgres" "$run-qdrant" "$run-minio" "$run-redis" "$run-parse" >/dev/null 2>&1 || true
	rm -rf "$scratch"
}
trap cleanup EXIT INT TERM HUP

# The tokenizer where a profile's folder is looked for: <dir>/bge-m3/tokenizer.json.
mkdir -p "$scratch/tokenizers/bge-m3"
cp "$SOLOCLEF_TOKENIZER" "$scratch/tokenizers/bge-m3/tokenizer.json"

echo "building the parser service image" >&2
docker build --quiet --tag "$parse_image" "$repository/soloclef_parse_service" >/dev/null

docker run --detach --name "$run-postgres" --publish 127.0.0.1::5432 \
	--env POSTGRES_USER=index --env POSTGRES_PASSWORD=index --env POSTGRES_DB=index \
	--tmpfs /var/lib/postgresql/data "$postgres_image" >/dev/null
docker run --detach --name "$run-qdrant" --publish 127.0.0.1::6333 \
	--tmpfs /qdrant/storage "$qdrant_image" >/dev/null
docker run --detach --name "$run-minio" --publish 127.0.0.1::9000 \
	--env MINIO_ROOT_USER=example-access --env MINIO_ROOT_PASSWORD=example-secret \
	--tmpfs /data "$minio_image" server /data >/dev/null
docker run --detach --name "$run-redis" --publish 127.0.0.1::6379 "$redis_image" >/dev/null
docker run --detach --name "$run-parse" --publish 127.0.0.1::8080 \
	--env PARSE_SERVICE_LISTEN=0.0.0.0:8080 \
	--env PARSE_SERVICE_WORKERS=2 \
	--env PARSE_SERVICE_MAX_BYTES=52428800 \
	--env PARSE_SERVICE_PARSE_TIMEOUT_SECONDS=120 \
	--env PARSE_SERVICE_QUEUE_WAIT_SECONDS=60 \
	"$parse_image" >/dev/null

address() { docker port "$run-$1" "$2/tcp" | head -n 1; }

# until <what> <command...>: wait up to a minute for the command to succeed.
until_ready() {
	local what="$1"
	shift
	for _ in $(seq 1 120); do
		if "$@" >/dev/null 2>&1; then
			return 0
		fi
		sleep 0.5
	done
	echo "$what never became ready" >&2
	docker logs "$run-${what%% *}" 2>&1 | tail -n 20 >&2 || true
	exit 1
}

# Postgres restarts once while its image initialises, so "answers a query"
# is asked of the TCP port, which only the final server listens on.
until_ready "postgres" docker exec "$run-postgres" pg_isready --host=127.0.0.1 --username=index --dbname=index
until_ready "qdrant" curl --silent --fail "http://$(address qdrant 6333)/readyz"
until_ready "minio" curl --silent --fail "http://$(address minio 9000)/minio/health/live"
until_ready "redis" docker exec "$run-redis" redis-cli ping
until_ready "parse" curl --silent --fail "http://$(address parse 8080)/health"

# The schema, applied the way a deployment applies it: soloclef_schema's own
# migrate command, on an empty database. What is tested is then the
# migrations themselves and not a copy of their SQL.
(cd "$repository/soloclef_schema" &&
	SOLOCLEF_MIGRATE_DATABASE_URL="postgres://index:index@$(address postgres 5432)/index?sslmode=disable" \
		go run ./cmd/migrate up) >&2

export SOLOCLEF_INDEX_TEST_DATABASE_URL="postgres://index:index@$(address postgres 5432)/index"
export SOLOCLEF_INDEX_TEST_QDRANT_URL="http://$(address qdrant 6333)"
export SOLOCLEF_INDEX_TEST_S3_ENDPOINT="http://$(address minio 9000)"
export SOLOCLEF_INDEX_TEST_S3_ACCESS_KEY=example-access
export SOLOCLEF_INDEX_TEST_S3_SECRET_KEY=example-secret
export SOLOCLEF_INDEX_TEST_REDIS_URL="redis://$(address redis 6379)/0"
export SOLOCLEF_INDEX_TEST_PARSE_SERVICE_URL="http://$(address parse 8080)"
export SOLOCLEF_INDEX_TEST_TOKENIZER_DIR="$scratch/tokenizers"

cd "$here"
# SOLOCLEF_E2E_GO_TEST_FLAGS is for a one-off flag such as -race.
# shellcheck disable=SC2086
go test ./internal/endtoend/ ./internal/indexstore/ ./internal/chunking/ -count=1 -v -run . ${SOLOCLEF_E2E_GO_TEST_FLAGS:-} 2>&1 | grep -v '^=== '

if [[ "${1:-}" == "bench" ]]; then
	go test ./internal/endtoend/ -count=1 -run '^$' -bench . -benchtime "${SOLOCLEF_E2E_BENCHTIME:-20x}" -timeout 30m
	docker stats --no-stream --format 'memory after the run: {{.Name}} {{.MemUsage}}' "$run-parse" "$run-qdrant"
fi
