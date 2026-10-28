#!/usr/bin/env bash
# collect.sh BIN OUT [JOBS]
#
# Runs each end-to-end test named on stdin in its own process against BIN, a
# tuios built with -cover -tags coverflush, and writes OUT/<Test>.funcs: the
# functions the test ran. The test process itself is built with coverage of
# the same packages, so the list holds the code of this repository that the
# harness ran too, such as internal/session and internal/config. Code that
# runs in every process (package init, the start of main, and the harness's
# TestMain) is taken out first, from a run of `BIN --version` and a run of
# the suite with no test, so a function counts only when the test reached it
# on its own. testselect -build turns the .funcs files into the test map.
#
# A test that fails is run once more. Its coverage is kept either way, and its
# name goes to OUT/failed.txt, because a test that stopped early may have
# missed code it would reach on a pass. testselect marks those tests to run on
# every change.
#
# Run from e2e/tui with TUIOS_E2E set.
set -euo pipefail

bin=$(realpath "$1")
out=$(realpath -m "$2")
jobs=${3:-1}
mkdir -p "$out"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

go test -c -cover -coverpkg=github.com/Gaurav-Gosain/tuios/...,tuios.dev/... -o "$work/e2e.test" .

mkdir -p "$work/base"
GOCOVERDIR="$work/base" "$bin" --version >/dev/null
GOCOVERDIR="$work/base" TUIOS_E2E_BIN="$bin" "$work/e2e.test" \
	-test.count=1 -test.run '^$' -test.gocoverdir="$work/base" >/dev/null
go tool covdata textfmt -i "$work/base" -o "$work/base.txt"
awk 'NR > 1 && $NF > 0 { print $1 }' "$work/base.txt" >"$work/base.keys"
# The map says it holds the test process's coverage, which testselect needs
# before it judges a change to a package the harness imports by the map.
: >"$out/harness.cover"
# The functions that every process runs. A change inside one of them reaches
# every test that uses what it sets up, so testselect treats it like a change
# to a declaration. Lines are "path/file.go:line: name percent".
(cd ../.. && go tool cover -func="$work/base.txt") |
	awk '$NF != "0.0%" && $1 != "total:" { sub(/:[0-9]+:$/, "", $1); print $1, $2 }' | sort -u >"$out/init.funcs"

one() {
	local name=$1 dir="$work/cov/$1" status=0
	for attempt in 1 2; do
		rm -rf "$dir"
		mkdir -p "$dir"
		status=0
		GOCOVERDIR="$dir" TUIOS_E2E_BIN="$bin" timeout 15m "$work/e2e.test" \
			-test.count=1 -test.timeout 14m -test.run "^${name}\$" -test.gocoverdir="$dir" >"$dir.log" 2>&1 || status=$?
		[ "$status" -eq 0 ] && break
	done
	if [ "$status" -ne 0 ]; then
		echo "$name" >>"$out/failed.txt"
	fi
	if ls "$dir"/covmeta.* >/dev/null 2>&1; then
		go tool covdata textfmt -i "$dir" -o "$dir.txt" 2>/dev/null || true
	fi
	if [ -s "$dir.txt" ]; then
		# A profile line is "path/file.go:start,end statements count".
		# The functions the test ran: a profile with the start-up blocks
		# counted as not run, read by go tool cover.
		awk 'NR == FNR { base[$1] = 1; next } FNR > 1 && ($1 in base) { $NF = 0 } { print }' \
			"$work/base.keys" "$dir.txt" >"$dir.own"
		(cd ../.. && go tool cover -func="$dir.own") |
			awk '$NF != "0.0%" && $1 != "total:" { sub(/:[0-9]+:$/, "", $1); print $1, $2 }' | sort -u >"$out/$name.funcs"
	else
		: >"$out/$name.funcs"
	fi
	rm -rf "$dir" "$dir.txt" "$dir.own" "$dir.log"
	echo "$name $status $(wc -l <"$out/$name.funcs")"
}
export -f one
export work out bin
xargs -P "$jobs" -I{} bash -c 'one {}'
