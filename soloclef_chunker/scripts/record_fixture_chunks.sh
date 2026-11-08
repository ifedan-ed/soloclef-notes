#!/usr/bin/env bash
# Re-record the fixture goldens from the Python chunker in the backend
# container. Read-only against the app: it only runs a chunker.
set -euo pipefail

container="${SOLOCLEF_BACKEND_CONTAINER:-soloclef-backend-1}"
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

docker exec "$container" rm -rf /tmp/chunker_fixtures
docker cp "$here/../testdata/documents" "$container:/tmp/chunker_fixtures" >/dev/null
docker cp "$here/record_fixture_chunks.py" "$container:/tmp/record_fixture_chunks.py" >/dev/null
docker exec -w /app "$container" python /tmp/record_fixture_chunks.py /tmp/chunker_fixtures >/dev/null
docker cp "$container:/tmp/chunker_fixtures/." "$here/../testdata/documents" >/dev/null
docker exec "$container" rm -rf /tmp/chunker_fixtures /tmp/record_fixture_chunks.py
