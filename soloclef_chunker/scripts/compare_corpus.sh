#!/usr/bin/env bash
# Chunk every live document with both chunkers and require identical chunks.
#
# The Python side runs inside the backend container, which holds the chonkie
# the stored chunks were cut with; the Go side runs from this checkout. The
# document text stays in the work directory and is never committed: it is real
# user content, and this repository has a public mirror.
#
# Read-only. It takes no lock, restarts nothing, and writes only under the
# work directory given as the first argument (default: a mktemp -d).
set -euo pipefail

container="${SOLOCLEF_BACKEND_CONTAINER:-soloclef-backend-1}"
work="${1:-$(mktemp -d)}"
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
mkdir -p "$work"

docker cp "$here/dump_python_chunks.py" "$container:/tmp/dump_python_chunks.py" >/dev/null
docker exec -w /app "$container" python /tmp/dump_python_chunks.py /tmp/python_chunks.jsonl >/dev/null
docker cp "$container:/tmp/python_chunks.jsonl" "$work/python_chunks.jsonl" >/dev/null
docker exec "$container" rm -f /tmp/dump_python_chunks.py /tmp/python_chunks.jsonl

(cd "$here/.." && go run ./cmd/chunkdump) \
  <"$work/python_chunks.jsonl" >"$work/go_chunks.jsonl"

python3 "$here/report_differences.py" \
  "$work/python_chunks.jsonl" "$work/go_chunks.jsonl"
