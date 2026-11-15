#!/bin/sh
# The web app's hashed files from earlier builds, kept so that a tab opened
# before a deploy still works after it.
#
# A page loads part of its code only when it is needed (pressing play loads
# the player). Those files are /_next/static/..., named by a hash of their
# contents, and each build has its own set. A tab opened on the build before
# a deploy still asks for that build's names, which the new image does not
# have: without this, the tab fails with ChunkLoadError the first time it
# needs a part it has not loaded yet.
#
# So every build's /_next/static files are added to a named volume
# (docker-compose.go.yml, `site_assets`), and Caddy serves a /_next/static
# path the running build lacks from there (docker/proxy-go/routes.caddy,
# "Files of earlier builds"). Pages, page data and every other file always
# come from the running build: only hashed files can be mixed safely.
#
#   kept-assets.sh keep          add this image's files and mark it live
#   kept-assets.sh prune <days>  drop the builds that stopped being live
#                                more than <days> days ago, and the oldest
#                                past MAX_BUILDS, and every file no
#                                remaining build lists
#   kept-assets.sh run <days> <seconds>
#                                keep, then prune <days> at once and every
#                                <seconds> after, for as long as the
#                                container runs
#
# The image runs `run` at start (the Dockerfile's `site` stage, CMD),
# beside Caddy rather than before it, so a deploy is not slower: the files
# a running build needs are in the image, and the volume only serves the
# builds before it, whose files are already there. The prune repeats
# because a proxy runs for weeks between deploys: a build replaced the day
# before must still be dropped when its window ends, not at the next
# start.
#
# Only regular files are served from the volume. Caddy follows a symbolic
# link (file_server has no setting that stops it), so a link planted in
# the volume would serve whatever it points to inside the container. This
# script never adds anything but regular files (it lists them with
# `find -type f`, which does not follow links), and every keep and prune
# first removes whatever else is in the volume (sweep below).
#
# The volume's layout:
#   files/_next/static/...  every kept file, at its own path
#   builds/<id>             the files of one build, one path a line; its
#                           modification time is the last time that build
#                           was live (now, for the live one)
#   live                    the id of the live build
# A build's id is a hash of its list of files: two builds with the same
# files are, for this purpose, the same build.
#
# Deleting the volume costs only this: tabs opened before the next deploy
# reload once when they need a file of their own build (the web app's
# lib/chunk-reload). Nothing else reads it.
set -eu

# This image's own files, as the Dockerfile copies them.
SITE=/srv
# The volume's mount point (docker-compose.go.yml) and the root Caddy
# serves kept files from (routes.caddy). The three must agree.
KEPT=/var/lib/site-assets
FILES="$KEPT/files"
BUILDS="$KEPT/builds"
LIVE="$KEPT/live"

# How many builds the volume keeps, the live one included, however recent
# the others: a day of many deploys must not fill the disk. A build is
# about 1,700 files and 50 MB with their compressed copies (2026-11), and
# builds share most of their files (four builds took 56 MB), so twenty
# hold about 1 GB even if they shared nothing; a tab of an older build
# than that reloads once.
MAX_BUILDS=20
# The most one build may add, in MiB: five times what a build emits now.
# A build over it (source maps or media put in by mistake) is not kept at
# all, rather than partly: its own tabs reload once after the next deploy.
# With MAX_BUILDS this bounds the volume at 5 GiB whatever is built.
MAX_BUILD_MIB=256

say() { printf 'kept-assets: %s\n' "$*"; }

# One proxy runs per stack, but a recreate can start the new container
# while the old one's background run is still going; the lock keeps the two
# from interleaving their lists.
lock() {
	mkdir -p "$KEPT"
	exec 9>"$KEPT/.lock"
	flock 9
}

# Removes everything in the volume that is neither a regular file nor a
# directory: symbolic links above all, which Caddy would follow. find does
# not follow links (-P), so a link to a directory is removed as a link and
# nothing under its target is looked at. Nothing this script writes is
# ever anything else, so whatever is found was put there by hand or by
# something that should not write here, and is named in the log. The lock
# is held.
sweep() {
	odd=$(find "$KEPT" -mindepth 1 ! -type f ! -type d -print -exec rm -f {} + | wc -l)
	if [ "$odd" -gt 0 ]; then
		say "removed $odd entries that were not regular files or directories"
	fi
}

