#!/bin/sh
# Render pgbouncer.ini and its userlist from the environment, then run pgbouncer.
#
# The database password reaches the pooler through the environment only: the
# rendered userlist is written to a container-local runtime directory (never the
# image, never a volume) with 0600, and nothing here prints a value. The
# rendered pgbouncer.ini holds no credential, so it is safe to cat when
# debugging.
#
# Set PGBOUNCER_RENDER_ONLY to render the files and exit without starting
# pgbouncer.
set -eu

TEMPLATE="${PGBOUNCER_TEMPLATE:-/etc/pgbouncer/pgbouncer.ini.template}"
RUNTIME_DIR="${PGBOUNCER_RUNTIME_DIR:-/tmp/pgbouncer}"
PGBOUNCER_PORT="${PGBOUNCER_PORT:-6432}"

for name in DB_HOST DB_PORT DB_NAME DB_USER DB_PASSWORD; do
	eval "value=\${$name:-}"
	if [ -z "$value" ]; then
		echo "pgbouncer config: $name is required and is unset or empty" >&2
		exit 64
	fi
done

if [ ! -r "$TEMPLATE" ]; then
	echo "pgbouncer config: template $TEMPLATE is missing or unreadable" >&2
	exit 66
fi

umask 077
mkdir -p "$RUNTIME_DIR"
chmod 700 "$RUNTIME_DIR"

sed \
	-e "s|@DB_HOST@|${DB_HOST}|g" \
	-e "s|@DB_PORT@|${DB_PORT}|g" \
	-e "s|@DB_NAME@|${DB_NAME}|g" \
	-e "s|@DB_USER@|${DB_USER}|g" \
	-e "s|@PGBOUNCER_PORT@|${PGBOUNCER_PORT}|g" \
	-e "s|@RUNTIME_DIR@|${RUNTIME_DIR}|g" \
	"$TEMPLATE" >"$RUNTIME_DIR/pgbouncer.ini"

# scram-sha-256 against the client needs the password in clear here: pgbouncer
# cannot recover it from a SCRAM exchange, and it needs it again to
# authenticate to Postgres as the same user.
printf '"%s" "%s"\n' "$DB_USER" "$DB_PASSWORD" >"$RUNTIME_DIR/userlist.txt"
chmod 600 "$RUNTIME_DIR/userlist.txt"

if [ -n "${PGBOUNCER_RENDER_ONLY:-}" ]; then
	exit 0
fi

exec pgbouncer "$RUNTIME_DIR/pgbouncer.ini"
