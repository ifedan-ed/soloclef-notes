# soloclef_worker

The Go worker tier. Replaces Celery as the **consumer** of Soloclef's three
queues and as their **scheduler**. Nearest `AGENTS.md` wins; the root one owns
organisation and TDD.

## Why this exists

`celery_heavy` at concurrency 4 was the throughput ceiling for podcasts, video
presentations and Studio deliverables: four prefork children, each a full
Python process with the parsing stack resident, so the fifth request
waited. Go gives a goroutine per in-flight job, per-job-class limits, and queue
depth you can see.

## The one rule that makes this safe

**Only the consumer moves.** `soloclef_backend` keeps enqueuing through
Celery's Python client for every route it still serves, and this worker reads
Celery's own protocol 2 envelope off Redis and writes Celery's own
`celery-task-meta-<id>` rows back.
Nothing in the API, the web app or the mobile app knows which worker ran a job,
so a queue can be handed over — or handed straight back to `celery_worker` — by
changing which process consumes it. The **schedule** is the one exception, and
a deliberate one: `internal/beat` publishes, so what it publishes is compared
with what Celery's own client publishes byte for byte. The protocol itself now lives in
`soloclef_celery`, a module of its own that this tier and `soloclef_api` both
require, because both of them publish and a second copy of a byte-exact encoder
is a second spelling (`custom/docs/go-api-tier.md`, "Where the encoder lives").
It is tested against an envelope captured from this host's own Redis, and its
result rows are read back by Python's `AsyncResult` in that package's contract
test.

## What stays in Python

Job bodies that need the Python ML stack cannot move: sentence-transformers,
chonkie, unstructured, pymupdf4llm. For those this worker owns
scheduling, limits, timeouts, retries, cancellation and metrics, and runs the
body in a Python process. Go owns the tier; Python keeps the parts that are
only Python.

Those processes are **warm**: importing this app costs 27-33s, so a pool of
`python -m app.tasks.serve` processes import once and then run body after body,
one at a time, over a pipe. A process that is worn out, wedged, out of step
with its reports or whose group has been signalled is retired and replaced,
never reused. `python -m app.tasks.run <task>` still runs one body in a process
of its own and is the simplest thing to debug with. Both report the same way
(`custom/docs/go-worker.md`, phase 4).

## Layout

| Path | Role |
|------|------|
| `soloclef_celery` | Not this module: the Celery protocol, shared with `soloclef_api`. The envelope, the queue keys, the enqueue clock, the routing table and the producer. |
| `internal/broker` | Redis consumption: BRPOP across the queues, Celery's ack emulation, concurrency limits, graceful shutdown, metrics. |
| `internal/executor` | One job in a process: the command, the warm pool, the deadlines, the kill, the outcome. A `broker.Runner`. |
| `internal/results` | The Redis result backend, writing only: the rows Python's `AsyncResult` reads. |
| `internal/beat` | Celery Beat: the schedule, the crontab, the single-scheduler lease and the per-window fence. The one thing here that **publishes**, through `soloclef_celery/produce`. |
| `internal/metrics` | The Prometheus text format, served from the consumer's and the beat's snapshots. No client library. |
| `cmd/worker` | The process: configuration from the environment, warming the task processes, consuming, scheduling, serving the scrape, shutting down. |

## Commands

```bash
cd soloclef_worker && go test ./...
cd soloclef_worker && go vet ./... && gofmt -l .
cd soloclef_worker && go build ./cmd/worker
```

