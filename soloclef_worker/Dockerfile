# The worker (cmd/worker): one Go binary, and the ffmpeg it voices and joins
# podcasts with. No Python: every job body runs in Go now (internal/jobs),
# so the 5 GB backend image this used to be built on is gone from it.
#
# The build context is the REPOSITORY ROOT, not this directory: the worker
# requires its sibling modules through relative replaces, so they have to be
# inside the context too. Build it as:
#   docker build -f soloclef_worker/Dockerfile -t soloclef-worker:latest .

FROM golang:1.25-alpine AS build
WORKDIR /src
# Every module's manifest first, so a source change does not re-download.
# Go reads the manifest of every module go.mod names before it builds
# anything, the indexer's (chunker, ingest) included, though cmd/worker
# compiles none of their source.
COPY soloclef_celery/go.mod soloclef_celery/go.sum* soloclef_celery/
COPY soloclef_agent/go.mod soloclef_agent/go.sum soloclef_agent/
COPY soloclef_assistant/go.mod soloclef_assistant/go.sum soloclef_assistant/
COPY soloclef_automations/go.mod soloclef_automations/go.sum soloclef_automations/
COPY soloclef_worker/go.mod soloclef_worker/go.sum soloclef_worker/
COPY soloclef_chunker/go.mod soloclef_chunker/
# No go.sum: the module requires nothing outside the standard library.
COPY soloclef_connectors/go.mod soloclef_connectors/
COPY soloclef_filestore/go.mod soloclef_filestore/go.sum soloclef_filestore/
COPY soloclef_ingest/go.mod soloclef_ingest/go.sum soloclef_ingest/
COPY soloclef_schema/go.mod soloclef_schema/go.sum soloclef_schema/
COPY soloclef_studio/go.mod soloclef_studio/go.sum soloclef_studio/
RUN cd soloclef_worker && go mod download
COPY soloclef_celery soloclef_celery
COPY soloclef_agent soloclef_agent
# The chat's tools and the assembly an automation's run is built by.
COPY soloclef_assistant soloclef_assistant
# What an automation's definition says, when its schedule fires and how a run is created.
COPY soloclef_automations soloclef_automations
# How a Drive grant is opened and renewed (internal/drivesync).
COPY soloclef_connectors soloclef_connectors
COPY soloclef_filestore soloclef_filestore
# The parsers a run's mail and Drive tools read a fetched file with
# (soloclef_ingest/parserconfig).
COPY soloclef_ingest soloclef_ingest
# The migrations are compiled into the binary: the start-up check compares
# the database's version with them (soloclef_schema/schemaversion).
COPY soloclef_schema soloclef_schema
# The sandbox client and the artifact saver the video render uses.
COPY soloclef_studio soloclef_studio
COPY soloclef_worker soloclef_worker
RUN cd soloclef_worker && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker

FROM alpine:3.20
# ffmpeg and ffprobe voice and join a podcast (internal/audiofile); the
# worker finds both on PATH at start-up and stops if either is missing. TLS
# roots for an https proxy or bucket, and curl for the compose health check.
RUN apk add --no-cache ffmpeg ca-certificates curl \
    && adduser -S -D -H -u 10003 -h /nonexistent worker \
    && mkdir -p /var/lib/soloclef-worker/podcasts \
    && chown -R worker /var/lib/soloclef-worker
COPY --from=build /out/worker /usr/local/bin/soloclef-worker
# A podcast's voiced lines are kept here while it is made, if the deployment
# names this directory (SOLOCLEF_WORKER_PODCAST_WORKDIR): the image only
# provides a place the worker's user can write.
USER worker
ENTRYPOINT ["/usr/local/bin/soloclef-worker"]
