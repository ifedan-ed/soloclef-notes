# soloclef_worker

The Go worker tier: the consumer of Soloclef's background queues and the
runner of every job on them. Nearest `AGENTS.md` wins; the root one owns
organisation and TDD.

## Why this exists

`celery_heavy` at concurrency 4 was the throughput ceiling for podcasts,
video presentations and Studio deliverables. Go gives a goroutine per
in-flight job, per-job-class limits, and queue depth you can see.

## No Python

Every job body runs here, in Go, on the user-keyed schema
(`soloclef_schema/migrations/00001_baseline.sql`): `internal/jobs` is the
table of task name to Go function, and the consumer hands it every message.
The Python bodies read workspace columns that schema does not have, so they
are gone, and so are the warm Python pool (`internal/executor`) and the
Celery result rows (`internal/results`): nothing polls `AsyncResult`; each
job's outcome is the row it is about. The image is a Go binary and ffmpeg.

The messages keep Celery's protocol 2 envelope, through the one encoder in
`soloclef_celery`, which the API tier publishes with: one spelling on both
ends, held by that module's tests.

## Layout

| Path | Role |
|------|------|
| `internal/jobs` | Task name to Go job, with each job's deadline. A `broker.Runner`. |
| `internal/podcast` | `podcast.draft_transcript`: brief to transcript to voiced, joined MP3 kept as the podcast's artifact. Draft and render in one job. |
| `internal/notebookname` | `notebook_autoname`: a notebook named from its sources or first question. |
| `internal/folderdeletion` | `delete_folder_documents_background`: a deleted folder's documents, then the folders. |
| `internal/accounterasure` | `erase_account`: a locked-out account's vectors and files, then the account. |
| `internal/videojob` | `deliverables.execute_queued`: the narrated video. The model writes a narrated deck (soloclef_studio/deckspec) with one passage per build step, each passage is voiced on its own and each slide's passages joined with a measured pause (each part comes in as its passage starts), the deck is laid out and animated by soloclef_studio/deckrender, only the moving frames are rasterised by resvg, ffmpeg joins them with the voice, and the MP4 is checked and kept as an artifact (soloclef_studio/artifactsave). No sandbox. With `SOLOCLEF_WORKER_VIDEO_ENABLED=false`, marks each queued video failed at once. |
| `internal/jobreconcile` | `deliverables.reconcile_queued`: republishes a queued video whose message never reached the queue. A message that schedules its own successor each minute; no beat. |
| `internal/documentremoval` | Removing documents (points, chunks, files, rows) and whole accounts. |
| `internal/llm`, `internal/speech`, `internal/audiofile` | The proxy's chat and speech routes, and ffmpeg. |
| `internal/livepublish` | The live-change bus on the broker's connection (soloclef_notify/livechange): what the indexer (`indexstore`, each status move) and the worker's jobs (`cmd/worker/live_changes.go` wraps their stores; automations, folder removal and refusals publish directly) tell a person's open screens after a write they show. A hint, never a failure: a publish that cannot be sent is logged. |
| `internal/pushdelivery` | Notifications: the notifier every job that finishes work for a person calls (soloclef_notify/notify), and the two push jobs (`notifications.deliver_push`, `.check_push_receipts`, soloclef_notify/delivery) with their settings and Valkey reads. With `SOLOCLEF_WORKER_PUSH_ENABLED=false` the inbox rows are written and nothing is pushed. iPhones on APNs tokens are pushed straight to Apple when the five `SOLOCLEF_WORKER_APNS_*` are set (all or none); Android phones through Expo. |
| `internal/appupdate` | `app_store.check_update`: an hourly tick (self-scheduling, like the reconciler's) that asks the App Store once a day for the iPhone app's version (`SOLOCLEF_WORKER_APP_STORE_BUNDLE_ID`, read only with pushes on; empty is no check) and tells each iPhone on an older version once per store version (soloclef_notify/notify `AppUpdated`), holding an account in its quiet hours for a later pass. Android is not told: there is no Play listing. |
| `internal/indexrequest` | Publishing `index_document` for a document a job wrote. |
| `internal/workerconfig` | The jobs' settings. All required; no defaults. |
| `internal/proxystub`, `internal/testdatabase` | Test helpers: a stub proxy speaking the OpenAI shapes, a throwaway database. |
| `internal/broker` | Redis consumption: BRPOP, Celery's ack emulation, limits, shutdown, metrics. |
| `internal/beat` | The scheduler. Its schedule is empty (`schedule.go` says why each Python entry is gone). |
| `internal/metrics` | The Prometheus text format. |
| `cmd/worker` | The process: settings, the database check, the job table, consuming, the scrape. |
| `cmd/indexer` | The indexing worker, on `soloclef.index` only. |

## Commands

```bash
cd soloclef_worker && go test ./...
cd soloclef_worker && go vet ./... && gofmt -l .
cd soloclef_worker && go build ./cmd/worker
```

The job tests that need a database, a Redis or ffmpeg skip without them. To
run them, migrate a throwaway Postgres 17 with `soloclef_schema` and start a
throwaway Redis:

```bash
docker run -d --name worker-test-pg -e POSTGRES_PASSWORD=x -e POSTGRES_DB=jobs \
  -p 127.0.0.1:39877:5432 postgres:17-alpine
(cd soloclef_schema && SOLOCLEF_MIGRATE_DATABASE_URL='postgres://postgres:x@127.0.0.1:39877/jobs?sslmode=disable' go run ./cmd/migrate up)
docker run -d --name worker-test-redis -p 127.0.0.1:39878:6379 valkey/valkey:8-alpine

cd soloclef_worker && \
  SOLOCLEF_WORKER_TEST_DATABASE_URL='postgres://postgres:x@127.0.0.1:39877/jobs?sslmode=disable' \
  SOLOCLEF_WORKER_TEST_REDIS=redis://127.0.0.1:39878/0 \
  go test ./... -count=1

docker rm -f worker-test-pg worker-test-redis
```

The integration tests empty the Redis they are given, so the variable is
never defaulted. Each package empties a database number of its own on that
server (`internal/testredis` lists them), so packages may run side by side.
`go test ./internal/podcast/ -run '^$' -bench Render` measures the render.

## Do not

- Do not publish from here without going through `soloclef_celery/produce`.
  There is one encoder, and one byte comparison against Celery's own client.
- Do not consume a live queue from a dev build. Queues are named in config and
  default to none for exactly this reason (`broker.ErrNoQueues`).
- Do not point a test at a live Redis. The integration tests flush their
  database, and they refuse to start if they find a live queue in it.
- Do not shorten `VisibilityWindow` below `KombuVisibilityWindow`.
- Do not write a statement in a job that names a row by its id alone. The
  message carries an id; the owner is read off the row (or carried in the
  message), and every later statement names it.
- Do not write a podcast's transcript or episode without re-reading the
  status under `FOR UPDATE` in the same transaction (`podcast/store.go`,
  `lockStatus`). That is what keeps a cancel from being overwritten.
- Do not enable beat. The Go stack schedules nothing, and a worker with
  `SOLOCLEF_WORKER_BEAT` on refuses to start.
- Do not answer a scrape that could not be collected with zeroes.
- `ShutdownGrace` is the drain a deploy waits through: the worker's
  deployment sets 15 minutes (the Compose file says why), the indexer keeps
  the 30-second default. Work still running at its end is cut off and given
  `HandBack` (10s) to give its message back: a video releases its attempt
  (counted with takeovers; the third cut fails it as `interrupted`), a
  podcast carries on from the lines already voiced. A stop_grace_period must
  be at least the two together.
