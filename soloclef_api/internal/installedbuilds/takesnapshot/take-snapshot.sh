#!/usr/bin/env bash
# Takes, or retakes, one installed phone build's snapshot: the openapi.json
# its client is typed from and the routes its source calls.
#
#   take-snapshot.sh <snapshot directory> <typescript package directory>
#
#   take-snapshot.sh soloclef_api/internal/installedbuilds/snapshots/ios-1.1.0-build4 \
#     /path/to/soloclef_mobile/node_modules/typescript
#
# The directory must already hold build.json (who runs the build, and
# `source_commit`, the commit its over-the-air update was published from)
# and resolutions.json (the path mentions app-calls.mjs cannot read as calls;
# `[]` to start, then one entry per mention it names). The script writes:
#
#   openapi.json  the app's own soloclef_mobile/openapi.json at that commit,
#                 byte for byte, refused unless its sha256 is the one the
#                 app's generated types were made from
#                 (src/lib/api/openapi.sha256), so the snapshot is the
#                 contract the build was compiled against and not a later one
#   calls.json    every route that source calls (app-calls.mjs)
#   build.json    its openapi_sha256, set to that sha256
#
# Run it from anywhere inside the repository: the commit is read with git,
# never from a checkout's working tree, so a branch that has moved on since
# the update was published does not leak into the snapshot.
set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "usage: $0 <snapshot directory> <typescript package directory>" >&2
  exit 2
fi
snapshot=$(cd "$1" && pwd)
typescript=$(cd "$2" && pwd)
here=$(cd "$(dirname "$0")" && pwd)

commit=$(node -e 'const b = require(process.argv[1]); if (!b.source_commit) process.exit(1); console.log(b.source_commit)' "$snapshot/build.json") || {
  echo "$snapshot/build.json names no source_commit" >&2
  exit 1
}
[ -f "$snapshot/resolutions.json" ] || { echo "$snapshot/resolutions.json is missing ([] to start)" >&2; exit 1; }

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

git show "$commit:soloclef_mobile/openapi.json" >"$work/openapi.json"
want=$(git show "$commit:soloclef_mobile/src/lib/api/openapi.sha256" | tr -d '[:space:]')
got=$(sha256sum "$work/openapi.json" | cut -d' ' -f1)
if [ "$want" != "$got" ]; then
  echo "openapi.json at $commit is $got, but the app's types were generated from $want:" >&2
  echo "the build's client is not typed from this document, so it is not the build's contract" >&2
  exit 1
fi

git archive "$commit" soloclef_mobile/src | tar -x -C "$work"
node "$here/app-calls.mjs" --src "$work/soloclef_mobile/src" --openapi "$work/openapi.json" \
  --resolutions "$snapshot/resolutions.json" --typescript "$typescript" >"$work/calls.json"

cp "$work/openapi.json" "$snapshot/openapi.json"
cp "$work/calls.json" "$snapshot/calls.json"
# The sha256 goes into build.json, where the Go side checks openapi.json
# against it on every test run: the snapshot cannot be edited by hand.
node -e 'const fs = require("fs"); const file = process.argv[1]; const b = JSON.parse(fs.readFileSync(file, "utf8")); b.openapi_sha256 = process.argv[2]; fs.writeFileSync(file, JSON.stringify(b, null, 2) + "\n")' "$snapshot/build.json" "$got"
echo "$snapshot: openapi.json $got, $(node -e 'console.log(require(process.argv[1]).length)' "$snapshot/calls.json") operations called"
