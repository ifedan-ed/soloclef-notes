# Sign-in

## Email and password

The default. Nothing else is needed: `AUTH_TYPE=LOCAL`, with `OIDC_ENABLED`
unset. Passwords are stored only as Argon2id hashes. Forgot password?
sends a reset link when [email](self-hosting.md#email-optional) is set up;
without email, the administrator sets a password with:

```bash
docker compose -f docker-compose.yml exec -it backend \
  python scripts/create_account.py someone@example.com --set-password
```

## Single sign-on (optional)

Any OpenID Connect provider works beside the password form: Pocket ID,
Authentik, Keycloak and others. Pocket ID is the quickest to run yourself:
one container, and people sign in with a passkey.

1. In the provider, create a client (application) for Soloclef with the
   redirect URL `https://notes.example.org/auth/oidc/callback`. The phone app
   comes back through the same server, so nothing else is needed for it.
2. In `docker/.env`:

    ```dotenv
    OIDC_ENABLED=TRUE
    OIDC_DISCOVERY_URL=https://id.example.org/.well-known/openid-configuration
    OIDC_CLIENT_ID=<client id>
    OIDC_CLIENT_SECRET=<client secret>
    OIDC_BUTTON_LABEL=Continue with Example SSO
    ```

3. Rebuild the website and restart:
   `docker compose -f docker-compose.yml build frontend && docker compose -f docker-compose.yml up -d`.

The website and the phone app then show the button above the password form.

Who gets an account on their first single sign-on:

| Setting | What it does |
| --- | --- |
| `OIDC_AUTOCREATE_DOMAINS` | Addresses at these exact domains get an account, for example `example.org`. |
| `OIDC_ALLOW_SIGNUP=TRUE` | Any verified address gets an account. |
| neither | Only people who already have an account can sign in. |

## Who can join

Sign-ups are closed by default: the website and the phone app offer
Request access, which opens your server's contact page (set
`SUPPORT_EMAIL` so it has an address). You add people yourself with
`create_account.py`, or through single sign-on as above.

To let people create their own accounts, set `NEXT_PUBLIC_SIGNUPS_OPEN=true`
and rebuild the website. To close sign-ups completely, including requests sent
straight to the API, refuse them in the proxy as well:

```text
@signup {
	method POST
	path /auth/register /api/v1/auth/register
}
respond @signup "Registration is closed." 403
```

There are no invite codes or trial codes.
