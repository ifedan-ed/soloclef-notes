#!/usr/bin/env bash
# The web Playwright suite's stack on the Go services.
#
#   scripts/e2e-stack-go.sh up       build what is missing, start, seed the e2e user
#   scripts/e2e-stack-go.sh rebuild [service...]
#                                    rebuild every image from this checkout (or only
#                                    the ones named: api, proxy, ...), then up
#   scripts/e2e-stack-go.sh down     stop; delete containers, volumes, networks, secrets
#   scripts/e2e-stack-go.sh purge    down, and delete the images it built too
#   scripts/e2e-stack-go.sh status   docker compose ps
#   scripts/e2e-stack-go.sh logs [service]
#
# Then, from soloclef_web:   pnpm test:e2e
#
# The stack is docker/docker-compose.go.yml (the Go stack as deployed) with
# docker/docker-compose.e2e-web-go.yml laid over it, under the compose
# project "soloclefgo-e2e", published only on 127.0.0.1:3945. It never
# touches a deployment's project (soloclefgo), its volumes or its settings.
#
# Two stacks side by side (two branches tested at once on one host) each take
# a project and a port of their own:
#
#   E2E_STACK_PROJECT=soloclefgo-e2e-mine E2E_WEB_PORT=3961 scripts/e2e-stack-go.sh up
#   E2E_WEB_PORT=3961 pnpm test:e2e            # playwright.config.ts reads the same port
#
# Both must be given together, and to every command of that stack (down
# included), or the command would act on the default stack instead. Each
# checkout runs one stack at a time: the secrets the script makes live in
# the checkout (docker/e2e-web-go/.generated), and `down` deletes them.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$HERE/../.." && pwd)"
# The project and the port the committed settings file is written for. Its
# addresses carry both (the stand-ins' container names start with the
# project), so another stack gets a copy of it with both replaced
# (settings_file).
DEFAULT_PROJECT="soloclefgo-e2e"
DEFAULT_PORT="3945"
PROJECT="${E2E_STACK_PROJECT:-$DEFAULT_PROJECT}"
PORT="${E2E_WEB_PORT:-$DEFAULT_PORT}"
# Half of a pair would name one stack's containers and publish on another's
# port, or the reverse; both are refused rather than half-applied.
is_default_project=$([[ "$PROJECT" == "$DEFAULT_PROJECT" ]] && echo yes || echo no)
is_default_port=$([[ "$PORT" == "$DEFAULT_PORT" ]] && echo yes || echo no)
if [[ "$is_default_project" != "$is_default_port" ]]; then
	echo "give E2E_STACK_PROJECT and E2E_WEB_PORT together, or neither" >&2
	exit 1
fi
[[ "$PROJECT" =~ ^[a-z0-9][a-z0-9_-]*$ ]] || { echo "E2E_STACK_PROJECT must be a compose project name" >&2; exit 1; }
[[ "$PORT" =~ ^[0-9]+$ ]] || { echo "E2E_WEB_PORT must be a port number" >&2; exit 1; }
COMMITTED_SETTINGS="$REPO/docker/e2e-web-go/settings.env"
# Everything made here and deleted by `down`: nothing in it is committed.
GENERATED="$REPO/docker/e2e-web-go/.generated"

# The settings this stack runs with: the committed file for the default
# stack, else a copy of it under GENERATED with the project and the port
# replaced. The copy is written on every run, so a change to the committed
# file always reaches it.
settings_file() {
	if [[ "$PROJECT" == "$DEFAULT_PROJECT" ]]; then
		printf '%s' "$COMMITTED_SETTINGS"
		return
	fi
	local copy="$GENERATED/settings-$PROJECT.env"
	mkdir -p "$GENERATED"
	sed -e "s/$DEFAULT_PROJECT-/$PROJECT-/g" -e "s/:$DEFAULT_PORT\b/:$PORT/g" \
		"$COMMITTED_SETTINGS" >"$copy"
	printf '%s' "$copy"
}
SETTINGS="$(settings_file)"

# One setting, read from the one file that says it (no default here: a
# second copy of a value is a value that can disagree).
setting() {
	local value
	value="$(sed -n "s/^$1=//p" "$SETTINGS")"
	[[ -n "$value" ]] || { echo "$SETTINGS does not set $1" >&2; exit 1; }
	printf '%s' "$value"
}
BASE="$(setting SOLOCLEF_API_PUBLIC_URL)"
MODEL_NETWORK="$(setting SOLOCLEF_GO_MODEL_NETWORK)"
EMAIL="${PLAYWRIGHT_TEST_EMAIL:-e2e-test@soloclef.net}"
PASSWORD="${PLAYWRIGHT_TEST_PASSWORD:-E2eTestPassword123!}"
# The overlay gives the API these same words (SOLOCLEF_API_E2E_SEED_SECRET).
MINT_SECRET="e2e-mint-secret-not-for-production"
# Every image the two files build, in the order they are built.
BUILT=(migrate parse-service indexer worker api mcp proxy)

compose() {
	docker compose -p "$PROJECT" --env-file "$SETTINGS" \
		-f "$REPO/docker/docker-compose.go.yml" \
		-f "$REPO/docker/docker-compose.e2e-web-go.yml" "$@"
}

wait_for() {
	local url="$1" want="$2" tries="${3:-120}" code=""
	for _ in $(seq 1 "$tries"); do
		code="$(curl -s -o /dev/null -w '%{http_code}' "$url" || true)"
		if [[ "$code" == "$want" ]]; then return 0; fi
		sleep 2
	done
	echo "timed out waiting for $url (last status $code)" >&2
	return 1
}

