# The mail sender. Unlike the worker, it runs no Python: it opens a socket or
# makes an HTTPS request, so it needs a CA bundle and nothing else. That is the
# point of porting it — the service that delivers email stops carrying the
# parsing and embedding stack to do it.
#
# The build context is this directory, because this module requires nothing
# from the rest of the tree:
#   docker build -t soloclef-mail-sender:latest soloclef_mail
#
# It is not deployed. custom/docs/go-mail.md says what a cutover needs, and the
# first thing it needs is that only one sender process runs.
FROM golang:1.24-alpine AS build
WORKDIR /src
# The manifests first, so a source change does not re-download.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/mail_sender ./cmd/mail_sender

FROM alpine:3.21
# For the smtp2go API's certificate, and for STARTTLS to the SMTP endpoint.
RUN apk add --no-cache ca-certificates
COPY --from=build /out/mail_sender /usr/local/bin/soloclef-mail-sender
# No HEALTHCHECK: it answers nothing. What says it is working is the queue
# depth and its own log lines, which name the same mail ids the request side
# logged.
ENTRYPOINT ["/usr/local/bin/soloclef-mail-sender"]
