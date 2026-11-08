#!/usr/bin/env bash
# Refuse to run against anything live.
#
# The comparison truncates tables, reseeds them between every write and drains
# the broker's queues. Pointed at the running stack it would empty the app's
# database and eat work the workers were about to do, so this check is a
# precondition of standing the world up and not a nicety — the same reason
# soloclef_worker's beat tests refuse a Redis holding a live queue
# (soloclef_worker/internal/beat/redis_test.go).
#
# Two kinds of check: the names and ports this world will use must not be the
# live stack's, and anything already answering on them must be something this
# suite made. The emptiness checks that need a connection are in up.sh, after
# the containers answer.
#
# The names in world.sh are constants and not overridable settings, so the first
# check below cannot fail today — which is the point. It is there for the edit
# that would make it fail, and it fails loudly rather than letting a renamed
# target through. The checks that bite a correctly configured run are the label,
# the port, the checkout and up.sh's emptiness ones.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/world.sh"

refuse() { echo "parity: $*" >&2; exit 1; }

# The live stack's own containers, by the names docker/docker-compose.yml gives
# them. Nothing here may be one of these.
live_containers=(soloclef-db-1 soloclef-redis-1 soloclef-backend-1
                 soloclef-pgbouncer-1 soloclef-celery_worker-1
                 soloclef-celery_beat-1 soloclef-go_worker_heavy-1)

for ours in "$PARITY_DB" "$PARITY_REDIS" "$PARITY_PYTHON" "$PARITY_ANALYTICS"; do
  for live in "${live_containers[@]}"; do
    [[ "$ours" == "$live" ]] && refuse "$ours is the live stack's container"
  done
  # A name that merely looks like the live stack's is refused too: the prefix is
  # how compose names everything it owns, including containers added later.
  [[ "$ours" == soloclef-* ]] && refuse "$ours is in the live stack's namespace"
done

# An existing container of one of our names is only ours if we labelled it.
# Anything else is somebody's work and is not removed.
for ours in "$PARITY_DB" "$PARITY_REDIS" "$PARITY_PYTHON" "$PARITY_ANALYTICS"; do
  if docker container inspect "$ours" >/dev/null 2>&1; then
    labelled=$(docker container inspect "$ours" \
      --format '{{index .Config.Labels "parity.disposable"}}' 2>/dev/null || true)
    [[ "$labelled" == "true" ]] || \
      refuse "a container named $ours exists and this suite did not create it"
  fi
done

# The live checkout is where the running stack is built and deployed from. The
# comparison only reads it, but an agent pointed there has the wrong worktree.
case "$PARITY_REPO" in
  /home/danvics/docker/surfsense) refuse "PARITY_REPO is the live checkout" ;;
esac

# The ports the live stack publishes on loopback, read from Docker rather than
# guessed, so a port this world wants can never be one something else answers
# on.
for port in "$PARITY_DB_PORT" "$PARITY_REDIS_PORT" "$PARITY_PYTHON_PORT" \
            "$PARITY_ANALYTICS_PORT" "$PARITY_GO_PORT"; do
  holder=$(docker ps --format '{{.Names}} {{.Ports}}' \
    | grep -E "(:|->)${port}->" | awk '{print $1}' | head -1 || true)
  if [[ -n "$holder" ]]; then
    labelled=$(docker container inspect "$holder" \
      --format '{{index .Config.Labels "parity.disposable"}}' 2>/dev/null || true)
    [[ "$labelled" == "true" ]] || \
      refuse "port $port is published by $holder, which this suite did not create"
  fi
done

echo "parity: no live target in reach"
