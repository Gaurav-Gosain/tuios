#!/usr/bin/env bash
# SPLIT="PKG..." [LANES=4] [ONLY=split|rest] [LOG=FILE] [DURATIONS=FILE] go-test-lanes.sh [GO_TEST_FLAG]... -- PATTERN...
#
# Runs go test on PATTERN, like `go test GO_TEST_FLAG... PATTERN...`, but
# splits the tests of each package in SPLIT across LANES go test processes
# that run at the same time. Every other package runs in one more go test
# process beside them. ONLY=split runs only the SPLIT packages, and
# ONLY=rest only the others, so two CI jobs can share the work.
#
# Why: internal/app and internal/session hold no t.Parallel, so each runs its
# tests one after another for over three minutes, and a CI job waits for the
# slowest package. Those tests mostly wait (on a pty, a daemon, a timer), not
# compute: internal/session used 23 CPU seconds in 207 seconds of wall time on
# four cores. Each process is a test binary of its own, with its own TestMain
# and its own isolated XDG tree, which is the same isolation two packages
# already get when go test runs them side by side.
#
# A lane takes one contiguous run of the package's tests, in the order go test
# runs them, so each test still follows the tests it follows in a plain run.
# Some tests measure the process heap and are sensitive to what the tests
# before them left behind. A round-robin split put
# TestAFloodedStreamGivesBackItsBatchAfterASecond right after a test whose
# daemon frees two megabytes seconds later, and it failed every time.
#
# The lanes are cut by measured time, not by test count. DURATIONS (default
# go-test-lanes.durations beside this script) holds "PACKAGE TEST SECONDS"
# lines. A test the file does not name counts at the mean of its package, and
# a package the file does not name splits by count. A split by count left the
# internal/app lanes at 98, 49, 24 and 23 seconds, because a few slow tests sat
# together in one quarter of the list.
#
# Every test, fuzz target and example that `go test -list .` names in a split
# package runs in exactly one lane. The output of each process goes to LOG
# (default go-test-lanes.log) one after another, and then to stdout, so it
# reads as one go test run. The exit status is 0
# only when every process passed.
#
# A SPLIT package that PATTERN does not select runs no lane. If no package is
# left to test, the script says so and exits 0.
#
# Build tags go through GOFLAGS (GOFLAGS=-tags=ghostty), so go list and every
# go test process see them.
set -euo pipefail

lanes=${LANES:-4}
only=${ONLY:-}
case $only in
"" | split | rest) ;;
*)
	echo "go-test-lanes.sh: ONLY is split or rest, not $only" >&2
	exit 2
	;;
esac
log=${LOG:-go-test-lanes.log}
durations=${DURATIONS:-$(dirname "$0")/go-test-lanes.durations}
read -r -a split <<<"${SPLIT:?name the packages to split in SPLIT}"

flags=()
while [ $# -gt 0 ] && [ "$1" != -- ]; do
	flags+=("$1")
	shift
done
if [ $# -eq 0 ]; then
	echo "go-test-lanes.sh: put -- before the package patterns" >&2
	exit 2
fi
shift
patterns=("$@")

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# The import paths of the split packages, and of every other package. go list
# fails on a SPLIT name that is not a package, which stops the script.
go list "${split[@]}" | sort -u >"$work/split"
go list "${patterns[@]}" | sort -u >"$work/all"
comm -23 "$work/all" "$work/split" >"$work/rest"
# A split package the patterns do not name was not selected (a pull request
# tests only the packages its change can reach), so it runs no lane.
comm -12 "$work/all" "$work/split" >"$work/picked"
mapfile -t splitpkgs <"$work/picked"
mapfile -t restpkgs <"$work/rest"
[ "$only" != rest ] || splitpkgs=()
[ "$only" != split ] || restpkgs=()

procs=()
n=0
for pkg in "${splitpkgs[@]}"; do
	# The package's tests in the order go test runs them. go test -list
	# builds the test binary, so a build failure stops here with go's own
	# message.
	if ! go test -list . "$pkg" >"$work/list" 2>&1; then
		cat "$work/list" >&2
		exit 1
	fi
	grep -E '^(Test|Fuzz|Example)' "$work/list" | awk '!seen[$0]++' >"$work/names"
	total=$(wc -l <"$work/names")
	if [ "$total" -eq 0 ]; then
		echo "go-test-lanes.sh: $pkg lists no tests" >&2
		exit 1
	fi
	# Each name gets the lane number it runs in. The lanes stay contiguous,
	# and the cut points give the smallest longest lane: a binary search on
	# the lane time, where a lane closes before the test that would pass it.
	[ -r "$durations" ] || durations=/dev/null
	awk -v pkg="$pkg" -v lanes="$lanes" -v names="$work/names" '
		FILENAME != names { if ($1 == pkg) known[$2] = $3; next }
		{ name[++n] = $0 }
		function count(cap,   i, cur, k) {
			k = 1; cur = 0
			for (i = 1; i <= n; i++) {
				if (cur > 0 && cur + w[i] > cap) { k++; cur = 0 }
				cur += w[i]
			}
			return k
		}
		END {
			for (t in known) { sum += known[t]; cnt++ }
			mean = cnt ? sum / cnt : 1
			lo = 0; hi = 0
			for (i = 1; i <= n; i++) {
				w[i] = (name[i] in known) ? known[name[i]] : mean
				# A test that takes no measurable time still counts a little,
				# so a lane of many quick tests is not free.
				if (w[i] < 0.01) w[i] = 0.01
				hi += w[i]; if (w[i] > lo) lo = w[i]
			}
			for (step = 0; step < 50; step++) {
				mid = (lo + hi) / 2
				if (count(mid) <= lanes) hi = mid; else lo = mid
			}
			lane = 1; cur = 0
			for (i = 1; i <= n; i++) {
				if (cur > 0 && cur + w[i] > hi) { lane++; cur = 0 }
				cur += w[i]
				print lane, name[i]
			}
		}' "$durations" "$work/names" >"$work/lanes"
	for ((i = 1; i <= lanes; i++)); do
		awk -v i="$i" '$1 == i { print $2 }' "$work/lanes" >"$work/lane"
		[ -s "$work/lane" ] || continue
		n=$((n + 1))
		# The pattern is read here, before the next lane writes the file.
		run="^($(paste -sd'|' "$work/lane"))\$"
		go test "${flags[@]}" -run "$run" "$pkg" >"$work/out$n" 2>&1 &
		procs+=("$n:$!")
	done
done
if [ "${#restpkgs[@]}" -gt 0 ]; then
	n=$((n + 1))
	go test "${flags[@]}" "${restpkgs[@]}" >"$work/out$n" 2>&1 &
	procs+=("$n:$!")
fi

if [ "${#procs[@]}" -eq 0 ]; then
	echo "go-test-lanes.sh: no package to test" >&2
	: >"$log"
	exit 0
fi

status=0
: >"$log"
for entry in "${procs[@]}"; do
	if ! wait "${entry#*:}"; then
		status=1
	fi
	cat "$work/out${entry%%:*}" >>"$log"
done
cat "$log"
exit $status
