# Soloclef Notes

A self-hosted, NotebookLM-style notebook app: add sources (PDFs, web pages,
notes), chat with them with citations, and generate study outputs from them in
Studio (reports, slide decks, video overviews, podcasts, quizzes, flashcards,
mind maps, infographics, data tables). There is a web app, an iPhone/Android
app in `soloclef_mobile`, and an MCP server for coding agents.

The original server is <https://notes.danvics.com>. Every server is built from
this code and knows nothing about any other: the address, contact, sign-in and
payment settings are yours.

## Self-hosting

### What you need

- Docker Engine 24+ with Compose v2.
- About 20 GB of free disk: the sandbox image alone is roughly 13 GB after
  build.
- An OpenAI-compatible model endpoint for chat (LiteLLM, OpenRouter, OpenAI,
  Ollama, vLLM, ...). A vision model is optional; image generation is optional.
- An embedding endpoint. Point `EMBEDDING_MODEL` at it with the provider in
  the name, for example `litellm://openai/bge-m3` for an OpenAI-compatible
  gateway. The backend runs no model itself, so a bare model name is refused
  at startup.
- Optional: SMTP (password reset, invitations, automation results), an OIDC
  provider, S3-compatible storage, Stripe/BTCPay.

### 1. Configure

```bash
git clone <this-repository> soloclef
cd soloclef
cp docker/.env.example docker/.env
```

Edit `docker/.env` and set at least:

```dotenv
SECRET_KEY=<openssl rand -base64 32>
DB_PASSWORD=<a long random password>
SOLOCLEF_PUBLIC_URL=http://localhost:3929
BILLING_ADMIN_EMAILS=you@example.com
```

`SOLOCLEF_PUBLIC_URL` is the address people open; use the final https address
if you have one, because links in email, OIDC callbacks and phone push
notifications are built from it. See
[`soloclef_web/content/docs/self-hosting.mdx`](soloclef_web/content/docs/self-hosting.mdx)
for every setting (sign-in, SMTP, embeddings, parsing, billing, the phone app).

Document parsing is **Local (PyMuPDF4LLM)** by default: free text/table
extraction with OCR only where a page needs it, and the file never leaves the
server. Users can still choose **Cloud** per upload when a cloud parser is
configured. To keep Local as the default, leave:

```dotenv
ETL_DEFAULT_PARSER=LOCAL
```

### 2. Build and start

The backend is built in two stages (source, then the deployment stage), so
build it before Compose:

```bash
docker build -t soloclef-backend-src:latest --target production soloclef_backend
docker build -t soloclef-backend-fast:latest custom

cd docker
docker compose -f docker-compose.yml build
docker compose -f docker-compose.yml up -d
```

The website is at <http://localhost:3929>. `docker-compose.override.yml` is one
particular host's shape; name `docker-compose.yml` explicitly (as above) and
put your own overrides in your own file.

The first build downloads a base sandbox image and builds Remotion, LibreOffice
and the Python stack; it takes a while. Rebuild `sandbox-image` after pulling a
release that changes `docker/sandbox`.

### 3. Create the first account

Sign-ups are closed by default. Create a verified account from the backend
container; `--set-password` asks for a password (use `-it`):

```bash
docker compose -f docker-compose.yml exec -it backend \
  python scripts/create_account.py you@example.com --set-password
```

Without `--set-password`, the account gets a random password and the person
sets one with **Forgot password?**, which needs SMTP. Make the account an
administrator by putting its address in `BILLING_ADMIN_EMAILS` and restarting
the backend:

```bash
docker compose -f docker-compose.yml up -d backend celery_worker celery_beat
```

Sign in, open **Settings → Models**, and add a provider (OpenAI-compatible
works with LiteLLM, OpenRouter, Ollama, vLLM and most hosted APIs). Pick a chat
model for the workspace. Alternatively, provide server-wide models in
`docker/global_llm_config.yaml` (copy
`soloclef_backend/app/config/global_llm_config.example.yaml`) and mount it
read-only at `/app/app/config/global_llm_config.yaml` in `backend`,
`celery_worker` and `celery_beat`; that is how the original server shares one
set of models with every account.

### 4. Use the phone app

The app can sign in to any Soloclef server: on its sign-in screen choose
**Change server** and enter your https address. **One build works for every
server**; a separate build per server is not needed. To make your server the
default in a build of your own, set `EXPO_PUBLIC_DEFAULT_SERVER_URL` at build
time (see [`soloclef_mobile/README.md`](soloclef_mobile/README.md)). Push
notifications need `SOLOCLEF_PUBLIC_URL` to be the exact https address people
type in the app. Your own Apple/Google/EAS accounts and a new bundle id are
only needed to publish your own store listing, not to use the app with your
server.

## Layout

| Path | What |
|------|------|
| `soloclef_backend/` | FastAPI API, Celery workers, Alembic migrations |
| `soloclef_web/` | Next.js web app |
| `soloclef_mobile/` | iPhone/Android app (Expo) |
| `soloclef_mcp/` | MCP server over the REST API |
| `docker/` | Compose stack and the sandbox image (`docker/sandbox`) |
| `custom/` | Final backend image stage and deployment notes |

## Development

Build, test and deploy commands are in [`AGENTS.md`](AGENTS.md). Local
development notes are in [`soloclef_backend/README.md`](soloclef_backend/README.md),
[`soloclef_web/README.md`](soloclef_web/README.md) and
[`soloclef_mobile/README.md`](soloclef_mobile/README.md).

## Origin and licence

Soloclef Notes began as a fork of [SurfSense](https://github.com/MODSetter/SurfSense)
and has since diverged from it. It remains under the Apache License 2.0 (see
[`LICENSE`](LICENSE)); `soloclef_backend/app/proprietary/` carries its own
licence notice.
