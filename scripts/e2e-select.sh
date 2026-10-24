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
set -euo pipefail

all=$(mktemp)
trap 'rm -f "$all"' EXIT
go test -list '^Test' ./... | grep '^Test' >"$all"

base=""
if [ "${GITHUB_EVENT_NAME:-}" = pull_request ] && [ "$(git rev-list --parents -n 1 HEAD | wc -w)" -eq 3 ]; then
	base=$(git rev-parse HEAD^1)
fi

if ! selected=$(go run ./testselect -base "$base" -summary "${GITHUB_STEP_SUMMARY:-/dev/null}" <"$all"); then
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
