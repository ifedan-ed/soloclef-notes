#!/usr/bin/env bash
# The Local parser end to end: build the parser service's image, start it in
# a throwaway container, run the Go client's live test against it, and remove
# the container whatever happens.
#
#   bash scripts/parse_service_container_test.sh
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
image="soloclef-parse-service:ingest-test"
name="soloclef-parse-service-test-$$"

docker build --quiet --tag "$image" "$here/../soloclef_parse_service" >/dev/null

# Published on a port Docker picks, on loopback only: nothing else on the
# host can collide with it or reach it.
docker run --detach --rm --name "$name" --publish 127.0.0.1::8080 \
  --env PARSE_SERVICE_LISTEN=0.0.0.0:8080 \
  --env PARSE_SERVICE_WORKERS=1 \
  --env PARSE_SERVICE_MAX_BYTES=10485760 \
  --env PARSE_SERVICE_PARSE_TIMEOUT_SECONDS=60 \
  --env PARSE_SERVICE_QUEUE_WAIT_SECONDS=30 \
  "$image" >/dev/null
trap 'docker rm --force "$name" >/dev/null 2>&1 || true' EXIT

address="http://$(docker port "$name" 8080/tcp | head -n 1)"

# The worker imports the parser before the server listens (about 2 s).
for _ in $(seq 1 60); do
  if curl --silent --fail "$address/health" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl --silent --fail "$address/health"
echo

cd "$here"
SOLOCLEF_PARSE_SERVICE_URL="$address" go test ./localservice/ -run TestLiveService -count=1 -v

# What the idle service costs, for sizing its memory limit.
docker stats --no-stream --format 'memory after the test: {{.MemUsage}}' "$name"
