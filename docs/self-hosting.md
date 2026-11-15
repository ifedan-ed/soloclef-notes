# Install

Soloclef runs as one Docker Compose stack on a Linux host. This page takes
you from a fresh checkout to a working site with your own account.

## What you need

- Docker Engine 24 or later with Compose v2.
- About 8 GB of memory and 25 GB of free disk. The Studio sandbox image is
  most of that disk; without sandboxes you need far less.
- A LiteLLM proxy running on a Docker network of its own on the same
  host. Every model call goes through it. It must serve:
    - a chat model;
    - a small, fast model for short tasks such as naming a notebook;
    - the BGE-M3 embedding model;
    - a speech model, for audio and video overviews;
    - optionally a vision model, which reads pictures in uploads.
- An https address for the site, such as `https://notes.example.org`, from a
  reverse proxy or a tunnel. The phone app only talks to https servers.

## 1. Get the code and the settings file

```bash
git clone <repository URL> soloclef
cd soloclef
cp docker/.env.go.example docker/.env.go
```

All settings live in `docker/.env.go`. Every line in it must stay: Compose
stops before starting anything and names a setting that is missing. Lines
marked optional may be left empty, and empty means that feature is off.

Replace every example value that names a host, a model, a network or a
limit. At least set these:

```dotenv
SOLOCLEF_GO_MODEL_NETWORK=<the Docker network LiteLLM is on>
SOLOCLEF_GO_LITELLM_URL=http://litellm:4000/v1
SOLOCLEF_API_CHAT_MODEL=<LiteLLM's name for your chat model>
SOLOCLEF_WORKER_QUICK_MODEL=<LiteLLM's name for the small model>
SOLOCLEF_GO_EMBEDDING_MODEL=<LiteLLM's name for BGE-M3>
SOLOCLEF_GO_TTS_SERVICE=openai/<LiteLLM's name for the speech model>
SOLOCLEF_API_PUBLIC_URL=https://notes.example.org
SOLOCLEF_API_FRONTEND_URL=https://notes.example.org
SOLOCLEF_API_ALLOWED_ORIGINS=https://notes.example.org
SOLOCLEF_API_ERROR_REPORT_URL=https://notes.example.org/contact
SOLOCLEF_API_BRAND_NAME=Soloclef
SOLOCLEF_API_SIGNUP_ALLOWED=false
```

The [settings reference](settings-reference.md) explains every line.

## 2. Make the secrets

Secrets are files, one per secret, in `docker/secrets-go`. The services are
given the path to each file, never the value.

```bash
mkdir -m 700 docker/secrets-go
cd docker/secrets-go
openssl rand -hex 24 > postgres_password
openssl rand -hex 32 > session_secret
printf '%s\n' "your LiteLLM key" > litellm_key
printf '%s\n' "soloclef" > s3_access_key
openssl rand -hex 24 > s3_secret_key
chmod 0444 *
cd ../..
```

The services run as different users, so each file must be readable by all,
inside a directory only you can enter. Keep the database password to hex
characters.

Keep `session_secret` safe and never change it. It signs sign-ins, and it
also encrypts what people store in their connections: mailbox passwords,
their own model keys, Exa and Tavily keys and MCP server secrets. With a
different secret none of those can be opened again, and everyone has to
reconnect.

