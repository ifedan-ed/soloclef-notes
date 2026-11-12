# Every setting

The settings of a server live in one file, `docker/.env.go`, made from
`docker/.env.go.example` and read by `docker/docker-compose.go.yml`. Every
line of the example is required: Compose stops before anything starts, naming
the setting, if one is missing. A setting that may be empty says so in the
example, and empty then means off. Nothing has a hidden default.

The first start, the secrets and the first account are in
[Run your own server](../self-hosting.md). The example file itself explains
each line in more words than this page; this page groups them by what they
are for.

## The stack

| Setting | What it does |
| --- | --- |
| `SOLOCLEF_GO_SITE_PUBLISH` | The host address and port of the stack's one published port (Caddy's), such as `127.0.0.1:18470`. |
| `SOLOCLEF_GO_TRUSTED_PROXIES` | Whose `X-Forwarded-Proto` and `X-Forwarded-For` Caddy believes: `private_ranges`, or your front proxy's addresses. |
| `COMPOSE_PROFILES` | Leave it as `sandbox-true`: the sandbox server's profile is made from the two sandbox switches, and this line is what lets it start when one is on. |
| `SOLOCLEF_GO_MODEL_NETWORK` | The existing Docker network the LiteLLM proxy is on. |
| `SOLOCLEF_GO_SECRETS_DIR` | The directory of secret files, relative to `docker/`. |
| `SOLOCLEF_GO_POSTGRES_PASSWORD_FILE`, `SOLOCLEF_GO_SESSION_SECRET_FILE`, `SOLOCLEF_GO_LITELLM_KEY_FILE`, `SOLOCLEF_GO_S3_ACCESS_KEY_FILE`, `SOLOCLEF_GO_S3_SECRET_KEY_FILE` | Where each secret is inside a container (`/run/secrets/<name>`). They change only if the file names do. |
| `SOLOCLEF_GO_POSTGRES_USER`, `SOLOCLEF_GO_POSTGRES_DB` | The database's user and name. |
| `SOLOCLEF_GO_S3_BUCKET`, `SOLOCLEF_GO_S3_REGION` | The MinIO bucket every file is kept in (made at start if missing), and the region MinIO is told it is in. |
| `SOLOCLEF_GO_CELERY_DEFAULT_QUEUE` | The queue background jobs go on; the heavy queue is this name followed by `.heavy`. |
| `SOLOCLEF_GO_ZERO_SYNC_WORKERS` | How many sync processes zero-cache (the web app's live updates) runs. |
| `SOLOCLEF_API_LOG_LEVEL`, `SOLOCLEF_API_SHUTDOWN_GRACE_SECONDS`, `SOLOCLEF_API_DATABASE_MAX_CONNECTIONS` | The API's log level, how long requests in flight have after a stop, and its share of Postgres connections. |

## Models

The server's models are the proxy's, named in the settings:

| Setting | What it is |
| --- | --- |
| `SOLOCLEF_GO_LITELLM_URL` | The proxy's OpenAI-compatible root, ending in `/v1`, as reached on `SOLOCLEF_GO_MODEL_NETWORK`. The key is the `litellm_key` secret. |
| `SOLOCLEF_API_CHAT_MODEL` | The model that answers in chat, and drafts a podcast, for an account that chose no chat model of its own. |
| `SOLOCLEF_WORKER_QUICK_MODEL` | The model short calls are made with, such as naming a notebook. |
| `SOLOCLEF_API_CHAT_IMAGE_MODEL`, `SOLOCLEF_API_CHAT_VISION_MODEL` | Optional. The model that draws images and infographics, and the one that looks at pictures. Empty turns that ability off. |
| `SOLOCLEF_GO_TTS_SERVICE` | The speech service audio overviews are voiced with, written `roster/model` (for example `openai/tts-1`). The first part names the voices on offer (`openai`, `azure` or `vertex_ai`); a model whose name contains `kokoro` always gets Kokoro's voices. |

Each person can also add their own provider under Settings → Models, with
their own key, and pick their chat, vision and image models there. Those
calls go through the proxy too; the server never calls a provider directly.

## Address and contact

| Setting | What it does |
| --- | --- |
| `SOLOCLEF_API_PUBLIC_URL` | The one address people open, without a trailing slash. Links in emails, sign-in callbacks, the sitemap, share links and the agent address in these docs are built from it. |
| `SOLOCLEF_API_FRONTEND_URL` | The web app's address: the same as the public address unless the app is served from another origin. |
| `SOLOCLEF_API_ALLOWED_ORIGINS` | The origins a browser may call the API from. |
| `SOLOCLEF_API_ERROR_REPORT_URL` | The contact page every error message names, usually the public address followed by `/contact`. |
| `SOLOCLEF_API_SUPPORT_EMAIL` | Optional. Where Report Issue and the contact page (your server's /contact page) send people. Empty publishes no address. |
| `SOLOCLEF_API_BRAND_NAME` | The product's name in the chat's instructions, in refusals and in the password-reset email. |
| `SOLOCLEF_API_SESSION_COOKIE_SECURE_POLICY` | `always` on a site served over https. `auto` (follow the scheme each request came in on) is for a test stack on plain http only. |
| `SOLOCLEF_GO_SITE_PUBLISH` | The host address and port the stack's one published port (Caddy's) listens on, such as `127.0.0.1:18470`. Loopback unless the proxy in front runs on another machine. |
| `SOLOCLEF_GO_TRUSTED_PROXIES` | Whose `X-Forwarded-Proto` and `X-Forwarded-For` Caddy believes: your front proxy's addresses, or `private_ranges`. |

Rebuild the website after changing the public address, the support address,
any single sign-on setting or `SOLOCLEF_API_SIGNUP_ALLOWED`: the web app's
public pages are drawn when its image is built, and Compose hands those
settings to the build (`dc up -d --build frontend`). Everything else is read
when a service starts, so a restart of that service is enough.

## Sign-in

| Setting | What it does |
| --- | --- |
| `SOLOCLEF_API_SIGNUP_ALLOWED` | `true` or `false`. Whether people may make their own accounts, by the sign-up form and by a first single sign-on with an address no account has. With `false` the sign-in page and the phone app offer Request access instead, the API refuses sign-up requests itself, and you make accounts with `set-password`. |
| `SOLOCLEF_API_OIDC_ENABLED` | `true` or `false`. Single sign-on through an OpenID Connect provider (Pocket ID, Authentik, Keycloak and others), beside the password form. |
| `SOLOCLEF_API_OIDC_ISSUER`, `SOLOCLEF_API_OIDC_CLIENT_ID`, `SOLOCLEF_API_OIDC_CLIENT_SECRET_FILE`, `SOLOCLEF_API_OIDC_REDIRECT_URL` | Required when single sign-on is on: the provider's issuer exactly as it prints it, this server's client there, the file its secret is in (mounted in an override of your own), and the callback, which is the public address followed by `/auth/oidc/callback`. |
| `SOLOCLEF_API_OIDC_DISPLAY_NAME` | Required when single sign-on is on: the provider as a sentence names it, such as "Example SSO". |
| `SOLOCLEF_WEB_SSO_BUTTON_LABEL` | Optional. The words on the button; empty is "Continue with single sign-on". |
| `SOLOCLEF_API_OIDC_APP_REDIRECTS` | The phone app's return addresses, comma-separated (`soloclef://auth`). Empty: the app may not use single sign-on. |
| `SOLOCLEF_API_AGENT_ISSUER`, `SOLOCLEF_API_AGENT_CLIENT_ID`, `SOLOCLEF_API_AGENT_CLIENT_SECRET_FILE` | Optional, as a group. Lets Claude and ChatGPT sign in at your provider to use the agent server at `/mcp` (see [Connect your agent](connect-your-agent.md)). Empty, agents use API keys only. |

A password is checked at most ten times a minute from one client on each
sign-in route. Passwords are stored as Argon2id hashes.

## Email

The API sends one kind of email: the password-reset link. An automation can
also email its result, under the same settings.

| Setting | What it does |
| --- | --- |
| `SOLOCLEF_API_SMTP_ENABLED` | `true` or `false`. With `false`, Forgot password? answers that resets are unavailable. |
| `SOLOCLEF_API_SMTP_HOST`, `SOLOCLEF_API_SMTP_PORT`, `SOLOCLEF_API_SMTP_SECURITY`, `SOLOCLEF_API_SMTP_FROM`, `SOLOCLEF_API_SMTP_TIMEOUT_SECONDS` | Required when email is on. The security mode is `starttls`, `tls` or `none` and is never guessed from the port. |
| `SOLOCLEF_API_SMTP_USERNAME`, `SOLOCLEF_API_SMTP_PASSWORD_FILE` | Given together, or both empty for a relay that takes no sign-in. |

## Connections

| Setting | What it does |
| --- | --- |
| `SOLOCLEF_API_NEXTCLOUD_BASE_URL`, `SOLOCLEF_API_NEXTCLOUD_DOWNLOAD_CONCURRENCY` | Optional, as a pair. The one Nextcloud server people connect to, and how many files are fetched from it at once. Empty, Nextcloud is not offered. |
| `SOLOCLEF_GO_GOOGLE_OAUTH_CLIENT_ID`, `SOLOCLEF_GO_GOOGLE_OAUTH_CLIENT_SECRET_FILE`, `SOLOCLEF_API_GOOGLE_DRIVE_REDIRECT_URI`, `SOLOCLEF_API_GOOGLE_PICKER_API_KEY`, `SOLOCLEF_API_GOOGLE_PICKER_APP_ID` | Optional, as a group, with `SOLOCLEF_WORKER_GOOGLE_DRIVE_ENABLED=true` and `SOLOCLEF_WORKER_GOOGLE_DRIVE_CONCURRENCY`. Google Drive. The redirect URI is the public address followed by `/api/v1/auth/google/drive/connector/callback`. |
| `SOLOCLEF_API_MICROSOFT_MAIL_CLIENT_ID`, `SOLOCLEF_API_MICROSOFT_MAIL_CLIENT_SECRET_FILE`, `SOLOCLEF_API_MICROSOFT_MAIL_TENANT`, `SOLOCLEF_API_MICROSOFT_MAIL_REDIRECT_URI` | Optional, as a group. Signing a Microsoft mailbox in for reading. Empty, mailboxes connect with app passwords only. The redirect URI is the public address followed by `/api/v1/auth/microsoft/mail/connector/callback`. |
| `SOLOCLEF_API_NCBI_TOOL`, `SOLOCLEF_API_NCBI_EMAIL` | What this server calls itself to PubMed, and an optional contact address, as NCBI asks of every client. |

Exa, Tavily and Browserbase are not server settings: a person adds an MCP
server under Settings → Connectors → MCP servers with their own key, and
its tools then appear in their chat's Tools menu and research box, on their
own provider bill. The rules are in
[Chat and citations](chat-and-citations.md#tools) and
[Notebooks and sources](notebooks-and-sources.md#mcp-servers).

## The phone app

The Soloclef app for iPhone and Android can sign in to your server: on its
sign-in screen, Change server, then this server's https address. One app
build works with every server. Before it changes anything it reads this
server's public `/api/v1/sign-in-options`, which says that it is a Soloclef
server and which ways in the app may offer: single sign-on (while
`SOLOCLEF_API_OIDC_APP_REDIRECTS` lists `soloclef://auth`), the password form,
and Create an account or Request access, as `SOLOCLEF_API_SIGNUP_ALLOWED`
says. It takes https addresses only, with a certificate phones trust.

The app registers for push notifications and keeps its notification
choices, but this version of the server sends no pushes yet.

## Reading documents

An upload is read by a parser. Plain text, CSV and web pages are read by the
indexer itself; PDF, Word (`.docx`), PowerPoint (`.pptx`) and EPUB files go to
the parser service in the stack (`soloclef_parse_service`, PyMuPDF4LLM), so the
file never leaves the server. It runs no OCR: a scan with no text layer is
refused with a reason, and needs a hosted parser.

| Setting | What it does |
| --- | --- |
| `SOLOCLEF_PARSER_DEFAULT` | The parser a document gets when neither its upload nor its connection chose one. `local` is the parser service. |
| `SOLOCLEF_PARSE_MAX_TEXT_BYTES`, `SOLOCLEF_PARSE_MAX_MARKDOWN_BYTES` | The largest plain-text file read whole, and the largest Markdown a parser may hand back. |
| `SOLOCLEF_PARSE_SERVICE_TIMEOUT_SECONDS` | How long the indexer waits for the parser service to answer one file. |
| `PARSE_SERVICE_WORKERS` | Parser processes kept warm, which is also how many files parse at once. |
| `PARSE_SERVICE_MAX_BYTES` | The largest file the parser service accepts. Keep `SOLOCLEF_API_UPLOAD_MAX_FILE_BYTES` at or under it. |
| `PARSE_SERVICE_PARSE_TIMEOUT_SECONDS`, `PARSE_SERVICE_QUEUE_WAIT_SECONDS` | The longest one parse may run before its process is killed, and the longest a request waits for a free process. |
| `SOLOCLEF_GO_PARSE_MEMORY` | The parser service's memory ceiling. A hostile PDF is the cheapest way to ask for all of a host's memory. |
| `SOLOCLEF_API_UPLOAD_MAX_FILE_BYTES`, `SOLOCLEF_API_UPLOAD_MAX_FILES` | The largest file one upload may carry, and the most files in one request. |

The code can also send documents to three hosted parsers (Datalab,
LlamaParse and the Unstructured API), each configured by its own key
(`soloclef_ingest/parserconfig`). This Compose file passes none of those keys,
so the upload dialog shows them as unavailable.

## Embeddings and search

Search compares a question's embedding with the stored embeddings of every
chunk. The chunks are kept in Qdrant, divided by account.

| Setting | What it does |
| --- | --- |
| `SOLOCLEF_EMBEDDING_PROFILE` | The indexer's embedding choice: how chunks are sized and embedded. `bge-m3` is the one profile today. |
| `SOLOCLEF_GO_EMBEDDING_MODEL`, `SOLOCLEF_GO_EMBEDDING_DIMENSION` | The proxy's name for that profile's model, and its vector length (1024 for bge-m3), which the API embeds questions with. They must agree with the profile. |
| `SOLOCLEF_GO_QDRANT_ALIAS`, `SOLOCLEF_INDEX_QDRANT_COLLECTION` | The name searches address, and the collection behind it. The indexer creates the collection and points the alias at it. |
| `SOLOCLEF_API_QDRANT_SEARCH_HNSW_EF`, `SOLOCLEF_API_QDRANT_QUANTIZED`, `SOLOCLEF_API_QDRANT_SEARCH_RESCORE`, `SOLOCLEF_API_QDRANT_SEARCH_OVERSAMPLING` | How a search uses the collection: recall bought with time, whether the collection is quantized (it is created so), and rescoring the best candidates against the full vectors. |
| `SOLOCLEF_GO_QDRANT_MEMORY` | Qdrant's memory ceiling. |
| `SOLOCLEF_GO_INDEX_CONCURRENCY` | Documents indexed at once. |
| `SOLOCLEF_INDEX_EMBEDDING_TIMEOUT`, `SOLOCLEF_INDEX_QDRANT_TIMEOUT`, `SOLOCLEF_INDEX_MAX_ATTEMPTS`, `SOLOCLEF_INDEX_RETRY_DELAY` | One embedding request's and one Qdrant request's time limit, and how often a failure that may pass is tried. |
| `SOLOCLEF_API_NOTE_INDEX_DELAY_SECONDS` | How long a note's indexing waits after the save that queued it. |

Moving the embedding model to another host of the same model needs no
re-indexing. A different model is a new collection: give
`SOLOCLEF_INDEX_QDRANT_COLLECTION` a new name, and every document is embedded
again into it. Choose the model before the first upload.

## Studio, automations and the worker

Reports, slide decks, spreadsheets, pages and videos are built in sandboxes
on the stack's own OpenSandbox server, which starts only when one of the two
switches below is on. It runs containers on the host's Docker.

| Setting | What it does |
| --- | --- |
| `SOLOCLEF_API_SANDBOX_ENABLED` | `true` or `false`: whether reports, slides, spreadsheets and pages are built. Needs the `sandbox_api_key` secret. |
| `SOLOCLEF_API_SANDBOX_CPU`, `SOLOCLEF_API_SANDBOX_MEMORY`, `SOLOCLEF_API_SANDBOX_IDLE_TTL_SECONDS`, `SOLOCLEF_API_SANDBOX_COMMAND_TIMEOUT_SECONDS`, `SOLOCLEF_API_SANDBOX_MAX_PER_ACCOUNT` | Required with the switch on: one sandbox's size, how long it outlives its last use, the longest one command may run, and how many one account may have at once. |
| `SOLOCLEF_WORKER_VIDEO_ENABLED` and the other `SOLOCLEF_WORKER_VIDEO_` settings | `true` or `false`: whether video overviews are rendered, and, when on, the sandbox's size, the render's limits and the narration's voice. Off, a video asked for is marked failed at once with a reason. |
| `SOLOCLEF_WORKER_AUTOMATIONS_ENABLED` | `true` or `false`: whether automations run. An automation may email its result only where email is on. |
| `SOLOCLEF_API_TTS_PODCAST_VOICES` and the `SOLOCLEF_WORKER_TTS_` settings | Optional voices for a new podcast's speakers, a speaking style, and how long and how often one spoken line is tried. |
| `SOLOCLEF_GO_WORKER_CONCURRENCY`, `SOLOCLEF_GO_WORKER_HEAVY_CONCURRENCY` | Jobs run at once on the default queue and on the heavy one (podcasts). |
| `SOLOCLEF_API_CHAT_MAX_STEPS`, `SOLOCLEF_API_CHAT_MAX_TURN_SECONDS` | The bounds of one chat turn. |
