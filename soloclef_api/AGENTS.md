# soloclef_api

The Go API tier. Serves the routes Soloclef's FastAPI API has handed over, one
at a time, behind the same proxy. Nearest `AGENTS.md` wins; the root one owns
organisation and TDD. The design and the phases are in
`custom/docs/go-api-tier.md`.

## Why this exists

One `backend` container cannot serve 10k concurrent users: uvicorn serves a
request per worker process, and each worker is a full Python process with the
parsing and embedding stack resident. Replicas behind the proxy need a process
whose cost per idle connection is a goroutine.

## The one rule that makes this safe

**A route is identical or it has not moved.** The Python route stays mounted;
the only change is which upstream the proxy sends one method-and-path pair to,
so a route is handed back by removing one directive and reloading Caddy.

Identical means the whole response, not the happy path: the body's bytes, the
error envelope, the 422 fields, the request id, the CORS headers and the
`Content-Length`. Four packages exist only to keep that true, and each of them
cites the Python code it mirrors:

| Path | Role |
|------|------|
| `internal/pythonjson` | JSON as the Python tier writes it: insertion order, no HTML escaping, both datetime spellings — `isoformat`'s `+00:00` and pydantic's `Z` — and **three** number/text renderings that are not interchangeable: Starlette's (float `repr`), pydantic-core's (`PydanticFloat`, used by any route with a `response_model`), and the text SQLAlchemy stores in a `json` column (`MarshalColumn`). |
| `internal/errorenvelope` | `app.py:_build_error_response` and the 422 handler. |
| `internal/requestcontext` | `X-Request-ID` and the access line. |
| `internal/corsmirror` | Starlette's `CORSMiddleware` for a simple request, written when the response **starts** rather than before the handler runs — because Starlette has two error handlers and only one is inside it, so a 500 a route did not catch carries no CORS headers at all. `Suppress` is how a route says so. |
| `internal/principal` | `app.users.get_auth_context`, in its order, with its fall-throughs — including reading a bearer token's issuer exactly as PyJWT reads it, because that decides which tier can verify it. |
| `internal/agentsignin` | `app/agent_sign_in`: an AI agent's access token from the identity provider — discovery, JWKS with rotation, the claim checks, the access claim, and which account it acts as. |
| `internal/ratelimit` | `app/rate_limiter.py`'s SlowAPI counter, in the same Redis keys, so a client's budget is one budget across both tiers. |
| `internal/requestnumber` | FastAPI's integer coercion for a parameter — pydantic-core's `str_as_int`, which takes `1_000`, `1.0` and surrounding whitespace — and the 422 it raises. |
| `internal/workspaceaccess` | `app.utils.rbac`'s membership, permission and API-access checks, split as they are there. |
| `internal/pythondelegate` | Hands a request this tier cannot verify, or a body it cannot read, to the Python tier. It suppresses this tier's CORS headers first, so a forwarded response carries the upstream's once rather than both twice. |
| `internal/schema` | The schema every query is generated against, and the check that it is still what the migrations produce. |
| `internal/requestbody` | A JSON body as a pydantic model reads it: the field order failures are reported in, the length and type messages, pydantic's lax bool, its lax int, a string enum's refusal, and a partial-update model's "was this field mentioned at all". A body it cannot decode — or one carrying an unpaired surrogate escape — is the Python tier's, and it says so. |
| `internal/csrforigin` | `app/auth/csrf.py`'s `CsrfOriginMiddleware`: a cookie-authenticated write from a foreign origin. Only writes are checked, which is why it did not exist until there was one. |
| `internal/database` | The pool, and the transaction seam every write runs in — including `Effects`, which fires a side effect only after a commit. |
| `internal/posthogcapture` | The queue and the envelope posthog-python posts, shared by every event this tier sends. |
| `internal/routeanalytics` | `capture_for`: a route's own product event, as against the gated-request middleware's. |
| `internal/logsummary` | The workspace log summary. |
| `internal/workspacemembers` | The workspace member list. |
| `internal/myaccess` | The caller's own role and permissions in a workspace. |
| `internal/workspaceroles` | A workspace's roles: the whole screen — two reads, a create, a partial update and a delete. |
| `internal/permissioncatalog` | Everything a role can be given — the enum and its descriptions, read by the catalogue route and by the write that refuses an unknown one. |
| `internal/accesstokens` | The caller's own API keys: the list, and the revoke that owes a PostHog event. |
| `internal/celeryqueue` | The queue this tier enqueues background work on: the producer's deployment half (which Redis, which default queue), and the log-context header the Python middleware would have bound. The protocol itself is `soloclef_celery`, shared with the worker tier. |
| `internal/documentdeletion` | `DELETE /api/v1/documents/{document_id}`: the first write that enqueues. Mark, commit, enqueue, and put the marker back if the enqueue fails. |
| `internal/workspacedeletion` | `DELETE /api/v1/workspaces/{workspace_id}`: the same shape, with the soft-delete name marker and the refusal that nobody deletes their only workspace. |
| `internal/capturecompare` | Test support only: reads what the Python tier answered (each slice's `testdata/python_answers.json`) and publishes the same message here to compare. |
| `internal/workspacelogs` | The four single-log routes beside the summary: the read and the three writes. Not the list — its date parameters are pydantic's own parser. |
| `internal/markdownmemory` | An account's own memory document and a workspace's shared one: three routes each. A write re-renders the document, so the slice carries mirrors of Python's `str.strip`, `str.splitlines`, `str.isalnum` and `date.fromisoformat`. |
| `internal/fractionalindex` | `fractional_indexing.generate_key_between`, which is where a folder's `position` comes from. A mirror of a third-party library rather than of this app's code, and held to 676 pairs taken from that library itself — because it indexes **code points** and a byte-based port answered keys the library refuses. |
| `internal/folders` | The document browser's folder tree: eight routes, the one Celery message that carries a keyword argument, and the delegate that hands every folder write back while the git knowledge store's master switch is on. Not the list — its `ORDER BY position` has ties and neither tier's order is stable. |
| `cmd/api` | The process: config, pool, routes, shutdown. |

## Commands

```bash
cd soloclef_api && go test ./...
cd soloclef_api && go vet ./... && gofmt -l .

# Does a Python Celery worker run what this tier enqueues? Needs a throwaway
# Redis (it flushes the database it is given) and a container with this app's
# Celery. Never point it at soloclef-redis-1.
docker run -d --name api-test-redis --network danvics_litellm \
  -p 127.0.0.1:39741:6379 redis:8-alpine redis-server --save '' --appendonly no
cd soloclef_api && \
  SOLOCLEF_API_TEST_CELERY_CONTAINER=soloclef-backend-1 \
  SOLOCLEF_API_TEST_BROKER_FROM_CELERY=redis://api-test-redis:6379/0 \
  SOLOCLEF_API_TEST_BROKER=redis://127.0.0.1:39741/0 \
  go test ./internal/celeryqueue/ -count=1
docker rm -f api-test-redis

# Every query is generated; change a .sql file and regenerate.
cd soloclef_api && sqlc generate

# Is internal/schema/schema.sql still what the migrations produce? Needs Docker
# and soloclef-backend-fast:latest, and takes about 25 seconds, so it is opt-in.
cd soloclef_api && SOLOCLEF_API_CHECK_SCHEMA_DRIFT=1 go test ./internal/schema/
cd soloclef_api && internal/schema/dump_from_migrations.sh > internal/schema/schema.sql
```

## Do not

- Do not add a route without proving, against the running Python tier, that the
  bytes match for the same input. The proof belongs in
  `custom/docs/go-api-tier.md` and the capture in the slice's `testdata`.
- Do not commit a fixture with an account's own text in it. This repository has
  a public copy; replace the free text and say in the test that you did.
- Do not port a route that touches the Python ML stack or calls a model. The
  doc lists what cannot move and why. Enqueueing Celery work is no longer on
  that list — `internal/celeryqueue` does it, and `custom/docs/go-api-tier.md`'s
  "Producing Celery work, from Go" says what was proved and what is still
  Python about it.
- Do not invent an answer the Python tier would not have given — a refusal in
  different words, a 401 where it allowed, a guessed default. Where this tier
  cannot be sure, it forwards to the Python tier or it fails loudly.
- Do not write a query by hand, and do not edit a `*.sql.go` file. A query goes
  in the slice's `queries.sql` and comes back through `sqlc generate`.
- Do not "tidy" a query into something the Python route does not ask. An added
  `ORDER BY`, a `LIMIT`, a join where it runs two statements: each of those can
  be a different answer in a different order. Mirror the question, not the
  intent.
- Do not give a setting that names a server a default. An unconfigured
  container must refuse to start.
- Do not "simplify" the mirrors of another library's edge behaviour. The
  lenient base64 in `internal/principal`, the key kept in place that this tier
  cannot verify with in `internal/agentsignin`, the empty client key in
  `internal/ratelimit`, pydantic's lax bool in `internal/requestbody`, the
  scheme-blind same-origin test in `internal/csrforigin`, the underscores and
  trailing `.0` accepted in `internal/requestnumber`, and the four C0
  separators `internal/markdownmemory` treats as whitespace each look like a bug
  and each is a case where the two tiers were compared over HTTP and disagreed.
  Each one cites what it mirrors and has the vectors that came out of the Python
  tier beside it.

  `internal/requestnumber` is the cautionary one: it *was* simplified to a
  regular expression, and for five phases four handed-over routes answered 422
  where the Python tier answered 200, with two committed tests pinning the
  mistake.

- Do not render a number without deciding which renderer. A route with a
  `response_model` is serialised by pydantic-core and a route without one by
  Starlette's `json.dumps`, and the two write a float differently (`1e16`
  against `1e+16`, `0.00001` against `1e-05`). A `json` column's *stored* text
  is a third form again. All three are in `internal/pythonjson`; picking the
  wrong one differs by one byte per float, which no happy-path test catches.

