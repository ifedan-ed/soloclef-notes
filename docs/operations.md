# Updating and backups

The commands use the `dc` function from [Run your own server](self-hosting.md#first-start).

## Updating

```bash
cd soloclef
git pull
dc up -d --build
```

The `migrate` service applies any new database migration before the API, the
indexer and the worker start. Each of those checks at start that the database
is at the schema version its build expects and refuses to start, saying so,
if it is not, so a half-applied upgrade never serves requests.

Compare `docker/.env.go` with `docker/.env.go.example` after pulling: a new
required setting stops Compose before anything starts, naming it.

## What to back up

| What | Where |
| --- | --- |
| Accounts, notebooks, chats, notes and the text of every chunk | the `soloclefgo_postgres_data` volume; use `pg_dump` for a consistent copy |
| Uploaded files, audio, videos and other Studio files | the `soloclefgo_minio_data` volume (the MinIO bucket) |
| The search vectors | the `soloclefgo_qdrant_data` volume; they can be rebuilt by indexing again, at the cost of the embedding calls |
| Your settings | `docker/.env.go` and the secrets directory |

A nightly `pg_dump`, a copy of the MinIO volume, `docker/.env.go` and the
secrets are enough to rebuild the server elsewhere.

```bash
dc exec -T postgres sh -c 'pg_dump -U "$POSTGRES_USER" -Fc "$POSTGRES_DB"' > soloclef-$(date +%F).dump
```

`dc down` stops the stack and keeps the volumes; `dc down -v` deletes them,
and every document with them.
