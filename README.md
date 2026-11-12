# Soloclef Notes

A self-hosted, NotebookLM-style notebook app. You put your material in a
notebook (PDFs, Office files, web pages, papers, notes), ask questions and get
answers that cite the passage they came from, and turn the sources into
reports, slide decks, podcasts, videos, quizzes and more. Every server is built
from this code and knows nothing about any other: its address, sign-in, models
and storage are its own settings.

The server is written in Go, with one Python service for reading PDF and
Office files. The web app is Next.js. There is an MCP server for agents and an
Expo phone app.

## What it does

| Feature | What it is |
|---|---|
| Notebooks and sources | Upload PDF, Word, PowerPoint and EPUB files (read on the server), text, Markdown, CSV and HTML; pictures, described by a vision model when the upload asks for it. Import from Google Drive and Nextcloud, from web pages, and from PubMed, arXiv, medRxiv/bioRxiv and Google Scholar searches. A link to a PDF (from Read a web page, a research import or the chat) becomes a stored source parsed like an upload, with the source's chosen parser (pymupdf4llm by default); arXiv papers are read from their PDF. |
| Parsers | Local (pymupdf4llm, the file stays on the server) by default; Datalab, LlamaParse and Unstructured clients for a deployment that adds a key. A file is read by the parser chosen for it and no other. |
| Chat with citations | One chat per notebook, answering from the sources you tick, with numbered citations that open the passage. @-mention a source, choose the model, set instructions, approve or refuse an action the assistant wants to take. |
| Search | Two rankings fused into one: vectors in Qdrant and keywords in Postgres, over a notebook's sources and notes, for the chat and for the search box. |
| Studio | Reports (PDF, Word, web page), slide decks, workbooks, mind maps, infographics, flashcards, quizzes, data tables, audio overviews (two-voice podcasts) and video overviews. Documents and videos are built in a sandbox; podcasts by the worker. |
| Notes | Save an answer as a note, write your own, and turn a note into a source. |
| Automations | Tasks the assistant runs on a schedule or on an event, with the chat's own tools; results saved as notes and optionally emailed. |
| Email | Connect a mailbox (IMAP, or Microsoft with its own sign-in) so the assistant can search and read mail and attachments, read-only and live: mail is never copied in. |
| Sharing | Read-only public links to a chat, a Studio output or a whole notebook. |
| Agents (MCP) | An MCP server (streamable HTTP at `/mcp`, or stdio from the binary) so Claude, ChatGPT, Cursor and other agents can search and read your notebooks and notes, with an API key or the agent's own sign-in. |
| Sign-in | Email and password, optional single sign-on (any OpenID Connect provider), password reset by email. Sign-ups are one switch. A sign-in ends at once on logout, a device sign-out, sign-out everywhere, or a password reset or change: each access token names its sign-in, the API checks that it is still live on every request, and zero-cache rechecks open connections every 60 seconds. |
| Live updates | The web app follows the database through zero-cache, so a document that finishes indexing or an output that finishes rendering appears without a reload. |

## Architecture

