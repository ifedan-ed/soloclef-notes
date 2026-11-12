# syntax=docker/dockerfile:1
#
# The Go API (cmd/api): one static binary in an image with nothing else in it.
#
# The build context is the REPOSITORY ROOT, not this directory, as it is for
# soloclef_worker's two Dockerfiles: this module requires its sibling modules
# through relative replaces (go.mod), so they have to be inside the context.
#
#   docker build -f soloclef_api/Dockerfile -t soloclef-api:latest .
#
# A module added to go.mod's replaces needs its two COPY lines here in the
# same change; the build stops at `go mod download` otherwise, naming it.

# At least the `go` line of soloclef_api/go.mod: the official image will not
# download a newer toolchain than its own, so an older one stops the build.
FROM golang:1.25-alpine AS build
WORKDIR /src
# The manifests first and the sources after, so that editing a .go file does
# not download every dependency again: that layer is rebuilt only when a
# go.mod or go.sum changes. soloclef_celery and soloclef_mail have no
# dependencies and so no go.sum; the glob keeps this working if one gains one.
COPY soloclef_celery/go.mod soloclef_celery/go.sum* soloclef_celery/
COPY soloclef_agent/go.mod soloclef_agent/go.sum soloclef_agent/
COPY soloclef_assistant/go.mod soloclef_assistant/go.sum soloclef_assistant/
COPY soloclef_automations/go.mod soloclef_automations/go.sum soloclef_automations/
# No go.sum: the module requires nothing outside the standard library.
COPY soloclef_connectors/go.mod soloclef_connectors/
COPY soloclef_filestore/go.mod soloclef_filestore/go.sum soloclef_filestore/
COPY soloclef_ingest/go.mod soloclef_ingest/go.sum soloclef_ingest/
COPY soloclef_mail/go.mod soloclef_mail/go.sum* soloclef_mail/
COPY soloclef_schema/go.mod soloclef_schema/go.sum soloclef_schema/
COPY soloclef_studio/go.mod soloclef_studio/go.sum soloclef_studio/
# The chunker's manifest and nothing else of it. No package of the chunker is
# compiled into the API (its one cgo package belongs to the indexer), but
# soloclef_ingest's go.mod names the module, and Go reads the manifest of
# every module named before it builds anything. Its source is deliberately
# left out: it holds a Rust `target/` of hundreds of megabytes.
COPY soloclef_chunker/go.mod soloclef_chunker/
COPY soloclef_api/go.mod soloclef_api/go.sum soloclef_api/
# The module cache is a cache mount: it survives a changed go.sum, so a
# dependency bump fetches the one new module and not all of them.
RUN --mount=type=cache,target=/go/pkg/mod \
    cd soloclef_api && go mod download
COPY soloclef_celery soloclef_celery
COPY soloclef_agent soloclef_agent
# The chat's tools and the assembly of a turn's agent, shared with the worker.
COPY soloclef_assistant soloclef_assistant
# What a saved automation may say and how a run is created, shared with the worker.
COPY soloclef_automations soloclef_automations
# The sealing of a connection's stored credential, shared with the worker.
COPY soloclef_connectors soloclef_connectors
COPY soloclef_filestore soloclef_filestore
COPY soloclef_ingest soloclef_ingest
# The SMTP transport of the reset mail; its queue packages are not compiled.
COPY soloclef_mail soloclef_mail
# The migrations are compiled into the binary: the start-up check compares
# the database's version with them (soloclef_schema/schemaversion).
COPY soloclef_schema soloclef_schema
# The sandbox client and the artifact saver, shared with the worker.
COPY soloclef_studio soloclef_studio
COPY soloclef_api soloclef_api
# CGO off: nothing here links C, and a static binary is what lets the last
# stage have no libc at all. -trimpath keeps this machine's paths out of the
# binary; -s -w drop the symbol table and DWARF, about a quarter of its size,
# which nothing reads in production (a panic's stack trace does not need them).
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd soloclef_api && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
    -o /out/soloclef-api ./cmd/api

# distroless/static: CA roots (for an https model proxy or bucket), the time
# zone database and a passwd file, and no shell, package manager or libc. The
# API reads strangers' requests; an image with no shell in it has nothing for
# a break-in to run. That is also why the healthcheck below is the binary
# itself (cmd/api/healthcheck.go) and not curl.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/soloclef-api /usr/local/bin/soloclef-api
# uid 65532. A secret mounted for this container has to be readable by it.
USER nonroot:nonroot
# No port is exposed and no setting is baked in: the address is
# SOLOCLEF_API_LISTEN_ADDRESS, required like every other setting
# (internal/config), and the probe reads the same variable.
HEALTHCHECK --interval=10s --timeout=5s --start-period=20s --retries=5 \
    CMD ["/usr/local/bin/soloclef-api", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/soloclef-api"]
