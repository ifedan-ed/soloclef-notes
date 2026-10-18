#!/usr/bin/env bash
# Isolated SurfSense stack for the web Playwright suite.
#
#   scripts/e2e-stack.sh up       build images from this checkout, start, seed the e2e user
#   scripts/e2e-stack.sh down     stop and remove everything (the database is tmpfs)
#   scripts/e2e-stack.sh rebuild  rebuild the backend and frontend images, then up
#   scripts/e2e-stack.sh status   docker compose ps
#   scripts/e2e-stack.sh logs [service]
#
# Compose project "surfsense-web-e2e", published only on 127.0.0.1:${E2E_WEB_PORT:-3940}.
# It never touches the live "surfsense" project, its volumes or docker/.env.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$HERE/../.." && pwd)"
COMPOSE_FILE="$REPO/docker/docker-compose.e2e-web.yml"
PORT="${E2E_WEB_PORT:-3940}"
BASE="http://127.0.0.1:${PORT}"
EMAIL="${PLAYWRIGHT_TEST_EMAIL:-e2e-test@surfsense.net}"
PASSWORD="${PLAYWRIGHT_TEST_PASSWORD:-E2eTestPassword123!}"
MINT_SECRET="${E2E_MINT_SECRET:-e2e-mint-secret-not-for-production}"

compose() { E2E_WEB_PORT="$PORT" docker compose -f "$COMPOSE_FILE" "$@"; }

wait_for() {
	local url="$1" want="$2" tries="${3:-120}"
	for _ in $(seq 1 "$tries"); do
		code="$(curl -s -o /dev/null -w '%{http_code}' "$url" || true)"
		if [[ "$code" == "$want" ]]; then return 0; fi
		sleep 2
	done
	echo "timed out waiting for $url (last status $code)" >&2
	return 1
}

seed_user() {
	# 201 on a fresh database; 400 (already exists) is fine on a re-run.
	code="$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/auth/register" \
		-H 'Content-Type: application/json' -H 'X-Turnstile-Token: XXXX.DUMMY.TOKEN.XXXX' \
		-d "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\"}")"
	case "$code" in
		201 | 400) ;;
		*) echo "registering the e2e user failed ($code)" >&2; exit 1 ;;
	esac
	code="$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/__e2e__/auth/token" \
		-H 'Content-Type: application/json' -H "X-E2E-Mint-Secret: $MINT_SECRET" \
		-d "{\"email\":\"$EMAIL\"}")"
	[[ "$code" == "200" ]] || { echo "token mint is not answering ($code) — is this the E2E backend?" >&2; exit 1; }
}

case "${1:-}" in
	up)
		if [[ -z "$(docker images -q surfsense-web-e2e-backend:local)" || -z "$(docker images -q surfsense-web-e2e-frontend:local)" ]]; then
			compose build
		fi
		compose up -d --wait
		wait_for "$BASE/login" 200
		seed_user
		echo "e2e stack ready at $BASE"
		;;
	rebuild)
		compose build
		"$0" up
		;;
	down)
		compose down -v --remove-orphans
		;;
	status)
		compose ps
		;;
	logs)
		shift
		compose logs --tail=200 "$@"
		;;
	*)
		sed -n '2,12p' "$0"
		exit 1
		;;
esac
