#!/bin/sh
# Checks that a tab of an earlier build still gets its code after a deploy
# (soloclef_web/site-image/kept-assets.sh, routes.caddy "Files of earlier
# builds"), with two real proxy images whose builds differ:
#
#   docker build --target site -t kept-a soloclef_web     # one commit
#   docker build --target site -t kept-b soloclef_web     # a later one
#   sh docker/proxy-go/kept-assets-test.sh kept-a kept-b
#
# Each image runs as the stack's proxy does (this directory's Caddyfile and
# routes.caddy, one volume), one after the other, as a deploy and a
# rollback would. Everything it makes is named kept-assets-test-<pid> and
# removed at the end; the images are the caller's.
set -eu
# comm wants byte order, whatever the caller's locale sorts by.
export LC_ALL=C

A=${1:?usage: kept-assets-test.sh IMAGE_A IMAGE_B}
B=${2:?usage: kept-assets-test.sh IMAGE_A IMAGE_B}
HERE=$(cd "$(dirname "$0")" && pwd)
RUN=kept-assets-test-$$
VOLUME=$RUN
WORK=$(mktemp -d)
failures=0

cleanup() {
	docker rm -f "$RUN" >/dev/null 2>&1 || true
	docker volume rm "$VOLUME" >/dev/null 2>&1 || true
	rm -rf "$WORK"
}
trap cleanup EXIT

ok() { printf 'ok   %s\n' "$*"; }
bad() {
	printf 'FAIL %s\n' "$*"
	failures=$((failures + 1))
}
check() { # check <description> <command...>
	what=$1
	shift
	if "$@"; then ok "$what"; else bad "$what"; fi
}

# Runs an image as the proxy, on the shared volume, and waits until its
# kept-assets run (beside Caddy) has finished.
serve() {
	docker rm -f "$RUN" >/dev/null 2>&1 || true
	docker run -d --name "$RUN" \
		-e SOLOCLEF_STACK=kept-assets-test -e SOLOCLEF_TRUSTED_PROXIES=127.0.0.1/32 \
		-v "$HERE/Caddyfile:/etc/caddy/Caddyfile:ro" \
		-v "$HERE/routes.caddy:/etc/caddy/routes.caddy:ro" \
		-v "$VOLUME:/var/lib/site-assets" \
		-p 127.0.0.1::80 "$1" >/dev/null
	timeout 120 sh -c "until docker logs $RUN 2>&1 | grep -q 'kept-assets: pruned'; do sleep 0.5; done"
	timeout 30 sh -c "until docker exec $RUN wget -q -O /dev/null http://127.0.0.1:80/; do sleep 0.5; done"
	PORT=$(docker port "$RUN" 80/tcp | head -n1 | sed 's/.*://')
}

# status, then headers, of a request.
get() { curl -s -o "$WORK/body" -D "$WORK/headers" -w '%{http_code}' "$@"; }
header() { grep -i "^$1:" "$WORK/headers" | tr -d '\r' | sed 's/^[^:]*: *//'; }
same_as_in() { # same_as_in <image> <path in /srv>: the last body is that image's file
	[ "$(md5sum <"$WORK/body" | cut -c1-32)" = "$(docker run --rm --entrypoint cat "$1" "/srv$2" | md5sum | cut -c1-32)" ]
}
chunks_of() { docker run --rm --entrypoint sh "$1" -c 'cd /srv && find _next/static/chunks -name "*.js" | sort'; }

chunks_of "$A" >"$WORK/a"
chunks_of "$B" >"$WORK/b"
OLD=/$(comm -23 "$WORK/a" "$WORK/b" | head -n1)
NEW=/$(comm -13 "$WORK/a" "$WORK/b" | head -n1)
[ "$OLD" != / ] && [ "$NEW" != / ] || {
	echo "the two images have the same chunks: build them from commits that differ"
	exit 1
}
echo "a chunk only A has: $OLD"
echo "a chunk only B has: $NEW"

docker volume create "$VOLUME" >/dev/null

