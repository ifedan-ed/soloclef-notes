# soloclef_worker

The Go worker tier. Replaces Celery as the **consumer** of Soloclef's three
queues. Nearest `AGENTS.md` wins; the root one owns organisation and TDD.

## Why this exists

`celery_heavy` at concurrency 4 was the throughput ceiling for podcasts, video
presentations and Studio deliverables: four prefork children, each a full
Python process with torch and the parsing stack resident, so the fifth request
waited. Go gives a goroutine per in-flight job, per-job-class limits, and queue
depth you can see.

## The one rule that makes this safe

**Only the consumer moves.** `surfsense_backend` keeps enqueuing through
Celery's Python client, unchanged, and this worker reads Celery's own protocol 2
envelope off Redis and writes Celery's own `celery-task-meta-<id>` rows back.
Nothing in the API, the web app or the mobile app knows which worker ran a job,
so a queue can be handed over — or handed straight back to `celery_worker` — by
changing which process consumes it. `internal/celery` is tested against an
envelope captured from this host's own Redis, and its result rows are read back
by Python's `AsyncResult` in that package's contract test.

## What stays in Python

Job bodies that need the Python ML stack cannot move: sentence-transformers,
chonkie, unstructured, pymupdf4llm, torch. For those this worker owns
scheduling, limits, timeouts, retries, cancellation and metrics, and executes
the body as a Python process. Go owns the tier; Python keeps the parts that are
only Python.

## Layout

| Path | Role |
|------|------|
| `internal/celery` | Celery protocol 2: envelope in, result rows out. No Redis, no I/O. |
| `internal/broker` | Redis consumption: BRPOP across the queues, Celery's ack emulation, concurrency limits, graceful shutdown, metrics. |
| `cmd/worker` | The process: config, queue consumption, shutdown. |

## Commands

```bash
cd soloclef_worker && go test ./...
cd soloclef_worker && go vet ./... && gofmt -l .
```

`internal/broker`'s integration tests need a Redis of their own. They flush the
database they are given, so the variable is never defaulted:

```bash
docker run -d --name worker-test-redis --network danvics_litellm \
  -p 127.0.0.1:39637:6379 redis:8-alpine redis-server --save '' --appendonly no

cd soloclef_worker && SOLOCLEF_WORKER_TEST_REDIS=redis://127.0.0.1:39637/0 go test ./...
```

The Celery interop tests additionally run real Celery and real kombu against
that same Redis, from a container that has them installed:

```bash
cd soloclef_worker && \
  SOLOCLEF_WORKER_TEST_REDIS=redis://127.0.0.1:39637/0 \
  SOLOCLEF_WORKER_TEST_CELERY_CONTAINER=soloclef-backend-1 \
  SOLOCLEF_WORKER_TEST_REDIS_FROM_CELERY=redis://worker-test-redis:6379/0 \
  go test ./internal/broker/ -count=1

docker rm -f worker-test-redis
```

Celery resolves its broker from `CELERY_BROKER_URL` in preference to anything
its constructor is given, and in `soloclef-backend-1` that names the live
broker. The interop tests override it and assert the resolved URL before
publishing; anything new that runs Celery in that container must do the same.

## Do not

- Do not change how tasks are **produced**. A Go producer would strand every
  queued message the moment it is deployed.
- Do not consume a live queue from a dev build. Queues are named in config and
  default to none for exactly this reason (`broker.ErrNoQueues`).
- Do not point a test at `soloclef-redis-1`. The integration tests flush their
  database, and they refuse to start if they find one of the three live queues
  in it.
- Do not shorten `VisibilityWindow` below `KombuVisibilityWindow`. A window
  shorter than the one Celery's own workers use would let this worker restore a
  message a live Python worker is still running.
