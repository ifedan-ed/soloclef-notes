# Sign-in

The commands use the `dc` function from [Run your own server](self-hosting.md#first-start).

## Email and password

Always on. Passwords are stored only as Argon2id hashes, and a password is
checked at most ten times a minute from one client. Forgot password? sends a
reset link when [email](self-hosting.md#email-optional) is set up; without
email, the administrator sets a password with:

```bash
printf '%s\n' "a new password" | dc exec -T api soloclef-api set-password someone@example.com
```

## Single sign-on (optional)

Any OpenID Connect provider works beside the password form: Pocket ID,
Authentik, Keycloak and others. Pocket ID is the quickest to run yourself:
one container, and people sign in with a passkey.

1. In the provider, create a client (application) for Soloclef with the
   redirect URL `https://notes.example.org/auth/oidc/callback`.
2. Put the client secret in a file named `oidc_client_secret` in your secrets
   directory, and give it to the `api` service in a Compose override of your
   own (the comment above `SOLOCLEF_API_OIDC_CLIENT_SECRET_FILE` in
   `docker/docker-compose.go.yml` shows the lines).
3. In `docker/.env.go`:

    ```dotenv
    SOLOCLEF_API_OIDC_ENABLED=true
    SOLOCLEF_API_OIDC_ISSUER=https://id.example.org
    SOLOCLEF_API_OIDC_CLIENT_ID=<client id>
    SOLOCLEF_API_OIDC_CLIENT_SECRET_FILE=/run/secrets/oidc_client_secret
    SOLOCLEF_API_OIDC_REDIRECT_URL=https://notes.example.org/auth/oidc/callback
    SOLOCLEF_API_OIDC_DISPLAY_NAME=Example SSO
    SOLOCLEF_WEB_SSO_BUTTON_LABEL=Continue with Example SSO
    SOLOCLEF_API_OIDC_APP_REDIRECTS=soloclef://auth
    ```

    The issuer is the provider's exactly as its discovery document prints
    it. `SOLOCLEF_API_OIDC_APP_REDIRECTS` lets the phone app use the button;
    leave it empty to keep single sign-on to the website.

4. Rebuild the website and restart, with your override file added after the
   main one: `dc -f <your override> up -d --build`.

The website and the phone app then show the button above the password form.
Single sign-on signs a person in to the account with the same, confirmed,
email address. Whether a first sign-on with an address no account has makes
an account is `SOLOCLEF_API_SIGNUP_ALLOWED`, below.

## Who can join

`SOLOCLEF_API_SIGNUP_ALLOWED` is the one switch, and it is exactly `true` or
`false`:

| Value | What it does |
| --- | --- |
| `false` | The website and the phone app offer Request access, which opens your server's contact page (set `SOLOCLEF_API_SUPPORT_EMAIL` so it has an address). The API refuses sign-up requests, and single sign-on makes no new accounts. You add people with `set-password`. |
| `true` | The sign-up form is offered, and a first single sign-on with a new address makes an account. |

The website reads it when it is built, so rebuild the website after changing
it (`dc up -d --build`). Nothing needs closing in the proxy: with `false` the
API itself refuses.

There are no invite codes or trial codes.

## Agents

Claude and ChatGPT can sign in at the same provider to use the MCP server at
`/mcp`, with a client of their own there: `SOLOCLEF_API_AGENT_ISSUER`,
`SOLOCLEF_API_AGENT_CLIENT_ID` and `SOLOCLEF_API_AGENT_CLIENT_SECRET_FILE`.
Left empty, agents use API keys, which each person makes in their settings.
See [Connect your agent](guide/connect-your-agent.md).
