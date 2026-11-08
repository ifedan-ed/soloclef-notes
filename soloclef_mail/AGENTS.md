# soloclef_mail

The mail queue in Go: the job a producer writes, the transports that deliver
it, and the sender process that drains it. Nearest `AGENTS.md` wins; the root
one owns organisation and TDD.

The Python it mirrors is `soloclef_backend/app/mail_queue/` (the queue and its
senders) and `soloclef_backend/app/mailer/` (the transports). Neither moves.
Both senders drain the same Redis keys, so either one can be the one running.

## Why it is a module and not a package

Mail was the last mechanism a Go process could not reach. A Go tier could
enqueue Celery work but not an email, so every feature that mails something had
to stay Python.

Both Go tiers can enqueue mail — the API tier owns password reset and
workspace invites, and the worker tier owns the automation result email — and
the bytes they put on `soloclef:mail:ready` have to be the bytes
`app/mail_queue/job.py` writes, because a Python `mail_sender` is reading them.
A second copy of that encoder is a second spelling. `internal` cannot be shared
across modules, so this is a module, required with a relative `replace` rather
than a tagged version, for the reason `soloclef_celery` is.

**It carries go-redis and that costs the tiers nothing.** The dependency is
only in `queuestore` and `cmd/mail_sender`. A tier that imports `enqueue` and
`mailjob` gets neither the requirement nor the package — module graph pruning
keeps it out of that tier's `go.mod`, `go.sum` and build list, which was
checked with a scratch module before this was laid out this way. So the rule
`soloclef_celery` has ("no dependency here, or one tier carries the other's
Redis client") is met by where the dependency sits rather than by not having
one, and the sender process lives with the code it is.

## Layout

| Path | Role |
|------|------|
| `mailjob` | One queued email as `job.py` writes it: the fields, the expiry, the next try, and the exact JSON in and out. No I/O. |
| `pythonjson` | Text and numbers as Python's `json` writes them, in **both** spellings this app uses: `ensure_ascii=True` for the queue entry and `ensure_ascii=False` for the smtp2go request body. `repr(float)` is here too. |
| `queuekeys` | The Redis keys the queue lives in. Both halves need them. |
| `enqueue` | Handing a mail to the queue: `enqueue.py`'s half. One `LPUSH` through a one-method `Broker` each tier satisfies with its own client. |
| `retrypolicy` | `retry_policy.py`'s delays, unchanged. |
| `mailer` | The port and its three failures: `protocol.py`. |
| `mailer/mime` | The message `smtp.py`'s `_build_mime` builds, and the bytes `smtplib` puts in DATA for it. |
| `mailer/smtp` | The SMTP transport: the security modes, the kept session, and which step was refused. |
| `mailer/smtp2go` | The smtp2go HTTP API transport. |
| `drain` | One sender's loop: `sender.py`. |
| `dedupe` | Not sending the same mail twice: the keys, the window, and the two guards. `app/mail_queue/dedupe/`. No I/O. |
| `dedupe/dedupetest` | The in-memory `dedupe.Keys` every package above it tests against. |
| `dedupestore` | Those keys in Redis, with go-redis behind them. |
| `queuestore` | The moves between the keys: `queue_store.py`, including its Lua script. |
| `recovery` | Putting back what a stopped run held, and dropping its claims: `recovery.py`. |
| `service` | The senders and the retry clock: `service.py`. |
| `cmd/mail_sender` | The process, and the settings it reads. |
| `scripts` | The Python that generates every reference fixture, out of the app's own code. |

## How it dedupes

Two failures, two guards, both in `dedupe` and both the same keys, window and
decisions as `app/mail_queue/dedupe/`. `custom/docs/mail-queue.md` argues the
choices; this is what the code here does.

- **The same mail queued twice** is refused at the push. A producer whose
  `Message.IdempotencyKey` is already held by a mail queued in the last day
  pushes nothing and hands back that mail's id, so the caller still truthfully
  reports that the email is on its way. The three callers' keys are the reset
  token's hash, the invite and its `last_sent_at`, and the run and its status.
  An empty key is queued every time.
- **A message already handed to the mail server** is refused at the send.
  `drain.Sender` claims `soloclef:mail:handover:<id>:<attempt>` before it calls
  `Send`, marks it `sent` once the mail server has taken it, and only then
  drops the queue entry. `recovery.RequeueHeld` puts held mail back and drops
  the *claims* of the senders that held it -- a claim whose sender is gone
  cannot be told from a live one -- but never a `sent` mark.

**A retry always sends.** The attempt is in the handover key, and a failed job
is written straight onto the retry set rather than pushed again, so neither
guard sees it.

**The mark is after the send, not before.** A process that dies between the
mail server's acceptance and the mark sends that message twice; one that died
after a mark placed before the send would never send it at all. Never-sent is
worse than twice-sent for a password reset, so the window left is one Redis
`SET` rather than the whole SMTP conversation. The queue stays **at least
once**: nothing is acknowledged before dispatch.

**The window is 24 hours**, `dedupe.Window`, a constant and not a setting: the
two senders drain the same Redis and two different numbers would be a duplicate
nobody can see. `dedupe/testdata/python_dedupe_keys.json` is generated out of
the Python's own code and compared key for key, as every other spelling in this
module is.

**Still run one sender process.** The senders inside one process are safe
against each other -- two cannot claim the same attempt -- but two processes
are not, because each recovers the other's held mail and drops its claims at
start. During a cutover that means one of the two, never both. Making two
processes safe needs per-sender leases, so only a sender whose lease expired
has its mail recovered; that is not here.

## Do not

- Do not change a spelling without the byte comparison. The queue entry, the
  smtp2go request body and the whole MIME message are each compared against
  what the app's own code produces for the same input, and the fixtures are
  generated from that code rather than written by hand. Five divergences were
  found that way and none of them was visible from inside Go: a `not_after` far
  in the future wrapping negative through int64 nanoseconds, an `enqueued_at`
  rounded to microseconds, `MAIL FROM` carrying the display name, a non-ASCII
  display name refused as if it were a non-ASCII address, and a header folded
  one word later than Python folds it.
- Do not "simplify" `mailer/mime`. The bias toward quoted-printable by five
  characters, the `MIME-Version` on the HTML subpart and not the text one, the
  36-character boundary that makes `multipart/mixed` fit in 78 columns and
  `multipart/alternative` not, the encoded word that absorbs the plain words
  behind it, the 8-bit body that is not encoded at all, and
  `bytes.splitlines()`'s six line endings each look like a mistake and each is
  what has been going out. A different fold point is a different message, and
  the thing it changes is which folder it lands in.
- Do not encode a line ending in a header. `email.policy.default` refuses one
  when the header is set, so `_build_mime` raises and the message is dropped;
  that refusal is what stops a subject built out of someone's text from adding
  headers of its own.
- Do not add a transport fallback. `MAIL_TRANSPORT` names the one way mail
  leaves and has no default, as `factory.py` says: an unreachable or
  unconfigured mail server is an error to fix, never a reason to drop mail
  quietly or to try the other one.
- Do not log an address, a link, a code or a key. Every log line names the mail
  id, the kind, the record and the try number, and the smtp2go error path reads
  the provider's error *code* and never its free text, which quotes the
  recipient.
- Do not run the three interop packages in parallel. They share one Mailpit
  and each clears it, so `-p 1` is part of the command, not a precaution.
- Do not point the Python half at an image built before the checkout under
  test. The interop tests run the app's own `enqueue_email` and `service`
  inside `SOLOCLEF_MAIL_TEST_IMAGE`, so an older image is an older queue and
  the failure reads as a parity bug.
- Do not point a test at `soloclef-redis-1` or at a real mail server. The
  interop tests empty the Redis they are given and send only into a throwaway
  Mailpit; the smtp2go tests answer themselves.

## The one place the two senders differ

An address that needs SMTPUTF8 — a non-ASCII local part or domain, not a
non-ASCII display name. `smtplib` sends such a message with raw UTF-8 headers
when the server advertises SMTPUTF8, and raises when it does not. This
transport speaks neither, so it refuses the message as the mail server being
unable to take it, which the queue retries and then gives up on. No current
caller can produce one: the two settings are ours and a user's address comes
from a validated row. If one ever does, that is the message to look at first.

Two smaller ones, both in the envelope rather than the message: Go's `net/smtp`
adds `SMTPUTF8` and `BODY=8BITMIME` to `MAIL FROM` whenever the server
advertises them, where `smtplib` adds them only when it needs them; and
`smtplib` prefers CRAM-MD5 for AUTH when offered, where this prefers PLAIN then
LOGIN, because the standard library has no CRAM-MD5.

## Commands

```bash
cd soloclef_mail && go test ./...
cd soloclef_mail && go vet ./... && gofmt -l .
```

The comparisons against Python need a throwaway Redis, a throwaway Mailpit and
a container of the backend image. Nothing is defaulted, so they never touch the
live queue or a real mail server:

```bash
docker run -d --name go-mail-test-redis --network danvics_litellm \
  -p 127.0.0.1:39742:6379 redis:8-alpine redis-server --save '' --appendonly no
docker run -d --name go-mail-test-mailpit --network danvics_litellm \
  -p 127.0.0.1:21026:1025 -p 127.0.0.1:28026:8025 axllent/mailpit:v1.21

# The Python half runs this checkout's app, so the image has to carry it.
# From the repository root, which is the build context:
docker build -t soloclef-mail-interop:latest -f - . <<'DOCKERFILE'
FROM soloclef-backend-fast:latest
COPY soloclef_backend/app /app/app
DOCKERFILE

cd soloclef_mail && \
  SOLOCLEF_MAIL_TEST_IMAGE=soloclef-mail-interop:latest \
  SOLOCLEF_MAIL_TEST_NETWORK=danvics_litellm \
  SOLOCLEF_MAIL_TEST_REDIS=redis://127.0.0.1:39742/0 \
  SOLOCLEF_MAIL_TEST_REDIS_CONTAINER=go-mail-test-redis \
  SOLOCLEF_MAIL_TEST_REDIS_FROM_CONTAINER=redis://go-mail-test-redis:6379/0 \
  SOLOCLEF_MAIL_TEST_MAILPIT_API=http://127.0.0.1:28026 \
  SOLOCLEF_MAIL_TEST_SMTP_HOST=127.0.0.1 \
  SOLOCLEF_MAIL_TEST_SMTP_PORT=21026 \
  SOLOCLEF_MAIL_TEST_SMTP_FROM_CONTAINER=go-mail-test-mailpit:1025 \
  go test ./enqueue/ ./mailer/smtp/ ./service/ -count=1 -p 1

docker rm -f go-mail-test-redis go-mail-test-mailpit
```

Regenerating the fixtures (after any change to `app/mailer` or
`app/mail_queue`) — the first two need only the repository, the third the
backend image because the encoding is httpx's:

```bash
cd soloclef_mail
python3 -I scripts/python_dedupe_keys.py > dedupe/testdata/python_dedupe_keys.json
python3 -I scripts/python_job_bytes.py   > mailjob/testdata/python_job_bytes.json
python3 -I scripts/python_float_repr.py  > pythonjson/testdata/python_float_repr.json
python3 -I scripts/python_mime_bytes.py  > mailer/mime/testdata/python_mime_bytes.json
docker run --rm --network danvics_litellm -e PYTHONPATH=/app -w /app \
  -v "$PWD/scripts/python_smtp2go_payload.py:/tmp/p.py:ro" \
  soloclef-backend-fast:latest python /tmp/p.py \
  > mailer/smtp2go/testdata/python_request_body.json
```
