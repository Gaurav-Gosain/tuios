#!/usr/bin/env bash
# Run one fuzz target for the nightly fuzz workflow and exit non-zero only on
# a finding. Usage: scripts/fuzz-target.sh <go test arguments...>
#
# When -fuzztime ends while a worker is busy, the go fuzz coordinator can
# report its own deadline as "--- FAIL ... context deadline exceeded". It
# writes no input then. A real finding always prints "Failing input written
# to", so a failure without that line and with the deadline message is the
# coordinator and not the code under test.
set -uo pipefail
log=$(mktemp)
trap 'rm -f "$log"' EXIT
go test "$@" 2>&1 | tee "$log"
status=${PIPESTATUS[0]}
if [ "$status" -ne 0 ] &&
  grep -q 'context deadline exceeded' "$log" &&
  ! grep -q 'Failing input written to' "$log"; then
  echo "::warning::the fuzz run hit its own -fuzztime deadline with no failing input; not a finding"
  exit 0
fi
exit "$status"
