# Settings reference

Every setting lives in `docker/.env.go`, made from `docker/.env.go.example`.
Every line must be present. Compose stops before starting anything and names
a missing one. Settings marked optional may be empty, and empty means off.
Nothing has a hidden default.

Most settings are read when their service starts, so `dc up -d` applies
them. The public address, the support address, the sign-up switch and the
single sign-on settings are also built into the web app's pages, so after
changing one of those run `dc up -d --build proxy`.

How to switch on each optional feature, step by step, is in
[Optional features](optional-features.md).

## The stack

| Setting | What it does |
| --- | --- |
| `SOLOCLEF_GO_SITE_PUBLISH` | The address and port the site is published on, such as `127.0.0.1:18470`. Keep it on loopback unless your reverse proxy runs on another machine. |
| `SOLOCLEF_GO_TRUSTED_PROXIES` | Which proxies may say, in `X-Forwarded-Proto` and `X-Forwarded-For`, how a request arrived and from whom: `private_ranges`, or a space-separated list of addresses and ranges. |
| `COMPOSE_PROFILES` | Leave it as `sandbox-true`. It lets the sandbox service start when one of the sandbox switches is on. |
| `SOLOCLEF_GO_MODEL_NETWORK` | The existing Docker network the LiteLLM proxy is on. The stack joins it and never creates or removes it. |
| `SOLOCLEF_GO_SECRETS_DIR` | The directory of secret files, relative to `docker/`. |
| `SOLOCLEF_GO_POSTGRES_PASSWORD_FILE`, `SOLOCLEF_GO_SESSION_SECRET_FILE`, `SOLOCLEF_GO_LITELLM_KEY_FILE`, `SOLOCLEF_GO_S3_ACCESS_KEY_FILE`, `SOLOCLEF_GO_S3_SECRET_KEY_FILE` | Where each secret appears inside the containers, `/run/secrets/<name>`. Change them only if you rename the files. |
| `SOLOCLEF_GO_POSTGRES_USER`, `SOLOCLEF_GO_POSTGRES_DB` | The database user and database name. |
| `SOLOCLEF_GO_S3_BUCKET`, `SOLOCLEF_GO_S3_REGION` | The file store's bucket, created at start if missing, and the region name it uses. |
| `SOLOCLEF_GO_QDRANT_MEMORY`, `SOLOCLEF_GO_PARSE_MEMORY` | Memory ceilings for the search index and the parser service, in Docker's units such as `2g`. |
| `SOLOCLEF_GO_CELERY_DEFAULT_QUEUE` | The name of the background job queue. |
| `SOLOCLEF_API_LOG_LEVEL` | `debug`, `info`, `warn` or `error`. |
| `SOLOCLEF_API_SHUTDOWN_GRACE_SECONDS` | How long requests in progress get to finish when the API stops. |
| `SOLOCLEF_API_DATABASE_MAX_CONNECTIONS` | How many database connections the API may open. |

## Address and name

| Setting | What it does |
| --- | --- |
| `SOLOCLEF_API_PUBLIC_URL` | The address people open, without a trailing slash. Links in emails, sign-in callbacks and public links are built from it. |
| `SOLOCLEF_API_FRONTEND_URL` | The web app's address. The same as the public address unless you serve the app from somewhere else. |
| `SOLOCLEF_API_ALLOWED_ORIGINS` | The origins a browser may call the API from, comma-separated. |
| `SOLOCLEF_API_ERROR_REPORT_URL` | The contact page error messages point to, usually the public address followed by `/contact`. |
| `SOLOCLEF_API_SUPPORT_EMAIL` | Optional. The address shown on the contact page and used by Report Issue. Empty shows none. |
| `SOLOCLEF_API_BRAND_NAME` | The product's name, as the assistant and the password-reset email use it. |
| `SOLOCLEF_API_SESSION_COOKIE_SECURE_POLICY` | `always` for a site served over https. `auto` is only for a test stack on plain http. |

## Sign-in

