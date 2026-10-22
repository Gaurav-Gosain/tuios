#!/usr/bin/env bash
# retry-flaky.sh LOG PACKAGE...
#
# Called after a go test run failed, with that run's output in LOG. When every
# top-level test that failed is listed in .github/flaky-tests.txt, it runs
# those tests once more in the given packages and exits 0 if they pass. In
# every other case it exits 1, so the job fails as it would have without it:
#
#   - a test that is not on the list failed,
#   - the log names no failed test (a build error, a vet failure, a panic),
#   - the run hit its timeout,
#   - the retry failed.
#
# A pass on the retry prints a warning annotation that names the tests, so a
# flaky test that starts failing more often still shows on the run.
set -euo pipefail

log=$1
shift
list="$(dirname "$0")/../.github/flaky-tests.txt"

if grep -qE '^panic: ' "$log"; then
	echo "The test run panicked or timed out. No retry." >&2
	exit 1
fi

# go test prints each package's output in one block that ends with its
# summary line. A FAIL summary with no failed test in its block is a build
# failure or a TestMain that failed, which a retry of named tests would hide.
if ! awk '/^--- FAIL: /{seen=1}
	/^(ok|FAIL|\?)[ \t]+[^ \t]/{ if ($1 == "FAIL" && !seen) bad=1; seen=0 }
	END{exit bad}' "$log"; then
	echo "A package failed with no failed test in it. No retry." >&2
	exit 1
fi

failed=$(grep -E '^--- FAIL: Test' "$log" | awk '{print $3}' | sort -u || true)
if [ -z "$failed" ]; then
	echo "The log names no failed test. No retry." >&2
	exit 1
fi

flaky=$(sed -e 's/#.*//' -e 's/[[:space:]]//g' "$list" | grep -v '^$' | sort -u)
others=$(comm -23 <(echo "$failed") <(echo "$flaky"))
if [ -n "$others" ]; then
	echo "These tests failed and are not on the flaky list. No retry:" >&2
	echo "$others" >&2
	exit 1
fi

names=$(echo "$failed" | paste -sd'|' -)
echo "Only tests on the flaky list failed. Running them once more: $names"
if ! go test -count=1 -timeout 5m -v -run "^($names)$" "$@"; then
	echo "::error title=Flaky test failed twice::$names failed on the retry too."
	exit 1
fi
for name in $failed; do
	echo "::warning title=Flaky test passed on the retry::$name failed once and passed on the retry. It is listed in .github/flaky-tests.txt."
done