echo "-- A live"
serve "$A"
check "A serves its own chunk" [ "$(get "http://127.0.0.1:$PORT$OLD")" = 200 ]

echo "-- deploy B"
serve "$B"
status=$(get "http://127.0.0.1:$PORT$OLD")
check "A's chunk answers 200 after the deploy" [ "$status" = 200 ]
check "  with A's bytes" same_as_in "$A" "$OLD"
check "  and the immutable cache header" [ "$(header Cache-Control)" = "public, max-age=31536000, immutable" ]
status=$(get -H 'Accept-Encoding: br' "http://127.0.0.1:$PORT$OLD")
check "  and its Brotli copy for a browser that takes one" [ "$status/$(header Content-Encoding)" = 200/br ]
check "B's own chunk is served" [ "$(get "http://127.0.0.1:$PORT$NEW")" = 200 ]
check "  with B's bytes" same_as_in "$B" "$NEW"
check "the home page is B's" [ "$(get "http://127.0.0.1:$PORT/")" = 200 ]
check "  byte for byte" same_as_in "$B" /index.html
check "  and is not cached as immutable" [ "$(header Cache-Control)" = no-cache ]
status=$(get "http://127.0.0.1:$PORT/_next/static/chunks/no-such-chunk.js")
check "a chunk no build has is a 404" [ "$status" = 404 ]
check "  that is not cached" [ "$(header Cache-Control)" = no-cache ]

# The running build never serves a kept copy of its own file: a copy in
# the volume that differs changes nothing, and a start names it and leaves
# it as it is rather than write over it.
docker exec "$RUN" sh -c "echo damaged > /var/lib/site-assets/files$NEW"
get "http://127.0.0.1:$PORT$NEW" >/dev/null
check "B serves its own file even when the kept copy differs" same_as_in "$B" "$NEW"
serve "$B"
check "a start names the differing copy" sh -c "docker logs $RUN 2>&1 | grep -q 'kept copy differs.*${NEW#/}'"
check "  and leaves it as it was" [ "$(docker exec "$RUN" cat "/var/lib/site-assets/files$NEW")" = damaged ]
# Put the copy right for the rest of the run: removed, the next start adds it.
docker exec "$RUN" rm "/var/lib/site-assets/files$NEW"
serve "$B"

echo "-- roll back to A"
serve "$A"
check "B's chunk is served to B's tabs" [ "$(get "http://127.0.0.1:$PORT$NEW")" = 200 ]
check "  with B's bytes" same_as_in "$B" "$NEW"
check "A's own chunk is served" [ "$(get "http://127.0.0.1:$PORT$OLD")" = 200 ]
check "  with A's bytes" same_as_in "$A" "$OLD"
check "the home page is A's" [ "$(get "http://127.0.0.1:$PORT/")" = 200 ]
check "  byte for byte" same_as_in "$A" /index.html

echo "-- forward to B again, then clean up"
serve "$B"
# The window counts from when a build was last live, not from its files'
# age: files copied 30 days ago stay while their build was live just now.
docker exec "$RUN" sh -c 'find /var/lib/site-assets/files -type f -exec touch -d "@$(($(date +%s) - 30 * 86400))" {} +'
docker exec "$RUN" kept-assets prune 14 >/dev/null
check "A, replaced just now, is kept by a 14-day window, whatever its files' age" [ "$(get "http://127.0.0.1:$PORT$OLD")" = 200 ]
# A's list says when it was last live: 15 days ago, as if no deploy since.
docker exec "$RUN" sh -c 'touch -d "@$(($(date +%s) - 15 * 86400))" /var/lib/site-assets/builds/$(ls /var/lib/site-assets/builds | grep -vx "$(cat /var/lib/site-assets/live)")'
docker exec "$RUN" kept-assets prune 14
check "A's chunk is gone once A was last live over 14 days ago" [ "$(get "http://127.0.0.1:$PORT$OLD")" = 404 ]
check "B's chunk is still served" [ "$(get "http://127.0.0.1:$PORT$NEW")" = 200 ]
kept=$(docker exec "$RUN" sh -c 'find /var/lib/site-assets/files -type f | wc -l')
live=$(docker exec "$RUN" sh -c 'wc -l < /var/lib/site-assets/builds/$(cat /var/lib/site-assets/live)')
check "the volume holds B's files and nothing else ($kept of $live)" [ "$kept" = "$live" ]
docker exec "$RUN" kept-assets prune 0 >/dev/null
check "a zero-day window never drops the live build" [ "$(get "http://127.0.0.1:$PORT$NEW")" = 200 ]

echo "-- only regular files are served"
# Caddy follows a symbolic link, so a link planted in the volume would
# serve what it points to; every prune and every start removes them.
KEPT=/var/lib/site-assets/files/_next/static
plant_links() {
	docker exec "$RUN" sh -c "ln -sf /etc/passwd $KEPT/chunks/planted.js && ln -sfn /etc $KEPT/planted-dir && mkfifo $KEPT/planted-fifo"
}
plant_links
docker exec "$RUN" kept-assets prune 14 >"$WORK/out"
check "a prune names what it removed" grep -q 'removed 3 entries that were not regular files' "$WORK/out"
check "  a planted link to a file is not served" [ "$(get "http://127.0.0.1:$PORT/_next/static/chunks/planted.js")" = 404 ]
check "  a planted link to a directory is not served" [ "$(get "http://127.0.0.1:$PORT/_next/static/planted-dir/passwd")" = 404 ]
check "  and neither is left in the volume" docker exec "$RUN" sh -c "[ -z \"\$(find /var/lib/site-assets ! -type f ! -type d)\" ]"
check "  while every kept file still is" [ "$(get "http://127.0.0.1:$PORT$NEW")" = 200 ]
plant_links
serve "$B"
check "a start removes them too" docker exec "$RUN" sh -c "[ -z \"\$(find /var/lib/site-assets ! -type f ! -type d)\" ]"
check "  and says so" sh -c "docker logs $RUN 2>&1 | grep -q 'removed 3 entries that were not regular files'"

echo "-- at most 20 builds are kept"
# 25 earlier builds, all live within the window, each with a file of its
# own: fake-1 was live a minute ago, fake-25 25 minutes ago.
docker exec "$RUN" sh -c '
	now=$(date +%s)
	for i in $(seq 1 25); do
		f=_next/static/chunks/fake-$i.js
		echo "fake $i" >/var/lib/site-assets/files/$f
		echo $f >/var/lib/site-assets/builds/fake$i
		touch -d "@$((now - i * 60))" /var/lib/site-assets/builds/fake$i
	done'
docker exec "$RUN" kept-assets prune 14 >/dev/null
builds=$(docker exec "$RUN" sh -c 'ls /var/lib/site-assets/builds | wc -l')
check "20 builds are left of 26 ($builds)" [ "$builds" = 20 ]
check "  the live one among them" [ "$(get "http://127.0.0.1:$PORT$NEW")" = 200 ]
check "  and the 19 last live most recently" [ "$(get "http://127.0.0.1:$PORT/_next/static/chunks/fake-19.js")/$(get "http://127.0.0.1:$PORT/_next/static/chunks/fake-1.js")" = 200/200 ]
check "  the older ones' files are gone" [ "$(get "http://127.0.0.1:$PORT/_next/static/chunks/fake-20.js")/$(get "http://127.0.0.1:$PORT/_next/static/chunks/fake-25.js")" = 404/404 ]
check "  from the volume as well" docker exec "$RUN" sh -c '! ls /var/lib/site-assets/files/_next/static/chunks/fake-2[0-5].js 2>/dev/null'

echo "-- a build over the size cap is not kept"
live_before=$(docker exec "$RUN" cat /var/lib/site-assets/live)
# A sparse file: 300 MiB as tar would copy it, nothing on disk here.
docker exec "$RUN" truncate -s 300M /srv/_next/static/huge.bin
if docker exec "$RUN" kept-assets keep >"$WORK/out" 2>&1; then bad "a keep over the cap succeeded"; else ok "a keep over the cap fails"; fi
check "  and says why" grep -q 'over the 256 MiB one build may keep; nothing kept' "$WORK/out"
check "  copying nothing" docker exec "$RUN" sh -c '[ ! -e /var/lib/site-assets/files/_next/static/huge.bin ]'
check "  and leaving the live build as it was" [ "$(docker exec "$RUN" cat /var/lib/site-assets/live)" = "$live_before" ]
serve "$B" # a container without the file

echo "-- the prune repeats while the container runs"
check "the image keeps, then prunes every hour" sh -c "docker image inspect -f '{{range .Config.Cmd}}{{.}} {{end}}' $B | grep -q 'kept-assets run 14 3600 &'"
# The same loop, every second: a build whose window ends, and a link
# planted, while the proxy runs are both gone without a restart.
docker exec -d "$RUN" kept-assets run 14 1
docker exec "$RUN" sh -c '
	echo aged >/var/lib/site-assets/files/_next/static/chunks/aged.js
	echo _next/static/chunks/aged.js >/var/lib/site-assets/builds/aged
	touch -d "@$(($(date +%s) - 15 * 86400))" /var/lib/site-assets/builds/aged'
plant_links
gone() {
	[ "$(get "http://127.0.0.1:$PORT/_next/static/chunks/aged.js")" = 404 ] &&
		docker exec "$RUN" sh -c "[ ! -e /var/lib/site-assets/builds/aged ] && [ -z \"\$(find /var/lib/site-assets ! -type f ! -type d)\" ]"
}
for _ in $(seq 1 20); do gone && break || sleep 0.5; done
check "a build past its window and a planted link go without a restart" gone
check "B's chunk is still served" [ "$(get "http://127.0.0.1:$PORT$NEW")" = 200 ]

echo
if [ "$failures" -eq 0 ]; then echo "all passed"; else echo "$failures failed"; fi
[ "$failures" -eq 0 ]
