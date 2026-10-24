#!/usr/bin/env bash
# e2e-tested.sh REPO SHA SHARDS
#
# Prints the URL of a pull_request run of the e2e workflow on commit SHA in
# which all SHARDS shards ran and passed. Prints nothing when there is no such
# run. It always exits 0, and any API error prints nothing, so a caller that
# skips work on a printed URL runs the full suite whenever this cannot prove
# the commit passed.
#
# A run on a draft pull request skips every shard and still ends as success,
# so the run's conclusion is not enough. The shards' own jobs are counted.
# A pull request may also run only the tests its change can reach. The
# "e2e-tui full suite" job passes only when every test ran, so it must pass
# too: main runs the whole suite after a partial selection.
#
# Needs gh with GH_TOKEN, and read access to Actions.
set -uo pipefail

repo=$1
sha=$2
shards=$3

runs=$(gh api "repos/$repo/actions/workflows/e2e-tui.yml/runs?head_sha=$sha&event=pull_request&status=success&per_page=20" \
	--jq '.workflow_runs[] | select(.head_sha == "'"$sha"'") | "\(.id) \(.html_url)"' 2>/dev/null) || exit 0

while read -r id url; do
	[ -n "$id" ] || continue
	read -r passed full < <(gh api "repos/$repo/actions/runs/$id/jobs?per_page=100" \
		--jq '[([.jobs[] | select((.name | startswith("e2e-tui (shard ")) and .conclusion == "success")] | length),
			([.jobs[] | select(.name == "e2e-tui full suite" and .conclusion == "success")] | length)] | @tsv' 2>/dev/null) || continue
	if [ "$passed" = "$shards" ] && [ "$full" = 1 ]; then
		echo "$url"
		exit 0
	fi
done <<<"$runs"
exit 0
