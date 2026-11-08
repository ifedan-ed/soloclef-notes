# The worker, in the image whose Python it runs. It starts
# `python -m app.tasks.serve` as its own child, so the interpreter, the venv
# and /app have to be in this container with it — and so that the task process
# is a real member of the process group this worker signals when a job has to
# be killed along with whatever ffmpeg it started.
#
# The build context is the REPOSITORY ROOT, not this directory: the worker
# requires soloclef_celery through a relative replace, so that module has to be
# inside the context too. Build it as:
#   docker build -f soloclef_worker/Dockerfile -t soloclef-worker:latest .
#
# Declared before the first stage because a FROM can only read an ARG from
# before any build stage.
ARG WORKER_BASE=soloclef-backend-fast:latest

FROM golang:1.24-alpine AS build
WORKDIR /src
# Both modules' manifests first, so a source change does not re-download.
# soloclef_celery has no dependencies and so no go.sum; the glob keeps this
# working if it ever gains one.
COPY soloclef_celery/go.mod soloclef_celery/go.sum* soloclef_celery/
COPY soloclef_worker/go.mod soloclef_worker/go.sum soloclef_worker/
RUN cd soloclef_worker && go mod download
COPY soloclef_celery soloclef_celery
COPY soloclef_worker soloclef_worker
RUN cd soloclef_worker && CGO_ENABLED=0 go build -trimpath -o /out/worker ./cmd/worker

FROM ${WORKER_BASE}
COPY --from=build /out/worker /usr/local/bin/soloclef-worker
ENTRYPOINT ["/usr/local/bin/soloclef-worker"]