| Setting | What it does |
| --- | --- |
| `SOLOCLEF_API_SIGNUP_ALLOWED` | `true` or `false`. Whether people can make their own accounts, by signing up or by a first single sign-on. |
| `SOLOCLEF_API_OIDC_ENABLED` | `true` or `false`. Single sign-on through an OpenID Connect provider. |
| `SOLOCLEF_API_OIDC_ISSUER` | Required with single sign-on. The provider's issuer, exactly as its discovery document gives it. |
| `SOLOCLEF_API_OIDC_CLIENT_ID`, `SOLOCLEF_API_OIDC_CLIENT_SECRET_FILE` | Required with single sign-on. This server's client at the provider, and the file holding its secret. |
| `SOLOCLEF_API_OIDC_REDIRECT_URL` | Required with single sign-on. The public address followed by `/auth/oidc/callback`. |
| `SOLOCLEF_API_OIDC_DISPLAY_NAME` | Required with single sign-on. The provider's name as messages use it, such as "Example SSO". |
| `SOLOCLEF_WEB_SSO_BUTTON_LABEL` | Optional. The words on the sign-in button. Empty is "Continue with single sign-on". |
| `SOLOCLEF_API_OIDC_APP_REDIRECTS` | Optional. Where the phone app may be sent back to after single sign-on: `soloclef://auth`. Empty keeps single sign-on to the website. |
| `SOLOCLEF_API_AGENT_ISSUER`, `SOLOCLEF_API_AGENT_CLIENT_ID`, `SOLOCLEF_API_AGENT_CLIENT_SECRET_FILE` | Optional, as a group. Let Claude and ChatGPT sign in at your provider to use the MCP server. The secret file may be empty for a client without a secret. Empty, agents use API keys only. |

A few things are fixed rather than set. A browser stays signed in for 14 days
without use, and at most 30 days. A phone stays signed in until it goes 180
days without use. Passwords can be tried ten times a minute from one place,
and any one client can make 1,024 requests a minute to each part of the API.

## Email

| Setting | What it does |
| --- | --- |
| `SOLOCLEF_API_SMTP_ENABLED` | `true` or `false`. Whether the server sends password-reset links and automation results. |
| `SOLOCLEF_API_SMTP_HOST`, `SOLOCLEF_API_SMTP_PORT` | Required with email. The mail server. |
| `SOLOCLEF_API_SMTP_SECURITY` | Required with email. `starttls`, `tls` or `none`. |
| `SOLOCLEF_API_SMTP_USERNAME`, `SOLOCLEF_API_SMTP_PASSWORD_FILE` | Both set, or both empty for a relay that needs no sign-in. |
| `SOLOCLEF_API_SMTP_FROM` | Required with email. The sender address. |
| `SOLOCLEF_API_SMTP_TIMEOUT_SECONDS` | Required with email. How long to wait for the mail server. |

## Models

| Setting | What it does |
| --- | --- |
| `SOLOCLEF_GO_LITELLM_URL` | LiteLLM's OpenAI-compatible address, ending in `/v1`, as seen from the model network. Its key is the `litellm_key` secret. |
| `SOLOCLEF_API_CHAT_MODEL` | LiteLLM's name for the model that answers in chat and writes outputs, for anyone who has not chosen another. |
| `SOLOCLEF_WORKER_QUICK_MODEL` | LiteLLM's name for the model used for short tasks, such as naming a notebook. |
| `SOLOCLEF_API_CHAT_VISION_MODEL` | Optional. The model that reads pictures: photos in a question and pictures in uploads. Empty turns this off. |
| `SOLOCLEF_GO_TTS_SERVICE` | The speech model, written `<voices>/<model>`. The first part is `openai`, `azure` or `vertex_ai` and decides which voices are offered. A model whose name contains `kokoro` always gets Kokoro's voices. |
| `SOLOCLEF_API_TTS_PODCAST_VOICES` | Optional. The voices for an audio overview's speakers, in order, such as `af_heart,am_adam`. Empty uses the voice list's own order. |
| `SOLOCLEF_WORKER_TTS_INSTRUCTIONS` | Optional. A speaking style, for speech models that take one. |
| `SOLOCLEF_WORKER_TTS_ATTEMPT_TIMEOUT`, `SOLOCLEF_WORKER_TTS_MAX_ATTEMPTS`, `SOLOCLEF_WORKER_TTS_CONCURRENCY` | How long one spoken line may take, how many tries it gets, and how many lines one audio overview or video overview voices at once. |
| `SOLOCLEF_WORKER_LLM_CALL_TIMEOUT` | How long one background model call may take, retries included, such as `5m`. |

## Search