- Do not assume a Python `None` is a SQL NULL. SQLAlchemy's `JSON` type leaves
  `none_as_null` off, so `None` is stored as the JSON scalar `null`. That is
  invisible in every response and visible only in a row diff, which is why
  every write comparison diffs the database afterwards.

- Do not write a 500 without deciding which Starlette middleware would have
  written it. `ExceptionMiddleware` answers an `HTTPException` a route raised
  and sits inside the CORS middleware; `ServerErrorMiddleware` answers what a
  route did not catch and is outside it, so **its 500 carries no CORS headers**.
  A route whose Python counterpart has no `except Exception` must call
  `corsmirror.Suppress` for every 500, and one that has it must still suppress
  the 500 it writes when *rendering* fails, because that happens after the route
  returned. The table in `custom/docs/go-api-tier.md` under divergence 5 says
  which routes are which; nothing in Go will fail if a new route gets it wrong,
  so provoke one 500 per route with an `Origin` and count the headers.

- Do not fire a write's side effect where you decide on it. Register it with
  `database.Effects` and let the commit run it; a Python route's capture is
  after `session.commit()` and a transaction that rolls back owes nothing.

- Do not register an enqueue the route has to be able to *revert* with
  `database.Effects`. An effect cannot fail the request and some enqueues must:
  `app/routes/documents_routes.py` and `app/routes/workspaces_routes.py` both
  put their marker back and answer 503. Those routes are two transactions with a
  compensation between them, not one with an effect after it.

