# The parity suite

Proves that a route handed from the Python API to the Go API tier answers
identically, by asking both tiers the same question and comparing the bytes.

Every real bug the port has produced was found this way, and none of them was
found by reading code or by the Go tier's own unit tests: a reversed
`timelimit` field that would have killed eight-hour jobs 600 seconds early, an
integer parser stricter than pydantic's on seven routes that were already
handed over, a missing CSRF origin check that would have shipped a cross-site
write hole, and CORS headers written twice on every forwarded cross-origin
response. Each of those now has a named case in `cases/regressions.json`, so a
failure says which bug came back.

```bash
bash parity/run.sh                     # every route, then take the world down
bash parity/run.sh regressions.json    # one case file
PARITY_KEEP=1 bash parity/run.sh       # leave the world up to poke at
bash parity/stack/down.sh              # take it down afterwards
```

It takes about half an hour for 1,382 comparisons and needs Docker, a Go
toolchain and the `soloclef-backend-fast:latest` image. It runs every case
whatever happens and exits non-zero if any difference neither tier can account
for was found, with every one of them listed at the end.

## Why it lives here

`AGENTS.md` organizes by feature, one responsibility per file, and this is a
top-level tree beside `soloclef_api` and `soloclef_backend` rather than inside
either. The responsibility is one thing — *prove the two tiers agree* — and it
belongs to neither tier: it stands up the Python API, the Go API, a Postgres, a
Redis and an analytics stand-in, and is written in a third language from the two
it compares. Nothing about it is a test *of* `soloclef_api`'s packages; those
have their own `_test.go` files beside the code they test, which is where unit
tests belong. Putting this under `soloclef_api/` would make the Go module own
the Python tier's lifecycle; putting it under `soloclef_backend/` would make the
suite being deleted own the suite proving the deletion is safe. So it is its own
tree, and `custom/docs/go-api-tier.md` records the decision.

## Adding a route

A route is a data entry. Write one file in `cases/`:

```json
{
 "route": "GET /api/v1/workspaces/{workspace_id}/snapshots",
 "why": "One sentence on what this route makes observable that no other does.",
 "parameters": {"workspace_id": 1},
 "matrices": ["credentials", "identifiers:workspace_id", "origins"]
}
```

and add the same `"METHOD /path"` to `routes.json`. That is all: no code, and
nothing to register. A full run fails and names any route in `routes.json` that
no case asked, so a case file cannot quietly stop covering a route it used to.

The `matrices` are the shared questions every route is asked, in
`matrices/*.json` — each one is data too:

| Matrix | Asks |
|---|---|
| `credentials` | all sixteen credential shapes: none, malformed, wrong scheme, wrong signature, expired, wrong audience, a switched-off account, an agent token from a provider this deployment cannot read, an API key, an unissued key, the session cookie, and the four accounts with different permissions |
| `identifiers:<parameter>` | every id that is not a row: absent, malformed, zero, negative, a decimal, a leading plus, an underscore separator, surrounding spaces, past int32, past int64, twenty digits, a traversal |
| `integer_spellings:<parameter>` | every spelling of a whole number a query parameter can carry, including the ones that are only numbers to pydantic |
| `origins` | the origins a browser sends, which decide the CORS headers |
| `cross_site` | the credential and the origin together, which is the CSRF question |

A write adds `"tables"` (diffed afterwards), `"queues"` (the Celery messages it
owed), `"mint": true` if the answer contains a timestamp it minted, and
`"bodies"`, which are the route's own body matrix — a conflict, a constraint
violation, a missing field, an extra undeclared field, and a rollback whose
rows have to be unchanged. The route's peculiarities go in `"cases"`, which are
sent exactly as written.

A case names a credential (`"as": "owner"`); `stack/mint_credentials.py` mints
them at startup from the made-up session secret in `stack/world.sh`, so nothing
in the repository is a token and nothing expires.

## What is compared

