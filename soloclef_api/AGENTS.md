# soloclef_api

Soloclef's HTTP API: every route under `/api/v1`, `/auth` and `/users`, the
chat included, in one Go process (`cmd/api`). The proxy (`docker/proxy-go`)
sends it those prefixes and nothing else. Nearest `AGENTS.md` wins; the root one
owns organisation and TDD.

## Why it is Go

It replaced a FastAPI backend that could not serve 10k concurrent users:
uvicorn serves a request per worker process, and each worker was a full Python
process with the parsing and embedding stack resident. Here the cost of an idle
connection is a goroutine, and replicas are cheap. Nothing is forwarded to a
Python process any more: a credential nothing here can verify is a 401, and a
body that is not JSON is refused here (`internal/requestbody`).

## The rule (changed 2026-11-11)

**Implement what the product needs, and make it better than the Python.**

This replaces the earlier rule, "a route is identical or it has not moved",
which was never the owner's instruction. It was a design choice written into
these docs, and it made every route cost hours of proving byte-identity with
Python, quirks and defects included. That rule is gone. In its place:

- **Read the Python for what a route does, not for how it does it.** Its
  quirks are not requirements: pydantic's lax parsing, Starlette's router
  oddities, float spellings, error-message wording, which of two repeated
  query values wins.
- **Fix what the Python gets wrong.** A list with no `ORDER BY` gets one. A
  write that checks no state checks it. A route that leaks a credential, an
  email address or ciphertext does not. A route missing a gate gets the gate.
  One bad row does not 500 a whole list. Say what you improved in the commit.
- **Keep the contracts clients actually read.** The JSON field names and shapes
  the web and mobile apps consume, the error envelope's shape
  (`{"error":{code,message,status,request_id,...}}`), the request id, and the
  chat stream protocol (the Vercel AI SDK data stream the web app speaks).
  Those are interfaces; changing one means changing its client in the same
  commit.
- **Test intended behaviour with ordinary tests.** Table-driven HTTP tests
  with hand-written cases, and the opt-in real-database tests. Do not record
  Python's answers and replay them; do not write new mirrors of Python
  behaviour.
- **A route is done when it does what the product needs and its tests pass.**

The packages below were written under the old rule. New work must not extend
their Python-mirroring parts, and they will be simplified as routes are
reworked; `custom/docs/soloclef-go-inventory.md` lists which are parity-only.