- Do not write a Celery message by hand, and do not add a second encoder. One
  module writes them (`soloclef_celery`), both Go tiers require it, and it is
  held to a byte comparison against Celery's own client. A call site says which
  shape it is: `produce.Delay(task, args…)` for a `.delay(…)`, a
  `produce.Message` for an `apply_async(args=[…])` — Celery tells them apart in
  the `argsrepr` header.

- Do not leave the log context off a message. Every task published during a
  request carries `soloclef_log_context`, and the workspace id in it comes from
  the *path* rather than from the route's own workspace, because that is where
  the Python middleware reads it (`internal/celeryqueue.LogContextFor`).

- Do not port a read without checking its `ORDER BY` for **ties**. A column
  that is not unique — `position`, `name`, `created_at` — leaves the order of
  the tied rows to the plan, and the plan is not the same on both tiers: a
  prepared statement goes from a custom to a generic plan on its fifth
  execution on a connection, and the two drivers' connection reuse differs. The
  Python tier's own folder list changes its answer on the fifth request to a
  fresh connection, which is why `GET /api/v1/folders` is not served here. Ask
  the Python tier the same read ten times in a row before costing it.

- Do not edit one key of a `json` or `jsonb` column with `jsonb_set`. The
  Python route reads the whole value into Python, changes it and writes it back
  through `json.dumps`, which rounds a number the column held more precisely
  than a float64. `jsonb_set` would keep the digits and leave a different row;
  `internal/pythonjson.MarshalColumn` over a decoded value is the mirror
  (`internal/folders/watched_flag.go`).

- Do not assume SQLAlchemy emits an `UPDATE` because a route assigned an
  attribute. It compares the new value with the loaded one and flushes nothing
  when they are equal, so `updated_at` is left alone — for a rename to the same
  name, a reorder to the same key, and a `watched` flag that is already false,
  including when the stored value is the number `0`, because Python's
  `False == 0` is true. All three answer 200 and only a row diff sees it.

- Do not add a route with an unsafe method without checking what
  `internal/csrforigin` does with it. Every route handed over before the first
  write was a GET, and that is exactly why a cookie-authenticated write was
  unprotected here for one commit.