The process reads its whole configuration from the environment and defaults to
consuming nothing **and scheduling nothing** (`SOLOCLEF_WORKER_QUEUES` and
`SOLOCLEF_WORKER_BEAT`). It serves `/metrics` on `:9464` unless
`SOLOCLEF_WORKER_METRICS_ADDR` is `off`. What the deployed worker sets, and
the image it runs in, are in `custom/docs/go-worker.md` ("The compose service
to add").

## The schedule

`internal/beat` runs `app/celery_app.py`'s `beat_schedule` — all 17 entries,
and the two merged from `app/automations` — reading the cadence and the queue
from **Celery's own** `SCHEDULE_CHECKER_INTERVAL` and
`CELERY_TASK_DEFAULT_QUEUE`, so the two schedulers cannot disagree. Beat is
off unless `SOLOCLEF_WORKER_BEAT` says otherwise, because two schedulers run
every scheduled task twice.

Two Redis keys are what make that safe: `soloclef-beat:lease`, which nothing
ticks without, and `soloclef-beat:fired:<entry>:<minute>`, which makes one
window's tick idempotent whatever the lease did. Where the schedule has got to
is `soloclef-beat:last-run`, in Redis rather than in a shelve file beside the
process, which is what makes a missed window recoverable.

`TestTheScheduleIsTheOneCeleryBeatWouldRun` reads
`celery_app.conf.beat_schedule` out of the backend container and compares it
entry for entry, so adding a Python beat entry fails a test here. It needs the
same two variables as the broker's interop tests.

`internal/broker`, `internal/results` and `internal/executor` have integration
tests that need a Redis of their own. They flush the database they are given,
so the variable is never defaulted — and because all three flush it, they must
not run at the same time:

```bash
docker run -d --name worker-test-redis --network danvics_litellm \
  -p 127.0.0.1:39637:6379 redis:8-alpine redis-server --save '' --appendonly no

cd soloclef_worker && SOLOCLEF_WORKER_TEST_REDIS=redis://127.0.0.1:39637/0 go test -p 1 ./...
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

`internal/executor`'s end-to-end tests go further: they publish with real
Celery, consume with this worker, run real task bodies — one per process and
many in a warm one — and read the rows back through Python's `AsyncResult`.
They need a container running **this checkout's** backend, which the live
backend container is not — its code is baked into the image, and the task
entrypoints are newer than it:

```bash
docker run -d --name worker-exec-python --network danvics_litellm \
  -v "$PWD/soloclef_backend/app/tasks/run.py:/app/app/tasks/run.py:ro" \
  -v "$PWD/soloclef_backend/app/tasks/serve.py:/app/app/tasks/serve.py:ro" \
  -e EMBEDDING_MODEL=fake-embeddings://384 \
  -e DATABASE_URL=postgresql+asyncpg://nobody:nobody@127.0.0.1:1/none \
  -e CELERY_BROKER_URL=redis://worker-test-redis:6379/0 \
  -e CELERY_RESULT_BACKEND=redis://worker-test-redis:6379/0 \
  -w /app soloclef-backend-fast:latest sleep infinity

cd soloclef_worker && \
  SOLOCLEF_WORKER_TEST_REDIS=redis://127.0.0.1:39637/0 \
  SOLOCLEF_WORKER_TEST_PYTHON_CONTAINER=worker-exec-python \
  SOLOCLEF_WORKER_TEST_REDIS_FROM_PYTHON=redis://worker-test-redis:6379/0 \
  go test ./internal/executor/ -count=1

docker rm -f worker-exec-python
```

That container's own `CELERY_BROKER_URL` names the test Redis, so nothing it
runs can reach the live broker. A job in a process of its own takes 21-35s
before its body starts — this app's import graph, not the worker — and a warm
one takes milliseconds. `TestTheWarmStartSavingAgainstAProcessPerJob` measures
both and needs only the container, not the Redis.

Celery resolves its broker from `CELERY_BROKER_URL` in preference to anything
its constructor is given, and in `soloclef-backend-1` that names the live
broker. The interop tests override it and assert the resolved URL before
publishing; anything new that runs Celery in that container must do the same.

## Do not

- Do not publish from here without going through `soloclef_celery/produce`.
  The Go API tier publishes too now, and the only thing keeping the two from
  drifting is that there is one encoder and one byte comparison against Celery's
  own client.
- Do not consume a live queue from a dev build. Queues are named in config and
  default to none for exactly this reason (`broker.ErrNoQueues`).
- Do not point a test at `soloclef-redis-1`. The integration tests flush their
  database, and they refuse to start if they find one of the three live queues
  in it.
- Do not give `app/tasks/run.py` anything to do but run one body and report on
  it. It must never write a result row: the rows are this worker's, and two
  writers would race over the same key. It is also not the place for task
  logic — the body it calls is the one Celery calls.
- Do not shorten `VisibilityWindow` below `KombuVisibilityWindow`. A window
  shorter than the one Celery's own workers use would let this worker restore a
  message a live Python worker is still running.
- Do not reuse a warm task process this worker has any reason to doubt. Its
  group having been signalled, a report for a job nobody was waiting for, a
  handover it would not take: each is a retirement, because the alternative is
  a row written against the wrong task.
- Do not publish anything but a beat tick **from this tier**. A worker that
  enqueued work of its own would be a scheduler nobody asked for; what this tier
  publishes is the schedule, and `soloclef_celery/celery.Encode` is held to a
  byte comparison against Celery's own client for exactly that reason.
- Do not enable beat on a dev build pointed at the live broker. It publishes
  real scheduled work, and the lease it takes is the live schedule's.
- Do not let a queue's concurrency limit exceed its task pool size. A job that
  waits for a warm process has already been taken off the queue, so the
  backlog moves out of `soloclef_worker_queue_depth` and into
  `soloclef_worker_jobs_in_flight`, where waiting and running look the same.
- Do not answer a scrape that could not be collected with zeroes. A dashboard
  draws zero depth as an empty queue, not as an unknown one.
- Do not treat `ShutdownGrace` as a drain. It is a backstop for a deploy that
  reached this process while it was working. **Every restart runs
  `soloclef_backend/scripts/deploy_gate.py --wait` first** — without it an
  in-flight job is killed and written FAILURE, holding a billing hold until the
  stale sweep and leaving a row in `GENERATING`.
