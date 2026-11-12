#!/usr/bin/env bash
# Which of the API's sqlc blocks generate against the migrations?
#
#   scripts/sqlc_status.sh                          # every block
#   scripts/sqlc_status.sh internal/folders ...     # only these packages
#
# Why this exists. soloclef_api/sqlc.yaml points every block at
# soloclef_schema/migrations, and `sqlc generate` there writes nothing at
# all when any block fails, stopping at the first error. After a migration
# changes a table, this script runs each block on its own, so the report
# says, package by package, which queries the change broke.
#
# For each block it prints one of:
#
#   generates, unchanged   the code in the tree is what the migrations give
#   generates, differs     it generates, and the code in the tree is older:
#                          run `sqlc generate` in soloclef_api, then fix what
#                          no longer compiles
#   FAILS  n of m queries  n queries name something the migrations do not have
#
# and exits 1 if any block fails, 0 if every block generates.
#
# The count is of queries sqlc cannot compile, and sqlc stops at a query's
# first error. So it is how much is certainly left, not all of it: a query
# that compiles is not thereby right about whose rows it reads. That is the
# isolation tests' question, not this script's.
#
# Nothing in the repository is touched: each block is generated in a
# temporary directory that reaches the migrations and the queries through
# symbolic links, and that directory is removed on exit.
#
# The options every block is generated with are not repeated here: they are
# read from the first block of sqlc.yaml, which carries them for the rest.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
api="$(cd "$here/../soloclef_api" && pwd)"
config="$api/sqlc.yaml"

wanted=("$@")

command -v sqlc >/dev/null || { echo "sqlc is not installed (https://docs.sqlc.dev/en/latest/overview/install.html)" >&2; exit 2; }

scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT INT TERM HUP
ln -s "$here/migrations" "$scratch/migrations"
ln -s "$api/internal" "$scratch/internal"

# The template: everything from `version:` up to the second list item, which
# is the first block with every option written out.
awk '/^version:/ { on = 1 } /^  - / { items++ } on && items < 2' "$config" >"$scratch/template.yaml"
grep -q '^    schema: \.\./soloclef_schema/migrations$' "$scratch/template.yaml" || {
	echo "the first block of $config does not name ../soloclef_schema/migrations as its schema, so it cannot be the template" >&2
	exit 2
}

# The blocks: "<queries> <package> <out>" a line, in the file's order. Every
# block names the three, in this order, and nothing else differs.
awk '
	$1 == "queries:" { queries = $2 }
	$1 == "package:" { package = $2 }
	$1 == "out:"     { print queries, package, $2 }
' "$config" >"$scratch/blocks"
[[ -s "$scratch/blocks" ]] || { echo "no blocks found in $config" >&2; exit 2; }

# A package asked for by name must be one the configuration has.
for package in "${wanted[@]}"; do
	awk -v out="${package%/}" '$3 == out { found = 1 } END { exit !found }' "$scratch/blocks" || {
		echo "$package is not the \`out\` of any block in $config" >&2
		exit 2
	}
done

total=0 generating=0 failing=0 failed_queries=0 all_queries=0
while read -r queries package out; do
	if [[ ${#wanted[@]} -gt 0 ]]; then
		listed=false
		for name in "${wanted[@]}"; do [[ "${name%/}" == "$out" ]] && listed=true; done
		$listed || continue
	fi
	total=$((total + 1))
	count="$(grep -c '^-- name:' "$api/$queries" || true)"
	all_queries=$((all_queries + count))

	# One block, generated into the scratch directory.
	work="$scratch/run/$out"
	rm -rf "$scratch/run" && mkdir -p "$work"
	sed -e "s|^    schema: .*|    schema: migrations|" \
		-e "s|^    queries: .*|    queries: $queries|" \
		-e "s|^        package: .*|        package: $package|" \
		-e "s|^        out: .*|        out: run/$out|" \
		"$scratch/template.yaml" >"$scratch/sqlc.yaml"

	if (cd "$scratch" && sqlc generate -f sqlc.yaml) >"$scratch/output" 2>&1; then
		generating=$((generating + 1))
		state="generates, unchanged"
		for file in "$work"/*.go; do
			cmp -s "$file" "$api/$out/$(basename "$file")" || state="generates, differs"
		done
		printf '%-34s %-22s %3d queries\n' "$out" "$state" "$count"
	else
		# One line per query sqlc could not compile.
		errors="$(grep -c "queries.sql:" "$scratch/output" || true)"
		failing=$((failing + 1))
		failed_queries=$((failed_queries + errors))
		printf '%-34s FAILS %3d of %3d queries\n' "$out" "$errors" "$count"
		# Each distinct reason once, with how many queries gave it.
		sed -n 's/^.*queries\.sql:[0-9]*:[0-9]*: //p' "$scratch/output" | sort | uniq -c | sort -rn | sed 's/^ */        /'
		# An error that is not about a query (the schema itself, the
		# configuration) is the whole output, and is shown.
		[[ "$errors" -gt 0 ]] || sed 's/^/        /' "$scratch/output"
	fi
done <"$scratch/blocks"

echo
echo "$generating of $total blocks generate against the migrations; $failing do not ($failed_queries of $all_queries queries do not compile)."
[[ "$failing" -eq 0 ]]
