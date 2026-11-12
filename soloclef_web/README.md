# Soloclef web

The Next.js web app (App Router, `pnpm`). It talks to the Go API and to
zero-cache for live updates; in a deployment both sit behind the same Caddy
(`docker/docker-compose.go.yml`).

## You need

- Node.js 20+ and [pnpm](https://pnpm.io/installation) (not npm)
- A running Go stack to talk to: `docker/docker-compose.go.yml` (the root
  README's quick start). The web app has no API of its own.

## Run it

```bash
cp .env.example .env
```

In `.env`, point at the stack. Its Caddy serves the API and zero-cache on
the one address `SOLOCLEF_GO_SITE_PUBLISH` publishes (`127.0.0.1:18470` in
`docker/.env.go.example`):

```bash
NEXT_PUBLIC_FASTAPI_BACKEND_URL=http://127.0.0.1:18470
SOLOCLEF_BACKEND_INTERNAL_URL=http://127.0.0.1:18470
NEXT_PUBLIC_ZERO_CACHE_URL=http://127.0.0.1:18470/zero
DEPLOYMENT_MODE=self-hosted
```

The API only answers a browser from the origins it is told about, so add
`http://localhost:3000` to `SOLOCLEF_API_ALLOWED_ORIGINS` in `docker/.env.go`
and restart the api service.

Then:

```bash
pnpm install
pnpm dev
```

App: http://localhost:3000. To try a change the way it is deployed, build it
into the stack instead (`up -d --build frontend` with the quick start's
compose command) and open the stack's own address.

## Tests

```bash
pnpm test:unit
pnpm format
```

Playwright runs against a throwaway copy of the Go stack. See
[tests/README.md](tests/README.md).
