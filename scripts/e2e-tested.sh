#!/usr/bin/env bash
# e2e-tested.sh REPO SHA
#
# Prints the URL of a pull_request or merge_group run of the e2e workflow on
# commit SHA in which every real shard on the cells path ran and passed. A
# shard's job is named "e2e-tui (shard N of TOTAL)", and the run must have
# passed TOTAL of them, so the count is the run's own and a change of the
# shard count needs no change here. Prints nothing when there is no such
# run. It always exits
# 0, and any API error prints nothing, so a caller that skips work on a
# printed URL runs the full suite whenever this cannot prove the commit
# passed.
#
# A run on a draft pull request skips every shard and still ends as success,
# so the run's conclusion is not enough. The shards' own jobs are counted. A
# red sim shard does not fail a landing (continue-on-error), so sim shards
# never count.
# A pull request may also run only the tests its change can reach. The shard
# plan job leaves the notice "mode=all" titled "e2e-tui selection" when every
# test ran, so it must be there too: main runs the whole suite after a partial
# selection. The string frame path and the sim backend run every night on
# main, not before a landing, so they never count either.
#
# Needs gh with GH_TOKEN, and read access to Actions.
set -uo pipefail

repo=$1
sha=$2

runs=$(gh api "repos/$repo/actions/workflows/e2e-tui.yml/runs?head_sha=$sha&status=completed&per_page=20" \
	--jq '.workflow_runs[] | select(.head_sha == "'"$sha"'" and (.event == "pull_request" or .event == "merge_group")) | "\(.id) \(.html_url)"' 2>/dev/null) || exit 0

while read -r id url; do
	[ -n "$id" ] || continue
	read -r shards real plan < <(gh api "repos/$repo/actions/runs/$id/jobs?per_page=100" \
		--jq '[([.jobs[] | .name | capture("^e2e-tui \\(shard [0-9]+ of (?<n>[0-9]+)\\)$") | .n] | unique | if length == 1 then .[0] else "0" end),
			([.jobs[] | select((.name | test("^e2e-tui \\(shard [0-9]+ of [0-9]+\\)$")) and .conclusion == "success")] | length),
			([.jobs[] | select(.name == "e2e-tui shard plan" and .conclusion == "success") | .id] | first // 0)] | @tsv' 2>/dev/null) || continue
	[ "$shards" != 0 ] && [ "$real" = "$shards" ] && [ "$plan" != 0 ] || continue
	notes=$(gh api "repos/$repo/check-runs/$plan/annotations?per_page=100" \
		--jq '.[] | "\(.title)=\(.message)"' 2>/dev/null) || continue
	grep -qx 'e2e-tui selection=mode=all' <<<"$notes" || continue
	echo "$url"
	exit 0
done <<<"$runs"
exit 0
