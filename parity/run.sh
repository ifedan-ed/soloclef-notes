#!/usr/bin/env bash
# The whole proof, in one command: stand up a disposable world, ask both tiers
# every case, take the world down, and exit non-zero on the first difference
# either tier could not account for.
#
#   bash parity/run.sh                       # every route
#   bash parity/run.sh regressions.json      # one case file
#   PARITY_KEEP=1 bash parity/run.sh         # leave the world up to poke at
#
# The world comes down from a trap, so it comes down when a comparison fails,
# when a case file is malformed, and when the run is interrupted.
set -euo pipefail
cd "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source stack/world.sh

down() { bash stack/down.sh; }
if [[ -z "${PARITY_KEEP:-}" ]]; then
  trap down EXIT
fi

bash stack/up.sh

export PARITY_STATE_DIR PARITY_DB PARITY_DB_USER PARITY_DB_NAME PARITY_REDIS
export PARITY_PYTHON_BASE PARITY_GO_BASE
# Unbuffered, so a redirected run shows the case it is on rather than the last
# eight kilobytes it happened to flush.
python3 -u comparison/run.py "$@"
