#!/usr/bin/env bash
# e2e-select.sh
#
# Prints the end-to-end tests this checkout should run, one per line, and
# writes mode=all, mode=some or mode=none to $GITHUB_OUTPUT when it is set.
# Run it from e2e/tui with TUIOS_E2E set.
#
# On a pull request the checkout is the merge of the head into the base, so
# HEAD^1 is the base, and e2e/tui/testselect keeps the tests that diff can
# reach. Every other event, and any error, selects every test.
#
# The test map is the newest one the e2e-tui test map workflow made on main
# before this run started (see map_for_run), or the committed one when that
# cannot be read. Every job of a run picks the same map, so the shards agree
# on the plan.
set -euo pipefail

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
all="$work/all"
go test -list '^Test' ./... | grep '^Test' >"$all"

# map_for_run downloads the e2e-test-map artifact of the newest successful
# run of e2e-tui-map.yml on main that ended before this workflow run was
# created, and prints its path and the run's URL. It prints nothing when
# there is no such run or the API fails. A rerun keeps the created time, so
# it picks the same map.
map_for_run() {
	[ -n "${GITHUB_REPOSITORY:-}" ] && [ -n "${GITHUB_RUN_ID:-}" ] && command -v gh >/dev/null || return 0
	local created id url
	created=$(gh api "repos/$GITHUB_REPOSITORY/actions/runs/$GITHUB_RUN_ID" --jq .created_at 2>/dev/null) || return 0
	read -r id url < <(gh run list -R "$GITHUB_REPOSITORY" --workflow e2e-tui-map.yml --branch main \
		--status success --limit 20 --json databaseId,updatedAt,url \
		--jq "[.[] | select(.updatedAt < \"$created\")] | first | select(. != null) | \"\\(.databaseId) \\(.url)\"" 2>/dev/null) || return 0
	[ -n "${id:-}" ] || return 0
	gh run download "$id" -R "$GITHUB_REPOSITORY" -n e2e-test-map -D "$work/map" >/dev/null 2>&1 || return 0
	[ -s "$work/map/testmap.txt" ] || return 0
	echo "$work/map/testmap.txt $url"
}

map=testselect/testmap.txt
read -r fetched url < <(map_for_run) || true
if [ -n "${fetched:-}" ]; then
	map=$fetched
	echo "e2e-select: the test map is from $url" >&2
else
	echo "e2e-select: the test map is the committed one, $map" >&2
fi

base=""
if [ "${GITHUB_EVENT_NAME:-}" = pull_request ] && [ "$(git rev-list --parents -n 1 HEAD | wc -w)" -eq 3 ]; then
	base=$(git rev-parse HEAD^1)
fi

if ! selected=$(go run ./testselect -base "$base" -map "$map" -summary "${GITHUB_STEP_SUMMARY:-/dev/null}" <"$all"); then
	echo "::warning title=Test selection failed::testselect failed, so every e2e test runs."
	selected=$(cat "$all")
fi

total=$(wc -l <"$all")
count=$(printf '%s\n' "$selected" | grep -c '^Test' || true)
if [ "$count" -eq "$total" ]; then
	mode=all
elif [ "$count" -eq 0 ]; then
	mode=none
else
	mode=some
fi
if [ -n "${GITHUB_OUTPUT:-}" ]; then
	echo "mode=$mode" >>"$GITHUB_OUTPUT"
fi
printf '%s\n' "$selected" | grep '^Test' || true
