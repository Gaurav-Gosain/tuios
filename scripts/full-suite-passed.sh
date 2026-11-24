#!/usr/bin/env bash
# full-suite-passed.sh REPO SHA
#
# Exits 0 when commit SHA passed the whole suite: a run of ci.yml that ran
# every job ("CI selection" notice "all") and passed "CI result", and a run of
# e2e-tui.yml that ran every test on the cells path on the real binary (see
# e2e-tested.sh). It prints the two run URLs. It exits 1 when either is
# missing or not done yet, and prints what it did not find. Landing on main
# calls it after the full-suite label's runs end.
#
# Needs gh with GH_TOKEN, and read access to Actions.
set -uo pipefail

repo=$1
sha=$2
e2e=$("$(dirname "$0")/e2e-tested.sh" "$repo" "$sha")

ci=""
runs=$(gh api "repos/$repo/actions/workflows/ci.yml/runs?head_sha=$sha&status=completed&per_page=20" \
	--jq '.workflow_runs[] | select(.head_sha == "'"$sha"'" and (.event == "pull_request" or .event == "merge_group")) | "\(.id) \(.html_url)"' 2>/dev/null) || runs=""
while read -r id url; do
	[ -n "$id" ] || continue
	read -r result changes < <(gh api "repos/$repo/actions/runs/$id/jobs?per_page=100" \
		--jq '[([.jobs[] | select(.name == "CI result") | .conclusion] | first // "none"),
			([.jobs[] | select(.name == "changes" and .conclusion == "success") | .id] | first // 0)] | @tsv' 2>/dev/null) || continue
	[ "$result" = success ] && [ "$changes" != 0 ] || continue
	all=$(gh api "repos/$repo/check-runs/$changes/annotations?per_page=100" \
		--jq '[.[] | select(.title == "CI selection" and .message == "all")] | length' 2>/dev/null) || continue
	if [ "$all" -ge 1 ]; then
		ci=$url
		break
	fi
done <<<"$runs"

[ -n "$ci" ] || echo "full-suite-passed: no ci.yml run that ran every job passed on $sha" >&2
[ -n "$e2e" ] || echo "full-suite-passed: no e2e-tui.yml run of every test passed on $sha" >&2
[ -n "$ci" ] && [ -n "$e2e" ] || exit 1
echo "ci: $ci"
echo "e2e: $e2e"
