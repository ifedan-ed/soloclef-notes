# Soloclef Notes

A self-hosted, NotebookLM-style workspace for your own documents. Add PDFs,
web pages or notes, ask questions with cited answers, and generate reports,
slide decks, mind maps, quizzes, flashcards, audio/video overviews and more.
The web app, iPhone/Android app and MCP server are all in this repository.

Soloclef Notes is a fork of [SurfSense](https://github.com/MODSetter/SurfSense).
Thanks to the SurfSense team for the foundation.

## What you need

- Docker Engine 24+ with Compose v2, and about 25 GB of free disk.
- An OpenAI-compatible model endpoint for chat (LiteLLM, OpenRouter, OpenAI,
  Ollama, vLLM, ...). A vision model and an image model are optional.
- An embedding model. The default is a small local model that runs on the
  server; you can point it at any OpenAI-compatible embeddings endpoint.

## Quick start

```bash
git clone https://github.com/ifedan-ed/soloclef-notes.git soloclef
cd soloclef
cp docker/.env.example docker/.env
```

Edit `docker/.env` and set at least:

```dotenv
SECRET_KEY=<openssl rand -base64 32>
DB_PASSWORD=<a long random password>
SURFSENSE_PUBLIC_URL=http://localhost:3929
BILLING_ADMIN_EMAILS=you@example.com
```

Build and start:

```bash
docker build -t soloclef-backend-src:latest --target production surfsense_backend
docker build -t soloclef-backend-fast:latest custom

cd docker
docker compose -f docker-compose.yml build
docker compose -f docker-compose.yml up -d
```

Open <http://localhost:3929>. The first build is large (the sandbox image
contains LibreOffice, Remotion and a PDF stack); later builds use the cache.

Create the first account (sign-ups are closed by default):

```bash
docker compose -f docker-compose.yml exec -it backend \
  python scripts/create_account.py you@example.com --set-password
```

Set the same address in `BILLING_ADMIN_EMAILS` and restart the backend. Sign
in, open **Settings → Models**, and add a provider. Or provide server-wide
models by copying
`surfsense_backend/app/config/global_llm_config.example.yaml`, editing it, and
mounting it at `/app/app/config/global_llm_config.yaml` in the backend and
Celery services.

## Billing is optional

You do not need payments or credits to self-host. The default is
`BILLING_MODE=shadow`: usage is recorded, but nothing is blocked or charged.
For a completely free server, set:

```dotenv
BILLING_MODE=off
```

and restart the backend. If you keep live billing for other people but want
your own account unlimited, put your address in `BILLING_ADMIN_EMAILS`;
configured administrators are exempt. An administrator can also mark any
account **exempt** on the billing admin page.

## OIDC is optional: use a password

The default sign-in is a local email/password account (`AUTH_TYPE=LOCAL`,
`OIDC_ENABLED` unset). No identity provider is required. Create the account
with the `--set-password` command above; it asks for the password
interactively. If you want people to create their own accounts, set
`NEXT_PUBLIC_SIGNUPS_OPEN=true` in `docker/.env`, rebuild the frontend, and
keep the sign-up route open (see the self-hosting docs for closing it later).
Password reset by email needs `SMTP_*`; without SMTP, create accounts with
`--set-password`.

OIDC is only for adding single sign-on beside the password form. Set
`OIDC_ENABLED=TRUE` and the `OIDC_*` values in `docker/.env`, and register
`{BACKEND_URL}/auth/oidc/callback` with your provider.

## Add a PDF

Choose **Add sources** and upload a PDF. The default parser is **Local
(PyMuPDF4LLM)**: it extracts text and tables, runs OCR only where a page needs
it, and the file never leaves the server. Cloud parsing (LlamaCloud, Datalab or
the bundled Unstructured library) is optional and per upload. To keep Local as
the default, leave `ETL_DEFAULT_PARSER=LOCAL` in `docker/.env`.

## The phone app

The app in `surfsense_mobile` can sign in to any Soloclef server. On first
launch enter your server's https address, or set
`EXPO_PUBLIC_DEFAULT_SERVER_URL` when you build it. One build works for every
server. Push notifications need `SURFSENSE_PUBLIC_URL` to be the exact https
address people type in the app.

**iPhone beta:** <https://testflight.apple.com/join/1tjGWu1Q>

## Documentation

- Documentation index: [`docs/README.md`](docs/README.md)
- Full self-hosting reference: `surfsense_web/content/docs/self-hosting.mdx`
  (served at `/docs/self-hosting` by a running server).
- User guide: `surfsense_web/content/docs/getting-started.mdx`.
- Mobile build notes: `surfsense_mobile/README.md`.

## What runs

| Service | What it does |
| --- | --- |
| `frontend` | Next.js web app |
| `backend` | FastAPI API and chat/agent runtime |
| `celery_worker`, `celery_beat` | indexing, Studio outputs and scheduled jobs |
| `db`, `redis` | Postgres + pgvector, and queues/cache |
| `zero-cache` | live document/chat updates |
| `sandbox-image` / `opensandbox-server` | isolated code execution and Studio artifacts |
| `searxng` | bundled web-search fallback |
| `mcp` | MCP server for coding agents |
| `proxy` | bundled Caddy entry point |

## Licence

Apache License 2.0. See [LICENSE](LICENSE). `surfsense_backend/app/proprietary/`
carries its own licence notice.
