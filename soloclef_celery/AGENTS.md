# soloclef_celery

The Celery wire protocol in Go. Required by `soloclef_worker` and
`soloclef_api`; it serves no process of its own. Nearest `AGENTS.md` wins; the
root one owns organisation and TDD.

## Why it is a module and not a package

Both Go tiers publish Celery tasks — the worker tier's beat publishes the
schedule, the API tier publishes the background work its writes owe — and both
have to be indistinguishable from Celery's own Python client, byte for byte. A
second copy of the encoder is a second spelling, and the whole point is that
there is one. `internal` cannot be shared across modules, so this is a module,
required with a relative `replace` from each tier rather than a tagged version:
both are deployed from this checkout, and a tag would let them publish different
bytes for the length of a version bump.

The decision and what it replaced are in `custom/docs/go-api-tier.md`, "Where
the encoder lives".

## Layout

| Path | Role |
|------|------|
| `celery` | Protocol 2: the envelope in and out, Python's `json.dumps` spelling and `repr`, and the `celery-task-meta-<id>` result rows. No I/O. |
| `queues` | The Redis list keys kombu holds a queue's work in, one per priority step. |
| `monotonic` | `CLOCK_MONOTONIC`, the clock `surfsense.enqueued_at_ns` is in. |
| `routing` | `app/celery_app.py`'s `task_routes`: the heavy queue, the connectors queue, and the default everything else falls through to. |
| `produce` | Putting a task on a queue: the ids, the routing, this app's two `before_task_publish` headers, `countdown`, `expires`, the limits, and one `LPUSH`. |

## Do not

- Do not add a dependency. The two tiers reach Redis with different clients
  (go-redis and valkey-go), and the one call a producer makes is
  `produce.Broker` — one method, satisfied in five lines on either side. A Redis
  client here would make one tier carry the other's.
- Do not change a spelling without the byte comparison. Every field, every
  separator, every header Celery writes `null` and the two headers this app
  adds are there because an envelope published here is compared with one
  Celery's own client published for the same call. Four divergences have been
  found that way and none of them was visible from inside Go.
- Do not rename `surfsense.enqueued_at_ns` or the queue names. Both are read by
  Python producers and consumers that were not renamed with the tree, and
  `custom/docs/rename-soloclef.md` says why the old word stays. The log-context
  header is `soloclef_log_context` for the same reason in reverse: it is the
  name `app/observability/logs/celery_signals.py` writes.
- Do not guess at a Celery option this app does not use. `expires` as a
  datetime, chains, callbacks, `shadow` and a non-empty exchange are each
  absent on purpose: writing one without a comparison against Celery would put a
  value in the envelope that Celery did not compute.

## Commands

```bash
cd soloclef_celery && go test ./...
cd soloclef_celery && go vet ./... && gofmt -l .
```

The interop tests compare against Celery's own client and need a throwaway Redis
and a container with this app installed. Neither is defaulted, so they never
publish to the live broker by accident:

```bash
docker run -d --name celery-test-redis --network danvics_litellm \
  -p 127.0.0.1:39741:6379 redis:8-alpine redis-server --save '' --appendonly no

cd soloclef_celery && \
  SOLOCLEF_CELERY_TEST_CONTAINER=soloclef-backend-1 \
  SOLOCLEF_CELERY_TEST_BROKER=redis://celery-test-redis:6379/0 \
  go test ./produce/ -count=1

docker rm -f celery-test-redis
```

`TestTheRoutingIsTheOneCeleryWouldApply` reads `task_routes` out of the backend
container, so adding a route there fails a test here.