# The files the stack mounts and no repository should hold: the five secrets
# the Go stack reads (docker/.env.go.example says what each is), the Google
# client secret, and a test authority with one certificate for the stand-in
# Google. Made once for a stack's life: Postgres keeps the password its
# volume was created with, so new ones are made only after `down` has
# deleted the old ones and the volume together.
make_generated() {
	[[ -d "$GENERATED/secrets" && -f "$GENERATED/google/cert.pem" ]] && return 0
	rm -rf "$GENERATED"
	mkdir -p "$GENERATED/secrets" "$GENERATED/google"
	chmod 700 "$GENERATED" "$GENERATED/secrets"
	local s="$GENERATED/secrets" g="$GENERATED/google"
	openssl rand -hex 24 >"$s/postgres_password"
	openssl rand -hex 32 >"$s/session_secret"
	echo "sk-e2e-$(openssl rand -hex 8)" >"$s/litellm_key"
	echo "e2e-access" >"$s/s3_access_key"
	openssl rand -hex 16 >"$s/s3_secret_key"
	echo "e2e-google-client-secret" >"$s/google_oauth_client_secret"
	# The services run as different users; each file is read-only to all,
	# inside a directory only the operator can enter.
	chmod 444 "$s"/*

	# An authority and a certificate it signs, for Google's two API names.
	# The API and the worker trust the authority (SSL_CERT_FILE); they are
	# not told to skip verification, so the TLS code under test is the
	# real one.
	openssl req -x509 -newkey rsa:2048 -nodes -days 30 -subj "/CN=soloclef e2e test authority" \
		-keyout "$g/ca.key" -out "$g/ca.pem" 2>/dev/null
	openssl req -newkey rsa:2048 -nodes -subj "/CN=oauth2.googleapis.com" \
		-keyout "$g/key.pem" -out "$g/cert.csr" 2>/dev/null
	printf 'subjectAltName=DNS:oauth2.googleapis.com,DNS:www.googleapis.com\n' >"$g/san.cnf"
	openssl x509 -req -in "$g/cert.csr" -CA "$g/ca.pem" -CAkey "$g/ca.key" -CAcreateserial \
		-days 30 -extfile "$g/san.cnf" -out "$g/cert.pem" 2>/dev/null
	chmod 444 "$g/ca.pem" "$g/cert.pem" "$g/key.pem"
}

# One image at a time. Compose would build them all at once, and this host
# has been taken down by exactly that: a Next.js build and four Go builds
# together want more memory than is free.
#
# With no names, every image that is missing is built (or every image, when
# forced). With names, those are rebuilt and the rest only if missing. The
# four Go services and `migrate` are built from one schema: after a change
# that adds a migration, rebuild them together (migrate indexer worker api),
# or the ones left behind refuse to start and say which version they expect.
build_images() {
	local force="$1" service
	shift
	for service in "${BUILT[@]}"; do
		if [[ "$force" == "yes" || " $* " == *" $service "* || -z "$(docker images -q "$PROJECT-$service:local")" ]]; then
			compose build "$service"
		fi
	done
}

# The one MP3 the speech stand-in answers with, made by the worker image's
# own ffmpeg: one second of a tone, in the shape the worker joins lines in.
make_line() {
	[[ -s "$GENERATED/line.mp3" ]] && return 0
	docker run --rm --network none --entrypoint ffmpeg "$PROJECT-worker:local" \
		-hide_banner -loglevel error -f lavfi -i sine=frequency=440:duration=1 \
		-ac 1 -ar 24000 -b:a 48k -f mp3 - >"$GENERATED/line.mp3"
}

seed_user() {
	local code
	# 201 on a new database; 400 (the address has an account) on a re-run.
	code="$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/auth/register" \
		-H 'Content-Type: application/json' -H "Origin: $BASE" \
		-d "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\"}")"
	case "$code" in
		201 | 400) ;;
		*) echo "registering the e2e user failed ($code)" >&2; exit 1 ;;
	esac
	code="$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/__e2e__/auth/token" \
		-H 'Content-Type: application/json' -H "X-E2E-Mint-Secret: $MINT_SECRET" \
		-d "{\"email\":\"$EMAIL\"}")"
	[[ "$code" == "200" ]] || { echo "the token route is not answering ($code): is this the e2e stack?" >&2; exit 1; }
}

case "${1:-}" in
	up | rebuild)
		make_generated
		# make_generated starts GENERATED afresh on a new stack, settings
		# copy included: write it again.
		SETTINGS="$(settings_file)"
		# The model proxy's network is the Go stack's one external network:
		# it joins it and never makes it. Here it is this script's, internal
		# (no route out), and holds only the stand-in.
		docker network inspect "$MODEL_NETWORK" >/dev/null 2>&1 ||
			docker network create --internal "$MODEL_NETWORK" >/dev/null
		if [[ "$1" == "rebuild" && $# -eq 1 ]]; then
			build_images yes
		else
			build_images no "${@:2}"
		fi
		make_line
		compose up -d --wait
		wait_for "$BASE/login" 200
		seed_user
		echo "e2e stack (Go) ready at $BASE"
		;;
	down | purge)
		compose down -v --remove-orphans
		docker network rm "$MODEL_NETWORK" >/dev/null 2>&1 || true
		rm -rf "$GENERATED"
		if [[ "$1" == "purge" ]]; then
			for service in "${BUILT[@]}"; do
				docker image rm "$PROJECT-$service:local" >/dev/null 2>&1 || true
			done
		fi
		;;
	status)
		compose ps
		;;
	logs)
		shift
		compose logs --tail=200 "$@"
		;;
	*)
		sed -n '2,13p' "$0"
		exit 1
		;;
esac