| Dimension | How |
|---|---|
| Status | exactly |
| Body | byte for byte, masked only as below |
| Headers | the content, framing and CORS headers, as a sorted multiset — so a header written **twice** is a divergence, while the order two different headers are written in is not |
| Database rows | `SELECT *` of each named table, ordered by primary key, after each tier answered the same seed |
| Celery messages | each envelope on each named queue, with the task id, the publishing process, the enqueue stamp and the ids derived from them masked |

Only three things may differ: the `request_id`, the error envelope's
`timestamp`, and — for a case that opts in with `"mint": true` — a timestamp
the answer minted while it was being written. That is it. Whitespace, key
order, the spelling of a float and the number of digits in a microsecond are
all divergences.

### What is not compared

- **Product analytics.** `stack/analytics_standin.py` answers both tiers'
  captures so neither reaches anything off this host, and records them at
  `/captured`. The payloads are not compared case by case: posthog-python
  flushes on a timer from a background thread, so an event's arrival is not
  ordered against the response that caused it, and a comparison of it would be
  a comparison of timing. One property differs on purpose anyway — `$lib` names
  the tier that sent the event. What `/captured` does show, and is worth a look
  after a run, is both tiers sending the same event names for the same cases:
  `pat_revoked` seventeen times from each of `soloclef-api` and
  `posthog-python` in the run this suite was committed on, and `pat_api_request`
  six times against five — which is the flush timer, not a difference in what
  was recorded.
- **A path or method no ported route answers.** The proxy's matchers are
  anchored regexes that match exactly the routes in `routes.json`, so such a
  request never reaches the Go tier. Its refusals there are its own and differ
  from Starlette's; `custom/docs/go-api-tier.md` records that as an open
  question. Comparing it would fail on a request neither tier is asked.
- **The MCP servers.** Two of the divergences this comparison has caught were
  between the Python and Go MCP servers, which speak JSON-RPC over stdio.
  `cases/regressions.json` lists where each is pinned instead.

## The world it stands up

`stack/up.sh` builds it from scratch every run, and `stack/down.sh` removes it
— from a trap, so it comes down when a comparison fails or the run is
interrupted.

| Piece | What |
|---|---|
| `parity-db` | a throwaway `pgvector/pgvector:pg17`, migrated by `alembic upgrade head` the way `soloclef_api/internal/schema/dump_from_migrations.sh` runs it, then seeded from `stack/seed.sql` |
| `parity-redis` | a throwaway `redis:8`, which is both tiers' rate-limit counter and their Celery broker, and which nothing consumes |
| `parity-python` | the real `app.app:app` under uvicorn, from the image the stack runs |
| `parity-analytics` | the PostHog stand-in |
| the Go tier | built from this checkout and run on loopback, so a rebuild is a second |

### It refuses to run against anything live

`stack/refuse_live_targets.sh` is a precondition, not a nicety: the comparison
truncates tables, reseeds between every write and drains the broker's queues.
Pointed at the running stack it would empty the app's database and eat work the
workers were about to do — the same reason
`soloclef_worker/internal/beat/redis_test.go` refuses a Redis holding a live
queue. It refuses when:

- a container it would use is named like one of the live stack's, or exists
  without this suite's `parity.disposable` label (it never removes a container
  it did not create);
- a port it would publish is already published by a container this suite did
  not create;
- `PARITY_REPO` is the live checkout.

And once connected, `stack/up.sh` refuses a database that already holds tables
and a Redis that holds any key at all — least of all one of the app's own
queues.

Every address in the world is at `example.invalid`, every secret in it is
invented for the comparison, and every account, workspace, document and log in
`stack/seed.sql` is synthetic. Nothing here was read from a live database.

## Running it in CI

`.forgejo/workflows/go.yml` says what this would need: a runner with a Docker
daemon it may create containers on and the `soloclef-backend-fast:latest`
image. No change to the suite — it already stands up its own world and cleans
up after itself. Until there is such a runner, it is run by hand before a route
is handed over.