| Path | Role |
|------|------|
| `internal/pythonjson` | JSON as the Python tier writes it: insertion order, no HTML escaping, both datetime spellings — `isoformat`'s `+00:00` and pydantic's `Z` — and **three** number/text renderings that are not interchangeable: Starlette's (float `repr`), pydantic-core's (`PydanticFloat`, used by any route with a `response_model`), and the text SQLAlchemy stores in a `json` column (`MarshalColumn`). |
| `internal/errorenvelope` | `app.py:_build_error_response` and the 422 handler. |
| `internal/requestcontext` | `X-Request-ID` and the access line. |
| `internal/corsmirror` | Starlette's `CORSMiddleware` for a simple request, written when the response **starts** rather than before the handler runs — because Starlette has two error handlers and only one is inside it, so a 500 a route did not catch carries no CORS headers at all. `Suppress` is how a route says so. |
| `internal/principal` | Who a request is: an API key, this app's own session token (bearer or cookie), or an agent's token from the identity provider, in `get_auth_context`'s order with its fall-throughs. A token naming an issuer goes to the agent verifier and nowhere else; with none configured, or one that does not own the issuer, it is a 401. |
| `internal/agentsignin` | An AI agent's access token from the identity provider — discovery, JWKS with rotation, the claim checks, the access claim, and which account it acts as. A provider that cannot be read is a 503 (`ErrSignInUnreachable`), never a 401 that would sign every agent out. |
| `internal/ratelimit` | `app/rate_limiter.py`'s SlowAPI counter, in the same Redis keys, so a client's budget is one budget across both tiers. |
| `internal/requestnumber` | FastAPI's integer coercion for a parameter — pydantic-core's `str_as_int`, which takes `1_000`, `1.0` and surrounding whitespace — and the 422 it raises. It also says which of a repeated query parameter's values is the one read: the **last**, as Starlette's `query_params` holds it, not `url.Values.Get`'s first. |
| `internal/starlettepath` | How the Python tier's **router** reads a path, where Go's `ServeMux` reads it differently: uvicorn decodes before Starlette splits, so `a%2Fb` is two segments there and one here, and `ServeMux` cleans `//` and answers a 301 where Starlette matches nothing. Two halves. A route asks `Unroutable` about its own parameters, answers `WriteRouterNotFound` — Starlette's bare 22-byte `{"detail":"Not Found"}`, which keeps its CORS headers — and reads them through `AsDecodedByTheServer`. And `Guard` stands in front of the whole mux in `cmd/api`, because a handler cannot stop a redirect and a route behind the gate cannot answer before the gate's 401; it touches only a path the two routers disagree about, and decides against the served routes alone. Every served route is held to the guard's rules in `cmd/api/router_test.go`; what it deliberately leaves alone (a trailing slash, a dot where no parameter is) is listed on `Guard`. |
| `internal/requestbody` | A JSON body as a pydantic model reads it, **including a datetime** (`moment.go`): 844 of pydantic's own answers are replayed, of which 15 are a documented divergence named in `momentOpaqueDigits`. Also: the field order failures are reported in, the length and type messages, pydantic's lax bool, its lax int, a string enum's refusal, and a partial-update model's "was this field mentioned at all". A body that is not JSON — or one carrying an unpaired surrogate escape — is refused by `Middleware` before the route and before the credential: the 422 envelope with `JSON decode error` at `body`, or a 400 for bytes that are not UTF-8 or a body that did not arrive. |
| `internal/csrforigin` | `app/auth/csrf.py`'s `CsrfOriginMiddleware`: a cookie-authenticated write from a foreign origin. Only writes are checked, which is why it did not exist until there was one. |
| `internal/database` | The pool, and the transaction seam every write runs in — including `Effects`, which fires a side effect only after a commit. |
| `internal/posthogcapture` | The queue and the envelope posthog-python posts, shared by every event this tier sends. |
| `internal/routeanalytics` | `capture_for`: a route's own product event, as against the gated-request middleware's. |
| `internal/activitysummary` | The summary of the caller's own activity log over a recent window: `GET /api/v1/logs/summary`. |
| `internal/artifacts` | All thirteen routes of the Studio's artifacts, under `/api/v1/artifacts`, each statement naming the owner: the list, the video payload, the delete, the manifest, the three byte streams (download, a file's content, slide audio; Range and 206) and the six flashcard and quiz state writes, which read the deck or quiz through `internal/filestore` and remember it once read (`StudyFiles`; safe because every artifact file key carries a fresh uuid). Rewritten on 2026-11-11 to do the job rather than mirror Python, and its recordings and recording scripts are deleted; the package comment lists what it now does better: one odd row no longer fails the list, a slide never carries a server path, the delete refuses a document mid-indexing and restores the status it read when the queue is down, and a 500 keeps its CORS headers. Tested by ordinary table tests and the opt-in database test. |
| `internal/accesstokens` | The caller's own API keys: the list, and the revoke that owes a PostHog event. |
| `internal/celeryqueue` | The queue the API enqueues background work on for the Go worker: the producer's deployment half (which Valkey, which default queue), and the log-context header. The protocol itself is `soloclef_celery`, shared with the worker. |
| `internal/documentdeletion` | `DELETE /api/v1/documents/{document_id}`: the first write that enqueues. Mark, commit, enqueue, and put the marker back if the enqueue fails. |
| `internal/capturecompare` | Test support only: reads what the Python version answered, recorded before it was retired (each slice's `testdata/python_answers.json`), and publishes the same message here to compare. |
| `internal/activitylogs` | The four single-entry routes of the account's activity log: the read and the three writes. Every statement names the owner, so another account's entry is the same 404 as one never written. |
| `internal/markdownmemory` | An account's own memory document: three routes. A write re-renders the document, so the slice carries mirrors of Python's `str.strip`, `str.splitlines`, `str.isalnum` and `date.fromisoformat`. |
| `internal/fractionalindex` | `fractional_indexing.generate_key_between`, which is where a folder's `position` comes from. A mirror of a third-party library rather than of this app's code, and held to 676 pairs taken from that library itself — because it indexes **code points** and a byte-based port answered keys the library refuses. |
| `internal/folders` | Every route of `app/routes/folders_routes.py`: the document browser's folder tree, the list beside it, and the two document moves. It carries the one Celery message with a keyword argument, and the one domain event this tier publishes. The delegate that handed every folder and document-move write back while the git knowledge store was on (`StoreDelegate`) is deleted: the store was cut from the product on 2026-11-11. |
| `internal/eventstream` | Domain events on a Redis Stream, each belonging to one account (`user_id`). `document.entered_folder` is the one event so far; `internal/folders` decides whether a move owes it and `cmd/api` adapts the two. |
| `internal/prompts` | An account's prompt library and the community shelf it publishes to: six routes, the caller's id in every predicate. Only an administrator (the account's `is_superuser`, set with `soloclef-api set-admin`) may publish. |
| `internal/publiclinks` | The caller's own public share links: `GET /api/v1/snapshots` (one statement, which reads one key of each frozen copy and not the copy) and `DELETE /api/v1/snapshots/{snapshot_id}` (one guarded DELETE). Both name the owner; another account's link is the same 404 as one never made. Rewritten on 2026-11-11 to do the job rather than mirror Python, and its recordings are deleted. Tested by table tests and the opt-in database test. |
| `internal/pythontext` | Python's own string behaviour where Go's differs, kept in one place for the same reason `internal/pythonjson` is: `str.strip()` is the Unicode White_Space property **plus** the four C0 separators U+001C–U+001F, which `strings.TrimSpace` is not. Two slices need it — a memory document and a rule's permission — and a value wrapped in one of those separators is accepted there and a 400 under a Go trim. |
| `internal/accountprofile` | The caller's own account: the read any credential may make, the profile update that is the API's first `extra="forbid"` body, and the delete that deactivates the account, ends every sign-in, enqueues the erasure and clears the two session cookies — the only route of this tier that writes a `Set-Cookie`. |
| `internal/notes` | The documents a person writes rather than uploads: `POST`, `GET /api/v1/notes` and `DELETE /api/v1/notes/{note_id}`, every statement naming the owner. The create writes the `documents` row and the `notes` row beside it in one transaction. Moved to the schema with no workspaces and nothing more: the one-kind notes design the `notes` table was made for is a later piece of work and replaces these three routes. |
| `internal/agentrules` | The rules that decide whether the chat agent may run a tool: four routes behind a **feature flag**, so a 503 naming the setting is part of the answer. Its 409 is a caught `IntegrityError` and so is wider than it reads, and the constraint it is written for cannot fire for a workspace-wide rule at all. |
| `internal/documents` | The reads about one document and about all of the caller's documents, and the restore: a cited chunk's document (`GET /api/v1/chunks/{chunk_id}/document`, moved from the Python tier's `/documents/by-chunk/{chunk_id}`, which `net/http` could not hold beside `/documents/{document_id}/chunks`; both clients call the new path), a document's chunks, its versions, one version and the restore, the status poll, the counts by type, the watched folders and the title search. The restore always **adds** a version, so every restore runs both clean-ups, which can delete the very version being restored; and its enqueue is outside any `try`. The title search needs `pg_trgm`. |
| `internal/publicchat` | Three of the ten routes of `app/routes/public_chat_routes.py`: the page a public share link shows, a shared video's payload and a shared podcast's details. They have **no caller** — the share token is the whole of the authorisation — so `cmd/api` mounts them outside the gate and inside the rate limiter, as it does the invite-info route (`publicRoutes()`). Everything they answer comes out of jsonb with no type asked, so the answers follow Python's rules for a value of any JSON type (`python_value.go`), and `disclosure_test.go` pins what a stranger holding a link is and is not sent. The other seven stream bytes, write, or render with `repr()`. |
| `internal/modelconnections` | The providers, which are a constant; the caller's own connections with their models; and making, changing, removing and testing a connection (`write_routes.go`), which is an administrator's in a session. Every statement names the owner. The key is sealed before it is stored (`keyseal.go`: AES-256-GCM under the deployment's secret, bound to its owner), **never answered**, and read by exactly two statements, the tests', which hand it to the model proxy (`proxy_probe.go`). A test goes through the proxy like every other model call; no provider is called from here. An update keeps `extra`'s two credential bags unless it names them, because they are never answered and so cannot be sent back. |
| `internal/modelchoices` | Which model does each of an account's jobs other than chat, by the name the model proxy lists it under, in the chat model's shape: `quick-model`, `studio-model`, `model-roles` (vision and image) and `llm-setup-status`, all under `/api/v1/users/me`. One row of `user_models` per purpose; no row is the default, which each answer names. No fallback model: a withdrawn model is said, not replaced. The package comment lists who reads each purpose. |
| `internal/instructionslint` | `POST /api/v1/system-instructions/lint`: warnings about chat instructions before they are saved. Fixed patterns always; a reading by the account's quick model through the proxy when asked, bounded to seconds. When the model cannot be had the patterns are answered alone and `model_checked` is false. |
| `internal/searchconnectors` | An account's connections and MCP servers: the two lists, one of each, the `enabled` switch, both removals and the two trusted-tool edits. Every statement names the owner. The lists are ordered by name, then id. A stored credential is never answered; every change to an account's MCP servers moves its counter in Valkey (`internal/chat/mcptools`). |
| `internal/notebooks` | Nine routes: a notebook's own row (list, create, read, delete, pin, unpin) and the account's edits of the report templates. Every statement names the owner and each write is one guarded statement; a pin is `notebooks.featured_at`. A notebook's notes are no longer here (a note is a document, `internal/notes`). `cmd/api` takes its route and body lists from `Served()` and `WithBodies()`, and wraps `SearchPrecedence` outside the gate. |
| `internal/podcasts` | Five of the eleven podcast routes: the detail, the three transitions that need neither a model nor the billing ledger (approve the brief, regenerate, revert), and the delete. A transition is written only from the status it was decided on, so two that race cannot both enqueue a draft. |
| `cmd/api` | The process: config, pool, routes, shutdown. |

## Commands

```bash
cd soloclef_api && go test ./...
cd soloclef_api && go vet ./... && gofmt -l .

# Every query is generated; change a .sql file and regenerate.
cd soloclef_api && sqlc generate
```

## Do not

- Do not add a route without ordinary tests of what it does: the status, the
  body the web and phone apps read, and the rows it leaves.
- Do not commit a fixture with an account's own text in it. This repository has
  a public copy; replace the free text and say in the test that you did.
- Do not call a model provider directly. Every model call goes through the
  model proxy (LiteLLM); heavy work (parsing, indexing, Studio builds) is
  queued for the worker, not run in a request.
- Do not guess. Where the API cannot be sure — a credential nothing here can
  verify, a body it cannot read, a setting nobody gave — it refuses in so many
  words or fails loudly. There is no other tier to hand a request to.
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
  Python tier's own folder list changed its answer on the fifth request to a
  fresh connection, which is why `GET /api/v1/folders` was not served here
  until the Python query named `id` as a tie-break. Ask for the same read ten
  times in a row before trusting its order, because one request proves nothing
  about an order the plan cache decides.

  The same applies to an order no response carries. `bulk_move_documents`
  publishes one `document.entered_folder` per row, and the hook built that list
  by walking `session.dirty` — which is a **set** of instance states, hashed by
  object identity, so the same request published its two events `8, 1` twelve
  times out of eighteen and `1, 8` six. An order out of a Python `set` is not an
  order. It is sorted by `document_id` in the hook now, and the read carries an
  `ORDER BY id` as well.

- Do not read a query parameter with `url.Values.Get`. Starlette's
  `QueryParams` is an immutable multi-dict whose mapping is built last-wins, so
  a parameter sent twice is read as its **last** occurrence there and its first
  here — which is a difference of *which workspace a route answers about*.
  `internal/requestnumber.LastValue` is the mirror. It had been wrong on the
  log summary's `hours` since phase 1 and the comparison found it on the folder
  list.

- Do not publish a domain event because the route looks like it should. Which
  moves publish `document.entered_folder` is decided by a SQLAlchemy flush hook
  and not by either move route: a move that changes nothing, a move out to the
  root, and a document whose status is not exactly `ready` each write the row
  and publish nothing, and no response or row says so. Mirror the hook
  (`internal/folders/document_arrival.go`), and register the publish with
  `database.Effects` as the hook publishes from `after_commit`.

- Do not decide how a `json` or `jsonb` column is edited; read what the Python
  route does **today**. `PATCH /api/v1/folders/{folder_id}/watched` read the
  whole value into Python, changed one key and wrote it back through
  `json.dumps`, which rounds a number the column held more precisely than a
  float64 — so this tier mirrored that with
  `internal/pythonjson.MarshalColumn` and deliberately did *not* reach for
  `jsonb_set`, which would have kept the digits and left a different row. The
  Python route narrowed to `jsonb_set` on 2026-11-08 and this tier followed, a
  day late, which is one of the three divergences `main` carried
  (`custom/docs/go-api-tier.md`, "The drift the two branches left"). The rule
  cuts both ways: the better statement is wrong while the Python route writes
  the worse one, and wrong again the moment it stops.

- Do not assume SQLAlchemy emits an `UPDATE` because a route assigned an
  attribute — and do not assume it does not when the route issues a Core
  `update()` instead. The ORM compares the new value with the loaded one and
  flushes nothing when they are equal, so `updated_at` is left alone for a
  rename to the same name and a reorder to the same key. The watched flag used
  to be a third case of that, with the number `0` among them because Python's
  `False == 0` is true; it is a Core `update()` now, which has no loaded value
  to compare, so it writes whenever the route's guard passes. Each answers 200
  and only a row diff sees which.

- Do not refuse a number that is too wide for its column anywhere but where
  the statement that fails on it would have run. Python holds the integer and
  the query raises, so the 500 lands wherever that query is — **after** every
  refusal the route decides before it. `POST /api/v1/prompts` with
  `is_public: true` and a **twenty-digit** workspace id, asked by an account
  that may not publish, is the publishing 403 on both tiers; checked before the
  transaction it was a 500 here. `internal/requestbody`'s `OptionalNumber`
  carries `OutOfRange` per field for exactly this reason, and phase 7's
  `parent_id` past int32 asked by an outsider is the same shape. An id that is
  only too wide for the *column* needs no check at all — it fits in an int64,
  so the query runs and raises where it should.

- Do not read a query parameter with `url.Values.Get`. A query string may name
  one twice, and Starlette's `query_params` is a dict built from the pairs in
  order, so a single-valued annotation reads the **last** value while `Get`
  returns the first: `?hours=1&hours=2` answered about hour 2 there and hour 1
  here. `internal/requestnumber.QueryValue` is the one way to find a parameter's
  value, and the log summary's `hours` was reading the wrong one from phase 1
  until the prompts slice found it.

- Do not loop over a query's rows writing one row per iteration unless the
  query says what order they come in. The order is then in the database, and a
  bare `DISTINCT` or a missing `ORDER BY` leaves it to the plan: the comment
  reply's `SELECT DISTINCT author_id` was answered in two different orders by
  the two tiers, which swapped two notifications' ids, and it took three runs
  of the same fifty-six requests to see it once. This is the sibling of the
  tie rule above and it bites a *write* rather than a read.

- Do not pass a `map[string]any` as a Celery task's argument when its key
  order can be read. Python writes a dict in insertion order, in the body and
  in the `argsrepr` header alike, and `soloclef_celery`'s own encoder sorts a
  map's keys — which agreed with Python only for as long as no envelope carried
  an object with more than one key. `celery.Mapping` is the ordered form, and
  the push payload is the first thing that needed it.

- Do not render a list from the row when the Python route builds its model by
  hand. `read_workspaces` constructs `WorkspaceWithStats(...)` naming ten
  fields, so the two it does not name — `shared_memory_md` and
  `knowledge_store_enabled` — are the **model's defaults** for every row,
  whatever the columns hold. Reading the row instead is what a port does by
  default and it differed on 37 of 79 cases. The rule is the same one as for a
  `json` column: read what the route does today, not what the schema implies.

- Do not add a route with an unsafe method without checking what
  `internal/csrforigin` does with it. Every route handed over before the first
  write was a GET, and that is exactly why a cookie-authenticated write was
  unprotected here for one commit.

- Do not read a setting without checking **which** of this app's boolean
  parsers reads it. `app/config` tests the exact word `TRUE`
  (`PUSH_ENABLED`) and
  `app/agents/chat/multi_agent_chat/shared/feature_flags.py` tests `1`, `true`,
  `yes` or `on` stripped and lower-cased (`SOLOCLEF_ENABLE_PERMISSION`, the
  new-agent-stack kill-switch). A deployment setting one of the second kind to
  `1` is served by one parser and not the other, so `internal/config` carries
  both and they are deliberately not unified.

- Do not trim a string with `strings.TrimSpace` where Python strips it.
  `str.isspace()` is the Unicode White_Space property **plus** U+001C–U+001F,
  which `unicode.IsSpace` does not include, so a value wrapped in a file
  separator is stripped there and kept here — which is a stored title that
  differs, and a rule's permission that fails a pattern it passed on the other
  tier. `internal/pythontext` is the mirror; there is one of it, not one per
  slice.

- Do not multiply two numbers a Python route multiplies and hand the product to
  a query. Python's integers are unbounded and the *query* fails to encode the
  product, which is a sanitised 500; an int64 wraps silently and answers 200
  about a **different** page, which is the worst shape of divergence this port
  can ship — two valid-looking answers. `GET /api/v1/workspaces/{id}/notes`'s
  `page * page_size` is the one that exists today
  (`internal/notes/list_parameters.go`).

- Do not re-validate on the way out what the write path validated on the way
  in, and do not mirror a library that does. `UserRead` declared fastapi-users'
  `email: EmailStr`, and a response model is validated on serialisation — so
  every profile read ran `email_validator` over the column and answered 500 for
  a row it refused. A Go mirror of that library would have been a parser
  deciding a *status*, which is the line this tier draws at `GET /api/v1/logs`'s
  dates. It is `email: str` on both tiers now, and the lesson is to look for the
  same shape in the next read model handed over.
