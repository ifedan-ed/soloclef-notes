# syntax=docker/dockerfile:1
#
# The migrate command: one static binary with the migrations compiled in.
# The build context is the repository root, as for the other Go images:
#
#   docker build -f soloclef_schema/Dockerfile -t soloclef-migrate .
#
# It is an image of its own, not a second binary in the API's, because
# bringing a database to a version is a step with one dependency (Postgres)
# and should not wait for, or be rebuilt with, the service that has thirty.
FROM golang:1.24-alpine AS build
WORKDIR /src/soloclef_schema
COPY soloclef_schema/go.mod soloclef_schema/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY soloclef_schema ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/soloclef-migrate ./cmd/migrate

# No shell and no package manager: the command needs neither, and reads the
# database password from a mounted file itself (cmd/migrate/settings.go).
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/soloclef-migrate /usr/local/bin/soloclef-migrate
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/soloclef-migrate"]
CMD ["up"]
