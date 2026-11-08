# syntax=docker.io/docker/dockerfile:1
# The Go MCP server — remote (streamable-http) image.
#
# Serves /mcp with a per-request credential and no baked secret, plus a public
# /health probe, exactly as surfsense_mcp's image does. Not wired into
# docker/docker-compose.yml: the cutover is one image swap on the mcp service,
# described in custom/docs/go-mcp.md.

FROM golang:1.25-alpine AS build

WORKDIR /src

# Dependencies first, so a source change does not re-resolve the module graph.
COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
# Static, so the runtime stage needs no C library.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/soloclef-mcp ./cmd/mcp


FROM alpine:3.21 AS production

# curl is used by the container healthcheck probe against /health.
RUN apk add --no-cache curl ca-certificates

COPY --from=build /out/soloclef-mcp /usr/local/bin/soloclef-mcp

# The same variables the Python image sets, so one compose service serves either.
ENV SURFSENSE_MCP_TRANSPORT=streamable-http \
    SURFSENSE_MCP_HOST=0.0.0.0 \
    SURFSENSE_MCP_PORT=8080

EXPOSE 8080

# An unprivileged account: this process reads no file the image does not ship,
# except an upload a self-hosted caller names.
RUN adduser -S -u 10001 mcp
USER mcp

CMD ["soloclef-mcp"]
