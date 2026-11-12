# soloclef_mail

The mail transports in Go: the contract a sender delivers through
(`mailer`), the MIME builder (`mailer/mime`), and two transports, SMTP
(`mailer/smtp`) and the smtp2go HTTP API (`mailer/smtp2go`). Nearest
`AGENTS.md` wins; the root one owns organisation and TDD.

The API sends with it today: the password-reset link and an automation's
result email, over SMTP (`SOLOCLEF_API_SMTP_*` in `docker/.env.go.example`).
The smtp2go transport is built and tested but no service is configured to
use it yet.

## Why it is a module and not a package

Both Go tiers may send mail, and `internal` cannot be shared across modules,
so this is a module, required with a relative `replace` for the reason
`soloclef_celery` is one. It has no dependency outside the standard library.

## Layout

| Path | What |
|------|------|
| `mailer` | `Mailer` (send one message), `Message`, and the three failures a transport reports: unavailable (try again later), rejected (do not), and the rest |
| `mailer/mime` | The message's bytes: headers, encoded words, folding, parts, boundaries |
| `mailer/smtp` | SMTP with STARTTLS, implicit TLS or none, and AUTH |
| `mailer/smtp2go` | The smtp2go HTTP API |
| `pythonjson` | The one JSON spelling the smtp2go request body is pinned to |

## Do not

- Do not "tidy" the MIME bytes. The fold points, the boundary length, the
  encoded word that absorbs the plain words behind it, the 8-bit body that is
  not encoded at all: each looks like a mistake and each is what has been
  going out. They were recorded from the sender this replaced
  (`mailer/mime/testdata/python_mime_bytes.json`) and are held there. A
  different fold point is a different message, and what it changes is which
  folder it lands in.
- Do not encode a line ending in a header. `mime.Build` refuses one, and that
  refusal is what stops a subject built out of someone's text from adding
  headers of its own.
- Do not add a transport fallback. A deployment names the one way mail leaves:
  an unreachable or unconfigured mail server is an error to fix, never a reason
  to drop mail quietly or to try another way.
- Do not log an address, a link, a code or a key.

## Commands

```bash
docker run --rm -v "$PWD":/src -w /src/soloclef_mail golang:1.25 \
  sh -c 'gofmt -l . && go vet ./... && go test ./...'
```

(from the repository root). Everything here is a unit test: the SMTP tests
talk to an in-process fake server, the smtp2go tests to an `httptest` server.
