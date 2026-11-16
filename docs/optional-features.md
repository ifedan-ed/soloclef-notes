# Optional features

Everything on this page is off in the example settings. Each feature is
switched on in `docker/.env.go`, and some also need a secret file. The
commands use the `dc` function from [Install](self-hosting.md#3-start-it).

## Adding a secret file

Optional secrets are not part of the main Compose file, because a file that
is listed there must exist on every server. You add them in a Compose
override file of your own, for example `docker/compose.local.yml`:

```yaml
services:
  api:
    secrets: [oidc_client_secret, smtp_password]
secrets:
  oidc_client_secret:
    file: ./secrets-go/oidc_client_secret
  smtp_password:
    file: ./secrets-go/smtp_password
```

Paths are relative to `docker/`. Each file goes in your secrets directory with
mode 0444, like the others. Inside the container the file appears as
`/run/secrets/<name>`, and that is the path you give the setting.

Add the override to the `dc` function, after the main file, and use it for
every command from then on:

```bash
dc() { docker compose -p soloclefgo --env-file docker/.env.go -f docker/docker-compose.go.yml -f docker/compose.local.yml "$@"; }
```

Each section below says which service needs which file.

## Single sign-on

People can sign in through any OpenID Connect provider, such as Pocket ID,
Authentik or Keycloak. The password form stays beside it.

1. In the provider, create a client for Soloclef with the redirect URL
   `https://notes.example.org/auth/oidc/callback`.
2. Save its client secret as `oidc_client_secret` and give it to the `api`
   service, as shown above.
3. Set these in `docker/.env.go`:

    ```dotenv
    SOLOCLEF_API_OIDC_ENABLED=true
    SOLOCLEF_API_OIDC_ISSUER=https://id.example.org
    SOLOCLEF_API_OIDC_CLIENT_ID=<client id>
    SOLOCLEF_API_OIDC_CLIENT_SECRET_FILE=/run/secrets/oidc_client_secret
    SOLOCLEF_API_OIDC_REDIRECT_URL=https://notes.example.org/auth/oidc/callback
    SOLOCLEF_API_OIDC_PROVIDER_NAME=example-sso
    SOLOCLEF_API_OIDC_DISPLAY_NAME=Example SSO
    SOLOCLEF_WEB_SSO_BUTTON_LABEL=Continue with Example SSO
    SOLOCLEF_API_OIDC_APP_REDIRECTS=soloclef://auth
    ```

    The issuer must match the provider's discovery document exactly. The
    provider name is what each person's sign-in link is stored under: pick
    one and never change it, or everyone's link is lost. The display name
    is how messages refer to the provider. The button label
    may be left empty for "Continue with single sign-on". Leave the app
    redirects empty to keep single sign-on to the website only.

4. Rebuild and restart: `dc up -d --build`.

Single sign-on signs a person in to the account with the same email address,
if that address is confirmed. Whether it makes accounts for new addresses
follows `SOLOCLEF_API_SIGNUP_ALLOWED` (see
[Who can join](self-hosting.md#who-can-join)). Who may use the client at all
is decided in your provider.

## Email

The server sends password-reset links and, when people ask for it,
automation results. Without email, people cannot reset a forgotten password
themselves: Forgot password? says that password reset by email isn't
available on this server, and you set it with `set-password`. People who
know their password can still change it under Settings, Security.

```dotenv
SOLOCLEF_API_SMTP_ENABLED=true
SOLOCLEF_API_SMTP_HOST=smtp.example.org
SOLOCLEF_API_SMTP_PORT=587
SOLOCLEF_API_SMTP_SECURITY=starttls
SOLOCLEF_API_SMTP_USERNAME=notes@example.org
SOLOCLEF_API_SMTP_PASSWORD_FILE=/run/secrets/smtp_password
SOLOCLEF_API_SMTP_FROM=Soloclef <notes@example.org>
SOLOCLEF_API_SMTP_TIMEOUT_SECONDS=20
```

The security mode is `starttls`, `tls` or `none`. It is never guessed from
the port. Give the password to the `api` service as `smtp_password`. For a
relay that needs no sign-in, leave the username and the password file both
empty.

This is the server's own mail. The mailboxes people connect for the
assistant to read are separate, and are set up by each person under
Settings, Connectors.

## Nextcloud

People can pick files from one Nextcloud server. Name it and say how many
files are fetched at once:

```dotenv
SOLOCLEF_API_NEXTCLOUD_BASE_URL=https://cloud.example.org
SOLOCLEF_API_NEXTCLOUD_DOWNLOAD_CONCURRENCY=4
```

Nothing is needed in Nextcloud itself. Each person connects with Nextcloud's
own sign-in, which gives Soloclef an app password for their account.

## Google Drive

People can pick files from their Drive with Google's own picker. Soloclef
only ever sees the files they pick.

In the Google Cloud console:

1. Create a project, or use one you have.
2. Enable the Google Drive API and the Google Picker API.
3. Set up the OAuth consent screen. Add the scope `.../auth/drive.file`,
   and nothing broader.
4. Create an OAuth client of type Web application, with the redirect URI
   `https://notes.example.org/api/v1/auth/google/drive/connector/callback`.
5. Create an API key, restricted to the Google Picker API and to your site's
   address.

Save the client secret as `google_oauth_client_secret`. Both the `api` and
the `worker` service need it, because the worker keeps picked files in step
with Drive:

```yaml
services:
  api:
    secrets: [google_oauth_client_secret]
  worker:
    secrets: [google_oauth_client_secret]
secrets:
  google_oauth_client_secret:
    file: ./secrets-go/google_oauth_client_secret
```

Then set:

```dotenv
SOLOCLEF_GO_GOOGLE_OAUTH_CLIENT_ID=<client id>.apps.googleusercontent.com
SOLOCLEF_GO_GOOGLE_OAUTH_CLIENT_SECRET_FILE=/run/secrets/google_oauth_client_secret
SOLOCLEF_API_GOOGLE_DRIVE_REDIRECT_URI=https://notes.example.org/api/v1/auth/google/drive/connector/callback
SOLOCLEF_API_GOOGLE_PICKER_API_KEY=<the API key>
SOLOCLEF_API_GOOGLE_PICKER_APP_ID=
SOLOCLEF_WORKER_GOOGLE_DRIVE_ENABLED=true
SOLOCLEF_WORKER_GOOGLE_DRIVE_CONCURRENCY=4
```

The app ID is the project number. Leave it empty when it is the number the
client ID starts with.

## Microsoft mailboxes

Microsoft no longer lets IMAP sign in with a password, so Outlook.com and
Microsoft 365 mailboxes connect through Microsoft's sign-in. Without this,
people can still connect any mailbox that takes an app password.

In the Microsoft Entra admin center, register an application:

1. Choose who may sign in: personal accounts, work and school accounts, or
   both.
2. Add a Web redirect URI:
   `https://notes.example.org/api/v1/auth/microsoft/mail/connector/callback`.
3. Under API permissions, add the delegated permissions `openid`, `email`,
   `profile`, `offline_access` and `IMAP.AccessAsUser.All`.
4. Create a client secret.

Save the secret as `microsoft_mail_client_secret` and give it to both `api`
and `worker`, as for Google Drive above. The worker needs it because
automations read mail too. Then set:

```dotenv
SOLOCLEF_API_MICROSOFT_MAIL_CLIENT_ID=<application id>
SOLOCLEF_API_MICROSOFT_MAIL_CLIENT_SECRET_FILE=/run/secrets/microsoft_mail_client_secret
SOLOCLEF_API_MICROSOFT_MAIL_TENANT=common
SOLOCLEF_API_MICROSOFT_MAIL_REDIRECT_URI=https://notes.example.org/api/v1/auth/microsoft/mail/connector/callback
```

The tenant is `common` for personal and work accounts, `organizations` for
work and school accounts only, `consumers` for personal accounts only, or
your own tenant's id or domain.

## Stalwart sign-in

People can always connect a Stalwart mailbox with an app password. To let
them sign in with Stalwart's own page instead, set:

```dotenv
SOLOCLEF_API_JMAP_MAIL_CLIENT_ID=https://notes.example.org/api/v1/connectors/jmap/oauth-client.json
SOLOCLEF_API_JMAP_MAIL_REDIRECT_URI=https://notes.example.org/api/v1/auth/jmap/mail/connector/callback
```

There is no secret. Soloclef publishes a client document at the first
address, and the Stalwart server reads it. The administrator of each Stalwart
server must allow client metadata documents, under Settings,
Authentication, OIDC Provider.

## MCP servers that sign in with OAuth

People can add MCP servers that need no sign-in, an API key or a bearer token
without any server setting. For servers that sign in with OAuth, set the
callback:

```dotenv
SOLOCLEF_API_MCP_OAUTH_REDIRECT_URI=https://notes.example.org/api/v1/connectors/mcp/oauth/callback
```

## Phone pushes

The worker can push a notification to people's phones when a Studio output
is ready or fails, a source fails, an automation run ends or a mail draft is
waiting. Every notification also stays in the inbox, pushed or not.

```dotenv
SOLOCLEF_WORKER_PUSH_ENABLED=true
```

Android phones are pushed through Expo's push service, which needs nothing
more. Set `EXPO_ACCESS_TOKEN` only if the app's Expo project has enhanced
push security turned on.

iPhones are pushed straight to Apple. Apple only accepts a push signed with
a key from the developer team that publishes the app. For the App Store app
that is the publisher's key, so a server you run cannot push to it. If you
build and publish your own copy of the app, set your team's key:

```dotenv
SOLOCLEF_GO_APNS_KEY_SOURCE=/srv/keys/AuthKey_ABC123.p8
SOLOCLEF_WORKER_APNS_KEY_FILE=/run/secrets/apns_key
SOLOCLEF_WORKER_APNS_KEY_ID=ABC123
SOLOCLEF_WORKER_APNS_TEAM_ID=<team id>
SOLOCLEF_WORKER_APNS_TOPIC=<your app's bundle id>
SOLOCLEF_WORKER_APNS_ENVIRONMENT=production
```

Set all of these or none. With none, leave the key source as `/dev/null`;
iPhone users still see everything in the app's inbox. The environment is
`production` for App Store and TestFlight builds and `sandbox` for
development builds.

## Studio sandboxes

Reports, spreadsheets and interactive pages are built in sandboxes, and a
slide deck uses one to draw its preview. Without sandboxes, asking for one of
those says it isn't available on this server. Audio and video overviews, mind
maps, flashcards, quizzes and infographics do not need them.

Each sandbox is a container with no network, started from an image the stack
builds. The `sandboxd` service starts them, and for that it holds the host's
Docker socket. Anything that can use that socket can act as root on the
host, so `sandboxd` is kept on a network that only the API and the worker can
reach, and every request to it needs a key. Switch sandboxes on only on a
host where that is acceptable.

Make the key:

```bash
openssl rand -hex 32 > docker/secrets-go/sandbox_api_key
chmod 0444 docker/secrets-go/sandbox_api_key
```

For reports, decks, tables and pages:

```dotenv
SOLOCLEF_API_SANDBOX_ENABLED=true
SOLOCLEF_API_SANDBOX_CPU=2
SOLOCLEF_API_SANDBOX_MEMORY=4Gi
SOLOCLEF_API_SANDBOX_IDLE_TTL_SECONDS=600
SOLOCLEF_API_SANDBOX_COMMAND_TIMEOUT_SECONDS=300
SOLOCLEF_API_SANDBOX_MAX_PER_ACCOUNT=3
```

Then `dc up -d --build`. Leave `COMPOSE_PROFILES=sandbox-true` as it is: it
lets the sandbox service start when the switch is on. The first build of the
sandbox image is large, about 12 GB.

`SOLOCLEF_API_SANDBOX_MAX_PER_ACCOUNT` is how many sandboxes one person may
hold at once. A Studio output or an automation that finds all of them in use
waits for one, up to ten minutes, and shows "Waiting for a sandbox" meanwhile;
a chat is told straight away. At most 32 requests of one person wait at once;
past that, a new one is told straight away, as a chat is. Studio outputs give their sandbox back as soon
as they finish, and an unused one is removed after
`SOLOCLEF_API_SANDBOX_IDLE_TTL_SECONDS`.

## Video overviews

A video overview is a narrated slide deck. The worker draws each slide and
its animations itself and joins them with the narration, so it needs no
sandbox, only your speech model.

```dotenv
SOLOCLEF_WORKER_VIDEO_ENABLED=true
SOLOCLEF_WORKER_VIDEO_RESVG=/usr/local/bin/resvg
SOLOCLEF_WORKER_VIDEO_FRAME_WORKERS=4
SOLOCLEF_WORKER_VIDEO_RENDER_TIMEOUT=15m
SOLOCLEF_WORKER_VIDEO_MAX_RENDERS=1
SOLOCLEF_WORKER_VIDEO_NARRATION_VOICE=kokoro:af_heart
SOLOCLEF_WORKER_VIDEO_NARRATION_INSTRUCTIONS=
```

The narration voice must be one of your speech model's voices.

Each slide is narrated part by part: one passage as the slide appears, then
one for each bullet, column, row, event, figure or stage as it comes in.
Every passage is a speech request of its own, so a part comes in as its
passage starts. A 20-slide video with four passages a slide makes 80 speech
requests; `SOLOCLEF_WORKER_TTS_CONCURRENCY` sets how many run at once, and
`SOLOCLEF_WORKER_TTS_MAX_ATTEMPTS` how many times a failed one is tried.

## Hosted parsers

Uploads are read on the server by default. You can also offer Datalab,
LlamaParse or Unstructured, which read scans, tables and figures more
closely. A file someone sends to one of them leaves your server, and the
service bills you for it.

Each parser is offered once its key is set. Put the keys and their settings
in `docker/.env.go`:

```dotenv
DATALAB_API_KEY=<key>
DATALAB_MODE=balanced

LLAMA_CLOUD_API_KEY=<key>
LLAMA_CLOUD_BASE_URL=<LlamaParse's API address for your key's region>
LLAMA_PARSE_TIER=cost_effective
LLAMA_PARSE_VERSION=latest

UNSTRUCTURED_API_KEY=<key>
UNSTRUCTURED_API_URL=<your account's API endpoint>
UNSTRUCTURED_STRATEGY=hi_res
```

Then pass them to the `api`, `indexer` and `worker` services in your override
file. These three must always get the same parser settings:

```yaml
x-hosted-parsers: &hosted-parsers
  DATALAB_API_KEY: ${DATALAB_API_KEY}
  DATALAB_MODE: ${DATALAB_MODE}

services:
  api:
    environment: *hosted-parsers
  indexer:
    environment: *hosted-parsers
  worker:
    environment: *hosted-parsers
```

Add only the parsers you use. Once a key is set, its other settings are
required:

| Parser | Settings |
| --- | --- |
| Datalab | `DATALAB_MODE`: `lite`, `fast`, `balanced` or `accurate`. |
| LlamaParse | `LLAMA_CLOUD_BASE_URL`: LlamaParse's US or EU API address, whichever your key belongs to. `LLAMA_PARSE_TIER`: `fast`, `cost_effective`, `agentic` or `agentic_plus`. `LLAMA_PARSE_VERSION`: `latest` or a dated version. |
| Unstructured | `UNSTRUCTURED_API_URL`: your account's endpoint. `UNSTRUCTURED_STRATEGY`: `auto`, `fast`, `hi_res`, `ocr_only` or `vlm`. |

`SOLOCLEF_PARSER_DEFAULT` stays `local` unless you want uploads to use a
hosted parser when nobody chooses one.

## Models on your own network

People's own model connections may not reach addresses inside your network
unless you allow them. To let them use, say, an Ollama machine at
`192.168.1.20`, add it to the site's list of allowed hosts:

```bash
dc exec -T postgres sh -c 'psql -U "$POSTGRES_USER" "$POSTGRES_DB"' <<'SQL'
INSERT INTO site_settings (key, value)
VALUES ('model_connection_allowed_hosts', '["192.168.1.20"]')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now();
SQL
```

The value is a JSON list of host names or addresses.
