#!/usr/bin/env bash
# go-files.sh [check]: list the Go files of internal/session that hold the
# grant checks the model describes, in spec/grants/go-files.txt.
# scripts/ci-changes.sh reads the list to start the spec job.
#
# The list is derived, not picked by hand. The differential test runs twice
# with coverage and a fixed seed: once in full, and once with
# -grantspec.apply-only, which puts each state on the daemon and runs no
# call. A file is listed when a function in it runs in the full run and not
# in the setup run, so the list holds the code the calls reach.
#
# With check, the script fails when a file the calls reach is missing from
# the list. A listed file that the calls no longer reach is reported and
# does not fail, since a function can also run on a daemon timer.
#
# Needs the model built (cd spec/grants && lake build).
set -euo pipefail
root=$(git rev-parse --show-toplevel)
cd "$root"
list=spec/grants/go-files.txt
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

covered() {
	go test -tags spec -count=1 -run '^TestGrantsMatchLeanModel$' \
		-coverpkg=./internal/session -coverprofile="$tmp/$1.out" \
		./internal/session/ -spec.seed=1 -grantspec.n=2000 "${@:2}" >"$tmp/$1.log" 2>&1 ||
		{ cat "$tmp/$1.log" >&2; exit 1; }
	go tool cover -func="$tmp/$1.out" |
		awk '$NF != "0.0%" && $1 ~ /\.go:/ { sub(/:[0-9]+:$/, "", $1); print $1 " " $2 }' |
		sort -u >"$tmp/$1.funcs"
}
covered all
covered setup -grantspec.apply-only

# The test copies the order of the checks from these files instead of
# calling them, so no run reaches them:
#   verb_protocol.go     admitVerb: checkGrants, then checkScope
#   verb_start_agent.go  start-agent: launchGrants, then depthRefusal
#   verb_worktree.go     fan: launchGrants, then depthRefusal
# The test itself is listed too.
copied="internal/session/verb_protocol.go
internal/session/verb_start_agent.go
internal/session/verb_worktree.go
internal/session/grants_spec_test.go"

{
	comm -23 "$tmp/all.funcs" "$tmp/setup.funcs" |
		awk '{ print $1 }' | sed 's#^github.com/Gaurav-Gosain/tuios/##'
	echo "$copied"
} | sort -u >"$tmp/files"

if [ "${1:-}" = check ]; then
	missing=$(grep -v '^#' "$list" | comm -13 - "$tmp/files")
	extra=$(grep -v '^#' "$list" | comm -23 - "$tmp/files")
	if [ -n "$extra" ]; then
		printf 'These listed files were not reached in this run:\n%s\n' "$extra"
	fi
	if [ -n "$missing" ]; then
		printf 'The grant checks reach files that %s does not list:\n%s\nRun spec/grants/go-files.sh and commit the list.\n' "$list" "$missing" >&2
		exit 1
	fi
	echo "$list is current."
	exit 0
fi

{
	echo "# The Go files of internal/session that hold the grant checks the model"
	echo "# describes. spec/grants/go-files.sh writes this list. Do not edit it."
	cat "$tmp/files"
} >"$list"
echo "Wrote $list: $(grep -vc '^#' "$list") files."
