# syntax=docker.io/docker/dockerfile:1
# The sandbox service: one static binary on an empty image. It needs no
# shell, no CA certificates and no time zones: it speaks HTTP on the stack's
# sandbox network and on Docker's Unix socket, and its health probe is the
# binary itself (`sandboxd health`).

FROM golang:1.25-alpine AS build

WORKDIR /src

# No requirements (go.mod says why), so there is no go.sum and nothing to
# download; the module file alone is copied first all the same, so a source
# change keeps the toolchain layer.
COPY go.mod ./
COPY cmd ./cmd
COPY api ./api
COPY docker ./docker
COPY sandboxes ./sandboxes
# Static, so the empty runtime image needs no C library.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/sandboxd ./cmd/sandboxd


FROM scratch AS production

COPY --from=build /out/sandboxd /sandboxd

# Root, on purpose and only for one thing: Docker's socket is root's. The
# Compose service takes every capability away, makes the root filesystem
# read-only and forbids new privileges, so root here can open that socket
# and write the key's copy into its volume, and nothing else.
USER 0:0

ENTRYPOINT ["/sandboxd"]
