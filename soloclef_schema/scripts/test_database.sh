#!/usr/bin/env bash
# The module's tests, with the ones that need a database given one.
#
#   scripts/test_database.sh               # go test ./... -count=1
#   scripts/test_database.sh -run Rolling  # any go test arguments
#
# `go test ./...` on its own skips the tests that apply the migrations,
# because they need a Postgres server they are free to create and drop
# databases on. This starts one (the image the stack runs), points the tests
# at it, and removes it when they end, however they end. The container has a
# name unique to this run and is published on a loopback port Docker picks,
# so two runs, or a run beside a real stack, do not meet.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
image="${SOLOCLEF_SCHEMA_TEST_POSTGRES_IMAGE:-pgvector/pgvector:pg17}"
container="soloclef-schema-test-$$-$RANDOM"

cleanup() { docker rm --force "$container" >/dev/null 2>&1 || true; }
trap cleanup EXIT INT TERM HUP

docker run --detach --name "$container" --publish 127.0.0.1::5432 \
	--env POSTGRES_USER=schema --env POSTGRES_PASSWORD=schema --env POSTGRES_DB=schema \
	--tmpfs /var/lib/postgresql/data "$image" >/dev/null

# Over TCP, not the socket: while the image initialises it runs a temporary
# server that listens on the socket only and is then stopped.
ready=false
for _ in $(seq 1 120); do
	if docker exec "$container" psql --host=127.0.0.1 --username=schema --dbname=schema --quiet --command='SELECT 1' >/dev/null 2>&1; then
		ready=true
		break
	fi
	sleep 0.5
done
$ready || { echo "Postgres did not start within a minute:" >&2; docker logs "$container" >&2; exit 1; }

address="$(docker port "$container" 5432/tcp | head -n 1)"
cd "$here"
SOLOCLEF_SCHEMA_TEST_SERVER_URL="postgres://schema:schema@$address/schema?sslmode=disable" \
	go test ./... -count=1 "$@"
