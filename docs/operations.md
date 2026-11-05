# Updating and backups

## Updating

```bash
cd soloclef
git pull
docker build -t soloclef-backend-src:latest --target production surfsense_backend
docker build -t soloclef-backend-fast:latest custom
cd docker
docker compose -f docker-compose.yml build
docker compose -f docker-compose.yml up -d
```

The backend runs any database migrations when it starts, and refuses to start
if one fails, so a half-applied upgrade never serves requests.

## What to back up

| What | Where |
| --- | --- |
| The database (accounts, notebooks, chats, notes, the search index) | the `db` service's volume; use `pg_dump` for a consistent copy |
| Uploaded files, audio and other Studio files | the backend's object-store volume (`/app/.local_object_store`) |
| Your settings | `docker/.env`, and `global_llm_config.yaml` if you use one |

A nightly `pg_dump` plus a copy of the object-store volume and `docker/.env`
is enough to rebuild the server elsewhere.

```bash
docker compose -f docker-compose.yml exec -T db \
  pg_dump -U "$DB_USER" -Fc "$DB_NAME" > soloclef-$(date +%F).dump
```
