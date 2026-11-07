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
| `internal/pythonjson` | JSON as the Python tier writes it: insertion order, no HTML escaping, float `repr`, and both datetime spellings — `isoformat`'s `+00:00` and pydantic's `Z`. |
| `internal/errorenvelope` | `app.py:_build_error_response` and the 422 handler. |
| `internal/requestcontext` | `X-Request-ID` and the access line. |
| `internal/corsmirror` | Starlette's `CORSMiddleware` for a simple request. |
| `internal/principal` | `app.users.get_auth_context`, in its order, with its fall-throughs. |
| `internal/requestnumber` | FastAPI's integer coercion for a parameter, and the 422 it raises. |
| `internal/workspaceaccess` | `app.utils.rbac`'s membership, permission and API-access checks, split as they are there. |
| `internal/pythondelegate` | Hands a request this tier cannot verify to the Python tier. |
| `internal/schema` | The schema every query is generated against, and the check that it is still what the migrations produce. |
| `internal/logsummary` | The workspace log summary. |
| `internal/workspacemembers` | The workspace member list. |
| `internal/myaccess` | The caller's own role and permissions in a workspace. |
| `cmd/api` | The process: config, pool, routes, shutdown. |

## Commands

```bash
cd soloclef_api && go test ./...
cd soloclef_api && go vet ./... && gofmt -l .

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
- Do not port a route that touches the Python ML stack, calls a model, or
  enqueues Celery work. The doc lists what cannot move and why.
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