Everything runs from one Compose file, `docker/docker-compose.go.yml`, under
the project `soloclefgo`. One port is published (Caddy's); every other service
is reachable only on the stack's own networks.

| Service | Built from | What it does |
|---|---|---|
| `proxy` | `caddy:2-alpine` + `docker/proxy-go` | The one door. `/api/v1`, `/auth` and `/users` go to the API, `/zero` to zero-cache, `/mcp` to the MCP server, everything else to the web app. Plain HTTP: TLS is the front proxy's. |
| `frontend` | `soloclef_web` | The Next.js web app. |
| `api` | `soloclef_api` | The HTTP API, the chat and its agent, sign-in, uploads, Studio requests. Also the admin commands (`soloclef-api set-password`, `set-admin`). |
| `indexer` | `soloclef_worker` (`cmd/indexer`) | Makes a document searchable: fetches the file from MinIO, has it parsed, chunks it, embeds the chunks through LiteLLM and writes them to Postgres and Qdrant. |
| `worker` | `soloclef_worker` (`cmd/worker`) | Background jobs: podcasts, videos, automation runs, Drive sync, a notebook's name, folder and account removal. |
| `parse-service` | `soloclef_parse_service` | PDF, DOCX, PPTX and EPUB to Markdown with pymupdf4llm. The one Python service (below). |
| `mcp` | `soloclef_mcp_go` | The MCP server. Holds no data; it calls the API over REST with the caller's credential. |
| `migrate` | `soloclef_schema` | Applies the SQL migrations (goose) and exits. The only thing that changes the schema; the API, indexer and worker refuse to start against a database at another version. |
| `zero-cache` | `rocicorp/zero` | The web app's live sync, following Postgres by logical replication. |
| `postgres` | `pgvector/pgvector:pg17` | Accounts, notebooks, documents, chunks and their keyword index, jobs. |
| `valkey` | `valkey/valkey` | The job queues (the Celery wire protocol, spoken by Go on both ends), rate-limit counters, the event stream and chat-turn leases. |
| `qdrant` | `qdrant/qdrant` | The vector store the indexer writes and the chat searches. |
| `minio`, `minio-init` | `minio/minio` | The file store: every uploaded and generated file is an object in one bucket. `minio-init` makes the bucket once. |
| `opensandbox`, `sandbox-image` | `opensandbox/server` + `docker/sandbox` | Only with a sandbox switch on. Runs the code a model writes to build a report, deck or video in a container of its own (Python document libraries, LibreOffice, Remotion), never in the API's or the worker's process. |

Outside the stack: a LiteLLM proxy on a Docker network of its own
(`SOLOCLEF_GO_MODEL_NETWORK`). Every model call goes through it: chat, the short
jobs, embeddings, vision, image generation and speech. No service calls a model
provider directly, and nothing runs a model locally.

### Go modules

Each is its own module; the services require their siblings with relative
`replace` directives, so everything is built from this checkout. The module
path is `git.danvics.com/danvics/soloclef/<directory>`.

| Module | What it is |
|---|---|
| `soloclef_api` | The API service (`cmd/api`) and its routes, one package per feature under `internal/`. |
| `soloclef_worker` | The worker (`cmd/worker`) and the indexer (`cmd/indexer`). |
| `soloclef_mcp_go` | The MCP server. |
| `soloclef_schema` | The SQL migrations, the `migrate` command and the start-up version check. |
| `soloclef_agent` | The tool-calling loop the chat runs on: streaming events, approvals, checkpoints, nested agents. |
| `soloclef_assistant` | The chat's tools (document search, web and academic sources, the page reader, mail, Drive, Nextcloud, MCP servers) and how a turn's agent is assembled. |
| `soloclef_automations` | What an automation definition may say, when a schedule fires, how an event matches a trigger. |
| `soloclef_celery` | The Celery wire protocol the API and the worker share on Valkey. |
| `soloclef_chunker` | Where a chunk begins and ends: chonkie's Rust core and HuggingFace `tokenizers` through cgo (`chonkie/rust`), and the table-aware chunker. |
| `soloclef_ingest` | Parsing: the parser registry, the client for the parse service, the hosted parsers, and the text, CSV and HTML readers. |
| `soloclef_connectors` | Stored credentials and the Google Drive grant. |
| `soloclef_filestore` | The file store (S3/MinIO, or local disk). |
| `soloclef_mail` | Mail transports: MIME, SMTP, smtp2go. |
| `soloclef_studio` | The sandbox client and how a Studio output is saved. |

### The one Python service

`soloclef_parse_service` wraps pymupdf4llm behind one HTTP endpoint. MuPDF's
layout analysis (headings, reading order across columns, real pipe tables) has
no Go equivalent, so this one step stays in Python. It imports nothing from
the rest of the application, publishes no port, and is called only by the Go
services on the stack's network. pymupdf4llm is pinned exactly, and MuPDF is
AGPL. The hosted parsers' clients are Go (`soloclef_ingest`).

Other Python in the tree is not part of the running site: the stand-in servers
of the browser test stack (`docker/e2e-web-go`) and the document libraries
inside the sandbox image.

## Quick start

You need Docker Engine 24+ with Compose v2, about 25 GB of disk (the sandbox
image alone is about 13 GB), and a LiteLLM proxy on a Docker network of its
own with a chat model, an embedding model (the indexer is built for BGE-M3)
and, for audio overviews, a speech model.

```bash
git clone <this repository> soloclef && cd soloclef
cp docker/.env.go.example docker/.env.go
```

Edit `docker/.env.go`. Every line is required and every value is an example:
replace each one that names a host, a model, a network or a limit. Then make
the secret files, one per secret, in the directory `SOLOCLEF_GO_SECRETS_DIR`
names (relative to `docker/`):

```bash
mkdir -m 700 docker/secrets-go && cd docker/secrets-go
openssl rand -hex 24 > postgres_password
openssl rand -hex 32 > session_secret
printf '%s\n' "your LiteLLM key" > litellm_key
printf '%s\n' "soloclef" > s3_access_key
openssl rand -hex 24 > s3_secret_key
openssl rand -hex 32 > sandbox_api_key   # only with a sandbox switch on
chmod 0444 * && cd ../..
```

Build and start:

```bash
docker compose -p soloclefgo --env-file docker/.env.go \
  -f docker/docker-compose.go.yml up -d --build
```

The site answers on `SOLOCLEF_GO_SITE_PUBLISH`; put your HTTPS proxy in front
of it. Sign-ups are off in the example, so make the first account with the
API's own command (the password is read from standard input):

```bash
dc() { docker compose -p soloclefgo --env-file docker/.env.go -f docker/docker-compose.go.yml "$@"; }
printf '%s\n' "your password" | dc exec -T api soloclef-api set-password you@example.com
dc exec api soloclef-api set-admin you@example.com true
```

To update: pull, then run the same `up -d --build`. The `migrate` service
brings the database to the new schema before the other services start.

## Configuration

- `docker/.env.go.example`: every setting, with what it means. Compose stops
  and names the setting if one is missing; there are no hidden defaults.
- `docker/docker-compose.go.yml`: the stack itself, with the reason for each
  service's shape written beside it.
- `docs/self-hosting.md` and `docs/guide/settings-reference.md`: the
  self-hosting guide and the settings grouped by purpose (sign-in, email,
  connections, parsing, embeddings and search, Studio). The same guide is in
  the web app at `/docs/self-hosting`.
- The optional secrets (single sign-on, Google Drive, Microsoft mail, an SMTP
  password) are mounted by a Compose override file of your own; the comment
  beside each setting in `docker/docker-compose.go.yml` names the secret.

## Development

| What | Command |
|---|---|
| A Go module | `cd soloclef_<name> && gofmt -l . && go vet ./... && go test ./...` (Go 1.25; modules that import the chunker need its Rust library first: `cargo build --release --manifest-path soloclef_chunker/chonkie/rust/Cargo.toml`) |
| The parse service | `cd soloclef_parse_service && uv sync && uv run pytest` |
| Web unit tests | `cd soloclef_web && pnpm install && pnpm test:unit` |
| Web end-to-end | `cd soloclef_web && pnpm test:e2e:stack:up && pnpm test:e2e` (a throwaway copy of the Go stack with stand-in models, on 127.0.0.1:3945; `soloclef_web/tests/README.md`) |

CI is in `.forgejo/workflows/`: one job per Go module, and the web unit tests.

## Licence

Soloclef Notes began as a fork of [SurfSense](https://github.com/MODSetter/SurfSense)
and has since been rewritten. It is under the Apache License 2.0 (see
[`LICENSE`](LICENSE)). The parse service depends on MuPDF, which is AGPL.
