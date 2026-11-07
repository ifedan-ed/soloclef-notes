# Run your own server

## What you need

- A Linux server with Docker Engine 24+ and Compose v2, and about 25 GB of free
  disk. 4 CPU cores and 8 GB of memory are a comfortable start.
- A chat model behind an OpenAI-compatible endpoint (LiteLLM, Ollama, vLLM,
  OpenRouter, OpenAI and others). A vision model and an image model are
  optional.
- An embedding model. The default is a small local model that runs on the
  server; you can point it at any OpenAI-compatible embeddings endpoint
  instead.
- For the phone app and for people outside your network: an https address,
  such as `https://notes.example.org`, in front of the server.

## First start

```bash
git clone https://github.com/ifedan-ed/soloclef-notes.git soloclef
cd soloclef
cp docker/.env.example docker/.env
```

Edit `docker/.env` and set at least:

```dotenv
SECRET_KEY=<output of: openssl rand -base64 32>
DB_PASSWORD=<a long random password>
SURFSENSE_PUBLIC_URL=https://notes.example.org   # or http://localhost:3929 to try it
BILLING_ADMIN_EMAILS=you@example.com
BILLING_MODE=off
```

`BILLING_MODE=off` means nothing is metered and nobody is ever limited; see
[Usage and limits](usage.md).

Build and start:

```bash
docker build -t soloclef-backend-src:latest --target production surfsense_backend
docker build -t soloclef-backend-fast:latest custom

cd docker
docker compose -f docker-compose.yml build
docker compose -f docker-compose.yml up -d
```

The first build is large (the sandbox image holds LibreOffice, Remotion and a
PDF stack); later builds use the cache. The site answers on port 3929.

## The first account

Sign-ups are closed by default. Create your account from the backend
container; `--set-password` asks for the password, so no email setup is
needed yet:

```bash
docker compose -f docker-compose.yml exec -it backend \
  python scripts/create_account.py you@example.com --set-password
```

Use the same address as `BILLING_ADMIN_EMAILS`. That account is the
administrator: it can add model providers, change server settings, and give
other people unlimited use.

## Models

Sign in, open Settings → Models, and add a provider: its base URL, key and
model names. Or give the whole server its models from a file: copy
`surfsense_backend/app/config/global_llm_config.example.yaml`, edit it, and
mount it at `/app/app/config/global_llm_config.yaml` in the backend and Celery
services. Without a chat model the app opens, but chat and Studio cannot
answer.

## An https address

Put any reverse proxy in front of port 3929. With Caddy:

```text
notes.example.org {
	reverse_proxy localhost:3929
}
```

Set `SURFSENSE_PUBLIC_URL` to that exact address and rebuild the website
(`docker compose -f docker-compose.yml build frontend` then `up -d`): links in
emails, share links and the phone app's push notifications all use it.

## Email (optional)

Set `SMTP_ENABLED=TRUE`, `SMTP_HOST`, `SMTP_FROM` and the other `SMTP_`
settings to send password-reset links, invitations and automation results.
Without email, create accounts with `--set-password`.

## What runs

| Service | What it does |
| --- | --- |
| `frontend` | The Next.js website |
| `backend` | The FastAPI API and the chat and agent runtime |
| `celery_worker`, `celery_beat` | Reading sources, Studio outputs and scheduled jobs |
| `db`, `redis` | Postgres with pgvector, and queues and cache |
| `zero-cache` | Live updates to notebooks and chats |
| `opensandbox-server` | Isolated code execution for Studio |
| `searxng` | Bundled web search |
| `mcp` | The MCP server for agents |
| `proxy` | The bundled Caddy entry point |

Every setting, with its default, is in the in-app guide at `/docs/self-hosting`
on your server.