keep() {
	lock
	sweep
	mkdir -p "$FILES" "$BUILDS"
	# What an earlier run left when its container stopped half way.
	rm -rf "$KEPT"/.work.*
	work=$(mktemp -d "$KEPT/.work.XXXXXX")
	trap 'rm -rf "$work"' EXIT

	(cd "$SITE" && find _next/static -type f) | sort >"$work/list"
	if [ ! -s "$work/list" ]; then
		say "no /_next/static files in $SITE: nothing to keep"
		return 1
	fi
	# The lists are one path a line and are passed through xargs. Next.js
	# names these files with letters, digits and . _ - ~ only; a name with
	# white space in it would be misread, so it is refused rather than
	# guessed at.
	if grep -q '[[:space:]]' "$work/list"; then
		say "a file name has white space in it; nothing kept"
		return 1
	fi
	# Apparent sizes, as tar would copy them, summed before anything is.
	bytes=$( (cd "$SITE" && xargs stat -c %s <"$work/list") | awk '{ s += $1 } END { printf "%d\n", s }')
	if [ "$bytes" -gt $((MAX_BUILD_MIB * 1048576)) ]; then
		say "this build's files are $((bytes / 1048576)) MiB, over the $MAX_BUILD_MIB MiB one build may keep; nothing kept"
		return 1
	fi
	build=$(md5sum <"$work/list" | cut -c1-32)

	: >"$work/new"
	: >"$work/both"
	while IFS= read -r f; do
		if [ -e "$FILES/$f" ]; then
			printf '%s\n' "$f" >>"$work/both"
		else
			printf '%s\n' "$f" >>"$work/new"
		fi
	done <"$work/list"

	# A name already kept is never written again. Its name is a hash of its
	# contents, so the same name from another build is the same bytes, and
	# there is nothing to replace. This checks that it is so, because a
	# kept file that differs would be served to another build's tabs as if
	# it were theirs; if one ever differs it is left as it is and named
	# here, for an operator to look at. The running build never reads these
	# copies (it serves its own from $SITE), so a mismatch cannot reach its
	# own pages.
	if [ -s "$work/both" ]; then
		(cd "$SITE" && xargs md5sum <"$work/both") >"$work/sum.site"
		(cd "$FILES" && xargs md5sum <"$work/both") >"$work/sum.kept"
		# Both sums are in the order of the same list, so line n of one is
		# line n of the other.
		paste "$work/sum.site" "$work/sum.kept" | awk '$1 != $3 { print $2 }' | while IFS= read -r f; do
			say "kept copy differs from this build's, left as it was: $f"
		done
	fi

	# New files are copied beside the kept ones first and then renamed into
	# place, each rename atomic: a container stopped half way leaves no
	# half-written file under a real name, which the check above would then
	# keep forever.
	added=0
	if [ -s "$work/new" ]; then
		mkdir "$work/stage"
		(cd "$SITE" && tar -cf - -T "$work/new") | tar -xf - -C "$work/stage"
		(cd "$work/stage" && find . -type d) | while IFS= read -r d; do
			mkdir -p "$FILES/$d"
			(cd "$work/stage/$d" && find . -maxdepth 1 -type f | xargs -r mv -n -t "$FILES/$d")
		done
		added=$(wc -l <"$work/new")
	fi

	# The build that was live until now stops being live now: that is the
	# time its window is counted from, not the age of its files.
	previous=$(cat "$LIVE" 2>/dev/null || true)
	if [ -n "$previous" ] && [ "$previous" != "$build" ] && [ -f "$BUILDS/$previous" ]; then
		touch "$BUILDS/$previous"
	fi
	cp "$work/list" "$BUILDS/.$build" && mv "$BUILDS/.$build" "$BUILDS/$build"
	printf '%s\n' "$build" >"$LIVE.new" && mv "$LIVE.new" "$LIVE"
	say "build $build is live: $(wc -l <"$work/list") files, $added new"
}

prune() {
	days=${1:-}
	case "$days" in
	'' | *[!0-9]*)
		say "prune takes a whole number of days"
		return 2
		;;
	esac
	lock
	sweep
	live=$(cat "$LIVE" 2>/dev/null || true)
	# Without the live build's list, every kept file would look unlisted.
	if [ -z "$live" ] || [ ! -f "$BUILDS/$live" ]; then
		say "no live build recorded; nothing pruned"
		return 1
	fi
	work=$(mktemp -d "$KEPT/.work.XXXXXX")
	trap 'rm -rf "$work"' EXIT
	cutoff=$(($(date +%s) - days * 86400))
	dropped=0
	for list in "$BUILDS"/*; do
		[ -f "$list" ] || continue
		[ "${list##*/}" = "$live" ] && continue
		if [ "$(stat -c %Y "$list")" -lt "$cutoff" ]; then
			rm -f "$list"
			dropped=$((dropped + 1))
		fi
	done
	# Then the oldest past MAX_BUILDS, by when each was last live (its
	# list's modification time; ls -t is newest first). The live build is
	# never one of them: MAX_BUILDS - 1 others stay beside it.
	ls -t "$BUILDS" | grep -vx "$live" | tail -n +"$MAX_BUILDS" >"$work/over"
	while IFS= read -r id; do
		rm -f "$BUILDS/$id"
		dropped=$((dropped + 1))
	done <"$work/over"

	cat "$BUILDS"/* | sort -u >"$work/listed"
	(cd "$FILES" && find _next -type f) | sort >"$work/kept"
	comm -23 "$work/kept" "$work/listed" >"$work/gone"
	(cd "$FILES" && xargs -r rm -f <"$work/gone")
	find "$FILES" -mindepth 1 -type d -empty -delete
	say "pruned $dropped builds (last live over $days days ago, or past the newest $MAX_BUILDS), $(wc -l <"$work/gone") files"
}

# run keeps this build and then prunes for as long as the container runs.
# Each step is this script run again, not a function call: each then takes
# the lock and lets it go (a lock held by the loop would stop a newer
# container's keep until this one stopped), and each keeps its own set -e,
# which a function's would lose under `||`. A step that fails is in the
# log and the loop goes on: a keep that kept nothing still leaves the
# earlier builds to age out, and a prune that failed is tried again.
run() {
	days=${1:-}
	seconds=${2:-}
	case "$days" in '' | *[!0-9]*) days=bad ;; esac
	case "$seconds" in '' | *[!0-9]*) seconds=0 ;; esac
	if [ "$days" = bad ] || [ "$seconds" -le 0 ]; then
		say "run takes a whole number of days and a whole, positive number of seconds"
		return 2
	fi
	sh "$0" keep || say "keep failed; pruning goes on"
	while :; do
		sh "$0" prune "$days" || say "prune failed; trying again in $seconds seconds"
		sleep "$seconds"
	done
}

case "${1:-}" in
keep) keep ;;
prune) prune "${2:-}" ;;
run) run "${2:-}" "${3:-}" ;;
*)
	say "usage: kept-assets.sh keep | prune <days> | run <days> <seconds>"
	exit 2
	;;
esac