| Setting | What it does |
| --- | --- |
| `SOLOCLEF_EMBEDDING_PROFILE` | How documents are split and embedded. `bge-m3` is the one profile. |
| `SOLOCLEF_GO_EMBEDDING_MODEL`, `SOLOCLEF_GO_EMBEDDING_DIMENSION` | LiteLLM's name for the embedding model, and its vector length, 1024 for BGE-M3. They must match the profile. |
| `SOLOCLEF_GO_QDRANT_ALIAS`, `SOLOCLEF_INDEX_QDRANT_COLLECTION` | The name searches use, and the collection behind it. The indexer creates the collection. |
| `SOLOCLEF_API_QDRANT_SEARCH_HNSW_EF` | How hard a search looks. Higher finds more and takes longer. |
| `SOLOCLEF_API_QDRANT_QUANTIZED`, `SOLOCLEF_API_QDRANT_SEARCH_RESCORE`, `SOLOCLEF_API_QDRANT_SEARCH_OVERSAMPLING` | Keep them `true`, `true` and `2.0`. The collection is created compressed, and the best candidates are scored again against the full vectors. |
| `SOLOCLEF_GO_INDEX_CONCURRENCY` | How many documents are indexed at once. |
| `SOLOCLEF_INDEX_EMBEDDING_TIMEOUT`, `SOLOCLEF_INDEX_QDRANT_TIMEOUT` | Time limits for one embedding request and one index write. |
| `SOLOCLEF_INDEX_MAX_ATTEMPTS`, `SOLOCLEF_INDEX_RETRY_DELAY` | How many times a failed indexing step is tried, and the wait between tries. |
| `SOLOCLEF_WORKER_QDRANT_TIMEOUT` | The time limit for removing a deleted document from the index. |
| `SOLOCLEF_API_NOTE_INDEX_DELAY_SECONDS` | How long after an edit a note is indexed. Shorter makes edits searchable sooner; longer makes fewer embedding calls while someone types. |

## Uploads and parsing

| Setting | What it does |
| --- | --- |
| `SOLOCLEF_API_UPLOAD_MAX_FILE_BYTES` | The largest file, in bytes, for uploads, folder uploads, Nextcloud and Drive imports and PDF links. Keep it at or under `PARSE_SERVICE_MAX_BYTES`. |
| `SOLOCLEF_API_UPLOAD_MAX_FILES` | The most files in one upload. |
| `SOLOCLEF_PARSER_DEFAULT` | The parser used when nobody chose one. `local` reads files on the server. |
| `SOLOCLEF_PARSE_MAX_TEXT_BYTES` | The largest plain-text, CSV or HTML file read whole. |
| `SOLOCLEF_PARSE_MAX_MARKDOWN_BYTES` | The most text any parser may return for one file. |
| `SOLOCLEF_PARSE_SERVICE_TIMEOUT_SECONDS` | How long the indexer waits for the parser service on one file. |
| `PARSE_SERVICE_WORKERS` | How many files the parser service reads at once. |
| `PARSE_SERVICE_MAX_BYTES` | The largest file the parser service accepts. |
| `PARSE_SERVICE_PARSE_TIMEOUT_SECONDS`, `PARSE_SERVICE_QUEUE_WAIT_SECONDS` | How long one file may take to read, and how long a file may wait for a free reader. |

