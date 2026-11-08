# The disposable world's names, ports and made-up secrets, in one place because
# every script and the comparison itself have to agree on them. Sourced, never
# run.
#
# Nothing here names a real server and no value here is read from the live
# stack: every address is at example.invalid and every secret is invented for
# the comparison. The ports are deliberately not the live stack's.

# This file's own directory, resolved however it was sourced, because every
# other path here is relative to it.
PARITY_STACK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# The checkout this world is built from. A worktree is fine; the live checkout
# is refused by refuse_live_targets.sh.
PARITY_REPO="${PARITY_REPO:-$(git -C "$PARITY_STACK_DIR" rev-parse --show-toplevel)}"

# Where the Go binary, the logs and the captured side effects go. Never inside
# the checkout, so a failed run cannot leave anything stageable behind.
PARITY_STATE_DIR="${PARITY_STATE_DIR:-${TMPDIR:-/tmp}/soloclef-parity}"

# Containers. The `parity.disposable` label is what lets up.sh and down.sh
# remove them: a container of the same name without it is somebody else's and
# is refused rather than removed.
PARITY_LABEL="parity.disposable=true"
PARITY_NETWORK=parity-net
PARITY_DB=parity-db
PARITY_REDIS=parity-redis
PARITY_PYTHON=parity-python
PARITY_ANALYTICS=parity-analytics

# Published on loopback only. The Go tier runs on the host, so it reaches the
# database and the broker through these, and the containers reach each other by
# container name on PARITY_NETWORK.
PARITY_DB_PORT=55433
PARITY_REDIS_PORT=56380
PARITY_PYTHON_PORT=58001
PARITY_ANALYTICS_PORT=58002
PARITY_GO_PORT=58081

PARITY_DB_USER=parity
PARITY_DB_PASSWORD=parity-throwaway-not-a-secret
PARITY_DB_NAME=parity

# The session secret both tiers verify with, and the credentials minted from it
# (stack/mint_credentials.py). It is made up here and exists only for the life
# of the world.
PARITY_SESSION_SECRET=parity-comparison-secret

# app/config: CELERY_TASK_DEFAULT_QUEUE's own default. Both tiers enqueue here
# and nothing consumes it, so a write's message is still on the list when the
# comparison reads it.
PARITY_CELERY_QUEUE=surfsense

# The analytics stand-in's project key. Both tiers send to it rather than to
# anything outside this host; its payloads are not compared (parity/README.md,
# "What is not compared").
PARITY_ANALYTICS_KEY=phc_parity_standin_not_a_secret

# The site both tiers believe they are, and the origin a cross-site request
# comes from. Neither resolves.
PARITY_PUBLIC_URL=https://notes.example.invalid
PARITY_FOREIGN_ORIGIN=https://attacker.example.invalid

PARITY_PYTHON_BASE="http://127.0.0.1:${PARITY_PYTHON_PORT}"
PARITY_GO_BASE="http://127.0.0.1:${PARITY_GO_PORT}"
