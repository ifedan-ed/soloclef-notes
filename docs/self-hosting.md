# Run your own server

## What you need

- A Linux host with Docker Engine 24 or later and Compose v2, about 25 GB of
  free disk (the sandbox image alone is about 13 GB) and 8 GB of memory.
- A [LiteLLM](https://docs.litellm.ai/) proxy on a Docker network of its own.
  Every model call goes through it: chat, embeddings, picture models and
  speech. It must serve, under the names you put in the settings, a chat
  model, a quick model for short calls, the embedding model (BGE-M3) and, for
  audio overviews, a speech model.
- For the phone app and for people outside your network: an https address,
  such as `https://notes.example.org`, from a reverse proxy or tunnel in
  front of the server.

## First start

```bash
git clone https://github.com/ifedan-ed/soloclef-notes.git soloclef
cd soloclef
cp docker/.env.go.example docker/.env.go
```

Edit `docker/.env.go`. Every line is required and every value in the example
is a placeholder: replace each one that names a host, a model, a network or a
limit with your own. At least:

```dotenv
SOLOCLEF_GO_MODEL_NETWORK=<the LiteLLM proxy's Docker network>
SOLOCLEF_GO_LITELLM_URL=http://litellm:4000/v1
SOLOCLEF_API_CHAT_MODEL=<the proxy's name for your chat model>
SOLOCLEF_WORKER_QUICK_MODEL=<the proxy's name for a small, fast model>
SOLOCLEF_GO_EMBEDDING_MODEL=<the proxy's name for BGE-M3>
SOLOCLEF_API_PUBLIC_URL=https://notes.example.org
SOLOCLEF_API_FRONTEND_URL=https://notes.example.org
SOLOCLEF_API_ALLOWED_ORIGINS=https://notes.example.org
SOLOCLEF_API_ERROR_REPORT_URL=https://notes.example.org/contact
```

The secrets are files, one per secret, in `docker/secrets-go` (the directory
`SOLOCLEF_GO_SECRETS_DIR` names). The services are given each file's path,
never its value:

```bash
mkdir -m 700 docker/secrets-go
cd docker/secrets-go
openssl rand -hex 24 > postgres_password
openssl rand -hex 32 > session_secret
printf '%s\n' "your LiteLLM key" > litellm_key
printf '%s\n' "soloclef" > s3_access_key
openssl rand -hex 24 > s3_secret_key
# Only when SOLOCLEF_API_SANDBOX_ENABLED or SOLOCLEF_WORKER_VIDEO_ENABLED is true:
openssl rand -hex 32 > sandbox_api_key
chmod 0444 *
cd ../..
```

The services run as different users, so each file must be readable by all
(0444) inside a directory only you can enter (0700).

Build and start:

```bash
docker compose -p soloclefgo --env-file docker/.env.go \
  -f docker/docker-compose.go.yml up -d --build
```

The first build compiles the Go services and the chunker's Rust core and
downloads BGE-M3's tokenizer at a pinned revision, checked against its
checksum. With a sandbox switch on it also builds the sandbox image
(LibreOffice, Remotion and a PDF stack), which is large; later builds use the
cache. The site answers on `SOLOCLEF_GO_SITE_PUBLISH` (loopback port 18470 in
the example).

The commands on these pages use a shell function for the three options every
command needs:

```bash
dc() { docker compose -p soloclefgo --env-file docker/.env.go -f docker/docker-compose.go.yml "$@"; }
```

## The first account

Sign-ups are off in the example (`SOLOCLEF_API_SIGNUP_ALLOWED=false`). Make
your account with the API's own command, which reads the password from
standard input so it is not left in a shell history, and make it the
administrator:

```bash
printf '%s\n' "your password" | dc exec -T api soloclef-api set-password you@example.com
dc exec api soloclef-api set-admin you@example.com true
```

The same two commands make and promote every other account while sign-ups
are off. See [Sign-in](sign-in.md) for single sign-on and open sign-up.

## Models

The server's own models are the proxy's, named in `docker/.env.go`:
`SOLOCLEF_API_CHAT_MODEL` answers in chat for an account that chose no model,
`SOLOCLEF_WORKER_QUICK_MODEL` makes short calls, `SOLOCLEF_GO_EMBEDDING_MODEL`
embeds, and `SOLOCLEF_GO_TTS_SERVICE` voices audio overviews. Image and vision
models are optional. Each person can also add a provider of their own under
Settings → Models, with their own key; those calls go through the proxy too.
Every model setting is in [Every setting](guide/settings-reference.md#models).

## An https address

The stack publishes one port, Caddy's, on `SOLOCLEF_GO_SITE_PUBLISH`. It
speaks plain http; put your TLS proxy in front of it. With Caddy on the same
host:

```text
notes.example.org {
	reverse_proxy 127.0.0.1:18470
}
```

Set `SOLOCLEF_GO_TRUSTED_PROXIES` so the stack believes that proxy's
`X-Forwarded-Proto`, keep `SOLOCLEF_API_SESSION_COOKIE_SECURE_POLICY=always`,
and set the four address settings above to the https address. Then rebuild
the web app, which draws its public pages with the address when it is built:
`dc up -d --build frontend`.

## Email (optional)

Set `SOLOCLEF_API_SMTP_ENABLED=true` and the other `SOLOCLEF_API_SMTP_`
settings to send password-reset links and automation results. Without email,
make and reset accounts with `set-password`.

## What runs

| Service | What it does |
| --- | --- |
| `proxy` | Caddy, the one published port, routing to the services below |
| `frontend` | The Next.js web app |
| `api` | The Go API, with the chat and its agent |
| `indexer` | Reads uploaded files, chunks and embeds them into Postgres and Qdrant |
| `worker` | Podcasts, videos, automations, Drive sync and other background jobs |
| `parse-service` | PDF, Word, PowerPoint and EPUB to Markdown with PyMuPDF4LLM; the one Python service |
| `mcp` | The MCP server for agents, at `/mcp` |
| `zero-cache` | Live updates to notebooks and chats |
| `migrate` | Brings the database schema up to date on every start, then exits |
| `postgres`, `valkey`, `qdrant`, `minio` | The database, the queue and counters, the vector store and the file store |
| `opensandbox` | Isolated sandboxes for Studio's reports, decks and videos, only when switched on |

[Every setting](guide/settings-reference.md) covers the rest of
`docker/.env.go`.