The hosted parsers, Datalab, LlamaParse and Unstructured, are set with their
own keys in a Compose override. See
[Optional features](optional-features.md#hosted-parsers).

## Chat and research

| Setting | What it does |
| --- | --- |
| `SOLOCLEF_API_CHAT_MAX_STEPS` | The most steps one answer may take. A model call and the tools it asks for are each a step. |
| `SOLOCLEF_API_CHAT_MAX_TURN_SECONDS` | The longest one answer may run. |
| `SOLOCLEF_API_NCBI_TOOL` | The name this server gives PubMed, as NCBI asks of every client. |
| `SOLOCLEF_API_NCBI_EMAIL` | Optional. A contact address for NCBI. |

## Connections

| Setting | What it does |
| --- | --- |
| `SOLOCLEF_API_NEXTCLOUD_BASE_URL`, `SOLOCLEF_API_NEXTCLOUD_DOWNLOAD_CONCURRENCY` | Optional, as a pair. The one Nextcloud server people may connect to, and how many files are fetched from it at once. |
| `SOLOCLEF_GO_GOOGLE_OAUTH_CLIENT_ID`, `SOLOCLEF_GO_GOOGLE_OAUTH_CLIENT_SECRET_FILE` | Optional, with the rest of the Google group. The Google OAuth client and the file holding its secret. |
| `SOLOCLEF_API_GOOGLE_DRIVE_REDIRECT_URI` | The public address followed by `/api/v1/auth/google/drive/connector/callback`. |
| `SOLOCLEF_API_GOOGLE_PICKER_API_KEY`, `SOLOCLEF_API_GOOGLE_PICKER_APP_ID` | The browser key for Google's file picker, and the project number. The project number may be empty when the client ID starts with it. |
| `SOLOCLEF_WORKER_GOOGLE_DRIVE_ENABLED`, `SOLOCLEF_WORKER_GOOGLE_DRIVE_CONCURRENCY` | `true` when Google Drive is set up, and how many files one sync reads at once. |
| `SOLOCLEF_API_MICROSOFT_MAIL_CLIENT_ID`, `SOLOCLEF_API_MICROSOFT_MAIL_CLIENT_SECRET_FILE`, `SOLOCLEF_API_MICROSOFT_MAIL_TENANT`, `SOLOCLEF_API_MICROSOFT_MAIL_REDIRECT_URI` | Optional, as a group. Microsoft sign-in for Outlook.com and Microsoft 365 mailboxes. |
| `SOLOCLEF_API_JMAP_MAIL_CLIENT_ID`, `SOLOCLEF_API_JMAP_MAIL_REDIRECT_URI` | Optional, as a pair. Stalwart's own sign-in for Stalwart mailboxes. |
| `SOLOCLEF_API_MCP_OAUTH_REDIRECT_URI` | Optional. The public address followed by `/api/v1/connectors/mcp/oauth/callback`, for MCP servers that sign in with OAuth. |

Exa, Tavily and MCP servers have no server settings. Each person adds their
own keys under Settings, Connectors.

## Studio and background work

| Setting | What it does |
| --- | --- |
| `SOLOCLEF_API_SANDBOX_ENABLED` | `true` or `false`. Whether reports, spreadsheets and interactive pages can be built, and slide decks previewed. Needs the `sandbox_api_key` secret. |
| `SOLOCLEF_API_SANDBOX_CPU`, `SOLOCLEF_API_SANDBOX_MEMORY` | Required with sandboxes. One sandbox's CPUs and memory, such as `2` and `4Gi`. Its files live in its memory. |
| `SOLOCLEF_API_SANDBOX_IDLE_TTL_SECONDS` | How long an unused sandbox is kept, at least 60. |
| `SOLOCLEF_API_SANDBOX_COMMAND_TIMEOUT_SECONDS` | The longest one command in a sandbox may run. |
| `SOLOCLEF_API_SANDBOX_MAX_PER_ACCOUNT` | How many sandboxes one person may have at once. A Studio output or an automation that finds them all in use waits up to ten minutes for one, in the order they asked; a chat is told at once. A Studio output gives its sandbox back when it finishes. |
| `SOLOCLEF_WORKER_VIDEO_ENABLED` | `true` or `false`. Whether video overviews are made. Off, a video someone asks for fails at once with a reason. |
| `SOLOCLEF_WORKER_VIDEO_RESVG` | Required with video. The resvg program that draws the frames; the worker image has it at `/usr/local/bin/resvg`. |
| `SOLOCLEF_WORKER_VIDEO_FRAME_WORKERS` | Required with video. How many frames one video draws at once, the cores it may use. |
| `SOLOCLEF_WORKER_VIDEO_RENDER_TIMEOUT` | Required with video. The longest drawing and joining one video may take, such as `15m`. |
| `SOLOCLEF_WORKER_VIDEO_MAX_RENDERS` | How many videos this worker makes at once. |
| `SOLOCLEF_WORKER_VIDEO_NARRATION_VOICE`, `SOLOCLEF_WORKER_VIDEO_NARRATION_INSTRUCTIONS` | The narrator's voice, such as `kokoro:af_heart`, and an optional speaking style. |
| `SOLOCLEF_WORKER_AUTOMATIONS_ENABLED` | `true` or `false`. Whether automations run. |
| `SOLOCLEF_GO_WORKER_CONCURRENCY`, `SOLOCLEF_GO_WORKER_HEAVY_CONCURRENCY` | How many background jobs run at once, and how many heavy ones such as audio overviews. |

## Phone pushes

| Setting | What it does |
| --- | --- |
| `SOLOCLEF_WORKER_PUSH_ENABLED` | `true` or `false`. Whether notifications are also pushed to phones. |
| `EXPO_ACCESS_TOKEN` | Optional. Only if the app's Expo project has enhanced push security on. |
| `SOLOCLEF_GO_APNS_KEY_SOURCE` | Apple's `.p8` push key on the host, or `/dev/null` when there is none. |
| `SOLOCLEF_WORKER_APNS_KEY_FILE` | `/run/secrets/apns_key` when there is a key, or empty. |
| `SOLOCLEF_WORKER_APNS_KEY_ID`, `SOLOCLEF_WORKER_APNS_TEAM_ID`, `SOLOCLEF_WORKER_APNS_TOPIC` | The key's ID, the Apple developer team's ID and the app's bundle ID. |
| `SOLOCLEF_WORKER_APNS_ENVIRONMENT` | `production` for App Store and TestFlight builds, `sandbox` for development builds. |

The Apple settings are all set or all empty.

## Site settings in the database

Two lists live in the database, in the `site_settings` table, rather than in
`docker/.env.go`. Each is a JSON list of host names or addresses.

| Key | What it allows |
| --- | --- |
| `model_connection_allowed_hosts` | Hosts inside your network that people's own model connections may reach, such as an Ollama machine. With no row, none. |
| `mcp_allowed_hosts` | Hosts that MCP servers added by an administrator for the whole site may use. With no row, only Exa's, Tavily's and Browserbase's MCP hosts. |

[Optional features](optional-features.md#models-on-your-own-network) shows how
to set one.