Studio sandboxes need one more file. See
[Optional features](optional-features.md#studio-sandboxes).

## 3. Start it

Every command on these pages uses the same three Compose options, so define
a shell function for them first:

```bash
dc() { docker compose -p soloclefgo --env-file docker/.env.go -f docker/docker-compose.go.yml "$@"; }
```

Then build and start:

```bash
dc up -d --build
```

The first build takes a while. Later builds reuse the cache. On this and
every later start, the database schema is brought up to date before the
other services start.

Check that everything is running:

```bash
dc ps
dc logs -f api
```

The site now answers on the address in `SOLOCLEF_GO_SITE_PUBLISH`, which is
`127.0.0.1:18470` in the example.

## 4. Put https in front

The stack publishes one port. It speaks plain http, so put your TLS proxy or
tunnel in front of it. With Caddy on the same host:

```text
notes.example.org {
	reverse_proxy 127.0.0.1:18470
}
```

With nginx:

```nginx
server {
    listen 443 ssl;
    server_name notes.example.org;
    # ssl_certificate and ssl_certificate_key as usual

    client_max_body_size 600m;

    location / {
        proxy_pass http://127.0.0.1:18470;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_buffering off;
        proxy_read_timeout 900s;
    }
}
```

Whatever you use, it must:

- pass `X-Forwarded-Proto` and `X-Forwarded-For`;
- not buffer responses, because chat answers, live updates and the MCP
  server all stream;
- accept a request as large as one upload: the largest file times the most
  files per upload, 500 MB with the example values;
- let a request run for several minutes.

Then tell the stack to trust that proxy. Set `SOLOCLEF_GO_TRUSTED_PROXIES` to
its address, or leave `private_ranges` if it reaches the stack from a
private address. Keep `SOLOCLEF_API_SESSION_COOKIE_SECURE_POLICY=always`.

## 5. Make your account

Sign-ups are off in the example, so make your own account from the command
line. The password is read from standard input, so it stays out of your
shell history:

```bash
printf '%s\n' "your password" | dc exec -T api soloclef-api set-password you@example.com
dc exec api soloclef-api set-admin you@example.com true
```

Open your https address and sign in.

`set-password` makes an account, or gives an existing one a new password. A
password needs at least 8 characters. `set-admin` makes an account an
administrator, or an ordinary account again with `false`. Use the same two
commands for everyone else while sign-ups are off.

## Who can join

`SOLOCLEF_API_SIGNUP_ALLOWED` decides whether people can make their own
accounts. It must be `true` or `false`.

- With `false`, the sign-in page and the phone app offer Request access,
  which opens your contact page. The server refuses sign-ups, and single
  sign-on makes no new accounts. You add people with `set-password`.
- With `true`, anyone who can reach the site can sign up, and a first single
  sign-on with a new address makes an account.

Set `SOLOCLEF_API_SUPPORT_EMAIL` so the contact page has an address to show.

The website's pages are built with the sign-up setting, the public address,
the support address and the single sign-on settings in them. After changing
any of those, rebuild the web app:

```bash
dc up -d --build proxy
```

Every other setting is read when its service starts, so `dc up -d` is
enough.

## Models and costs

Everyone uses the models named in `docker/.env.go` unless they choose
otherwise, and those calls are on your bill. They all go through your
LiteLLM proxy, so set budgets and rate limits there.

People can also connect their own OpenAI-compatible service with their own
key, under Settings, Models. Soloclef calls that service directly, not
through your proxy, and those calls are on their bill. An address inside
your own network, such as an Ollama machine, is refused until you allow it.
See [Optional features](optional-features.md#models-on-your-own-network).

Choose the embedding model before the first upload. Changing it later means
embedding every document again. See
[Updating and backups](operations.md#the-embedding-model).

## What runs

| Service | What it does |
| --- | --- |
| `proxy` | Caddy, the one published port. It serves the web app's files and routes everything else to the services below. |
| `api` | The API: sign-in, notebooks, the chat, uploads, and the live updates the web app and the phone app follow. |
| `indexer` | Reads uploaded files, splits them into passages and embeds them. |
| `parse-service` | Turns PDF, Word, PowerPoint and EPUB files into text, on the server. |
| `worker` | Background work: audio and video overviews, automations, Google Drive sync and phone pushes. |
| `mcp` | The MCP server that AI agents connect to at `/mcp`. |
| `migrate` | Brings the database schema up to date on every start, then exits. |
| `postgres`, `valkey`, `qdrant`, `minio` | The database, the job queue, the search index and the file store. |
| `minio-init` | Creates the file store's bucket, then exits. |
| `sandboxd` | Runs Studio's sandboxes. It only starts when sandboxes are switched on. |

Next: [Optional features](optional-features.md) covers single sign-on, email,
Google Drive, Nextcloud, mail sign-ins, phone pushes, Studio sandboxes and
hosted parsers.
